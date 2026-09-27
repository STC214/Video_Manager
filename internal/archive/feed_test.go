package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestFeedPlanAppendsWithoutMovingExistingFiles(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	feed := filepath.Join(root, "feed")
	cfg := PlanConfig{TargetDir: target, LevelCount: 2, LevelNames: []string{"Season", "Episode"}, FoldersPerLevel: []int{1, 2}, FilesPerLeaf: 2}
	leaf := filepath.Join(target, "Season_001", "Episode_001")
	if err := os.MkdirAll(leaf, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(feed, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"old1.mp4", "old2.mp4"} {
		if err := os.WriteFile(filepath.Join(leaf, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"new1.mp4", "new2.mp4", "new3.mp4"} {
		if err := os.WriteFile(filepath.Join(feed, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveStructureConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	scan := ScanVideos(context.Background(), feed, nil)
	plan, audit := BuildFeedPlanContext(context.Background(), scan.Files, cfg)
	if len(audit.Errors) != 0 || audit.RequiresAdoption || audit.Existing != 2 || len(plan.Items) != 3 {
		t.Fatalf("audit=%+v plan=%+v", audit, plan)
	}
	for i, item := range plan.Items {
		leafNo := 2
		if i == 2 {
			leafNo = 3
		}
		season := 1
		if leafNo == 3 {
			season = 2
		}
		want := filepath.Join(target, fmt.Sprintf("Season_%03d", season), fmt.Sprintf("Episode_%03d", leafNo))
		if filepath.Dir(item.TargetPath) != want {
			t.Fatalf("item %d target %q, want %q", i, item.TargetPath, want)
		}
	}
	if !plan.AutoExpanded || plan.EffectiveFolders[0] != 2 {
		t.Fatalf("expected first-level expansion: %+v", plan)
	}
	if _, err := os.Stat(filepath.Join(leaf, "old1.mp4")); err != nil {
		t.Fatal(err)
	}
	summary := ExecuteMovePlan(context.Background(), plan, MoveOptions{ManifestDir: filepath.Join(root, "manifests")}, nil)
	if summary.Moved != 3 || summary.Failed != 0 || summary.Error != "" {
		t.Fatalf("feed move: %+v", summary)
	}
	for _, item := range plan.Items {
		if _, err := os.Stat(item.TargetPath); err != nil {
			t.Fatalf("missing fed file %q: %v", item.TargetPath, err)
		}
	}
	if _, err := os.Stat(filepath.Join(leaf, "old1.mp4")); err != nil {
		t.Fatalf("existing file changed: %v", err)
	}
	undo := UndoManifest(context.Background(), summary.ManifestPath, nil)
	if undo.Restored != 3 {
		t.Fatalf("feed undo: %+v", undo)
	}
	if _, err := os.Stat(structurePath(target)); err != nil {
		t.Fatalf("pre-existing structure record removed: %v", err)
	}
}

func TestFeedAuditRequiresRecordedOriginalRules(t *testing.T) {
	root := t.TempDir()
	leaf := filepath.Join(root, "Batch_001")
	if err := os.MkdirAll(leaf, 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := os.WriteFile(filepath.Join(leaf, fmt.Sprintf("old%02d.mp4", i)), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	original := PlanConfig{TargetDir: root, LevelCount: 1, LevelNames: []string{"Batch"}, FoldersPerLevel: []int{2}, FilesPerLeaf: 30}
	legacy := AuditFeedTarget(context.Background(), original)
	if len(legacy.Errors) != 0 || !legacy.RequiresAdoption {
		t.Fatalf("expected explicit legacy adoption: %+v", legacy)
	}
	if err := SaveStructureConfig(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	changed := original
	changed.FilesPerLeaf = 20 // Both values fit the partial leaf; only metadata distinguishes them.
	audit := AuditFeedTarget(context.Background(), changed)
	if len(audit.Errors) == 0 {
		t.Fatal("changed original capacity was accepted")
	}
	if err := SaveStructureConfig(context.Background(), changed); err == nil {
		t.Fatal("stored rules were overwritten")
	}
	audit = AuditFeedTarget(context.Background(), original)
	if len(audit.Errors) != 0 || audit.RequiresAdoption {
		t.Fatalf("original rules rejected: %+v", audit)
	}
}

func TestFeedAuditRejectsMissingOrEmptyTarget(t *testing.T) {
	root := t.TempDir()
	cfg := PlanConfig{TargetDir: filepath.Join(root, "missing"), LevelCount: 1, LevelNames: []string{"Batch"}, FoldersPerLevel: []int{1}, FilesPerLeaf: 2}
	if audit := AuditFeedTarget(context.Background(), cfg); len(audit.Errors) == 0 {
		t.Fatal("missing target accepted")
	}
	if err := os.MkdirAll(cfg.TargetDir, 0755); err != nil {
		t.Fatal(err)
	}
	if audit := AuditFeedTarget(context.Background(), cfg); len(audit.Errors) == 0 {
		t.Fatal("empty target accepted")
	}
}

func TestNormalTargetRejectsExistingVideoAndChangedRules(t *testing.T) {
	root := t.TempDir()
	cfg := PlanConfig{TargetDir: root, LevelCount: 1, LevelNames: []string{"Batch"}, FoldersPerLevel: []int{2}, FilesPerLeaf: 2}
	if err := CheckNormalTargetContext(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := SaveStructureConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	changed := cfg
	changed.FilesPerLeaf = 3
	if err := CheckNormalTargetContext(context.Background(), changed); err == nil {
		t.Fatal("changed recorded rules accepted")
	}
	if err := os.WriteFile(filepath.Join(root, "old.mp4"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := CheckNormalTargetContext(context.Background(), cfg); err == nil {
		t.Fatal("occupied target accepted for normal mode")
	}
}

func TestFirstFeedBindingIsUndoneWithRun(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	feed := filepath.Join(root, "feed")
	leaf := filepath.Join(target, "Batch_001")
	for _, dir := range []string{leaf, feed} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(leaf, "old.mp4"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(feed, "new.mp4"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := PlanConfig{TargetDir: target, LevelCount: 1, LevelNames: []string{"Batch"}, FoldersPerLevel: []int{1}, FilesPerLeaf: 2}
	scan := ScanVideos(context.Background(), feed, nil)
	plan, audit := BuildFeedPlanContext(context.Background(), scan.Files, cfg)
	if len(audit.Errors) != 0 || !audit.RequiresAdoption {
		t.Fatalf("expected legacy target: %+v", audit)
	}
	summary := ExecuteMovePlan(context.Background(), plan, MoveOptions{}, nil)
	if summary.Moved != 1 || summary.Error != "" {
		t.Fatalf("move: %+v", summary)
	}
	manifestBefore, err := os.ReadFile(summary.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := BindStructureToManifest(context.Background(), cfg, summary.ManifestPath, audit.Files); err != nil {
		t.Fatal(err)
	}
	manifestAfter, err := os.ReadFile(summary.ManifestPath)
	if err != nil || string(manifestAfter) != string(manifestBefore) {
		t.Fatalf("binding changed move manifest: %v", err)
	}
	if _, err := os.Stat(structureIntentPath(summary.ManifestPath)); err != nil {
		t.Fatalf("atomic sidecar missing: %v", err)
	}
	if _, err := os.Stat(structurePath(target)); err != nil {
		t.Fatalf("binding missing: %v", err)
	}
	undo := UndoManifest(context.Background(), summary.ManifestPath, nil)
	if undo.Restored != 1 || undo.Failed != 0 || undo.Error != "" {
		t.Fatalf("undo: %+v", undo)
	}
	if _, err := os.Stat(structurePath(target)); !os.IsNotExist(err) {
		t.Fatalf("binding survived undo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(leaf, "old.mp4")); err != nil {
		t.Fatalf("original file changed: %v", err)
	}
}

func TestUndoPreservesChangedStructureRecord(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	feed := filepath.Join(root, "feed")
	leaf := filepath.Join(target, "Batch_001")
	for _, dir := range []string{leaf, feed} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(leaf, "old.mp4"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(feed, "new.mp4"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := PlanConfig{TargetDir: target, LevelCount: 1, LevelNames: []string{"Batch"}, FoldersPerLevel: []int{1}, FilesPerLeaf: 2}
	plan, audit := BuildFeedPlanContext(context.Background(), ScanVideos(context.Background(), feed, nil).Files, cfg)
	summary := ExecuteMovePlan(context.Background(), plan, MoveOptions{}, nil)
	if summary.Moved != 1 {
		t.Fatalf("move: %+v", summary)
	}
	if err := BindStructureToManifest(context.Background(), cfg, summary.ManifestPath, audit.Files); err != nil {
		t.Fatal(err)
	}
	marker := structurePath(target)
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	undo := UndoManifest(context.Background(), summary.ManifestPath, nil)
	if undo.Restored != 1 || undo.Failed == 0 {
		t.Fatalf("changed marker was silently removed: %+v", undo)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("changed marker missing: %v", err)
	}
}

func TestUndoOlderFeedKeepsStructureUsedByLaterFiles(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	feed := filepath.Join(root, "feed")
	leaf := filepath.Join(target, "Batch_001")
	for _, dir := range []string{leaf, feed} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(leaf, "old.mp4"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(feed, "new.mp4"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := PlanConfig{TargetDir: target, LevelCount: 1, LevelNames: []string{"Batch"}, FoldersPerLevel: []int{2}, FilesPerLeaf: 2}
	plan, audit := BuildFeedPlanContext(context.Background(), ScanVideos(context.Background(), feed, nil).Files, cfg)
	summary := ExecuteMovePlan(context.Background(), plan, MoveOptions{}, nil)
	if summary.Moved != 1 {
		t.Fatalf("move: %+v", summary)
	}
	if err := BindStructureToManifest(context.Background(), cfg, summary.ManifestPath, audit.Files); err != nil {
		t.Fatal(err)
	}
	laterLeaf := filepath.Join(target, "Batch_002")
	if err := os.MkdirAll(laterLeaf, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(laterLeaf, "later.mp4"), []byte("later"), 0644); err != nil {
		t.Fatal(err)
	}
	undo := UndoManifest(context.Background(), summary.ManifestPath, nil)
	if undo.Restored != 1 || undo.Failed != 0 {
		t.Fatalf("undo: %+v", undo)
	}
	if _, err := os.Stat(structurePath(target)); err != nil {
		t.Fatalf("later files lost their structure record: %v", err)
	}
	if _, err := os.Stat(filepath.Join(laterLeaf, "later.mp4")); err != nil {
		t.Fatalf("later file changed: %v", err)
	}
}

func TestManifestRejectsMalformedStructureIntent(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "run.tsv")
	text := "status\tsource\ttarget\tsize\tconflict\terror\tmod_time_rfc3339_nano\n" +
		"structure_pending\tC:\\target\tC:\\elsewhere\t0\tfalse\tinvalid\t\n"
	if err := os.WriteFile(manifest, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckManifestUndoable(manifest); err == nil {
		t.Fatal("malformed structure intent accepted")
	}
}

func TestUndoRecoversFilesFromTruncatedLegacyStructureTail(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.mp4")
	target := filepath.Join(root, "target.mp4")
	manifest := filepath.Join(root, "run.tsv")
	if err := os.WriteFile(target, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	text := "status\tsource\ttarget\tsize\tconflict\terror\tmod_time_rfc3339_nano\n" +
		fmt.Sprintf("moved\t%s\t%s\t%d\tfalse\t\t%s\n", source, target, info.Size(), manifestModTimeField(info.ModTime())) +
		"structure_pending\tpartial"
	if err := os.WriteFile(manifest, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	undo := UndoManifest(context.Background(), manifest, nil)
	if undo.Restored != 1 || undo.Failed != 0 {
		t.Fatalf("truncated tail blocked file undo: %+v", undo)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source not restored: %v", err)
	}
}

func TestCorruptStructureSidecarDoesNotBlockFileUndo(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.mp4")
	target := filepath.Join(root, "target.mp4")
	manifest := filepath.Join(root, "run.tsv")
	if err := os.WriteFile(target, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	text := "status\tsource\ttarget\tsize\tconflict\terror\tmod_time_rfc3339_nano\n" +
		fmt.Sprintf("moved\t%s\t%s\t%d\tfalse\t\t%s\n", source, target, info.Size(), manifestModTimeField(info.ModTime()))
	if err := os.WriteFile(manifest, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(structureIntentPath(manifest), []byte("{bad"), 0644); err != nil {
		t.Fatal(err)
	}
	undo := UndoManifest(context.Background(), manifest, nil)
	if undo.Restored != 1 || undo.Failed == 0 {
		t.Fatalf("sidecar failure did not report while restoring file: %+v", undo)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source not restored: %v", err)
	}
}

func TestFeedAuditRejectsHoleAndOverfullLeaf(t *testing.T) {
	root := t.TempDir()
	cfg := PlanConfig{TargetDir: root, LevelCount: 1, LevelNames: []string{"Batch"}, FoldersPerLevel: []int{3}, FilesPerLeaf: 2}
	leaf := filepath.Join(root, "Batch_002")
	if err := os.MkdirAll(leaf, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "old.mp4"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	audit := AuditFeedTarget(context.Background(), cfg)
	if len(audit.Errors) == 0 {
		t.Fatal("expected structural error")
	}
}

func TestDeferredStructureBindingRemovedAfterLaterFeedUndo(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "archive")
	leaf := filepath.Join(target, "Batch_001")
	if err := os.MkdirAll(leaf, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "old.mp4"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := PlanConfig{TargetDir: target, LevelCount: 1, LevelNames: []string{"Batch"}, FoldersPerLevel: []int{2}, FilesPerLeaf: 2}
	feedOnce := func(name string) MoveSummary {
		t.Helper()
		feed := filepath.Join(root, name)
		if err := os.MkdirAll(feed, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(feed, name+".mp4"), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
		plan, audit := BuildFeedPlanContext(t.Context(), ScanVideos(t.Context(), feed, nil).Files, cfg)
		if len(audit.Errors) != 0 {
			t.Fatalf("feed audit: %v", audit.Errors)
		}
		summary := ExecuteMovePlan(t.Context(), plan, MoveOptions{}, nil)
		if summary.Moved != 1 || summary.Failed != 0 {
			t.Fatalf("feed move: %+v", summary)
		}
		if err := BindStructureToManifest(t.Context(), cfg, summary.ManifestPath, audit.Files); err != nil {
			t.Fatal(err)
		}
		return summary
	}
	first := feedOnce("first")
	second := feedOnce("second")
	if summary := UndoManifest(t.Context(), first.ManifestPath, nil); summary.Restored != 1 || summary.Failed != 0 {
		t.Fatalf("first undo: %+v", summary)
	}
	if _, err := os.Stat(structurePath(target)); err != nil {
		t.Fatalf("structure removed while later files remain: %v", err)
	}
	if summary := UndoManifest(t.Context(), second.ManifestPath, nil); summary.Restored != 1 || summary.Failed != 0 {
		t.Fatalf("second undo: %+v", summary)
	}
	if _, err := os.Stat(structurePath(target)); !os.IsNotExist(err) {
		t.Fatalf("deferred structure remains after all feeds undone: %v", err)
	}
}
