package archive

import (
	"context"
	"os"
	"path/filepath"
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

func TestBuildMovePlanInsufficientCapacity(t *testing.T) {
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

	if plan.ErrorCount != len(files) {
		t.Fatalf("ErrorCount = %d, want %d", plan.ErrorCount, len(files))
	}
	if plan.Items[0].Status != "error" {
		t.Fatalf("Status = %q, want error", plan.Items[0].Status)
	}
}
