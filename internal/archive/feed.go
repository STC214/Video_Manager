package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FeedAudit verifies that managed files occupy a contiguous sequence of leaves.
// Existing files are never included in the move plan.
type FeedAudit struct {
	Existing         int
	RequiresAdoption bool
	Errors           []string
	Files            []VideoFile
}

func AuditFeedTarget(ctx context.Context, cfg PlanConfig) FeedAudit {
	if ctx == nil {
		ctx = context.Background()
	}
	result := FeedAudit{}
	root := strings.TrimSpace(cfg.TargetDir)
	if root == "" {
		result.Errors = append(result.Errors, "目标目录为空")
		return result
	}
	info, err := os.Stat(fsPath(root))
	if os.IsNotExist(err) {
		result.Errors = append(result.Errors, "目标目录尚未建立，投料需要已有归档")
		return result
	}
	if err != nil || !info.IsDir() {
		result.Errors = append(result.Errors, fmt.Sprintf("目标目录不可读取: %v", err))
		return result
	}
	scan := ScanVideos(ctx, root, []string{filepath.Join(root, "_video-manager")})
	result.Files = scan.Files
	result.Existing = len(scan.Files)
	result.Errors = append(result.Errors, scan.Errors...)
	if scan.Cancelled {
		result.Errors = append(result.Errors, "目标审计已取消")
	}
	if len(result.Errors) > 0 {
		return result
	}
	if result.Existing == 0 {
		result.Errors = append(result.Errors, "目标目录中没有已归档视频，请先使用普通归档")
		return result
	}
	legacy, err := CheckStructureConfig(ctx, cfg)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	result.RequiresAdoption = legacy
	cfgCap := normalizeCapacityConfig(CapacityConfig{LevelCount: cfg.LevelCount, LevelNames: cfg.LevelNames, FoldersPerLevel: cfg.FoldersPerLevel, FilesPerLeaf: cfg.FilesPerLeaf})
	counts := map[string]int{}
	for _, file := range scan.Files {
		counts[strings.ToLower(filepath.Clean(filepath.Dir(file.SourcePath)))]++
	}
	for i := 0; i < result.Existing; i += cfgCap.FilesPerLeaf {
		leaf := i/cfgCap.FilesPerLeaf + 1
		expected := filepath.Join(root, filepath.FromSlash(formatPath(cfgCap.LevelNames, pathIndexes(leaf, cfgCap.FoldersPerLevel))))
		want := cfgCap.FilesPerLeaf
		if left := result.Existing - i; left < want {
			want = left
		}
		key := strings.ToLower(filepath.Clean(expected))
		if counts[key] != want {
			result.Errors = append(result.Errors, fmt.Sprintf("结构不连续或叶目录数量不符: %s，应有 %d，实际 %d", expected, want, counts[key]))
			return result
		}
		delete(counts, key)
	}
	if len(counts) > 0 {
		result.Errors = append(result.Errors, "目标目录存在不符合当前结构的受管文件")
	}
	return result
}

func BuildFeedPlanContext(ctx context.Context, feedFiles []VideoFile, cfg PlanConfig) (MovePlan, FeedAudit) {
	if ctx == nil {
		ctx = context.Background()
	}
	audit := AuditFeedTarget(ctx, cfg)
	plan := MovePlan{TargetRoot: filepath.Clean(cfg.TargetDir), ExistingTargetFiles: audit.Existing, FeedFileCount: len(feedFiles), TargetDirFileLimit: cfg.FilesPerLeaf}
	for ext := range videoExts {
		plan.ManagedExtensions = append(plan.ManagedExtensions, ext)
	}
	sort.Strings(plan.ManagedExtensions)
	if len(audit.Errors) > 0 || len(feedFiles) == 0 || ctx.Err() != nil {
		return plan, audit
	}
	files := append([]VideoFile(nil), feedFiles...)
	sortVideoFilesByTime(files)
	capCfg := normalizeCapacityConfig(CapacityConfig{TotalFiles: audit.Existing + len(files), LevelCount: cfg.LevelCount, LevelNames: cfg.LevelNames, FoldersPerLevel: cfg.FoldersPerLevel, FilesPerLeaf: cfg.FilesPerLeaf})
	capResult := CalculateCapacity(capCfg)
	if !capResult.Enough {
		plan.AutoExpanded = true
		plan.ConfiguredCapacity = capResult.MaxCapacity
		capCfg.FoldersPerLevel = append([]int(nil), capCfg.FoldersPerLevel...)
		capCfg.FoldersPerLevel[0] = ceilDiv(capResult.RequiredLeafDirs, product(capCfg.FoldersPerLevel[1:]))
		capResult = CalculateCapacity(capCfg)
	}
	plan.EffectiveCapacity = capResult.MaxCapacity
	plan.EffectiveFolders = capCfg.FoldersPerLevel
	plan.RequiredLeafDirs = capResult.RequiredLeafDirs
	plan.FinalTargetFiles = audit.Existing + len(files)
	resolver := newTargetResolver(ctx)
	dirs := map[string]struct{}{}
	for i, file := range files {
		leaf := (audit.Existing+i)/capCfg.FilesPerLeaf + 1
		dir := filepath.Join(cfg.TargetDir, filepath.FromSlash(formatPath(capCfg.LevelNames, pathIndexes(leaf, capCfg.FoldersPerLevel))))
		dirs[dir] = struct{}{}
		path, conflict, err := resolver.uniquePath(filepath.Join(dir, file.Name))
		item := MovePlanItem{SourcePath: file.SourcePath, TargetPath: path, Size: file.Size, ModTime: file.ModTime, Conflict: conflict, Status: "planned"}
		if err != nil {
			item.Status, item.Error, item.TargetPath = "error", err.Error(), filepath.Join(dir, file.Name)
			plan.ErrorCount++
		}
		if conflict {
			plan.ConflictCount++
		}
		plan.Items = append(plan.Items, item)
	}
	plan.TargetDirCount = len(dirs)
	return plan, audit
}
