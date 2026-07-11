package ui

import "testing"

func TestWindowTitle(t *testing.T) {
	original := BuildVersion
	t.Cleanup(func() { BuildVersion = original })

	BuildVersion = ""
	if got := windowTitle(); got != "Video Manager" {
		t.Fatalf("windowTitle() without version = %q", got)
	}

	BuildVersion = "20260711_1035"
	if got := windowTitle(); got != "Video Manager - 20260711_1035" {
		t.Fatalf("windowTitle() with version = %q", got)
	}
}
