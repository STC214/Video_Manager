package archive

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathTemplateCanDisableQuarterLayer(t *testing.T) {
	cfg := PlanConfig{
		StartYear:        2021,
		LeafDirsPerMonth: 3,
		FilesPerLeaf:     10,
		PathTemplate:     `{YYYY}\{YYYY}{MM}\{YYYY}{MM}{NN}`,
	}
	want := filepath.Join("2021", "202107", "20210701")
	got := CalendarLeafPathForConfig(cfg, 19)
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if depth := PathTemplateDepth(cfg.PathTemplate); depth != 3 {
		t.Fatalf("depth = %d, want 3", depth)
	}
	if index, ok := CalendarLeafIndex(got, cfg); !ok || index != 19 {
		t.Fatalf("round trip = %d, %v; want 19, true", index, ok)
	}
	if counts := EffectiveFolderCounts(cfg, 1); !equalInts(counts, []int{1, 12, 3}) {
		t.Fatalf("effective folder counts = %v", counts)
	}
}

func TestPathTemplateSupportsFiveLayersAndCustomNames(t *testing.T) {
	cfg := PlanConfig{
		StartYear:        2021,
		LeafDirsPerMonth: 3,
		FilesPerLeaf:     10,
		PathTemplate:     `{YYYY}\作品归档\{YYYY}S{Q}\{YYYY}{MM}\第{NN}组`,
	}
	want := filepath.Join("2021", "作品归档", "2021S3", "202107", "第01组")
	got := CalendarLeafPathForConfig(cfg, 19)
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if depth := PathTemplateDepth(cfg.PathTemplate); depth != 5 {
		t.Fatalf("depth = %d, want 5", depth)
	}
	if index, ok := CalendarLeafIndex(got, cfg); !ok || index != 19 {
		t.Fatalf("round trip = %d, %v; want 19, true", index, ok)
	}
	if counts := EffectiveFolderCounts(cfg, 1); !equalInts(counts, []int{1, 1, 4, 3, 3}) {
		t.Fatalf("effective folder counts = %v", counts)
	}
}

func TestBuildMovePlanUsesCustomPathTemplate(t *testing.T) {
	target := t.TempDir()
	cfg := PlanConfig{
		TargetDir:        target,
		StartYear:        2021,
		LeafDirsPerMonth: 2,
		FilesPerLeaf:     1,
		PathTemplate:     `{YYYY}\固定层\{YYYY}{MM}\批次{NN}`,
		Extensions:       []string{".pdf"},
	}
	plan := BuildMovePlanContext(t.Context(), []VideoFile{{SourcePath: "source.pdf", Name: "source.pdf", Ext: ".pdf", Size: 1}}, cfg)
	wantDir := filepath.Join(target, "2021", "固定层", "202101", "批次01")
	if plan.ErrorCount != 0 || len(plan.Items) != 1 || filepath.Dir(plan.Items[0].TargetPath) != wantDir {
		t.Fatalf("unexpected custom plan: %+v", plan)
	}
}

