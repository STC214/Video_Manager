package archive

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type TargetAudit struct {
	Correct           bool
	Message           string
	Files             []VideoFile
	ExistingCount     int
	LastLeafIndex     int
	LastLeafFileCount int
	ErrorCount        int
	Errors            []string
}

func AuditTargetContext(ctx context.Context, cfg PlanConfig) TargetAudit {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg = NormalizePlanConfig(cfg)
	audit := TargetAudit{Correct: true, Message: "目标目录为空，可直接投料。"}
	if strings.TrimSpace(cfg.TargetDir) == "" {
		audit.Correct = false
		audit.ErrorCount = 1
		audit.Errors = []string{"目标目录为空"}
		audit.Message = "目标目录为空。"
		return audit
	}
	targetRoot := DisplayPath(strings.TrimSpace(cfg.TargetDir))
	var info os.FileInfo
	err := RetryIOPaths(ctx, 3, []string{targetRoot}, func() error {
		var statErr error
		info, statErr = os.Stat(FSPath(targetRoot))
		return statErr
	})
	if os.IsNotExist(err) {
		return audit
	}
	if err != nil {
		audit.Correct = false
		audit.ErrorCount = 1
		audit.Errors = []string{err.Error()}
		audit.Message = "无法读取目标目录。"
		return audit
	}
	if !info.IsDir() {
		audit.Correct = false
		audit.ErrorCount = 1
		audit.Errors = []string{"目标路径不是目录"}
		audit.Message = "目标路径不是目录。"
		return audit
	}

	stagingParent := filepath.Join(targetRoot, "_file-manager-staging")
	var stagingInfo os.FileInfo
	stagingErr := RetryReadPaths(ctx, 3, []string{stagingParent}, func() error {
		var statErr error
		stagingInfo, statErr = os.Stat(FSPath(stagingParent))
		return statErr
	})
	if stagingErr == nil {
		if !stagingInfo.IsDir() {
			audit.Correct = false
			audit.ErrorCount = 1
			audit.Errors = []string{"投料临时路径不是目录"}
			audit.Message = "投料临时路径异常，已停止生成新计划。"
			return audit
		}
		stagingScan := ScanFilesWithProgress(ctx, stagingParent, nil, cfg.Extensions, nil)
		if stagingScan.Cancelled {
			audit.Correct = false
			audit.Message = "投料临时目录检查已取消。"
			return audit
		}
		if stagingScan.ErrorCount > 0 {
			audit.Correct = false
			audit.ErrorCount = stagingScan.ErrorCount
			audit.Errors = append(audit.Errors, stagingScan.Errors...)
			audit.Message = "无法安全检查投料临时目录。"
			return audit
		}
		if stagingScan.MatchedCount+stagingScan.IgnoredCount > 0 {
			audit.Correct = false
			audit.ErrorCount = 1
			audit.Errors = []string{"投料临时目录中仍有文件，请先撤销或人工恢复上次未完成的投料"}
			audit.Message = "发现上次投料遗留的临时文件，已停止生成新计划。"
			return audit
		}
	} else if !os.IsNotExist(stagingErr) {
		audit.Correct = false
		audit.ErrorCount = 1
		audit.Errors = []string{stagingErr.Error()}
		audit.Message = "无法访问投料临时目录。"
		return audit
	}

	excluded := []string{
		filepath.Join(targetRoot, "_video-manager"),
		stagingParent,
	}
	scan := ScanFilesWithProgress(ctx, targetRoot, excluded, cfg.Extensions, nil)
	audit.Files = scan.Files
	audit.ExistingCount = len(scan.Files)
	audit.ErrorCount = scan.ErrorCount
	audit.Errors = append(audit.Errors, scan.Errors...)
	if scan.Cancelled {
		audit.Correct = false
		audit.Message = "目标目录审计已取消。"
		return audit
	}
	if scan.ErrorCount > 0 {
		audit.Correct = false
		audit.Message = "目标目录审计存在读取错误，不能安全投料。"
		return audit
	}
	if len(scan.Files) == 0 {
		return audit
	}

	counts := map[int]int{}
	maxLeaf := 0
	for _, file := range scan.Files {
		if ctx.Err() != nil {
			audit.Correct = false
			audit.Message = "目标目录审计已取消。"
			return audit
		}
		index, ok := calendarLeafIndex(targetRoot, file.SourcePath, cfg)
		if !ok {
			audit.Correct = false
			audit.Message = "发现对应后缀文件不在规范的年份/季度/月/叶目录中，需要先重排。"
			return audit
		}
		counts[index]++
		if counts[index] > cfg.FilesPerLeaf {
			audit.Correct = false
			audit.Message = "发现叶目录文件数超过设置值，需要先重排。"
			return audit
		}
		if index > maxLeaf {
			maxLeaf = index
		}
	}
	for index := 1; index <= maxLeaf; index++ {
		if ctx.Err() != nil {
			audit.Correct = false
			audit.Message = "目标目录审计已取消。"
			return audit
		}
		count := counts[index]
		if index < maxLeaf && count != cfg.FilesPerLeaf {
			audit.Correct = false
			audit.Message = "发现空洞或未填满的中间叶目录，需要先重排。"
			return audit
		}
		if index == maxLeaf && (count < 1 || count > cfg.FilesPerLeaf) {
			audit.Correct = false
			audit.Message = "最后叶目录文件数量不正确，需要先重排。"
			return audit
		}
	}
	audit.LastLeafIndex = maxLeaf
	audit.LastLeafFileCount = counts[maxLeaf]
	audit.Message = "目标目录结构和每叶文件数量正确，可直接续排投料。"
	return audit
}

