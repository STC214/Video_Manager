package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditTargetAcceptsContiguousLeaves(t *testing.T) {
	target := t.TempDir()
	cfg := feedTestConfig(target)
	writeFeedTestFile(t, calendarTestLeaf(target, 1), "old-1.pdf", 1)
	writeFeedTestFile(t, calendarTestLeaf(target, 1), "old-2.pdf", 2)
	writeFeedTestFile(t, calendarTestLeaf(target, 2), "old-3.pdf", 3)

	audit := AuditTargetContext(t.Context(), cfg)
	if !audit.Correct || audit.ErrorCount != 0 || audit.ExistingCount != 3 {
		t.Fatalf("unexpected audit: %+v", audit)
	}
	if audit.LastLeafIndex != 2 || audit.LastLeafFileCount != 1 {
		t.Fatalf("unexpected last leaf: %+v", audit)
	}
}

func TestAuditTargetRejectsGapAndNonCalendarFile(t *testing.T) {
	t.Run("gap", func(t *testing.T) {
		target := t.TempDir()
		cfg := feedTestConfig(target)
		writeFeedTestFile(t, calendarTestLeaf(target, 1), "old-1.pdf", 1)
		writeFeedTestFile(t, calendarTestLeaf(target, 3), "old-2.pdf", 2)

		audit := AuditTargetContext(t.Context(), cfg)
		if audit.Correct || !strings.Contains(audit.Message, "空洞") {
			t.Fatalf("unexpected audit: %+v", audit)
		}
	})

	t.Run("outside hierarchy", func(t *testing.T) {
		target := t.TempDir()
		cfg := feedTestConfig(target)
		writeFeedTestFile(t, target, "loose.pdf", 1)

		audit := AuditTargetContext(t.Context(), cfg)
		if audit.Correct {
			t.Fatalf("unexpected audit: %+v", audit)
		}
	})
}

func TestAuditTargetBlocksUnfinishedStagingFiles(t *testing.T) {
	target := t.TempDir()
	cfg := feedTestConfig(target)
	writeFeedTestFile(t, filepath.Join(target, "_file-manager-staging", "unfinished", "000001"), "old.pdf", 1)

	audit := AuditTargetContext(t.Context(), cfg)
	if audit.Correct || audit.ErrorCount == 0 || !strings.Contains(audit.Message, "遗留") {
		t.Fatalf("unexpected audit: %+v", audit)
	}
}

func TestBuildFeedPlanAppendsWithoutRebalancingCorrectTarget(t *testing.T) {
	target := t.TempDir()
	feed := t.TempDir()
	cfg := feedTestConfig(target)
	writeFeedTestFile(t, calendarTestLeaf(target, 1), "old-1.pdf", 1)
	writeFeedTestFile(t, calendarTestLeaf(target, 1), "old-2.pdf", 2)
	writeFeedTestFile(t, calendarTestLeaf(target, 2), "old-3.pdf", 3)
	feedPath := writeFeedTestFile(t, feed, "new.pdf", 4)

	plan := BuildFeedPlanContext(t.Context(), scanFeedTestFiles(t, feed), cfg)
	if plan.ErrorCount != 0 || plan.Rebalanced || !plan.AuditCorrect || len(plan.Items) != 1 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if plan.FeedFileCount != 1 || plan.FinalTargetFiles != 4 {
		t.Fatalf("unexpected feed totals: %+v", plan)
	}
	if got := filepath.Dir(plan.Items[0].TargetPath); got != calendarTestLeaf(target, 2) {
		t.Fatalf("target dir = %q", got)
	}
	if plan.Items[0].SourcePath != feedPath {
		t.Fatalf("source = %q, want %q", plan.Items[0].SourcePath, feedPath)
	}
}

func TestValidateFeedPlanPreflightDetectsTargetChange(t *testing.T) {
	target := t.TempDir()
	feed := t.TempDir()
	cfg := feedTestConfig(target)
	first := writeFeedTestFile(t, calendarTestLeaf(target, 1), "old-1.pdf", 1)
	writeFeedTestFile(t, calendarTestLeaf(target, 1), "old-2.pdf", 2)
	writeFeedTestFile(t, calendarTestLeaf(target, 2), "old-3.pdf", 3)
	writeFeedTestFile(t, feed, "new.pdf", 4)

	plan := BuildFeedPlanContext(t.Context(), scanFeedTestFiles(t, feed), cfg)
	if err := ValidateFeedPlanPreflight(t.Context(), plan, cfg); err != nil {
		t.Fatalf("unchanged target failed preflight: %v", err)
	}
	loose := filepath.Join(target, "manually-moved.pdf")
	if err := os.Rename(first, loose); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFeedPlanPreflight(t.Context(), plan, cfg); err == nil {
		t.Fatal("changed target unexpectedly passed preflight")
	}
}

