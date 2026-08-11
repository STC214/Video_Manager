package archive

import (
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
		`{YYYY}\{YYYY}{MM}`,
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