func BuildFeedPlanContext(ctx context.Context, feedFiles []VideoFile, cfg PlanConfig) MovePlan {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg = NormalizePlanConfig(cfg)
	audit := AuditTargetContext(ctx, cfg)
	if ctx.Err() != nil {
		return MovePlan{
			TargetRoot:         filepath.Clean(strings.TrimSpace(cfg.TargetDir)),
			FeedFileCount:      len(feedFiles),
			TargetDirFileLimit: cfg.FilesPerLeaf,
			ManagedExtensions:  append([]string(nil), cfg.Extensions...),
			AuditCorrect:       false,
			AuditMessage:       "投料 Dry-run 已取消。",
		}
	}
	if audit.ErrorCount > 0 {
		return feedAuditErrorPlan(feedFiles, cfg, audit)
	}
	if err := ValidateConfigForFiles(cfg, audit.ExistingCount+len(feedFiles)); err != nil {
		audit.ErrorCount = 1
		audit.Errors = []string{err.Error()}
		audit.Message = "现有目标文件与投料文件合计后超出当前归档配置容量。"
		return feedAuditErrorPlan(feedFiles, cfg, audit)
	}
	if audit.Correct {
		plan := BuildMovePlanContext(ctx, feedFiles, cfg)
		plan.AuditCorrect = true
		plan.AuditMessage = audit.Message
		plan.ExistingTargetFiles = audit.ExistingCount
		plan.FeedFileCount = len(feedFiles)
		plan.FinalTargetFiles = audit.ExistingCount + len(feedFiles)
		plan.TargetSnapshot = targetSnapshot(audit.Files)
		return plan
	}
	return buildRebalanceFeedPlan(ctx, audit, feedFiles, cfg)
}

func ValidateFeedPlanPreflight(ctx context.Context, plan MovePlan, cfg PlanConfig) error {
	if plan.FeedFileCount <= 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	audit := AuditTargetContext(ctx, cfg)
	if err := ctx.Err(); err != nil {
		return err
	}
	if audit.ErrorCount > 0 {
		message := audit.Message
		if len(audit.Errors) > 0 {
			message += ": " + audit.Errors[0]
		}
		return fmt.Errorf("%s", message)
	}
	if audit.Correct != plan.AuditCorrect || targetSnapshot(audit.Files) != plan.TargetSnapshot {
		return fmt.Errorf("目标目录在 Dry-run 后发生变化，请重新生成投料 Dry-run")
	}
	return validateFeedSources(ctx, plan)
}