func TestBuildFeedPlanHonorsPreCancelledContextWithoutMaterializingItems(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	feed := make([]VideoFile, 10000)
	for index := range feed {
		feed[index] = VideoFile{
			SourcePath: filepath.Join("feed", fmt.Sprintf("%05d.pdf", index)),
			Name:       fmt.Sprintf("%05d.pdf", index),
			Size:       1,
		}
	}
	plan := BuildFeedPlanContext(ctx, feed, feedTestConfig(t.TempDir()))
	if len(plan.Items) != 0 || plan.FeedFileCount != len(feed) {
		t.Fatalf("cancelled plan materialized work: items=%d, feed=%d", len(plan.Items), plan.FeedFileCount)
	}
}

func TestTargetSnapshotPreservesDatesOutsideUnixNanoRange(t *testing.T) {
	base := VideoFile{
		SourcePath: filepath.Join(t.TempDir(), "same.pdf"),
		Size:       10,
	}
	early := base
	early.ModTime = time.Unix(-11644473600, 800000000).UTC()
	late := base
	// The timestamps differ by exactly 2^64 nanoseconds, so UnixNano wraps
	// them to the same int64 value even though the Windows file dates differ.
	late.ModTime = time.Unix(-11644473600+18446744073, 800000000+709551616).UTC()
	if early.ModTime.UnixNano() != late.ModTime.UnixNano() {
		t.Fatal("test timestamps must demonstrate UnixNano wraparound")
	}

	if targetSnapshot([]VideoFile{early}) == targetSnapshot([]VideoFile{late}) {
		t.Fatal("snapshots for distinct out-of-range timestamps unexpectedly match")
	}
}

