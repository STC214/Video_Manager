package appconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAtomicallyReplacesExistingConfig(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(filepath.Dir(exe), "file-manager-data")
	t.Cleanup(func() {
		_ = os.RemoveAll(dataDir)
	})

	first := Config{SourceDir: "first", Extensions: ".pdf", StartYear: 2021}
	second := Config{SourceDir: "second", Extensions: ".jpg", StartYear: 2030}
	if err := Save(first); err != nil {
		t.Fatal(err)
	}
	if err := Save(second); err != nil {
		t.Fatalf("replace existing config: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != second {
		t.Fatalf("loaded config = %+v, want %+v", got, second)
	}
}
