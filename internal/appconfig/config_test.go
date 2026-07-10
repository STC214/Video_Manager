package appconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveToPathReplacesExistingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "config.json")
	if err := saveToPath(path, []byte(`{"sourceDir":"first"}`)); err != nil {
		t.Fatal(err)
	}
	if err := saveToPath(path, []byte(`{"sourceDir":"second"}`)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.SourceDir != "second" {
		t.Fatalf("SourceDir = %q", cfg.SourceDir)
	}
}