func TestValidateFeedPlanPreflightDetectsFeedSourceChangeBeforeRebalance(t *testing.T) {
	target := t.TempDir()
	feed := t.TempDir()
	cfg := feedTestConfig(target)
	writeFeedTestFile(t, calendarTestLeaf(target, 1), "late.pdf", 30)
	writeFeedTestFile(t, calendarTestLeaf(target, 3), "early.pdf", 10)
	feedPath := writeFeedTestFile(t, feed, "feed.pdf", 40)

	plan := BuildFeedPlanContext(t.Context(), scanFeedTestFiles(t, feed), cfg)
	if !plan.Rebalanced {
		t.Fatalf("expected rebalance plan: %+v", plan)
	}
	if err := os.WriteFile(feedPath, []byte("changed after dry-run"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFeedPlanPreflight(t.Context(), plan, cfg); err == nil {
		t.Fatal("changed feed source unexpectedly passed preflight")
	}
	for i := 0; i < plan.ExistingTargetFiles; i++ {
		if _, err := os.Stat(plan.Items[i].SourcePath); err != nil {
			t.Fatalf("preflight changed an existing target file: %v", err)
		}
	}
}

func TestRebalancePlanStopsBeforeFeedWhenStagingFails(t *testing.T) {
	target := t.TempDir()
	feed := t.TempDir()
	cfg := feedTestConfig(target)
	writeFeedTestFile(t, calendarTestLeaf(target, 1), "late.pdf", 30)
	writeFeedTestFile(t, calendarTestLeaf(target, 3), "early.pdf", 10)
	feedPath := writeFeedTestFile(t, feed, "feed.pdf", 40)

	plan := BuildFeedPlanContext(t.Context(), scanFeedTestFiles(t, feed), cfg)
	if !plan.Rebalanced || !plan.StopOnError {
		t.Fatalf("rebalance plan is not fail-fast: %+v", plan)
	}
	if err := os.WriteFile(plan.Items[0].SourcePath, []byte("changed after dry-run"), 0644); err != nil {
		t.Fatal(err)
	}
	summary := ExecuteMovePlan(t.Context(), plan, MoveOptions{ManifestDir: t.TempDir()}, nil)
	if summary.Error == "" || summary.Moved != 0 || summary.Failed != len(plan.Items) {
		t.Fatalf("unexpected fail-fast summary: %+v", summary)
	}
	if _, err := os.Stat(feedPath); err != nil {
		t.Fatalf("feed file moved after staging failure: %v", err)
	}
}

func TestBuildAndExecuteFeedPlanRebalancesBeforeAppending(t *testing.T) {
	target := t.TempDir()
	feed := t.TempDir()
	cfg := feedTestConfig(target)
	oldLate := writeFeedTestFile(t, calendarTestLeaf(target, 1), "late.pdf", 30)
	oldEarly := writeFeedTestFile(t, calendarTestLeaf(target, 3), "early.pdf", 10)
	feedPath := writeFeedTestFile(t, feed, "feed.pdf", 40)

	plan := BuildFeedPlanContext(t.Context(), scanFeedTestFiles(t, feed), cfg)
	if plan.ErrorCount != 0 || !plan.Rebalanced || plan.AuditCorrect {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if len(plan.Items) != 5 {
		t.Fatalf("items = %d, want 5", len(plan.Items))
	}
	for i := 0; i < 2; i++ {
		if !strings.HasPrefix(plan.Items[i].TargetPath, plan.StagingRoot) {
			t.Fatalf("stage item %d = %+v", i, plan.Items[i])
		}
	}
	if filepath.Base(plan.Items[2].TargetPath) != "early.pdf" ||
		filepath.Base(plan.Items[3].TargetPath) != "late.pdf" ||
		filepath.Base(plan.Items[4].TargetPath) != "feed.pdf" {
		t.Fatalf("final chronological order is wrong: %+v", plan.Items[2:])
	}
	if filepath.Dir(plan.Items[2].TargetPath) != calendarTestLeaf(target, 1) ||
		filepath.Dir(plan.Items[3].TargetPath) != calendarTestLeaf(target, 1) ||
		filepath.Dir(plan.Items[4].TargetPath) != calendarTestLeaf(target, 2) {
		t.Fatalf("final leaf placement is wrong: %+v", plan.Items[2:])
	}
	if err := ValidateFeedPlanPreflight(t.Context(), plan, cfg); err != nil {
		t.Fatalf("unchanged rebalance target failed preflight: %v", err)
	}

	summary := ExecuteMovePlan(t.Context(), plan, MoveOptions{ManifestDir: t.TempDir()}, nil)
	if summary.Error != "" || summary.Failed != 0 || summary.Moved != len(plan.Items) {
		t.Fatalf("unexpected move summary: %+v", summary)
	}
	for _, path := range []string{oldEarly, feedPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("original path still exists: %s (%v)", path, err)
		}
	}
	if _, err := os.Stat(oldLate); err != nil {
		t.Fatalf("file that returns to its original target path is missing: %v", err)
	}
	audit := AuditTargetContext(t.Context(), cfg)
	if !audit.Correct || audit.ExistingCount != 3 || audit.LastLeafFileCount != 1 {
		t.Fatalf("unexpected final audit: %+v", audit)
	}
	if _, errs := CleanupFeedStaging(t.Context(), plan.StagingRoot); len(errs) != 0 {
		t.Fatalf("staging cleanup errors: %v", errs)
	}
	if _, err := os.Stat(plan.StagingRoot); !os.IsNotExist(err) {
		t.Fatalf("staging root still exists: %v", err)
	}
	undo := UndoManifest(t.Context(), summary.ManifestPath, nil)
	if undo.Failed != 0 || undo.Restored != summary.Moved {
		t.Fatalf("unexpected undo summary: %+v", undo)
	}
	for _, path := range []string{oldEarly, oldLate, feedPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("original path was not restored: %s (%v)", path, err)
		}
	}
}

func feedTestConfig(target string) PlanConfig {
	return PlanConfig{
		TargetDir:        target,
		StartYear:        2021,
		LeafDirsPerMonth: 2,
		FilesPerLeaf:     2,
		Extensions:       []string{".pdf"},
	}
}

func calendarTestLeaf(target string, leaf int) string {
	return filepath.Join(target, CalendarLeafPath(2021, leaf, 2))
}

func writeFeedTestFile(t *testing.T, dir, name string, unixTime int64) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(name), 0644); err != nil {
		t.Fatal(err)
	}
	modTime := time.Unix(unixTime, 0)
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	return path
}

func scanFeedTestFiles(t *testing.T, root string) []VideoFile {
	t.Helper()
	result := ScanFilesWithProgress(t.Context(), root, nil, []string{".pdf"}, nil)
	if result.ErrorCount != 0 {
		t.Fatalf("scan errors: %+v", result.Errors)
	}
	return result.Files
}
