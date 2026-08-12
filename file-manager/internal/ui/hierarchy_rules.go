package ui

import (
	"fmt"
	"strings"

	"github.com/lxn/win"

	"video-manager/file-manager/internal/archive"
)

const maxHierarchyDepth = 8

const (
	presetYear = iota
	presetQuarter
	presetMonth
	presetLeaf
	presetSimpleLeaf
	presetFixed
	presetCustom
)

type hierarchyPreset struct {
	name        string
	pattern     string
	description string
	needsInput  bool
}

type hierarchyRule struct {
	preset int
	value  string
}

var hierarchyPresets = []hierarchyPreset{
	{name: "年份", pattern: "{YYYY}", description: "按年份分目录。"},
	{name: "季度", pattern: "{YYYY}S{Q}", description: "按季度分目录；不用季度就不选。"},
	{name: "月份", pattern: "{YYYY}{MM}", description: "按月份分目录。"},
	{name: "月内分组", pattern: "{YYYY}{MM}{NN}", description: "按月内编号分组，通常放在最后。"},
	{name: "简洁分组", pattern: "第{NN}组", description: "易读的分组名，通常放在最后。"},
	{name: "固定名称", description: "固定使用右侧填写的目录名。", needsInput: true},
	{name: "自定义规则（高级）", description: "组合日期变量；详细写法见 README。", needsInput: true},
}

func defaultHierarchyRules(depth int) []hierarchyRule {
	depth = clamp(depth, 1, maxHierarchyDepth)
	switch depth {
	case 1:
		return []hierarchyRule{{preset: presetYear}}
	case 2:
		return []hierarchyRule{{preset: presetYear}, {preset: presetQuarter}}
	case 3:
		return []hierarchyRule{{preset: presetYear}, {preset: presetMonth}, {preset: presetLeaf}}
	case 4:
		return []hierarchyRule{{preset: presetYear}, {preset: presetQuarter}, {preset: presetMonth}, {preset: presetLeaf}}
	}

	rules := make([]hierarchyRule, 0, depth)
	rules = append(rules, hierarchyRule{preset: presetYear})
	for index := 0; index < depth-4; index++ {
		name := "分类"
		if depth > 5 {
			name = fmt.Sprintf("分类%d", index+1)
		}
		rules = append(rules, hierarchyRule{preset: presetFixed, value: name})
	}
	rules = append(rules,
		hierarchyRule{preset: presetQuarter},
		hierarchyRule{preset: presetMonth},
		hierarchyRule{preset: presetLeaf},
	)
	return rules
}

