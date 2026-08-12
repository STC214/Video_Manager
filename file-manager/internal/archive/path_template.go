package archive

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"
)

const DefaultPathTemplate = `{YYYY}\{YYYY}S{Q}\{YYYY}{MM}\{YYYY}{MM}{NN}`

var (
	pathTemplateToken = regexp.MustCompile(`\{YYYY\}|\{Q\}|\{MM\}|\{NN\}`)
	reservedDirName   = regexp.MustCompile(`(?i)^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\..*)?$`)
	pathPartCache     sync.Map
)

type calendarPosition struct {
	year, quarter, month, leaf int
}

type TemplateLayout struct {
	PeriodName     string
	PeriodsPerYear int
	HasSequence    bool
	temporalRank   int
}

func CalendarLeafPathForConfig(cfg PlanConfig, leafIndex int) string {
	cfg = NormalizePlanConfig(cfg)
	if leafIndex < 1 {
		return ""
	}
	parts, err := pathTemplateParts(cfg.PathTemplate)
	if err != nil {
		return ""
	}
	layout, err := analyzePathTemplateParts(parts)
	if err != nil {
		return ""
	}
	position := calendarPositionForIndex(cfg, leafIndex, layout)
	for index, part := range parts {
		parts[index] = renderPathTemplatePart(part, position)
	}
	return filepath.Join(parts...)
}

func CalendarLeafIndex(relativeLeafPath string, cfg PlanConfig) (int, bool) {
	cfg = NormalizePlanConfig(cfg)
	parts, err := pathTemplateParts(cfg.PathTemplate)
	if err != nil {
		return 0, false
	}
	actual := splitRelativePath(relativeLeafPath)
	if len(actual) != len(parts) {
		return 0, false
	}

	values := map[string]int{}
	for index, templatePart := range parts {
		pattern, tokens := pathPartPattern(templatePart)
		matches := pattern.FindStringSubmatch(actual[index])
		if matches == nil {
			return 0, false
		}
		for tokenIndex, token := range tokens {
			value, parseErr := strconv.Atoi(matches[tokenIndex+1])
			if parseErr != nil {
				return 0, false
			}
			if previous, exists := values[token]; exists && previous != value {
				return 0, false
			}
			values[token] = value
		}
	}

	layout, err := analyzePathTemplateParts(parts)
	if err != nil {
		return 0, false
	}
	year, yearOK := values["{YYYY}"]
	if !yearOK || year < cfg.StartYear || year > 9999 {
		return 0, false
	}
	quarter := values["{Q}"]
	month := values["{MM}"]
	if layout.temporalRank == 2 && (quarter < 1 || quarter > 4) {
		return 0, false
	}
	if layout.temporalRank >= 3 && (month < 1 || month > 12) {
		return 0, false
	}
	if parsedQuarter, exists := values["{Q}"]; exists && layout.temporalRank >= 3 && parsedQuarter != (month-1)/3+1 {
		return 0, false
	}
	periodOffset := year - cfg.StartYear
	switch layout.temporalRank {
	case 2:
		periodOffset = periodOffset*4 + quarter - 1
	case 3:
		periodOffset = periodOffset*12 + month - 1
	}
	groups := EffectiveLeafDirsPerPeriod(cfg)
	leaf := 1
	if layout.HasSequence {
		leaf = values["{NN}"]
		if leaf < 1 || leaf > groups {
			return 0, false
		}
	}
	index := periodOffset*groups + leaf
	if index < 1 || !strings.EqualFold(filepath.Clean(relativeLeafPath), CalendarLeafPathForConfig(cfg, index)) {
		return 0, false
	}
	return index, true
}

