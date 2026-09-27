package archive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

func TestCopyVerifyDeleteRejectsRoundedTargetModificationTime(t *testing.T) {
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
	rounded := info.ModTime().Truncate(2 * time.Second)
	if rounded.Equal(info.ModTime()) {
		rounded = rounded.Add(-time.Second)
	}
	err = copyVerifyDeleteWithHooks(t.Context(), source, target, info, nil, func(path string, _, _ time.Time) error {
		return os.Chtimes(path, rounded, rounded)
	})
	if err == nil || !strings.Contains(err.Error(), "target metadata preservation failed") {
		t.Fatalf("copy error = %v", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source must remain after target time mismatch: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target with rounded metadata must be removed: %v", err)
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

func TestManifestParserRejectsMalformedCriticalRows(t *testing.T) {
	header := "status\tsource\ttarget\tsize\tconflict\terror\tmod_time_rfc3339_nano\n"
	tests := map[string]string{
		"invalid time":   "moved\tsource\ttarget\t5\tfalse\t\tinvalid-time\n",
		"invalid size":   "moved\tsource\ttarget\tnot-a-size\tfalse\t\t2026-07-29T12:00:00Z\n",
		"truncated row":  "moved\tsource\ttarget\n",
		"unknown status": "mystery\tsource\ttarget\t5\tfalse\t\t2026-07-29T12:00:00Z\n",
		"extra column":   "moved\tsource\ttarget\t5\tfalse\t\t2026-07-29T12:00:00Z\textra\n",
	}
	for name, row := range tests {
		t.Run(name, func(t *testing.T) {
			manifest := filepath.Join(t.TempDir(), "manifest.tsv")
			if err := os.WriteFile(manifest, []byte(header+row), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := CheckManifestUndoable(manifest); err == nil {
				t.Fatal("malformed manifest unexpectedly passed validation")
			}
			summary := UndoManifest(t.Context(), manifest, nil)
			if summary.Error == "" || summary.Restored != 0 {
				t.Fatalf("malformed manifest was not rejected: %+v", summary)
			}
		})
	}
}

func TestManifestParserRejectsHeaderRowColumnMismatch(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "manifest.tsv")
	content := "status\tsource\ttarget\tsize\tconflict\terror\n" +
		"moved\tsource\ttarget\t5\tfalse\t\t2026-07-29T12:00:00Z\n"
	if err := os.WriteFile(manifest, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckManifestUndoable(manifest); err == nil {
		t.Fatal("six-column header with seven-column row unexpectedly passed")
	}
}

func TestManifestParserAcceptsLegacyNanosecondHeader(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "manifest.tsv")
	legacy := time.Date(2026, time.July, 29, 12, 0, 0, 123, time.UTC)
	content := "status\tsource\ttarget\tsize\tconflict\terror\tmod_time_unix_nano\n" +
		fmt.Sprintf("moved\tsource\ttarget\t5\tfalse\t\t%d\n", legacy.UnixNano())
	if err := os.WriteFile(manifest, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	available, err := CheckManifestUndoable(manifest)
	if err != nil || !available {
		t.Fatalf("legacy nanosecond manifest rejected: available=%t, err=%v", available, err)
	}
}

func TestManifestParserIgnoresDiagnosticRowWithEmptyTarget(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.mp4")
	target := filepath.Join(root, "target.mp4")
	if err := os.WriteFile(target, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "manifest.tsv")
	content := "status\tsource\ttarget\tsize\tconflict\terror\n" +
		fmt.Sprintf("moved\t%s\t%s\t5\tfalse\t\n", source, target) +
		"error\tunplanned-source\t\t5\tfalse\tinvalid plan\n"
	if err := os.WriteFile(manifest, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	available, err := CheckManifestUndoable(manifest)
	if err != nil || !available {
		t.Fatalf("diagnostic row prevented undo: available=%t, err=%v", available, err)
	}
	summary := UndoManifest(t.Context(), manifest, nil)
	if summary.Error != "" || summary.Restored != 1 || summary.Failed != 0 {
		t.Fatalf("unexpected undo summary: %+v", summary)
	}
}

func TestPendingManifestRecoversMoveWithoutCompletionRecord(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.mp4")
	target := filepath.Join(root, "target.mp4")
	if err := os.WriteFile(source, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, target); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "manifest.tsv")
	content := "status\tsource\ttarget\tsize\tconflict\terror\n" +
		fmt.Sprintf("pending\t%s\t%s\t5\tfalse\t\n", source, target)
	if err := os.WriteFile(manifest, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if !ManifestHasUndoableItems(manifest) {
		t.Fatal("recoverable pending move must enable undo")
	}
	summary := UndoManifest(t.Context(), manifest, nil)
	if summary.Error != "" || summary.Restored != 1 || summary.Failed != 0 {
		t.Fatalf("unexpected undo summary: %+v", summary)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "video" {
		t.Fatalf("pending move was not restored: %q, %v", data, err)
	}
}

func TestManifestTimeSupportsFullWindowsRangeAndLegacyNanos(t *testing.T) {
	for _, want := range []time.Time{
		time.Date(1601, time.January, 1, 0, 0, 0, 100, time.UTC),
		time.Date(2500, time.December, 31, 23, 59, 59, 999999900, time.UTC),
	} {
		field := manifestModTimeField(want)
		got, err := parseManifestTime(field)
		if err != nil || !got.Equal(want) {
			t.Fatalf("manifest time round trip = %s, %v; want %s", got, err, want)
		}
	}
	legacy := time.Date(2026, time.July, 29, 12, 0, 0, 123, time.UTC)
	got, err := parseManifestTime(strconv.FormatInt(legacy.UnixNano(), 10))
	if err != nil || !got.Equal(legacy) {
		t.Fatalf("legacy manifest time = %s, %v; want %s", got, err, legacy)
	}
}

func TestPendingManifestDistinguishesNotStartedAndAmbiguousMoves(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.mp4")
	target := filepath.Join(root, "target.mp4")
	if err := os.WriteFile(source, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "manifest.tsv")
	writePending := func() {
		t.Helper()
		content := "status\tsource\ttarget\tsize\tconflict\terror\n" +
			fmt.Sprintf("pending\t%s\t%s\t5\tfalse\t\n", source, target)
		if err := os.WriteFile(manifest, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	writePending()
	if ManifestHasUndoableItems(manifest) {
		t.Fatal("pending operation that did not start must not enable undo")
	}
	if err := os.WriteFile(target, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	writePending()
	if !ManifestHasUndoableItems(manifest) {
		t.Fatal("ambiguous pending operation must remain visible for recovery")
	}
	summary := UndoManifest(t.Context(), manifest, nil)
	if summary.Error == "" || summary.Restored != 0 {
		t.Fatalf("ambiguous pending operation was not rejected: %+v", summary)
	}
}

func TestUndoManifestHonorsPreCancelledContextWhileReadingManifest(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "manifest.tsv")
	if err := os.WriteFile(manifest, []byte("status\tsource\ttarget\tsize\tconflict\terror\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	summary := UndoManifest(ctx, manifest, nil)
	if !summary.Cancelled || summary.Failed != 0 || summary.Error != "" {
		t.Fatalf("unexpected cancelled undo summary: %+v", summary)
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

func TestUndoManifestRejectsSameSizeFileWithChangedModificationTime(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(sourceDir, "a.mp4")
	if err := os.WriteFile(sourcePath, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	scan := ScanVideos(t.Context(), sourceDir, nil)
	if len(scan.Files) != 1 {
		t.Fatalf("scan files = %d", len(scan.Files))
	}
	plan := BuildMovePlan(scan.Files, PlanConfig{
		TargetDir: filepath.Join(root, "target"), LevelCount: 1, LevelNames: []string{"Episode"},
		FoldersPerLevel: []int{5}, FilesPerLeaf: 30,
	})
	moveSummary := ExecuteMovePlan(t.Context(), plan, MoveOptions{}, nil)
	if moveSummary.Moved != 1 {
		t.Fatalf("moveSummary = %+v", moveSummary)
	}
	targetPath := plan.Items[0].TargetPath
	if err := os.WriteFile(targetPath, []byte("other"), 0644); err != nil {
		t.Fatal(err)
	}
	changedTime := scan.Files[0].ModTime.Add(2 * time.Hour)
	if err := os.Chtimes(targetPath, changedTime, changedTime); err != nil {
		t.Fatal(err)
	}
	undoSummary := UndoManifest(t.Context(), moveSummary.ManifestPath, nil)
	if undoSummary.Restored != 0 || undoSummary.Failed != 1 {
		t.Fatalf("undoSummary = %+v", undoSummary)
	}
	if _, err := os.Stat(sourcePath); !os.IsNotExist(err) {
		t.Fatalf("same-size changed file must not be restored, stat error: %v", err)
	}
	if data, err := os.ReadFile(targetPath); err != nil || string(data) != "other" {
		t.Fatalf("changed target was not preserved: %q, %v", data, err)
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

func TestMoveOneHonorsCancellationBeforeRename(t *testing.T) {
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
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := moveOne(ctx, MovePlanItem{
		SourcePath: source,
		TargetPath: target,
		Size:       info.Size(),
		ModTime:    info.ModTime(),
		Status:     "planned",
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("move error = %v, want context cancellation", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("cancelled move changed source: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("cancelled move created target: %v", err)
	}
}

func TestMoveOnePreservesLateTargetCollision(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.mp4")
	target := filepath.Join(root, "target.mp4")
	if err := os.WriteFile(source, []byte("new video"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	// The target appears after Dry-run chose this path.
	if err := os.WriteFile(target, []byte("existing video"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = moveOne(context.Background(), MovePlanItem{SourcePath: source, TargetPath: target, Size: info.Size(), ModTime: info.ModTime(), Status: "planned"})
	if err == nil {
		t.Fatal("late target collision was overwritten")
	}
	gotTarget, err := os.ReadFile(target)
	if err != nil || string(gotTarget) != "existing video" {
		t.Fatalf("target changed: %q, %v", gotTarget, err)
	}
	gotSource, err := os.ReadFile(source)
	if err != nil || string(gotSource) != "new video" {
		t.Fatalf("source changed: %q, %v", gotSource, err)
	}
}

func TestTargetMoveLockWaitsAndCancels(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	first, err := AcquireTargetMoveLock(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	if second, err := AcquireTargetMoveLock(ctx, root); !errors.Is(err, context.DeadlineExceeded) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second lock = %v, want deadline", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireTargetMoveLock(t.Context(), root)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	_ = second.Close()
}

func TestConcurrentMovePlansKeepLeafCapacity(t *testing.T) {
	root := t.TempDir()
	targetRoot := filepath.Join(root, "archive")
	leaf := filepath.Join(targetRoot, "Episode_001")
	if err := os.MkdirAll(leaf, 0755); err != nil {
		t.Fatal(err)
	}
	results := make(chan MoveSummary, 2)
	for _, name := range []string{"a.mp4", "b.mp4"} {
		source := filepath.Join(root, name)
		if err := os.WriteFile(source, []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(source)
		if err != nil {
			t.Fatal(err)
		}
		plan := MovePlan{TargetRoot: targetRoot, TargetDirFileLimit: 1, ManagedExtensions: []string{".mp4"}, Items: []MovePlanItem{{SourcePath: source, TargetPath: filepath.Join(leaf, name), Size: info.Size(), ModTime: info.ModTime(), Status: "planned"}}}
		go func() { results <- ExecuteMovePlan(t.Context(), plan, MoveOptions{}, nil) }()
	}
	first, second := <-results, <-results
	if first.Moved+second.Moved != 1 || first.Failed+second.Failed != 1 {
		t.Fatalf("concurrent summaries: %+v; %+v", first, second)
	}
	entries, err := os.ReadDir(leaf)
	if err != nil || len(entries) != 1 {
		t.Fatalf("leaf entries = %v, %v", entries, err)
	}
}

func TestExecuteMovePlanRejectsStaleTargetLock(t *testing.T) {
	root := t.TempDir()
	lock, err := AcquireTargetMoveLock(t.Context(), filepath.Join(root, "other"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	plan := MovePlan{TargetRoot: filepath.Join(root, "target"), Items: []MovePlanItem{{SourcePath: "source", TargetPath: "target"}}}
	summary := ExecuteMovePlan(t.Context(), plan, MoveOptions{TargetLock: lock}, nil)
	if summary.Moved != 0 || summary.Failed != 1 || !strings.Contains(summary.Error, "target move lock") {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestCopyVerifyDeleteKeepsLatePublishedTarget(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.mp4")
	target := filepath.Join(root, "target.mp4")
	if err := os.WriteFile(source, []byte("ours"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	err = copyVerifyDeleteWithHooks(t.Context(), source, target, info, func() {
		if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
			t.Fatalf("unverified target became visible: %v", statErr)
		}
		if writeErr := os.WriteFile(target, []byte("theirs"), 0644); writeErr != nil {
			t.Fatal(writeErr)
		}
	}, os.Chtimes)
	if err == nil || !strings.Contains(err.Error(), "cannot publish verified copy") {
		t.Fatalf("copy error = %v", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "theirs" {
		t.Fatalf("late target = %q, %v", data, err)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "ours" {
		t.Fatalf("source = %q, %v", data, err)
	}
	matches, err := filepath.Glob(filepath.Join(root, ".video-manager-copy-*.tmp"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("staging leftovers = %v, %v", matches, err)
	}
}

func TestUndoWaitsForTargetMoveLock(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.mp4")
	targetRoot := filepath.Join(root, "archive")
	if err := os.WriteFile(source, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	plan := BuildMovePlan([]VideoFile{{SourcePath: source, Name: "source.mp4", Size: info.Size(), ModTime: info.ModTime()}}, PlanConfig{
		TargetDir: targetRoot, LevelCount: 1, LevelNames: []string{"Episode"}, FoldersPerLevel: []int{1}, FilesPerLeaf: 1,
	})
	move := ExecuteMovePlan(t.Context(), plan, MoveOptions{ManifestDir: filepath.Join(root, "custom-manifests")}, nil)
	if move.Moved != 1 || move.Failed != 0 {
		t.Fatalf("move = %+v", move)
	}
	if got, err := manifestTargetRoot(move.ManifestPath); err != nil || !SamePath(got, targetRoot) {
		t.Fatalf("manifest target root = %q, %v", got, err)
	}
	lock, err := AcquireTargetMoveLock(t.Context(), targetRoot)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	undo := UndoManifest(ctx, move.ManifestPath, nil)
	if undo.Restored != 0 || undo.Failed != 1 || !strings.Contains(undo.Error, "cannot lock target for undo") {
		t.Fatalf("undo while locked = %+v", undo)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	undo = UndoManifest(t.Context(), move.ManifestPath, nil)
	if undo.Restored != 1 || undo.Failed != 0 {
		t.Fatalf("undo after release = %+v", undo)
	}
}

func TestPublishedCopyCleanupPreservesReplacement(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.mp4")
	if err := os.WriteFile(target, []byte("ours"), 0644); err != nil {
		t.Fatal(err)
	}
	owned, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(owned, owned) {
		t.Fatal("file identity unavailable")
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("theirs"), 0644); err != nil {
		t.Fatal(err)
	}
	err = handlePublishedCopyDeleteFailure(errors.New("source locked"), target, owned)
	var pending *publishedCopyError
	if !errors.As(err, &pending) {
		t.Fatalf("cleanup result = %v, want pending recovery", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "theirs" {
		t.Fatalf("replacement = %q, %v", data, err)
	}
}

func TestPublishedCopyCleanupRemovesOwnedTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target.mp4")
	if err := os.WriteFile(target, []byte("ours"), 0644); err != nil {
		t.Fatal(err)
	}
	owned, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	err = handlePublishedCopyDeleteFailure(errors.New("source locked"), target, owned)
	var pending *publishedCopyError
	if err == nil || errors.As(err, &pending) {
		t.Fatalf("cleanup result = %v, want ordinary move error", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("owned target survived cleanup: %v", err)
	}
}

func TestCopyPreservesSourceReplacementWithSameMetadata(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.mp4")
	target := filepath.Join(root, "target.mp4")
	if err := os.WriteFile(source, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	err = copyVerifyDeleteWithHooks(t.Context(), source, target, info, func() {
		if err := os.Remove(source); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(source, []byte("replaced"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(source, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
	}, os.Chtimes)
	if err == nil {
		t.Fatal("source replacement was accepted")
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "replaced" {
		t.Fatalf("source replacement = %q, %v", data, err)
	}
}

func TestCopyReadOnlySourceDoesNotPublishTarget(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "readonly.mp4")
	target := filepath.Join(root, "target.mp4")
	if err := os.WriteFile(source, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(source, 0666)
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0200 != 0 {
		t.Skip("filesystem does not expose read-only mode")
	}
	err = copyVerifyDelete(t.Context(), source, target, info)
	if err == nil {
		t.Fatal("read-only source copied then removed")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("read-only source changed: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("read-only source published target: %v", err)
	}
}
