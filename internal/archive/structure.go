package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const structureVersion = 1

type structureRecord struct {
	Version         int      `json:"version"`
	LevelCount      int      `json:"levelCount"`
	LevelNames      []string `json:"levelNames"`
	FoldersPerLevel []int    `json:"foldersPerLevel"`
	FilesPerLeaf    int      `json:"filesPerLeaf"`
	BindingRun      string   `json:"bindingRun,omitempty"`
	BaselineHash    string   `json:"baselineHash,omitempty"`
}

type structureIntent struct {
	Root string
	Path string
	Hash string
}

type deferredStructureRemoval struct {
	BindingRun string `json:"bindingRun"`
	Hash       string `json:"hash"`
}

func deferredStructurePath(root string) string {
	return structurePath(root) + ".owner-undone.json"
}

func structureIntentPath(manifestPath string) string {
	return manifestPath + ".structure-intent.json"
}

func snapshotVideoFiles(files []VideoFile) string {
	ordered := append([]VideoFile(nil), files...)
	sort.Slice(ordered, func(i, j int) bool {
		return strings.ToLower(displayPath(ordered[i].SourcePath)) < strings.ToLower(displayPath(ordered[j].SourcePath))
	})
	h := sha256.New()
	for _, file := range ordered {
		fmt.Fprintf(h, "%s\x00%d\x00%s\n", strings.ToLower(displayPath(file.SourcePath)), file.Size, file.ModTime.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func structurePath(root string) string {
	return filepath.Join(root, "_video-manager", "structure.json")
}

func bindingRunID(manifestPath string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.ToLower(displayPath(manifestPath)))))
}

// CheckNormalTargetContext keeps the initial-archive workflow separate from
// incremental feed: an occupied target must use the feed planner instead.
func CheckNormalTargetContext(ctx context.Context, cfg PlanConfig) error {
	if err := CheckTargetRootContext(ctx, cfg.TargetDir); err != nil {
		return err
	}
	root := strings.TrimSpace(cfg.TargetDir)
	info, err := os.Stat(fsPath(root))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("目标路径不是目录")
	}
	scan := ScanVideos(ctx, root, []string{filepath.Join(root, "_video-manager")})
	if scan.Cancelled {
		return ctx.Err()
	}
	if scan.ErrorCount > 0 {
		return fmt.Errorf("目标目录审计存在 %d 个读取错误: %s", scan.ErrorCount, strings.Join(scan.Errors, "; "))
	}
	if scan.VideoCount > 0 {
		return fmt.Errorf("目标目录已有 %d 个视频，请使用投料 Dry-run 续排", scan.VideoCount)
	}
	_, err = CheckStructureConfig(ctx, cfg)
	return err
}

func structureFromConfig(cfg PlanConfig) structureRecord {
	return structureRecord{Version: structureVersion, LevelCount: cfg.LevelCount,
		LevelNames:      append([]string(nil), cfg.LevelNames...),
		FoldersPerLevel: append([]int(nil), cfg.FoldersPerLevel...), FilesPerLeaf: cfg.FilesPerLeaf}
}

func sameStructure(record structureRecord, cfg PlanConfig) bool {
	want := structureFromConfig(cfg)
	if record.Version != want.Version || record.LevelCount != want.LevelCount || record.FilesPerLeaf != want.FilesPerLeaf ||
		len(record.LevelNames) != len(want.LevelNames) || len(record.FoldersPerLevel) != len(want.FoldersPerLevel) {
		return false
	}
	for i := range want.LevelNames {
		if record.LevelNames[i] != want.LevelNames[i] {
			return false
		}
	}
	for i := range want.FoldersPerLevel {
		if record.FoldersPerLevel[i] != want.FoldersPerLevel[i] {
			return false
		}
	}
	return true
}

// CheckStructureConfig returns true only when a pre-feature archive has no record.
func CheckStructureConfig(ctx context.Context, cfg PlanConfig) (legacy bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	path := structurePath(cfg.TargetDir)
	var data []byte
	err = RetryReadPaths(ctx, 3, []string{path}, func() error {
		var readErr error
		data, readErr = os.ReadFile(fsPath(path))
		return readErr
	})
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var record structureRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return false, fmt.Errorf("归档结构记录损坏: %w", err)
	}
	if !sameStructure(record, cfg) {
		return false, fmt.Errorf("当前目录结构设置与目标归档保存的原设定不一致")
	}
	return false, nil
}

// SaveStructureConfig binds a target to its initial rules and never overwrites
// a different existing record.
func SaveStructureConfig(ctx context.Context, cfg PlanConfig) error {
	_, err := saveStructureConfig(ctx, cfg, "")
	return err
}

// BindStructureToManifest persists the undo intent before publishing a newly
// created record. Existing matching records remain owned by their original run.
func BindStructureToManifest(ctx context.Context, cfg PlanConfig, manifestPath string, baselineFiles []VideoFile) error {
	legacy, err := CheckStructureConfig(ctx, cfg)
	if err != nil || !legacy {
		return err
	}
	if strings.TrimSpace(manifestPath) == "" {
		return fmt.Errorf("运行清单路径为空")
	}
	run := bindingRunID(manifestPath)
	record := structureFromConfig(cfg)
	record.BindingRun = run
	record.BaselineHash = snapshotVideoFiles(baselineFiles)
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	intent := structureIntent{Root: cfg.TargetDir, Path: structurePath(cfg.TargetDir), Hash: hash}
	intentData, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	sidecar := structureIntentPath(manifestPath)
	if err := publishNoReplace(ctx, sidecar, intentData); err != nil {
		if existing, readErr := os.ReadFile(fsPath(sidecar)); readErr != nil || string(existing) != string(intentData) {
			return err
		}
	}
	_, err = saveStructureData(ctx, cfg, data)
	return err
}

