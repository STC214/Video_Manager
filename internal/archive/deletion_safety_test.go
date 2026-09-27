package archive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyRejectsReplacedSourceBeforeOpening(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.mp4")
	target := filepath.Join(root, "target.mp4")
	if err := os.WriteFile(source, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	planned, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(planned, planned) {
		t.Fatal("source identity unavailable")
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("replaced"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(source, planned.ModTime(), planned.ModTime()); err != nil {
		t.Fatal(err)
	}
	err = copyVerifyDelete(context.Background(), source, target, planned)
	if err == nil || !strings.Contains(err.Error(), "source file changed before copy") {
		t.Fatalf("replacement accepted: %v", err)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "replaced" {
		t.Fatalf("replacement changed: %q, %v", data, err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("unexpected target: %v", err)
	}
}

func TestMovePlanRejectsSameMetadataReplacementAfterScan(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	targetDir := filepath.Join(root, "target")
	if err := os.Mkdir(sourceDir, 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceDir, "video.mp4")
	if err := os.WriteFile(source, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	scan := ScanVideos(context.Background(), sourceDir, nil)
	if scan.ErrorCount != 0 || len(scan.Files) != 1 || scan.Files[0].SourceInfo == nil {
		t.Fatalf("scan: %+v", scan)
	}
	plan := BuildMovePlan(scan.Files, PlanConfig{TargetDir: targetDir, LevelCount: 1, LevelNames: []string{"Batch"}, FoldersPerLevel: []int{1}, FilesPerLeaf: 1})
	if len(plan.Items) != 1 || plan.Items[0].SourceInfo == nil {
		t.Fatalf("identity lost in plan: %+v", plan)
	}
	backup := filepath.Join(root, "preserved-original.mp4")
	if err := os.Rename(source, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("replaced"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(source, scan.Files[0].ModTime, scan.Files[0].ModTime); err != nil {
		t.Fatal(err)
	}
	if _, err := moveOne(context.Background(), plan.Items[0]); err == nil || !strings.Contains(err.Error(), "identity changed after dry-run") {
		t.Fatalf("same-metadata replacement accepted: %v", err)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "replaced" {
		t.Fatalf("replacement changed: %q, %v", data, err)
	}
	if _, err := os.Stat(plan.Items[0].TargetPath); !os.IsNotExist(err) {
		t.Fatalf("unexpected target: %v", err)
	}
}

func TestOwnedEmptyDirectoryDeletionRejectsReplacement(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "empty")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	owned, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(owned, owned) {
		t.Fatal("directory identity unavailable")
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := removeOwnedEmptyDir(dir, owned); err == nil {
		t.Fatal("replacement directory was removed")
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("replacement directory missing: %v", err)
	}
}

func TestCopyPreservesSourceWhenBytesChangeWithoutMetadataChange(t *testing.T) {
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
	if !os.SameFile(info, info) {
		t.Fatal("source identity unavailable")
	}
	err = copyVerifyDeleteWithHooks(context.Background(), source, target, info, func() {
		if err := os.WriteFile(source, []byte("altered!"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(source, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
	}, os.Chtimes)
	if err == nil || !strings.Contains(err.Error(), "copy hash verification failed") {
		t.Fatalf("changed bytes accepted: %v", err)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "altered!" {
		t.Fatalf("changed source missing: %q, %v", data, err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("unverified target published: %v", err)
	}
}

func TestOwnedEmptyDirectoryDeletionRejectsFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := removeOwnedEmptyDir(path, info); err == nil {
		t.Fatal("regular file was accepted as an empty directory")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "keep" {
		t.Fatalf("file changed: %q, %v", data, err)
	}
}

func TestMatchingStructureSidecarPreservesChangedIntent(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "run.tsv")
	path := structureIntentPath(manifest)
	expected := structureIntent{Root: `C:\archive`, Path: `C:\archive\_video-manager\structure.json`, Hash: strings.Repeat("a", 64)}
	if err := os.WriteFile(path, []byte(`{"Root":"C:\\other","Path":"C:\\other\\_video-manager\\structure.json","Hash":"`+strings.Repeat("b", 64)+`"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := removeMatchingStructureSidecar(manifest, expected); err == nil {
		t.Fatal("changed sidecar was removed")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("changed sidecar missing: %v", err)
	}
}
