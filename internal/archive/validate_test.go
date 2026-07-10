package archive

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateLevelNames(t *testing.T) {
	if err := ValidateLevelNames([]string{"Arc", "Season", "Episode"}); err != nil {
		t.Fatalf("expected valid names: %v", err)
	}
	if err := ValidateLevelNames([]string{"Arc", "Bad/Name"}); err == nil {
		t.Fatal("expected invalid character error")
	}
	if err := ValidateLevelNames([]string{"Arc", " "}); err == nil {
		t.Fatal("expected empty name error")
	}
}

func TestCheckTargetRootAllowsNestedNewPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "new", "nested", "archive")
	if err := CheckTargetRoot(target); err != nil {
		t.Fatalf("expected nested target under existing root to pass: %v", err)
	}
}

func TestCheckReadableDirRejectsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := CheckReadableDir(file); err == nil {
		t.Fatal("expected file path to be rejected")
	}
}
