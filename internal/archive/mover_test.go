package archive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCopyVerifyDeleteRejectsSourceChangedDuringCopy(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.mp4")
	target := filepath.Join(root, "target.mp4")
	if err := os.WriteFile(source, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	err = copyVerifyDeleteWithHooks(context.Background(), source, target, info, func() {
		changed := info.ModTime().Add(time.Hour)
		if changeErr := os.Chtimes(source, changed, changed); changeErr != nil {
			t.Fatalf("change source time: %v", changeErr)
		}
	}, os.Chtimes)
	if err == nil || !strings.Contains(err.Error(), "source file changed during copy") {
		t.Fatalf("copy error = %v", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("changed source must remain: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("invalid target must be removed: %v", err)
	}
}

func TestCopyVerifyDeleteRevalidatesAndDeletesStableSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.mp4")
	target := filepath.Join(root, "target.mp4")
	if err := os.WriteFile(source, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyVerifyDelete(context.Background(), source, target, info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("stable source should be deleted: %v", err)
	}
	targetInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if targetInfo.Size() != info.Size() || !targetInfo.ModTime().Equal(info.ModTime()) {
		t.Fatalf("target metadata = size %d, time %s; want size %d, time %s", targetInfo.Size(), targetInfo.ModTime(), info.Size(), info.ModTime())
	}
}

func TestCopyVerifyDeletePreservesSourceWhenTargetTimeFails(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.mp4")
	target := filepath.Join(root, "target.mp4")
	if err := os.WriteFile(source, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	err = copyVerifyDeleteWithHooks(context.Background(), source, target, info, nil, func(string, time.Time, time.Time) error {
		return errors.New("timestamps unsupported")
	})
	if err == nil || !strings.Contains(err.Error(), "cannot preserve target modification time") {
		t.Fatalf("copy error = %v", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source must remain when timestamps fail: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target with invalid metadata must be removed: %v", err)
	}
}

func TestManifestHasUndoableItems(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "manifest.tsv")
	header := "status\tsource\ttarget\tsize\tconflict\terror\n"
	if err := os.WriteFile(manifest, []byte(header), 0644); err != nil {
		t.Fatal(err)
	}
	if ManifestHasUndoableItems(manifest) {
		t.Fatal("header-only manifest must not enable undo")
	}
	row := "moved\tD:\\source.mp4\tD:\\target.mp4\t5\tfalse\t\n"
	if err := os.WriteFile(manifest, []byte(header+row), 0644); err != nil {
		t.Fatal(err)
	}
	if !ManifestHasUndoableItems(manifest) {
		t.Fatal("manifest with moved item must enable undo")
	}
	if ManifestHasUndoableItems(filepath.Join(t.TempDir(), "missing.tsv")) {
		t.Fatal("missing manifest must not enable undo")
	}
}

func TestExecuteMovePlanMovesFilesAndWritesManifest(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	targetDir := filepath.Join(root, "target")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(sourceDir, "a.mp4")
	if err := os.WriteFile(sourcePath, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}

	plan := BuildMovePlan([]VideoFile{
		{SourcePath: sourcePath, Name: "a.mp4", Size: 5},
	}, PlanConfig{
		TargetDir:       targetDir,
		LevelCount:      1,
		LevelNames:      []string{"Episode"},
		FoldersPerLevel: []int{5},
		FilesPerLeaf:    30,
	})

	summary := ExecuteMovePlan(context.Background(), plan, MoveOptions{}, nil)
	if summary.Moved != 1 || summary.Failed != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	if _, err := os.Stat(sourcePath); !os.IsNotExist(err) {
		t.Fatalf("source still exists or unexpected stat error: %v", err)
	}
	targetPath := filepath.Join(targetDir, "Episode_001", "a.mp4")
	if data, err := os.ReadFile(targetPath); err != nil || string(data) != "video" {
		t.Fatalf("target read = %q, %v", string(data), err)
	}
	if summary.ManifestPath == "" {
		t.Fatal("manifest path is empty")
	}
	if _, err := os.Stat(summary.ManifestPath); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
}

func TestCleanupEmptyDirs(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "a", "b")
	nonEmpty := filepath.Join(root, "keep")
	if err := os.MkdirAll(empty, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nonEmpty, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "x.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	removed, errs := CleanupEmptyDirs(context.Background(), root, nil)
	if len(errs) != 0 {
		t.Fatalf("cleanup errors: %v", errs)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	if _, err := os.Stat(nonEmpty); err != nil {
		t.Fatalf("non-empty dir should remain: %v", err)
	}
}

func TestPreviewEmptyDirsProtectsTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "_Archived")
	oldEmpty := filepath.Join(root, "old")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(oldEmpty, 0755); err != nil {
		t.Fatal(err)
	}

	dirs, errs := PreviewEmptyDirs(context.Background(), root, []string{target})
	if len(errs) != 0 {
		t.Fatalf("preview errors: %v", errs)
	}
	if len(dirs) != 1 || dirs[0] != oldEmpty {
		t.Fatalf("dirs = %v, want [%s]", dirs, oldEmpty)
	}
}

func TestCleanupEmptyDirsProtectsExtendedPrefixTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "_Archived")
	protectedEmpty := filepath.Join(target, "empty")
	removableEmpty := filepath.Join(root, "old", "empty")
	for _, dir := range []string{protectedEmpty, removableEmpty} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	removed, errs := CleanupEmptyDirs(context.Background(), root, []string{`\\?\` + target})
	if len(errs) != 0 {
		t.Fatalf("cleanup errors: %v", errs)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	if _, err := os.Stat(protectedEmpty); err != nil {
		t.Fatalf("extended-prefix protected target was modified: %v", err)
	}
}

func TestUndoManifestRestoresMovedFile(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	targetDir := filepath.Join(root, "target")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(sourceDir, "a.mp4")
	if err := os.WriteFile(sourcePath, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	plan := BuildMovePlan([]VideoFile{
		{SourcePath: sourcePath, Name: "a.mp4", Size: 5},
	}, PlanConfig{
		TargetDir:       targetDir,
		LevelCount:      1,
		LevelNames:      []string{"Episode"},
		FoldersPerLevel: []int{5},
		FilesPerLeaf:    30,
	})
	moveSummary := ExecuteMovePlan(context.Background(), plan, MoveOptions{}, nil)
	if moveSummary.Moved != 1 {
		t.Fatalf("moveSummary = %+v", moveSummary)
	}

	undoSummary := UndoManifest(context.Background(), moveSummary.ManifestPath, nil)
	if undoSummary.Restored != 1 || undoSummary.Failed != 0 {
		t.Fatalf("undoSummary = %+v", undoSummary)
	}
	if data, err := os.ReadFile(sourcePath); err != nil || string(data) != "video" {
		t.Fatalf("source restore read = %q, %v", string(data), err)
	}
	secondUndo := UndoManifest(context.Background(), moveSummary.ManifestPath, nil)
	if secondUndo.Restored != 1 || secondUndo.Failed != 0 {
		t.Fatalf("idempotent undo summary = %+v", secondUndo)
	}
}

func TestExecuteMovePlanStopsWhenManifestCannotBeCreated(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "a.mp4")
	if err := os.WriteFile(sourcePath, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	invalidManifestDir := filepath.Join(root, "manifest-is-a-file")
	if err := os.WriteFile(invalidManifestDir, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	plan := BuildMovePlan([]VideoFile{{SourcePath: sourcePath, Name: "a.mp4", Size: 5}}, PlanConfig{
		TargetDir: filepath.Join(root, "target"), LevelCount: 1, LevelNames: []string{"Episode"},
		FoldersPerLevel: []int{5}, FilesPerLeaf: 30,
	})
	summary := ExecuteMovePlan(context.Background(), plan, MoveOptions{ManifestDir: invalidManifestDir}, nil)
	if summary.Error == "" || summary.Moved != 0 || summary.Failed != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	if _, err := os.Stat(sourcePath); err != nil {
		t.Fatalf("source must remain untouched: %v", err)
	}
}

func TestUndoManifestRejectsChangedArchivedFile(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source", "a.mp4")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	plan := BuildMovePlan([]VideoFile{{SourcePath: sourcePath, Name: "a.mp4", Size: 5}}, PlanConfig{
		TargetDir: filepath.Join(root, "target"), LevelCount: 1, LevelNames: []string{"Episode"},
		FoldersPerLevel: []int{5}, FilesPerLeaf: 30,
	})
	moveSummary := ExecuteMovePlan(context.Background(), plan, MoveOptions{}, nil)
	if moveSummary.Moved != 1 {
		t.Fatalf("moveSummary = %+v", moveSummary)
	}
	targetPath := plan.Items[0].TargetPath
	if err := os.WriteFile(targetPath, []byte("changed-content"), 0644); err != nil {
		t.Fatal(err)
	}
	undoSummary := UndoManifest(context.Background(), moveSummary.ManifestPath, nil)
	if undoSummary.Restored != 0 || undoSummary.Failed != 1 {
		t.Fatalf("undoSummary = %+v", undoSummary)
	}
	if _, err := os.Stat(sourcePath); !os.IsNotExist(err) {
		t.Fatalf("changed file must not be restored, stat error: %v", err)
	}
}

func TestExecuteMovePlanRejectsSourceChangedAfterDryRun(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(sourceDir, "a.mp4")
	if err := os.WriteFile(sourcePath, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	scan := ScanVideos(context.Background(), sourceDir, nil)
	if len(scan.Files) != 1 {
		t.Fatalf("scan files = %d", len(scan.Files))
	}
	plan := BuildMovePlan(scan.Files, PlanConfig{
		TargetDir: filepath.Join(root, "target"), LevelCount: 1, LevelNames: []string{"Episode"},
		FoldersPerLevel: []int{5}, FilesPerLeaf: 30,
	})
	changedTime := scan.Files[0].ModTime.Add(time.Hour)
	if err := os.Chtimes(sourcePath, changedTime, changedTime); err != nil {
		t.Fatal(err)
	}

	summary := ExecuteMovePlan(context.Background(), plan, MoveOptions{}, nil)
	if summary.Moved != 0 || summary.Failed != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	if _, err := os.Stat(sourcePath); err != nil {
		t.Fatalf("changed source must remain: %v", err)
	}
}