func hierarchyRulesFromTemplate(template string) []hierarchyRule {
	template = strings.TrimSpace(template)
	if template == "" {
		template = archive.DefaultPathTemplate
	}
	if err := archive.ValidatePathTemplate(template); err != nil {
		return defaultHierarchyRules(4)
	}
	parts := strings.Split(strings.ReplaceAll(template, "/", `\`), `\`)
	rules := make([]hierarchyRule, 0, len(parts))
	for _, part := range parts {
		matched := false
		for index, preset := range hierarchyPresets {
			if preset.pattern != "" && part == preset.pattern {
				rules = append(rules, hierarchyRule{preset: index})
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		preset := presetFixed
		if strings.ContainsAny(part, "{}") {
			preset = presetCustom
		}
		rules = append(rules, hierarchyRule{preset: preset, value: part})
	}
	return rules
}

func hierarchyTemplateFromRules(rules []hierarchyRule) (string, error) {
	if len(rules) < 1 || len(rules) > maxHierarchyDepth {
		return "", fmt.Errorf("目录层数必须是 1 到 %d", maxHierarchyDepth)
	}
	parts := make([]string, len(rules))
	for index, rule := range rules {
		if rule.preset < 0 || rule.preset >= len(hierarchyPresets) {
			return "", fmt.Errorf("第 %d 层的命名选项无效", index+1)
		}
		preset := hierarchyPresets[rule.preset]
		part := preset.pattern
		if preset.needsInput {
			part = strings.TrimSpace(rule.value)
		}
		if part == "" {
			parts[index] = part
			return strings.Join(parts, `\`), fmt.Errorf("请填写第 %d 层的名称或规则", index+1)
		}
		parts[index] = part
	}
	template := strings.Join(parts, `\`)
	if err := archive.ValidatePathTemplate(template); err != nil {
		return template, err
	}
	return template, nil
}

func isHierarchyComboID(id int) bool {
	return id >= idHierarchyComboBase && id < idHierarchyComboBase+maxHierarchyDepth
}

func isHierarchyEditID(id int) bool {
	return id >= idHierarchyEditBase && id < idHierarchyEditBase+maxHierarchyDepth
}

func (a *app) setHierarchyRules(rules []hierarchyRule) {
	if len(rules) < 1 || len(rules) > maxHierarchyDepth {
		rules = defaultHierarchyRules(4)
	}
	a.hierarchyDepth = len(rules)
	a.setText(a.controls[idHierarchyDepth], fmt.Sprintf("%d", a.hierarchyDepth))
	for index := 0; index < maxHierarchyDepth; index++ {
		visible := index < len(rules)
		showWindow(a.hierarchyLabels[index], visible)
		showWindow(a.hierarchyCombos[index], visible)
		showWindow(a.hierarchyExamples[index], visible)
		if !visible {
			showWindow(a.hierarchyEdits[index], false)
			continue
		}
		rule := rules[index]
		if rule.preset < 0 || rule.preset >= len(hierarchyPresets) {
			rule = hierarchyRule{preset: presetCustom, value: rule.value}
		}
		win.SendMessage(a.hierarchyCombos[index], win.CB_SETCURSEL, uintptr(rule.preset), 0)
		a.setText(a.hierarchyEdits[index], rule.value)
		a.refreshHierarchyRow(index)
	}
	a.refreshPeriodControls()
}

func (a *app) hierarchyRuleAt(index int) hierarchyRule {
	if index < 0 || index >= maxHierarchyDepth {
		return hierarchyRule{preset: -1}
	}
	selection := int(win.SendMessage(a.hierarchyCombos[index], win.CB_GETCURSEL, 0, 0))
	if selection < 0 || selection >= len(hierarchyPresets) {
		selection = presetCustom
	}
	return hierarchyRule{preset: selection, value: a.text(a.hierarchyEdits[index])}
}

func (a *app) currentHierarchyRules() []hierarchyRule {
	depth := clamp(a.hierarchyDepth, 1, maxHierarchyDepth)
	rules := make([]hierarchyRule, depth)
	for index := range rules {
		rules[index] = a.hierarchyRuleAt(index)
	}
	return rules
}

func (a *app) currentPathTemplate() string {
	template, _ := hierarchyTemplateFromRules(a.currentHierarchyRules())
	if strings.TrimSpace(template) == "" {
		return "{INVALID}"
	}
	return template
}

func (a *app) prepareHierarchyInput(index int) {
	rule := a.hierarchyRuleAt(index)
	if rule.preset < 0 || rule.preset >= len(hierarchyPresets) {
		return
	}
	if hierarchyPresets[rule.preset].needsInput && strings.TrimSpace(rule.value) == "" {
		value := "分类"
		if rule.preset == presetCustom {
			value = "{YYYY}{MM}{NN}"
		}
		a.setText(a.hierarchyEdits[index], value)
	}
	a.refreshHierarchyRow(index)
}

func (a *app) refreshHierarchyRow(index int) {
	rule := a.hierarchyRuleAt(index)
	if rule.preset < 0 || rule.preset >= len(hierarchyPresets) {
		return
	}
	showWindow(a.hierarchyEdits[index], hierarchyPresets[rule.preset].needsInput)
	a.setTextNoFlicker(a.hierarchyExamples[index], hierarchyRuleExample(rule))
	if !a.initializing {
		a.refreshPeriodControls()
	}
}

func (a *app) refreshPeriodControls() {
	template, err := hierarchyTemplateFromRules(a.currentHierarchyRules())
	if err != nil {
		showWindow(a.controls[idLeafDirsMonth], true)
		win.EnableWindow(a.controls[idLeafDirsMonth], true)
		a.setTextNoFlicker(a.periodCountLabel, "周期内分组数")
		return
	}
	layout, err := archive.AnalyzePathTemplate(template)
	if err != nil {
		return
	}
	showWindow(a.controls[idLeafDirsMonth], layout.HasSequence)
	showWindow(a.periodCountLabel, layout.HasSequence)
	win.EnableWindow(a.controls[idLeafDirsMonth], layout.HasSequence)
	if layout.HasSequence {
		a.setTextNoFlicker(a.periodCountLabel, "每"+periodUnitName(layout.PeriodName)+"分组数")
	}
	a.setTextNoFlicker(a.filesPerLeafLabel, "每目录文件数")
}

func periodUnitName(periodName string) string {
	switch periodName {
	case "年份":
		return "年"
	case "月份":
		return "月"
	default:
		return periodName
	}
}

func hierarchyRuleExample(rule hierarchyRule) string {
	if rule.preset < 0 || rule.preset >= len(hierarchyPresets) {
		return ""
	}
	preset := hierarchyPresets[rule.preset]
	value := preset.pattern
	if preset.needsInput {
		value = strings.TrimSpace(rule.value)
	}
	if value == "" {
		return preset.description
	}
	sample := strings.NewReplacer("{YYYY}", "2026", "{Q}", "1", "{MM}", "01", "{NN}", "01").Replace(value)
	return preset.description + " 示例：" + sample
}
