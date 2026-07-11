package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBuildMovePlan(t *testing.T) {
	files := []VideoFile{
		{SourcePath: `D:\in\a.mp4`, Name: "a.mp4", Size: 1},
		{SourcePath: `D:\in\b.mp4`, Name: "b.mp4", Size: 1},
		{SourcePath: `D:\in\c.mp4`, Name: "c.mp4", Size: 1},
	}
	plan := BuildMovePlan(files, PlanConfig{
		TargetDir:       `D:\out`,
		LevelCount:      2,
		LevelNames:      []string{"Season", "Episode"},
		FoldersPerLevel: []int{2, 2},
		FilesPerLeaf:    2,
	})

	if len(plan.Items) != 3 {
		t.Fatalf("len(plan.Items) = %d, want 3", len(plan.Items))
	}
	want0 := filepath.Join(`D:\out`, "Season_001", "Episode_001", "a.mp4")
	want2 := filepath.Join(`D:\out`, "Season_001", "Episode_002", "c.mp4")
	if plan.Items[0].TargetPath != want0 {
		t.Fatalf("TargetPath[0] = %q, want %q", plan.Items[0].TargetPath, want0)
	}
	if plan.Items[2].TargetPath != want2 {
		t.Fatalf("TargetPath[2] = %q, want %q", plan.Items[2].TargetPath, want2)
	}
	if plan.TargetDirCount != 2 {
		t.Fatalf("TargetDirCount = %d, want 2", plan.TargetDirCount)
	}
}

func TestBuildMovePlanAssignsEarlierFilesToLowerLeafNumbers(t *testing.T) {
	early := time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local)
	late := early.Add(24 * time.Hour)
	files := []VideoFile{
		{SourcePath: `D:\in\late.mp4`, RelPath: "late.mp4", Name: "late.mp4", Size: 1, ModTime: late},
		{SourcePath: `D:\in\early.mp4`, RelPath: "early.mp4", Name: "early.mp4", Size: 1, ModTime: early},
	}
	plan := BuildMovePlan(files, PlanConfig{
		TargetDir: `D:\out`, LevelCount: 1, LevelNames: []string{"Episode"},
		FoldersPerLevel: []int{2}, FilesPerLeaf: 1,
	})
	if plan.Items[0].SourcePath != `D:\in\early.mp4` || filepath.Base(filepath.Dir(plan.Items[0].TargetPath)) != "Episode_001" {
		t.Fatalf("earliest item assignment = %+v", plan.Items[0])
	}
	if plan.Items[1].SourcePath != `D:\in\late.mp4` || filepath.Base(filepath.Dir(plan.Items[1].TargetPath)) != "Episode_002" {
		t.Fatalf("latest item assignment = %+v", plan.Items[1])
	}
}

func TestBuildMovePlanContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	plan := BuildMovePlanContext(ctx, []VideoFile{{SourcePath: `D:\in\a.mp4`, Name: "a.mp4", Size: 1}}, PlanConfig{
		TargetDir: `D:\out`, LevelCount: 1, LevelNames: []string{"Episode"},
		FoldersPerLevel: []int{5}, FilesPerLeaf: 5,
	})
	if len(plan.Items) != 0 {
		t.Fatalf("cancelled plan contains %d items", len(plan.Items))
	}
}

func TestBuildMovePlanConflict(t *testing.T) {
	files := []VideoFile{
		{SourcePath: `D:\in\a.mp4`, Name: "a.mp4", Size: 1},
		{SourcePath: `D:\in2\a.mp4`, Name: "a.mp4", Size: 1},
	}
	plan := BuildMovePlan(files, PlanConfig{
		TargetDir:       `D:\out`,
		LevelCount:      1,
		LevelNames:      []string{"Episode"},
		FoldersPerLevel: []int{5},
		FilesPerLeaf:    5,
	})

	if plan.ConflictCount != 1 {
		t.Fatalf("ConflictCount = %d, want 1", plan.ConflictCount)
	}
	want := filepath.Join(`D:\out`, "Episode_001", "a_dup001.mp4")
	if plan.Items[1].TargetPath != want {
		t.Fatalf("TargetPath[1] = %q, want %q", plan.Items[1].TargetPath, want)
	}
}

func TestBuildMovePlanExistingTargetConflict(t *testing.T) {
	targetDir := t.TempDir()
	existing := filepath.Join(targetDir, "Episode_001", "a.mp4")
	if err := os.MkdirAll(filepath.Dir(existing), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}

	plan := BuildMovePlan([]VideoFile{
		{SourcePath: `D:\in\a.mp4`, Name: "a.mp4", Size: 1},
	}, PlanConfig{
		TargetDir:       targetDir,
		LevelCount:      1,
		LevelNames:      []string{"Episode"},
		FoldersPerLevel: []int{5},
		FilesPerLeaf:    5,
	})

	if plan.ConflictCount != 1 {
		t.Fatalf("ConflictCount = %d, want 1", plan.ConflictCount)
	}
	want := filepath.Join(targetDir, "Episode_001", "a_dup001.mp4")
	if plan.Items[0].TargetPath != want {
		t.Fatalf("TargetPath = %q, want %q", plan.Items[0].TargetPath, want)
	}
}

func TestExportMovePlanTSV(t *testing.T) {
	outputDir := t.TempDir()
	plan := MovePlan{
		TargetRoot: outputDir,
		Items: []MovePlanItem{
			{Status: "planned", SourcePath: `D:\in\a.mp4`, TargetPath: filepath.Join(outputDir, "Episode_001", "a.mp4"), Size: 1},
		},
	}
	path, err := ExportMovePlanTSV(plan, "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "status\tsource\ttarget") {
		t.Fatalf("unexpected TSV content: %s", string(data))
	}
}