func TestAuditTargetAcceptsCustomPathTemplate(t *testing.T) {
	target := t.TempDir()
	cfg := PlanConfig{
		TargetDir:        target,
		StartYear:        2021,
		LeafDirsPerMonth: 2,
		FilesPerLeaf:     2,
		PathTemplate:     `{YYYY}\{YYYY}{MM}\批次{NN}`,
		Extensions:       []string{".pdf"},
	}
	leaf := filepath.Join(target, CalendarLeafPathForConfig(cfg, 1))
	if err := os.MkdirAll(leaf, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "existing.pdf"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	audit := AuditTargetContext(t.Context(), cfg)
	if !audit.Correct || audit.LastLeafIndex != 1 || audit.LastLeafFileCount != 1 {
		t.Fatalf("unexpected audit: %+v", audit)
	}
}

func TestCustomPathTemplateMoveAndUndo(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "sample.pdf")
	if err := os.WriteFile(sourcePath, []byte("sample"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := PlanConfig{
		TargetDir:        targetDir,
		StartYear:        2021,
		LeafDirsPerMonth: 2,
		FilesPerLeaf:     2,
		PathTemplate:     `{YYYY}\自定义层\{YYYY}{MM}\叶{NN}`,
		Extensions:       []string{".pdf"},
	}
	plan := BuildMovePlanContext(t.Context(), []VideoFile{{
		SourcePath: sourcePath,
		Name:       "sample.pdf",
		Ext:        ".pdf",
		Size:       info.Size(),
		ModTime:    info.ModTime(),
	}}, cfg)
	manifestDir := t.TempDir()
	summary := ExecuteMovePlan(t.Context(), plan, MoveOptions{ManifestDir: manifestDir}, nil)
	if summary.Moved != 1 || summary.Failed != 0 {
		t.Fatalf("unexpected move summary: %+v", summary)
	}
	wantTarget := filepath.Join(targetDir, "2021", "自定义层", "202101", "叶01", "sample.pdf")
	if _, err := os.Stat(wantTarget); err != nil {
		t.Fatalf("custom target missing: %v", err)
	}
	undo := UndoManifest(t.Context(), summary.ManifestPath, nil)
	if undo.Restored != 1 || undo.Failed != 0 {
		t.Fatalf("unexpected undo summary: %+v", undo)
	}
	if _, err := os.Stat(sourcePath); err != nil {
		t.Fatalf("source not restored: %v", err)
	}
}

func TestValidatePathTemplateRejectsUnsafeOrAmbiguousRules(t *testing.T) {
	tests := []string{
		`{YYYY}\{YYYY}{MM}\{NN}\extra`,
		`{YYYY}\\{YYYY}{MM}\{NN}`,
		`{YYYY}\bad:name\{YYYY}{MM}\{NN}`,
		`{YYYY}\{UNKNOWN}\{YYYY}{MM}\{NN}`,
		"{YYYY}\\bad\x01name\\{YYYY}{MM}\\{NN}",
		`{YYYY}\` + strings.Repeat("长", 256) + `\{YYYY}{MM}\{NN}`,
	}
	for _, template := range tests {
		if err := ValidatePathTemplate(template); err == nil {
			t.Errorf("ValidatePathTemplate(%q) succeeded, want error", template)
		}
	}
}

func TestPathTemplateSupportsYearAndQuarterAsFinalContainer(t *testing.T) {
	cfg := PlanConfig{
		StartYear:        2021,
		LeafDirsPerMonth: 99,
		FilesPerLeaf:     10,
		PathTemplate:     `{YYYY}\{YYYY}S{Q}`,
	}
	if err := ValidatePathTemplate(cfg.PathTemplate); err != nil {
		t.Fatal(err)
	}
	if got := CalendarLeafPathForConfig(cfg, 1); got != filepath.Join("2021", "2021S1") {
		t.Fatalf("first path = %q", got)
	}
	if got := CalendarLeafPathForConfig(cfg, 5); got != filepath.Join("2022", "2022S1") {
		t.Fatalf("fifth path = %q", got)
	}
	for index := 1; index <= 8; index++ {
		path := CalendarLeafPathForConfig(cfg, index)
		if got, ok := CalendarLeafIndex(path, cfg); !ok || got != index {
			t.Fatalf("round trip %d: %q -> %d, %v", index, path, got, ok)
		}
	}
	if groups := EffectiveLeafDirsPerPeriod(cfg); groups != 1 {
		t.Fatalf("groups = %d, want 1 because template has no NN", groups)
	}
	result := CalculateCapacity(41, cfg)
	if result.PeriodName != "季度" || result.RequiredLeafDirs != 5 || result.RequiredQuarters != 5 || result.RequiredYears != 2 || result.FilesPerPeriod != 10 {
		t.Fatalf("unexpected capacity: %+v", result)
	}
}

func TestYearQuarterPlanIgnoresMonthGroupSetting(t *testing.T) {
	target := t.TempDir()
	files := make([]VideoFile, 11)
	for index := range files {
		files[index] = VideoFile{SourcePath: fmt.Sprintf("source-%02d.pdf", index), Name: fmt.Sprintf("source-%02d.pdf", index), Ext: ".pdf", Size: 1}
	}
	cfg := PlanConfig{TargetDir: target, StartYear: 2026, LeafDirsPerMonth: 99, FilesPerLeaf: 10, PathTemplate: `{YYYY}\{YYYY}S{Q}`, Extensions: []string{".pdf"}}
	plan := BuildMovePlanContext(t.Context(), files, cfg)
	if plan.ErrorCount != 0 || len(plan.Items) != 11 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if got, want := filepath.Dir(plan.Items[0].TargetPath), filepath.Join(target, "2026", "2026S1"); got != want {
		t.Fatalf("first dir = %q, want %q", got, want)
	}
	if got, want := filepath.Dir(plan.Items[10].TargetPath), filepath.Join(target, "2026", "2026S2"); got != want {
		t.Fatalf("eleventh dir = %q, want %q", got, want)
	}
	if plan.RequiredLeafDirs != 2 || plan.TargetDirCount != 2 {
		t.Fatalf("unexpected directory counts: %+v", plan)
	}
}

func TestPathTemplateSupportsYearOnlyAsFinalContainer(t *testing.T) {
	cfg := PlanConfig{StartYear: 2021, LeafDirsPerMonth: 9, FilesPerLeaf: 30, PathTemplate: `{YYYY}`}
	if err := ValidatePathTemplate(cfg.PathTemplate); err != nil {
		t.Fatal(err)
	}
	if got := CalendarLeafPathForConfig(cfg, 3); got != "2023" {
		t.Fatalf("path = %q, want 2023", got)
	}
	result := CalculateCapacity(61, cfg)
	if result.RequiredYears != 3 || result.RequiredLeafDirs != 3 || result.FilesPerYear != 30 {
		t.Fatalf("unexpected capacity: %+v", result)
	}
}

func TestPathTemplateSupportsQuarterGroupsWithoutMonths(t *testing.T) {
	cfg := PlanConfig{StartYear: 2021, LeafDirsPerMonth: 2, FilesPerLeaf: 10, PathTemplate: `{YYYY}\{YYYY}S{Q}\第{NN}组`}
	want := filepath.Join("2021", "2021S2", "第01组")
	if got := CalendarLeafPathForConfig(cfg, 3); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if index, ok := CalendarLeafIndex(want, cfg); !ok || index != 3 {
		t.Fatalf("round trip = %d, %v", index, ok)
	}
	result := CalculateCapacity(21, cfg)
	if result.RequiredLeafDirs != 3 || result.RequiredQuarters != 2 || result.RequiredYears != 1 || result.FilesPerPeriod != 20 {
		t.Fatalf("unexpected capacity: %+v", result)
	}
}

func TestDefaultPathTemplateRemainsLegacyFourLayers(t *testing.T) {
	cfg := NormalizePlanConfig(PlanConfig{StartYear: 2021, LeafDirsPerMonth: 3})
	if cfg.PathTemplate != DefaultPathTemplate {
		t.Fatalf("default template = %q", cfg.PathTemplate)
	}
	want := CalendarLeafPath(2021, 19, 3)
	if got := CalendarLeafPathForConfig(cfg, 19); got != want {
		t.Fatalf("default path = %q, legacy = %q", got, want)
	}
}

func TestCalendarLeafIndexRejectsQuarterMismatch(t *testing.T) {
	cfg := PlanConfig{StartYear: 2021, LeafDirsPerMonth: 3, PathTemplate: DefaultPathTemplate}
	path := filepath.Join("2021", "2021S2", "202101", "20210101")
	if index, ok := CalendarLeafIndex(path, cfg); ok || index != 0 {
		t.Fatalf("mismatched quarter accepted as %d", index)
	}
}

func TestPathTemplateSupportsSingleCombinedLayer(t *testing.T) {
	cfg := PlanConfig{StartYear: 2021, LeafDirsPerMonth: 2, PathTemplate: `{YYYY}{MM}{NN}`}
	if err := ValidatePathTemplate(cfg.PathTemplate); err != nil {
		t.Fatal(err)
	}
	if got := CalendarLeafPathForConfig(cfg, 25); got != "20220101" {
		t.Fatalf("path = %q, want 20220101", got)
	}
	if counts := EffectiveFolderCounts(cfg, 2); !equalInts(counts, []int{48}) {
		t.Fatalf("effective folder counts = %v, want [48]", counts)
	}
}

func equalInts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