func validateFeedSources(ctx context.Context, plan MovePlan) error {
	start := len(plan.Items) - plan.FeedFileCount
	if start < 0 {
		return fmt.Errorf("投料计划缺少源文件记录，请重新生成投料 Dry-run")
	}
	for _, item := range plan.Items[start:] {
		if err := ctx.Err(); err != nil {
			return err
		}
		var info os.FileInfo
		err := RetryReadPaths(ctx, 3, []string{item.SourcePath}, func() error {
			var statErr error
			info, statErr = os.Stat(FSPath(item.SourcePath))
			return statErr
		})
		if err != nil {
			return fmt.Errorf("投料源文件不可访问，请重新生成投料 Dry-run: %s: %w", item.SourcePath, err)
		}
		if info.IsDir() || info.Size() != item.Size || (!item.ModTime.IsZero() && !info.ModTime().Equal(item.ModTime)) {
			return fmt.Errorf("投料源文件在 Dry-run 后发生变化，请重新生成投料 Dry-run: %s", item.SourcePath)
		}
	}
	return nil
}

func targetSnapshot(files []VideoFile) string {
	rows := make([]string, 0, len(files))
	for _, file := range files {
		rows = append(rows, fmt.Sprintf("%s\x00%d\x00%s",
			strings.ToLower(DisplayPath(file.SourcePath)), file.Size,
			file.ModTime.UTC().Format(time.RFC3339Nano)))
	}
	sort.Strings(rows)
	sum := sha256.Sum256([]byte(strings.Join(rows, "\n")))
	return fmt.Sprintf("%x", sum)
}

func CleanupFeedStaging(ctx context.Context, stagingRoot string) (int, []string) {
	stagingRoot = DisplayPath(strings.TrimSpace(stagingRoot))
	if stagingRoot == "" || filepath.Base(filepath.Dir(stagingRoot)) != "_file-manager-staging" {
		return 0, []string{fmt.Sprintf("invalid feed staging path: %s", stagingRoot)}
	}
	removed, errs := CleanupEmptyDirs(ctx, stagingRoot, nil)
	if err := RetryIOPaths(ctx, 3, []string{stagingRoot}, func() error {
		return os.Remove(FSPath(stagingRoot))
	}); err == nil {
		removed++
	} else if !os.IsNotExist(err) {
		errs = append(errs, err.Error())
	}
	parent := filepath.Dir(stagingRoot)
	_ = RetryIOPaths(ctx, 3, []string{parent}, func() error {
		return os.Remove(FSPath(parent))
	})
	return removed, errs
}

func feedAuditErrorPlan(feedFiles []VideoFile, cfg PlanConfig, audit TargetAudit) MovePlan {
	plan := MovePlan{
		TargetRoot:          filepath.Clean(strings.TrimSpace(cfg.TargetDir)),
		ExistingTargetFiles: audit.ExistingCount,
		FeedFileCount:       len(feedFiles),
		FinalTargetFiles:    audit.ExistingCount + len(feedFiles),
		TargetSnapshot:      targetSnapshot(audit.Files),
		AuditCorrect:        false,
		AuditMessage:        audit.Message,
		TargetDirFileLimit:  cfg.FilesPerLeaf,
		ManagedExtensions:   append([]string(nil), cfg.Extensions...),
	}
	message := audit.Message
	if len(audit.Errors) > 0 {
		message += ": " + audit.Errors[0]
	}
	for _, file := range feedFiles {
		plan.Items = append(plan.Items, MovePlanItem{
			SourcePath: file.SourcePath,
			Size:       file.Size,
			ModTime:    file.ModTime,
			Status:     "error",
			Error:      message,
		})
	}
	plan.ErrorCount = len(plan.Items)
	return plan
}