func TestBuildMovePlanAutoExpandsInsufficientCapacity(t *testing.T) {
	files := []VideoFile{
		{SourcePath: `D:\in\a.mp4`, Name: "a.mp4", Size: 1},
		{SourcePath: `D:\in\b.mp4`, Name: "b.mp4", Size: 1},
		{SourcePath: `D:\in\c.mp4`, Name: "c.mp4", Size: 1},
	}
	plan := BuildMovePlan(files, PlanConfig{
		TargetDir:       `D:\out`,
		LevelCount:      1,
		LevelNames:      []string{"Episode"},
		FoldersPerLevel: []int{1},
		FilesPerLeaf:    2,
	})

	if plan.ErrorCount != 0 {
		t.Fatalf("ErrorCount = %d, want 0", plan.ErrorCount)
	}
	if !plan.AutoExpanded {
		t.Fatal("expected plan capacity to auto-expand")
	}
	if plan.ConfiguredCapacity != 2 || plan.EffectiveCapacity != 4 {
		t.Fatalf("capacities = configured %d, effective %d; want 2 and 4", plan.ConfiguredCapacity, plan.EffectiveCapacity)
	}
	if len(plan.EffectiveFolders) != 1 || plan.EffectiveFolders[0] != 2 {
		t.Fatalf("EffectiveFolders = %v, want [2]", plan.EffectiveFolders)
	}
	if plan.Items[2].Status != "planned" || filepath.Base(filepath.Dir(plan.Items[2].TargetPath)) != "Episode_002" {
		t.Fatalf("last item = %+v, want planned in Episode_002", plan.Items[2])
	}
}

func TestBuildMovePlanAutoExpandsFirstLevelOnly(t *testing.T) {
	files := make([]VideoFile, 294)
	for i := range files {
		files[i] = VideoFile{
			SourcePath: filepath.Join(`D:\in`, fmt.Sprintf("%03d.mp4", i)),
			Name:       fmt.Sprintf("%03d.mp4", i),
			Size:       1,
		}
	}
	plan := BuildMovePlan(files, PlanConfig{
		TargetDir:       `D:\out`,
		LevelCount:      2,
		LevelNames:      []string{"Arc", "Season"},
		FoldersPerLevel: []int{5, 5},
		FilesPerLeaf:    10,
	})
	if plan.ErrorCount != 0 || !plan.AutoExpanded {
		t.Fatalf("plan errors = %d, auto-expanded = %v", plan.ErrorCount, plan.AutoExpanded)
	}
	if got, want := plan.EffectiveFolders, []int{6, 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("EffectiveFolders = %v, want %v", got, want)
	}
	if plan.ConfiguredCapacity != 250 || plan.EffectiveCapacity != 300 {
		t.Fatalf("capacities = configured %d, effective %d; want 250 and 300", plan.ConfiguredCapacity, plan.EffectiveCapacity)
	}
	if got := filepath.Dir(plan.Items[len(plan.Items)-1].TargetPath); got != filepath.Join(`D:\out`, "Arc_006", "Season_030") {
		t.Fatalf("last target directory = %q", got)
	}
}

func TestBuildMovePlanAutoExpansionPreservesTargetReadError(t *testing.T) {
	target := t.TempDir()
	blockedDir := filepath.Join(target, "Episode_001")
	if err := os.WriteFile(blockedDir, []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}
	files := []VideoFile{
		{SourcePath: `D:\in\a.mp4`, Name: "a.mp4", Size: 1},
		{SourcePath: `D:\in\b.mp4`, Name: "b.mp4", Size: 1},
		{SourcePath: `D:\in\c.mp4`, Name: "c.mp4", Size: 1},
	}
	plan := BuildMovePlan(files, PlanConfig{
		TargetDir: target, LevelCount: 1, LevelNames: []string{"Episode"},
		FoldersPerLevel: []int{1}, FilesPerLeaf: 2,
	})
	if !plan.AutoExpanded {
		t.Fatal("expected capacity auto-expansion")
	}
	if plan.ErrorCount != 2 {
		t.Fatalf("ErrorCount = %d, want 2", plan.ErrorCount)
	}
	if strings.Contains(plan.Items[0].Error, "capacity") || !strings.Contains(plan.Items[0].Error, "target directory check failed") {
		t.Fatalf("unexpected plan error: %q", plan.Items[0].Error)
	}
	if plan.Items[2].Status != "planned" {
		t.Fatalf("item in auto-expanded directory = %+v", plan.Items[2])
	}
}

func TestBuildMovePlanRejectsFileInIntermediateTargetPath(t *testing.T) {
	target := t.TempDir()
	blockedAncestor := filepath.Join(target, "Arc_001")
	if err := os.WriteFile(blockedAncestor, []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}
	plan := BuildMovePlan([]VideoFile{
		{SourcePath: `D:\in\a.mp4`, Name: "a.mp4", Size: 1},
	}, PlanConfig{
		TargetDir: target, LevelCount: 2, LevelNames: []string{"Arc", "Season"},
		FoldersPerLevel: []int{1, 1}, FilesPerLeaf: 1,
	})
	if plan.ErrorCount != 1 {
		t.Fatalf("ErrorCount = %d, want 1", plan.ErrorCount)
	}
	if !strings.Contains(plan.Items[0].Error, "target path component is not a directory") ||
		!strings.Contains(plan.Items[0].Error, "Arc_001") {
		t.Fatalf("unexpected plan error: %q", plan.Items[0].Error)
	}
}
