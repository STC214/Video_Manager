package archive

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanVideosRecursesSortsByTimeAndExcludesTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "_Archived")
	oldVideo := filepath.Join(root, "z", "second.mkv")
	firstSameTime := filepath.Join(root, "a", "FIRST.MP4")
	secondSameTime := filepath.Join(root, "b", "same.mp4")
	paths := map[string]string{
		oldVideo:                              "video-old",
		firstSameTime:                         "video-same-1",
		secondSameTime:                        "video-same-2",
		filepath.Join(root, "a", "notes.txt"): "notes",
		filepath.Join(target, "Episode_001", "old.mp4"): "archived",
	}
	for path, data := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Date(2020, 1, 1, 8, 0, 0, 0, time.Local)
	sameTime := time.Date(2021, 1, 1, 8, 0, 0, 0, time.Local)
	if err := os.Chtimes(oldVideo, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{firstSameTime, secondSameTime} {
		if err := os.Chtimes(path, sameTime, sameTime); err != nil {
			t.Fatal(err)
		}
	}

	result := ScanVideos(context.Background(), root, []string{target})
	if result.ErrorCount != 0 || result.Cancelled {
		t.Fatalf("scan result = %+v", result)
	}
	if result.VideoCount != 3 || result.NonVideoCount != 1 {
		t.Fatalf("video=%d non-video=%d", result.VideoCount, result.NonVideoCount)
	}
	if len(result.Files) != 3 {
		t.Fatalf("files = %d", len(result.Files))
	}
	want := []string{"second.mkv", "FIRST.MP4", "same.mp4"}
	for i := range want {
		if result.Files[i].Name != want[i] {
			t.Fatalf("file[%d] = %q, want %q", i, result.Files[i].Name, want[i])
		}
	}
}

func TestScanVideosHonorsPreCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := ScanVideos(ctx, t.TempDir(), nil)
	if !result.Cancelled {
		t.Fatal("pre-cancelled scan should report Cancelled")
	}
	if len(result.Files) != 0 {
		t.Fatalf("cancelled scan returned %d files", len(result.Files))
	}
}

func TestScanVideosExcludesExtendedPrefixTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "_Archived")
	sourceVideo := filepath.Join(root, "source.mp4")
	archivedVideo := filepath.Join(target, "Episode_001", "archived.mp4")
	for _, path := range []string{sourceVideo, archivedVideo} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("video"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	result := ScanVideos(context.Background(), root, []string{`\\?\` + target})
	if result.ErrorCount != 0 || result.VideoCount != 1 || len(result.Files) != 1 || result.Files[0].SourcePath != sourceVideo {
		t.Fatalf("scan result = %+v", result)
	}
}

func TestNormalizeExcludedCanonicalizesExtendedPaths(t *testing.T) {
	excluded := normalizeExcluded([]string{
		`\\?\Z:\Videos\Archive`,
		`\\?\UNC\server\share\Archive`,
	})
	for _, want := range []string{
		`z:\videos\archive`,
		`\\server\share\archive`,
	} {
		if _, ok := excluded[want]; !ok {
			t.Fatalf("normalized exclusions %v do not contain %q", excluded, want)
		}
	}
}