func buildRebalanceFeedPlan(ctx context.Context, audit TargetAudit, feedFiles []VideoFile, cfg PlanConfig) MovePlan {
	targetRoot := filepath.Clean(strings.TrimSpace(cfg.TargetDir))
	stagingRoot := filepath.Join(targetRoot, "_file-manager-staging", time.Now().Format("20060102-150405.000000000"))
	plan := MovePlan{
		TargetRoot:          targetRoot,
		ExistingTargetFiles: audit.ExistingCount,
		FeedFileCount:       len(feedFiles),
		FinalTargetFiles:    audit.ExistingCount + len(feedFiles),
		TargetSnapshot:      targetSnapshot(audit.Files),
		StopOnError:         true,
		TargetDirFileLimit:  cfg.FilesPerLeaf,
		ManagedExtensions:   append([]string(nil), cfg.Extensions...),
		AuditCorrect:        false,
		AuditMessage:        audit.Message,
		Rebalanced:          true,
		StagingRoot:         stagingRoot,
	}

	existing := append([]VideoFile(nil), audit.Files...)
	sortFiles(existing)
	stagedPaths := make([]string, len(existing))
	ignored := make([]string, len(existing))
	for index, file := range existing {
		if ctx.Err() != nil {
			return plan
		}
		staged := filepath.Join(stagingRoot, fmt.Sprintf("%06d", index+1), file.Name)
		stagedPaths[index] = staged
		ignored[index] = file.SourcePath
		plan.Items = append(plan.Items, MovePlanItem{
			SourcePath: file.SourcePath,
			TargetPath: staged,
			Size:       file.Size,
			ModTime:    file.ModTime,
			Status:     "planned",
		})
	}

	resolver := newTargetResolverIgnoring(ctx, ignored)
	targetDirs := map[string]struct{}{}
	addFinal := func(source string, file VideoFile, globalIndex int) {
		leafIndex := globalIndex/cfg.FilesPerLeaf + 1
		targetDir := filepath.Join(targetRoot, CalendarLeafPath(cfg.StartYear, leafIndex, cfg.LeafDirsPerMonth))
		targetDirs[targetDir] = struct{}{}
		managedExt, _ := matchingExtension(file.Name, cfg.Extensions)
		targetPath, conflict, err := resolver.uniquePath(filepath.Join(targetDir, file.Name), managedExt)
		if err != nil {
			plan.ErrorCount++
			plan.Items = append(plan.Items, MovePlanItem{
				SourcePath: source,
				TargetPath: filepath.Join(targetDir, file.Name),
				Size:       file.Size,
				ModTime:    file.ModTime,
				Status:     "error",
				Error:      "target directory check failed: " + err.Error(),
			})
			return
		}
		if conflict {
			plan.ConflictCount++
		}
		plan.Items = append(plan.Items, MovePlanItem{
			SourcePath: source,
			TargetPath: targetPath,
			Size:       file.Size,
			ModTime:    file.ModTime,
			Conflict:   conflict,
			Status:     "planned",
		})
	}
	for index, file := range existing {
		if ctx.Err() != nil {
			return plan
		}
		addFinal(stagedPaths[index], file, index)
	}
	feed := append([]VideoFile(nil), feedFiles...)
	sortFiles(feed)
	for index, file := range feed {
		if ctx.Err() != nil {
			return plan
		}
		addFinal(file.SourcePath, file, len(existing)+index)
	}

	totalFinal := len(existing) + len(feed)
	if totalFinal > 0 {
		plan.RequiredLeafDirs = ceilDiv(totalFinal, cfg.FilesPerLeaf)
		plan.LastLeafPath = CalendarLeafPath(cfg.StartYear, plan.RequiredLeafDirs, cfg.LeafDirsPerMonth)
		plan.LastLeafFileCount = totalFinal % cfg.FilesPerLeaf
		if plan.LastLeafFileCount == 0 {
			plan.LastLeafFileCount = cfg.FilesPerLeaf
		}
	}
	plan.TargetDirCount = len(targetDirs)
	return plan
}

func calendarLeafIndex(targetRoot, filePath string, cfg PlanConfig) (int, bool) {
	rel, err := filepath.Rel(targetRoot, filepath.Dir(filePath))
	if err != nil {
		return 0, false
	}
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	if len(parts) != 4 {
		return 0, false
	}
	if len(parts[0]) != 4 {
		return 0, false
	}
	year, err := strconv.Atoi(parts[0])
	if err != nil || year < cfg.StartYear || year > 9999 {
		return 0, false
	}
	if len(parts[2]) != 6 || len(parts[3]) != 8 {
		return 0, false
	}
	month, err := strconv.Atoi(parts[2][4:6])
	if err != nil || month < 1 || month > 12 || parts[2][:4] != parts[0] {
		return 0, false
	}
	leafNo, err := strconv.Atoi(parts[3][6:8])
	if err != nil || leafNo < 1 || leafNo > cfg.LeafDirsPerMonth {
		return 0, false
	}
	quarter := (month-1)/3 + 1
	if parts[1] != fmt.Sprintf("%04dS%d", year, quarter) ||
		parts[3] != fmt.Sprintf("%04d%02d%02d", year, month, leafNo) {
		return 0, false
	}
	monthOffset := (year-cfg.StartYear)*12 + month - 1
	return monthOffset*cfg.LeafDirsPerMonth + leafNo, true
}