func ValidatePathTemplate(template string) error {
	parts, err := pathTemplateParts(template)
	if err != nil {
		return err
	}
	if len(parts) > 8 {
		return fmt.Errorf("目录模板最多支持 8 层")
	}
	normalized := strings.Join(parts, `\`)
	if !strings.Contains(normalized, "{YYYY}") {
		return fmt.Errorf("目录规则必须包含年份 {YYYY}")
	}
	if strings.Count(normalized, "{NN}") > 1 || (strings.Contains(normalized, "{NN}") && !strings.Contains(parts[len(parts)-1], "{NN}")) {
		return fmt.Errorf("{NN} 最多使用一次，使用时必须位于最后一层")
	}
	if _, err := analyzePathTemplateParts(parts); err != nil {
		return err
	}
	previousRank := 0
	seen := map[string]bool{}
	for _, part := range parts {
		remainder := pathTemplateToken.ReplaceAllString(part, "")
		if strings.ContainsAny(remainder, "{}") {
			return fmt.Errorf("目录模板包含未知变量，仅支持 {YYYY}、{Q}、{MM}、{NN}")
		}
		sample := renderPathTemplatePart(part, calendarPosition{year: 2026, quarter: 1, month: 1, leaf: 1})
		if err := validateDirectoryName(sample); err != nil {
			return err
		}
		for _, token := range []string{"{YYYY}", "{Q}", "{MM}"} {
			if strings.Contains(part, token) {
				seen[token] = true
			}
		}
		rank := pathPartTemporalRank(part)
		if rank >= 2 && !seen["{YYYY}"] {
			return fmt.Errorf("季度或月份之前必须先有年份层")
		}
		if rank == 3 && strings.Contains(normalized, "{Q}") && !seen["{Q}"] {
			return fmt.Errorf("月份必须位于季度之后")
		}
		if rank > 0 && rank < previousRank {
			return fmt.Errorf("目录中的年份、季度、月份必须按从大到小的顺序排列")
		}
		if rank > previousRank {
			previousRank = rank
		}
	}
	return nil
}

func AnalyzePathTemplate(template string) (TemplateLayout, error) {
	if err := ValidatePathTemplate(template); err != nil {
		return TemplateLayout{}, err
	}
	parts, _ := pathTemplateParts(template)
	return analyzePathTemplateParts(parts)
}

func analyzePathTemplateParts(parts []string) (TemplateLayout, error) {
	layout := TemplateLayout{PeriodName: "年份", PeriodsPerYear: 1, temporalRank: 1}
	foundYear := false
	for _, part := range parts {
		if strings.Contains(part, "{YYYY}") {
			foundYear = true
		}
		if strings.Contains(part, "{NN}") {
			layout.HasSequence = true
		}
		if rank := pathPartTemporalRank(part); rank > layout.temporalRank {
			layout.temporalRank = rank
		}
	}
	if !foundYear {
		return TemplateLayout{}, fmt.Errorf("目录规则必须包含年份 {YYYY}")
	}
	switch layout.temporalRank {
	case 2:
		layout.PeriodName = "季度"
		layout.PeriodsPerYear = 4
	case 3:
		layout.PeriodName = "月份"
		layout.PeriodsPerYear = 12
	}
	return layout, nil
}

func EffectiveLeafDirsPerPeriod(cfg PlanConfig) int {
	cfg = NormalizePlanConfig(cfg)
	layout, err := AnalyzePathTemplate(cfg.PathTemplate)
	if err != nil || !layout.HasSequence {
		return 1
	}
	return cfg.LeafDirsPerMonth
}

func MaximumLeafDirs(cfg PlanConfig) int {
	cfg = NormalizePlanConfig(cfg)
	layout, err := AnalyzePathTemplate(cfg.PathTemplate)
	if err != nil {
		return 0
	}
	return saturatedMultiply(saturatedMultiply(10000-cfg.StartYear, layout.PeriodsPerYear), EffectiveLeafDirsPerPeriod(cfg))
}

func PathTemplateDepth(template string) int {
	parts, err := pathTemplateParts(template)
	if err != nil {
		return 0
	}
	return len(parts)
}

func EffectiveFolderCounts(cfg PlanConfig, requiredYears int) []int {
	cfg = NormalizePlanConfig(cfg)
	parts, err := pathTemplateParts(cfg.PathTemplate)
	if err != nil {
		return nil
	}
	counts := make([]int, 0, len(parts))
	layout, _ := analyzePathTemplateParts(parts)
	previousRank := 0
	for _, part := range parts {
		rank := pathPartTemporalRank(part)
		count := 1
		if strings.Contains(part, "{NN}") {
			groups := EffectiveLeafDirsPerPeriod(cfg)
			switch {
			case previousRank == 0:
				count = saturatedMultiply(saturatedMultiply(requiredYears, layout.PeriodsPerYear), groups)
			case previousRank == layout.temporalRank:
				count = groups
			case layout.temporalRank == 3 && previousRank == 1:
				count = 12 * groups
			case layout.temporalRank == 3 && previousRank == 2:
				count = 3 * groups
			case layout.temporalRank == 2 && previousRank == 1:
				count = 4 * groups
			}
			counts = append(counts, count)
			continue
		}
		switch {
		case rank == 1 && previousRank == 0:
			count = requiredYears
		case rank == 2 && previousRank == 0:
			count = saturatedMultiply(requiredYears, 4)
		case rank == 2 && previousRank == 1:
			count = 4
		case rank == 3 && previousRank == 0:
			count = saturatedMultiply(requiredYears, 12)
		case rank == 3 && previousRank == 1:
			count = 12
		case rank == 3 && previousRank == 2:
			count = 3
		}
		counts = append(counts, count)
		if rank > previousRank {
			previousRank = rank
		}
	}
	return counts
}

func pathTemplateParts(template string) ([]string, error) {
	template = strings.TrimSpace(template)
	if template == "" {
		template = DefaultPathTemplate
	}
	template = strings.ReplaceAll(template, "/", `\`)
	parts := strings.Split(template, `\`)
	if len(parts) == 0 {
		return nil, fmt.Errorf("目录模板不能为空")
	}
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("目录模板不能包含空层")
		}
		if strings.TrimSpace(part) != part {
			return nil, fmt.Errorf("目录层名称不能以空格开头或结尾")
		}
	}
	return parts, nil
}

func splitRelativePath(path string) []string {
	path = strings.ReplaceAll(filepath.Clean(path), "/", `\`)
	return strings.Split(path, `\`)
}

func calendarPositionForIndex(cfg PlanConfig, leafIndex int, layout TemplateLayout) calendarPosition {
	groups := EffectiveLeafDirsPerPeriod(cfg)
	zero := leafIndex - 1
	periodOffset := zero / groups
	position := calendarPosition{year: cfg.StartYear, quarter: 1, month: 1, leaf: zero%groups + 1}
	switch layout.temporalRank {
	case 1:
		position.year += periodOffset
	case 2:
		position.year += periodOffset / 4
		position.quarter = periodOffset%4 + 1
		position.month = (position.quarter-1)*3 + 1
	case 3:
		position.year += periodOffset / 12
		position.month = periodOffset%12 + 1
		position.quarter = (position.month-1)/3 + 1
	}
	return position
}

func renderPathTemplatePart(part string, position calendarPosition) string {
	return strings.NewReplacer(
		"{YYYY}", fmt.Sprintf("%04d", position.year),
		"{Q}", strconv.Itoa(position.quarter),
		"{MM}", fmt.Sprintf("%02d", position.month),
		"{NN}", fmt.Sprintf("%02d", position.leaf),
	).Replace(part)
}

func pathPartPattern(part string) (*regexp.Regexp, []string) {
	if cached, ok := pathPartCache.Load(part); ok {
		value := cached.(compiledPathPart)
		return value.pattern, value.tokens
	}
	locations := pathTemplateToken.FindAllStringIndex(part, -1)
	tokens := make([]string, 0, len(locations))
	var pattern strings.Builder
	pattern.WriteString(`(?i)^`)
	last := 0
	for _, location := range locations {
		pattern.WriteString(regexp.QuoteMeta(part[last:location[0]]))
		token := part[location[0]:location[1]]
		tokens = append(tokens, token)
		switch token {
		case "{YYYY}":
			pattern.WriteString(`([0-9]{4})`)
		case "{Q}":
			pattern.WriteString(`([1-4])`)
		case "{MM}", "{NN}":
			pattern.WriteString(`([0-9]{2})`)
		}
		last = location[1]
	}
	pattern.WriteString(regexp.QuoteMeta(part[last:]))
	pattern.WriteString(`$`)
	compiled := compiledPathPart{pattern: regexp.MustCompile(pattern.String()), tokens: tokens}
	pathPartCache.Store(part, compiled)
	return compiled.pattern, compiled.tokens
}

type compiledPathPart struct {
	pattern *regexp.Regexp
	tokens  []string
}

func pathPartRank(part string) int {
	switch {
	case strings.Contains(part, "{NN}"):
		return 4
	case strings.Contains(part, "{MM}"):
		return 3
	case strings.Contains(part, "{Q}"):
		return 2
	case strings.Contains(part, "{YYYY}"):
		return 1
	default:
		return 0
	}
}

func pathPartTemporalRank(part string) int {
	switch {
	case strings.Contains(part, "{MM}"):
		return 3
	case strings.Contains(part, "{Q}"):
		return 2
	case strings.Contains(part, "{YYYY}"):
		return 1
	default:
		return 0
	}
}

func validateDirectoryName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("目录模板生成了无效目录名 %q", name)
	}
	if len(utf16.Encode([]rune(name))) > 255 {
		return fmt.Errorf("目录模板生成的单层名称超过 255 个 UTF-16 字符")
	}
	for _, char := range name {
		if char < 32 {
			return fmt.Errorf("目录模板不能包含控制字符")
		}
	}
	if strings.ContainsAny(name, `<>:"/\|?*`) || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") || reservedDirName.MatchString(name) {
		return fmt.Errorf("目录模板生成了 Windows 非法目录名 %q", name)
	}
	return nil
}
