package appconfig

import (
	"encoding/json"
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
	second := Config{SourceDir: "second", Extensions: ".jpg", StartYear: 2030, PathTemplate: `{YYYY}\自定义\{YYYY}{MM}\{NN}`}
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

func TestConfigPathTemplateIsBackwardCompatible(t *testing.T) {
	var legacy Config
	if err := json.Unmarshal([]byte(`{"startYear":2021,"filesPerLeaf":30}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.PathTemplate != "" {
		t.Fatalf("legacy path template = %q, want empty for archive defaulting", legacy.PathTemplate)
	}

	want := Config{StartYear: 2021, FilesPerLeaf: 30, PathTemplate: `{YYYY}\自定义\{YYYY}{MM}\{NN}`}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Config
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}
