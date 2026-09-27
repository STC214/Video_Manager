package archive

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureFeedArchiveCSVRecordsOnlyManagedBaselineOnce(t *testing.T) {
	root := t.TempDir()
	cfg := PlanConfig{TargetDir: root, LevelCount: 1, LevelNames: []string{"Batch"}, FoldersPerLevel: []int{2}, FilesPerLeaf: 3}
	leaf := filepath.Join(root, "Batch_001")
	unrelated := filepath.Join(root, "PC", "20260205", "BaseballBoy")
	for _, dir := range []string{leaf, unrelated} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{
		filepath.Join(leaf, "old,one.mp4"):  "managed",
		filepath.Join(unrelated, "old.mp4"): "unrelated",
	} {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveStructureConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	audit := AuditFeedTarget(context.Background(), cfg)
	if len(audit.Errors) != 0 || audit.Existing != 1 {
		t.Fatalf("bad baseline audit: %+v", audit)
	}
	lock, err := AcquireTargetMoveLock(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	path, created, err := EnsureFeedArchiveCSV(context.Background(), cfg, audit, lock)
	if err != nil || !created || path != filepath.Join(root, "_video-manager", "archive.csv") {
		t.Fatalf("catalog creation: path=%s created=%t err=%v", path, created, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil || len(rows) != 2 || rows[0][0] != "relative_path" || rows[1][0] != "./Batch_001/old,one.mp4" || rows[1][1] != "7" {
		t.Fatalf("bad catalog rows: %q, %v", rows, err)
	}
	if strings.Contains(string(data), "BaseballBoy") {
		t.Fatal("unrelated video entered catalog")
	}
	if again, made, err := EnsureFeedArchiveCSV(context.Background(), cfg, audit, lock); err != nil || made || again != path {
		t.Fatalf("existing catalog overwritten: path=%s made=%t err=%v", again, made, err)
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != string(data) {
		t.Fatalf("existing catalog changed: %v", err)
	}
}

func TestEnsureFeedArchiveCSVFailsBeforeMoveWhenCatalogPathInvalid(t *testing.T) {
	root := t.TempDir()
	leaf := filepath.Join(root, "Batch_001")
	if err := os.MkdirAll(leaf, 0755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(leaf, "old.mp4")
	if err := os.WriteFile(old, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := PlanConfig{TargetDir: root, LevelCount: 1, LevelNames: []string{"Batch"}, FoldersPerLevel: []int{1}, FilesPerLeaf: 2}
	audit := AuditFeedTarget(context.Background(), cfg)
	if len(audit.Errors) != 0 {
		t.Fatalf("bad audit: %+v", audit)
	}
	lock, err := AcquireTargetMoveLock(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := os.Mkdir(filepath.Join(root, "_video-manager", "archive.csv"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, created, err := EnsureFeedArchiveCSV(context.Background(), cfg, audit, lock); err == nil || created {
		t.Fatalf("invalid catalog path accepted: created=%t err=%v", created, err)
	}
	if data, err := os.ReadFile(old); err != nil || string(data) != "old" {
		t.Fatalf("existing video changed: %q %v", data, err)
	}
}
