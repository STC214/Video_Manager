package ui

import (
	"os"
	"path/filepath"
	"testing"

	"video-manager/internal/archive"
)

func TestSameFeedAuditDetectsDirectoryTreeChange(t *testing.T) {
	base := archive.FeedAudit{Existing: 1, Dirs: []string{`C:\archive\Batch_001`}}
	changed := archive.FeedAudit{Existing: 1, Dirs: []string{`C:\archive\Batch_002`}}
	if sameFeedAudit(base, changed) {
		t.Fatal("changed structure directory accepted at move-time recheck")
	}
	changed.Dirs = append(changed.Dirs, `C:\archive\Batch_003`)
	if sameFeedAudit(base, changed) {
		t.Fatal("added structure directory accepted at move-time recheck")
	}
}

func TestSameFeedAuditDetectsTailOccupancyChange(t *testing.T) {
	base := archive.FeedAudit{Existing: 36, LastLeafIndex: 3, LastLeafCount: 1}
	changed := archive.FeedAudit{Existing: 36, LastLeafIndex: 3, LastLeafCount: 2}
	if sameFeedAudit(base, changed) {
		t.Fatal("changed tail occupancy accepted at move-time recheck")
	}
}

func TestSameFeedAuditDetectsSameMetadataFileReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "old.mp4")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	first, err := os.Stat(path)
	if err != nil || !os.SameFile(first, first) {
		t.Fatalf("first identity: %v", err)
	}
	backup := filepath.Join(root, "backup.mp4")
	if err := os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replaced"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, first.ModTime(), first.ModTime()); err != nil {
		t.Fatal(err)
	}
	second, err := os.Stat(path)
	if err != nil || !os.SameFile(second, second) {
		t.Fatalf("second identity: %v", err)
	}
	a := archive.VideoFile{SourcePath: path, Size: 8, ModTime: first.ModTime(), SourceInfo: first}
	b := archive.VideoFile{SourcePath: path, Size: 8, ModTime: first.ModTime(), SourceInfo: second}
	if sameFeedAudit(archive.FeedAudit{Existing: 1, Files: []archive.VideoFile{a}}, archive.FeedAudit{Existing: 1, Files: []archive.VideoFile{b}}) {
		t.Fatal("same-metadata replacement accepted in target recheck")
	}
}

func TestSourceCleanupRequiresCompleteMove(t *testing.T) {
	cases := []struct {
		name    string
		summary archive.MoveSummary
		root    string
		want    bool
	}{
		{"all moved", archive.MoveSummary{Moved: 2}, `C:\source`, true},
		{"partial failure", archive.MoveSummary{Moved: 1, Failed: 1}, `C:\source`, false},
		{"cancelled", archive.MoveSummary{Moved: 1, Cancelled: true}, `C:\source`, false},
		{"error", archive.MoveSummary{Moved: 1, Error: "manifest failure"}, `C:\source`, false},
		{"empty source", archive.MoveSummary{Moved: 1}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldCleanupSourceAfterMove(tc.summary, tc.root); got != tc.want {
				t.Fatalf("cleanup = %t, want %t", got, tc.want)
			}
		})
	}
}
