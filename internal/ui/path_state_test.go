package ui

import (
	"path/filepath"
	"testing"

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
