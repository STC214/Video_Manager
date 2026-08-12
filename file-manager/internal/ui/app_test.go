package ui

import (
	"reflect"
	"testing"
	"time"

	"video-manager/file-manager/internal/archive"
)

func TestShouldPublishMoveProgress(t *testing.T) {
	last := time.Unix(100, 0)
	tests := []struct {
		name         string
		now          time.Time
		index, total int
		want         bool
	}{
		{name: "initial update", now: last.Add(moveProgressInterval), index: 0, total: 10, want: true},
		{name: "intermediate update is throttled", now: last.Add(moveProgressInterval - time.Millisecond), index: 4, total: 10, want: false},
		{name: "intermediate update at interval", now: last.Add(moveProgressInterval), index: 4, total: 10, want: true},
		{name: "final update bypasses throttle", now: last.Add(time.Millisecond), index: 10, total: 10, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldPublishMoveProgress(tt.now, last, tt.index, tt.total); got != tt.want {
				t.Fatalf("shouldPublishMoveProgress() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultHierarchyRulesProduceValidTemplates(t *testing.T) {
	for depth := 1; depth <= maxHierarchyDepth; depth++ {
		rules := defaultHierarchyRules(depth)
		template, err := hierarchyTemplateFromRules(rules)
		if err != nil {
			t.Fatalf("depth %d: hierarchyTemplateFromRules() error = %v", depth, err)
		}
		if got := archive.PathTemplateDepth(template); got != depth {
			t.Fatalf("depth %d: template %q has depth %d", depth, template, got)
		}
	}
}

func TestThreeLayerDefaultOmitsQuarter(t *testing.T) {
	template, err := hierarchyTemplateFromRules(defaultHierarchyRules(3))
	if err != nil {
		t.Fatal(err)
	}
	want := `{YYYY}\{YYYY}{MM}\{YYYY}{MM}{NN}`
	if template != want {
		t.Fatalf("template = %q, want %q", template, want)
	}
}

func TestTwoLayerDefaultUsesYearAndQuarterDirectly(t *testing.T) {
	template, err := hierarchyTemplateFromRules(defaultHierarchyRules(2))
	if err != nil {
		t.Fatal(err)
	}
	want := `{YYYY}\{YYYY}S{Q}`
	if template != want {
		t.Fatalf("template = %q, want %q", template, want)
	}
	layout, err := archive.AnalyzePathTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	if layout.PeriodName != "季度" || layout.HasSequence {
		t.Fatalf("layout = %+v, want quarter without sequence", layout)
	}
}

func TestArchiveRuleSummaryAdaptsToFinalPeriod(t *testing.T) {
	quarter := archive.PlanConfig{StartYear: 2026, LeafDirsPerMonth: 9, FilesPerLeaf: 30, PathTemplate: `{YYYY}\{YYYY}S{Q}`}
	if got := archiveRuleSummary(quarter); got != "目录规则: 从 2026 年开始，以季度目录作为最终容器，每个目录最多 30 个文件。" {
		t.Fatalf("quarter summary = %q", got)
	}
	monthGroups := archive.PlanConfig{StartYear: 2026, LeafDirsPerMonth: 4, FilesPerLeaf: 30, PathTemplate: `{YYYY}\{YYYY}{MM}\第{NN}组`}
	if got := archiveRuleSummary(monthGroups); got != "目录规则: 从 2026 年开始，每月 4 个分组目录，每个目录最多 30 个文件。" {
		t.Fatalf("month summary = %q", got)
	}
}

func TestFiveLayerFixedNameTemplate(t *testing.T) {
	rules := defaultHierarchyRules(5)
	rules[1].value = "作品归档"
	template, err := hierarchyTemplateFromRules(rules)
	if err != nil {
		t.Fatal(err)
	}
	want := `{YYYY}\作品归档\{YYYY}S{Q}\{YYYY}{MM}\{YYYY}{MM}{NN}`
	if template != want {
		t.Fatalf("template = %q, want %q", template, want)
	}
}

func TestHierarchyRulesRoundTripKnownAndCustomParts(t *testing.T) {
	templates := []string{
		archive.DefaultPathTemplate,
		`{YYYY}\作品归档\{YYYY}{MM}\第{NN}组`,
		`{YYYY}\{YYYY}年第{MM}月\第{NN}组`,
	}
	for _, template := range templates {
		rules := hierarchyRulesFromTemplate(template)
		got, err := hierarchyTemplateFromRules(rules)
		if err != nil {
			t.Fatalf("template %q: %v", template, err)
		}
		if !reflect.DeepEqual(got, template) {
			t.Fatalf("round trip = %q, want %q", got, template)
		}
	}
}

func TestHierarchyTemplateExplainsMissingCustomValue(t *testing.T) {
	_, err := hierarchyTemplateFromRules([]hierarchyRule{
		{preset: presetYear},
		{preset: presetMonth},
		{preset: presetCustom},
	})
	if err == nil || err.Error() != "请填写第 3 层的名称或规则" {
		t.Fatalf("error = %v", err)
	}
}

func TestShouldPublishMoveProgressBoundsBurst(t *testing.T) {
	const total = 1826
	now := time.Unix(100, 0)
	last := now.Add(-moveProgressInterval)
	published := 0
	for index := 0; index < total; index++ {
		if shouldPublishMoveProgress(now, last, index, total) {
			published++
			last = now
		}
		now = now.Add(time.Millisecond)
	}
	if shouldPublishMoveProgress(now, last, total, total) {
		published++
	}

	if published > 10 {
		t.Fatalf("published %d updates for %d rapid events; want at most 10", published, total+1)
	}
	if published < 2 {
		t.Fatalf("published %d updates; want initial and final updates", published)
	}
}

func TestSamePlanConfigIncludesPathTemplate(t *testing.T) {
	base := archive.PlanConfig{TargetDir: `C:\target`, StartYear: 2021, LeafDirsPerMonth: 4, FilesPerLeaf: 30, PathTemplate: archive.DefaultPathTemplate, Extensions: []string{".pdf"}}
	changed := base
	changed.PathTemplate = `{YYYY}\{YYYY}{MM}\{YYYY}{MM}{NN}`
	if samePlanConfig(base, changed) {
		t.Fatal("path template change must invalidate the existing plan")
	}
}
