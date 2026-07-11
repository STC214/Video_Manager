package ui

import (
	"path/filepath"
	"testing"

	"github.com/lxn/win"
	"video-manager/internal/archive"
)

func TestShouldFollowSourceTarget(t *testing.T) {
	oldSource := filepath.Join("Z:\\", "videos", "first")
	tests := []struct {
		name   string
		target string
		want   bool
	}{
		{name: "empty target", target: "", want: true},
		{name: "automatic target", target: filepath.Join(oldSource, "_Archived"), want: true},
		{name: "automatic target case insensitive", target: filepath.Join("z:\\VIDEOS\\FIRST", "_archived"), want: true},
		{name: "custom target", target: filepath.Join("Z:\\", "archive", "second"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldFollowSourceTarget(oldSource, tt.target); got != tt.want {
				t.Fatalf("shouldFollowSourceTarget(%q, %q) = %v, want %v", oldSource, tt.target, got, tt.want)
			}
		})
	}
}

func TestSamePlanConfig(t *testing.T) {
	base := archive.PlanConfig{
		TargetDir:       `Z:\archive`,
		LevelCount:      2,
		LevelNames:      []string{"Season", "Episode"},
		FoldersPerLevel: []int{5, 10},
		FilesPerLeaf:    30,
	}
	copyConfig := func() archive.PlanConfig {
		cfg := base
		cfg.LevelNames = append([]string(nil), base.LevelNames...)
		cfg.FoldersPerLevel = append([]int(nil), base.FoldersPerLevel...)
		return cfg
	}

	if !samePlanConfig(base, copyConfig()) {
		t.Fatal("identical configs should match")
	}
	extendedTarget := copyConfig()
	extendedTarget.TargetDir = `\\?\Z:\archive`
	if !samePlanConfig(base, extendedTarget) {
		t.Fatal("equivalent extended-prefix target should match")
	}
	changed := copyConfig()
	changed.FilesPerLeaf++
	if samePlanConfig(base, changed) {
		t.Fatal("changed files-per-leaf should invalidate the plan")
	}
	changed = copyConfig()
	changed.LevelNames[1] = "Part"
	if samePlanConfig(base, changed) {
		t.Fatal("changed level name should invalidate the plan")
	}
	changed = copyConfig()
	changed.TargetDir = `Z:\other`
	if samePlanConfig(base, changed) {
		t.Fatal("changed target should invalidate the plan")
	}
}

func TestPlanErrorMessagesDeduplicatesAndLimits(t *testing.T) {
	plan := archive.MovePlan{
		ErrorCount: 3,
		Items: []archive.MovePlanItem{
			{Error: "target directory is not readable"},
			{Error: "target directory is not readable"},
			{Error: "source file is unavailable"},
		},
	}
	if got := planErrorMessages(plan, 10); len(got) != 2 || got[0] != "target directory is not readable" || got[1] != "source file is unavailable" {
		t.Fatalf("planErrorMessages() = %v", got)
	}
	if got := planErrorMessages(plan, 1); len(got) != 1 || got[0] != "target directory is not readable" {
		t.Fatalf("planErrorMessages(limit=1) = %v", got)
	}
}

func TestBrowseCommandDoesNotMarkConfigurationEditedUntilPathChanges(t *testing.T) {
	if isConfigurationCommand(idBrowse, win.BN_CLICKED) || isConfigurationCommand(idBrowseTarget, win.BN_CLICKED) {
		t.Fatal("opening or cancelling a folder picker must not count as a configuration edit")
	}
	if !isConfigurationCommand(idSourceEdit, win.EN_CHANGE) || !isConfigurationCommand(idTargetEdit, win.EN_CHANGE) {
		t.Fatal("an actual path text change must count as a configuration edit")
	}
}

func TestManifestAfterMoveKeepsOnlyUndoableRun(t *testing.T) {
	if got, changed := manifestAfterMove("previous.tsv", archive.MoveSummary{ManifestPath: "failed.tsv", Moved: 0}); got != "previous.tsv" || changed {
		t.Fatalf("zero-move result = %q, %v", got, changed)
	}
	if got, changed := manifestAfterMove("", archive.MoveSummary{ManifestPath: "failed.tsv", Moved: 0}); got != "" || changed {
		t.Fatalf("zero-move result without previous manifest = %q, %v", got, changed)
	}
	if got, changed := manifestAfterMove("previous.tsv", archive.MoveSummary{ManifestPath: "new.tsv", Moved: 1}); got != "new.tsv" || !changed {
		t.Fatalf("successful move result = %q, %v", got, changed)
	}
}

func TestPrependLogMessageKeepsNewestFirst(t *testing.T) {
	got := prependLogMessage("second\r\nfirst", "third", 3)
	if got != "third\r\nsecond\r\nfirst" {
		t.Fatalf("prependLogMessage() = %q", got)
	}
	got = prependLogMessage(got, "fourth", 3)
	if got != "fourth\r\nthird\r\nsecond" {
		t.Fatalf("limited prependLogMessage() = %q", got)
	}
}

func TestDryRunProgressMessagesMatchStages(t *testing.T) {
	wants := []string{
		"",
		"Dry-run 进度 1/4: 目标目录校验完成。",
		"Dry-run 进度 2/4: 移动计划生成完成。",
		"Dry-run 进度 3/4: 空目录预览完成。",
		"Dry-run 进度 4/4: TSV 导出完成。",
	}
	for done, want := range wants {
		if got := dryRunProgressMessage(done); got != want {
			t.Fatalf("dryRunProgressMessage(%d) = %q, want %q", done, got, want)
		}
	}
}