func readStructureSidecar(ctx context.Context, manifestPath string) (*structureIntent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(fsPath(structureIntentPath(manifestPath)))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var intent structureIntent
	if err := json.Unmarshal(data, &intent); err != nil {
		return nil, err
	}
	hash, err := hex.DecodeString(intent.Hash)
	if err != nil || len(hash) != 32 || !SamePath(intent.Path, structurePath(intent.Root)) {
		return nil, fmt.Errorf("结构意图记录无效")
	}
	return &intent, nil
}

func saveStructureConfig(ctx context.Context, cfg PlanConfig, run string) (bool, error) {
	record := structureFromConfig(cfg)
	record.BindingRun = run
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return false, err
	}
	return saveStructureData(ctx, cfg, data)
}

func saveStructureData(ctx context.Context, cfg PlanConfig, data []byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(cfg.TargetDir) == "" {
		return false, fmt.Errorf("目标目录为空")
	}
	legacy, err := CheckStructureConfig(ctx, cfg)
	if err != nil || !legacy {
		return false, err
	}
	path := structurePath(cfg.TargetDir)
	if err := publishNoReplace(ctx, path, data); err != nil {
		if otherLegacy, checkErr := CheckStructureConfig(ctx, cfg); checkErr != nil || !otherLegacy {
			return false, checkErr
		}
		return false, err
	}
	return true, nil
}

func publishNoReplace(ctx context.Context, path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(fsPath(dir), 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(fsPath(dir), "publish-*.tmp")
	if err != nil {
		return err
	}
	stagingInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	if !os.SameFile(stagingInfo, stagingInfo) {
		_ = file.Close()
		return fmt.Errorf("cannot capture published file identity")
	}
	defer removeOwnedStagingFile(file.Name(), stagingInfo)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Same-directory no-replace rename publishes only a complete, synced file.
	return renameNoReplace(file.Name(), path)
}

func removeOwnedStructureIntent(ctx context.Context, manifestPath string, intent structureIntent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !SamePath(intent.Path, structurePath(intent.Root)) {
		return fmt.Errorf("结构记录路径与目标不符")
	}
	data, err := os.ReadFile(fsPath(intent.Path))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var record structureRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return fmt.Errorf("结构记录损坏，已保留: %w", err)
	}
	if record.BindingRun != bindingRunID(manifestPath) {
		return nil // A pre-existing record belongs to another run.
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != intent.Hash {
		return fmt.Errorf("结构记录已变化，已保留")
	}
	if record.BaselineHash == "" {
		return nil // Older bindings lack a reliable pre-run snapshot.
	}
	scan := ScanVideos(ctx, intent.Root, []string{filepath.Join(intent.Root, "_video-manager")})
	if scan.Cancelled {
		return ctx.Err()
	}
	if scan.ErrorCount > 0 {
		return fmt.Errorf("目标目录复审存在 %d 个读取错误", scan.ErrorCount)
	}
	if snapshotVideoFiles(scan.Files) != record.BaselineHash {
		// The owner was undone, but later feeds still depend on this record.
		// Leave a durable intent so the last later undo can finish cleanup.
		data, err := json.Marshal(deferredStructureRemoval{BindingRun: record.BindingRun, Hash: intent.Hash})
		if err != nil {
			return err
		}
		path := deferredStructurePath(intent.Root)
		if err := publishNoReplace(ctx, path, data); err != nil {
			if existing, readErr := os.ReadFile(fsPath(path)); readErr != nil || string(existing) != string(data) {
				return err
			}
		}
		return nil
	}
	if err := removeStructureFile(intent.Path); err != nil {
		return err
	}
	return nil
}

func removeDeferredStructureMarker(ctx context.Context, root string) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	intentPath := deferredStructurePath(root)
	data, err := os.ReadFile(fsPath(intentPath))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var deferred deferredStructureRemoval
	if err := json.Unmarshal(data, &deferred); err != nil {
		return err
	}
	if deferred.BindingRun == "" || len(deferred.Hash) != 64 {
		return fmt.Errorf("invalid deferred structure removal")
	}
	markerPath := structurePath(root)
	markerData, err := os.ReadFile(fsPath(markerPath))
	if os.IsNotExist(err) {
		return removeStructureFile(intentPath)
	}
	if err != nil {
		return err
	}
	var record structureRecord
	if err := json.Unmarshal(markerData, &record); err != nil {
		return err
	}
	if record.BindingRun != deferred.BindingRun || fmt.Sprintf("%x", sha256.Sum256(markerData)) != deferred.Hash {
		return nil // A changed structure record is not ours to remove.
	}
	scan := ScanVideos(ctx, root, []string{filepath.Join(root, "_video-manager")})
	if scan.Cancelled {
		return ctx.Err()
	}
	if scan.ErrorCount > 0 {
		return fmt.Errorf("cannot rescan target for deferred structure cleanup: %d errors", scan.ErrorCount)
	}
	if snapshotVideoFiles(scan.Files) != record.BaselineHash {
		return nil
	}
	if err := removeStructureFile(markerPath); err != nil {
		return err
	}
	return removeStructureFile(intentPath)
}

func removeStructureFile(path string) error {
	info, err := os.Stat(fsPath(path))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(info, info) {
		return fmt.Errorf("cannot capture structure file identity: %s", path)
	}
	if err := removeOwnedFile(path, info); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
