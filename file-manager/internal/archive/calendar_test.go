package archive

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCalendarLeafPathCrossesQuarterAndYear(t *testing.T) {
	tests := map[int]string{
		1:  filepath.Join("2021", "2021S1", "202101", "20210101"),
		7:  filepath.Join("2021", "2021S1", "202103", "20210301"),
		19: filepath.Join("2021", "2021S3", "202107", "20210701"),
		37: filepath.Join("2022", "2022S1", "202201", "20220101"),
	}
	for leaf, want := range tests {
		if got := CalendarLeafPath(2021, leaf, 3); got != want {
			t.Fatalf("leaf %d = %q, want %q", leaf, got, want)
		}
	}
}

func TestCalculateCapacity(t *testing.T) {
	result := CalculateCapacity(109, PlanConfig{StartYear: 2021, LeafDirsPerMonth: 3, FilesPerLeaf: 10})
	if result.RequiredLeafDirs != 11 || result.RequiredMonths != 4 || result.RequiredQuarters != 2 || result.RequiredYears != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.LastLeafFileCount != 9 {
		t.Fatalf("last leaf count = %d", result.LastLeafFileCount)
	}
}

func TestParseExtensions(t *testing.T) {
	got, err := ParseExtensions("JPG, .png；jpg")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != ".jpg" || got[1] != ".png" {
		t.Fatalf("extensions = %#v", got)
	}
}

func TestScanFilesMatchesCompoundExtension(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "backup.TAR.GZ")
	if err := os.WriteFile(path, []byte("archive"), 0644); err != nil {
		t.Fatal(err)
	}
	extensions, err := ParseExtensions(".gz,.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	result := ScanFilesWithProgress(t.Context(), root, nil, extensions, nil)
	if result.MatchedCount != 1 || len(result.Files) != 1 || result.Files[0].Ext != ".tar.gz" {
		t.Fatalf("unexpected scan result: %+v", result)
	}
}

func TestBuildMovePlanUsesCalendarHierarchy(t *testing.T) {
	target := t.TempDir()
	files := make([]VideoFile, 7)
	for i := range files {
		files[i] = VideoFile{
			SourcePath: filepath.Join(t.TempDir(), "file.txt"),
			Name:       "file.txt",
			Size:       1,
			ModTime:    time.Unix(int64(i), 0),
		}
	}
	plan := BuildMovePlanContext(nil, files, PlanConfig{
		TargetDir:        target,
		StartYear:        2021,
		LeafDirsPerMonth: 2,
		FilesPerLeaf:     2,
	})
	if plan.ErrorCount != 0 || len(plan.Items) != 7 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	wantDir := filepath.Join(target, "2021", "2021S1", "202102", "20210202")
	if got := filepath.Dir(plan.Items[6].TargetPath); got != wantDir {
		t.Fatalf("last target dir = %q, want %q", got, wantDir)
	}
}

func TestBuildMovePlanAddsDuplicateSuffix(t *testing.T) {
	target := t.TempDir()
	leaf := filepath.Join(target, "2021", "2021S1", "202101", "20210101")
	if err := os.MkdirAll(leaf, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "same.pdf"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	plan := BuildMovePlanContext(nil, []VideoFile{{SourcePath: "source.pdf", Name: "same.pdf", Size: 1}}, PlanConfig{
		TargetDir: target, StartYear: 2021, LeafDirsPerMonth: 2, FilesPerLeaf: 2,
	})
	if len(plan.Items) != 1 || !plan.Items[0].Conflict || filepath.Base(plan.Items[0].TargetPath) != "same_dup001.pdf" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestBuildMovePlanPreservesCompoundExtensionOnConflict(t *testing.T) {
	target := t.TempDir()
	leaf := filepath.Join(target, "2021", "2021S1", "202101", "20210101")
	if err := os.MkdirAll(leaf, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "backup.tar.gz"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	plan := BuildMovePlanContext(t.Context(), []VideoFile{{SourcePath: "backup.tar.gz", Name: "backup.tar.gz", Ext: ".tar.gz", Size: 1}}, PlanConfig{
		TargetDir: target, StartYear: 2021, LeafDirsPerMonth: 2, FilesPerLeaf: 2, Extensions: []string{".tar.gz"},
	})
	if len(plan.Items) != 1 || !plan.Items[0].Conflict || filepath.Base(plan.Items[0].TargetPath) != "backup_dup001.tar.gz" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestBuildMovePlanSkipsExistingFullLeaf(t *testing.T) {
	target := t.TempDir()
	firstLeaf := filepath.Join(target, "2021", "2021S1", "202101", "20210101")
	if err := os.MkdirAll(firstLeaf, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"existing-1.pdf", "existing-2.pdf"} {
		if err := os.WriteFile(filepath.Join(firstLeaf, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	plan := BuildMovePlanContext(t.Context(), []VideoFile{{SourcePath: "new.pdf", Name: "new.pdf", Ext: ".pdf", Size: 1}}, PlanConfig{
		TargetDir: target, StartYear: 2021, LeafDirsPerMonth: 2, FilesPerLeaf: 2, Extensions: []string{".pdf"},
	})
	wantDir := filepath.Join(target, "2021", "2021S1", "202101", "20210102")
	if plan.ErrorCount != 0 || len(plan.Items) != 1 || filepath.Dir(plan.Items[0].TargetPath) != wantDir {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestScanFilesExcludesEquivalentExtendedPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "_Archived")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.pdf"), []byte("source"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "archived.pdf"), []byte("archived"), 0644); err != nil {
		t.Fatal(err)
	}
	result := ScanFilesWithProgress(t.Context(), root, []string{`\\?\` + target}, []string{".pdf"}, nil)
	if result.MatchedCount != 1 || len(result.Files) != 1 || result.Files[0].Name != "source.pdf" {
		t.Fatalf("unexpected scan result: %+v", result)
	}
}

func TestExecuteMovePlanStopsWhenTargetCapacityChanges(t *testing.T) {
	sourceDir := t.TempDir()
	target := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "new.pdf")
	if err := os.WriteFile(sourcePath, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	plan := BuildMovePlanContext(t.Context(), []VideoFile{{
		SourcePath: sourcePath,
		Name:       "new.pdf",
		Ext:        ".pdf",
		Size:       info.Size(),
		ModTime:    info.ModTime(),
	}}, PlanConfig{
		TargetDir: target, StartYear: 2021, LeafDirsPerMonth: 2, FilesPerLeaf: 2, Extensions: []string{".pdf"},
	})
	leaf := filepath.Dir(plan.Items[0].TargetPath)
	if err := os.MkdirAll(leaf, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"external-1.pdf", "external-2.pdf"} {
		if err := os.WriteFile(filepath.Join(leaf, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	summary := ExecuteMovePlan(t.Context(), plan, MoveOptions{ManifestDir: t.TempDir()}, nil)
	if summary.Moved != 0 || summary.Failed != 1 || summary.Error == "" {
		t.Fatalf("unexpected move summary: %+v", summary)
	}
	if _, err := os.Stat(sourcePath); err != nil {
		t.Fatalf("source should remain after capacity change: %v", err)
	}
}

func TestScanFilesHonorsPreCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := ScanFilesWithProgress(ctx, t.TempDir(), nil, []string{".pdf"}, nil)
	if !result.Cancelled || len(result.Files) != 0 {
		t.Fatalf("unexpected scan result: %+v", result)
	}
}
