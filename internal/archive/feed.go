package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// FeedAudit verifies numbered leaves and per-leaf capacity without requiring
// historical leaves to be full. Existing files are never included in the plan.
type FeedAudit struct {
	Existing         int
	LastLeafIndex    int
	LastLeafCount    int
	RequiresAdoption bool
	Errors           []string
	Files            []VideoFile
	Dirs             []string
}

func AuditFeedTarget(ctx context.Context, cfg PlanConfig) FeedAudit {
	if ctx == nil {
		ctx = context.Background()
	}
	result := FeedAudit{}
	root := displayPath(strings.TrimSpace(cfg.TargetDir))
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
	legacy, err := CheckStructureConfig(ctx, cfg)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	result.RequiresAdoption = legacy
	cfgCap := normalizeCapacityConfig(CapacityConfig{LevelCount: cfg.LevelCount, LevelNames: cfg.LevelNames, FoldersPerLevel: cfg.FoldersPerLevel, FilesPerLeaf: cfg.FilesPerLeaf})
	if cfgCap.LevelCount < 1 || len(cfgCap.LevelNames) < cfgCap.LevelCount {
		result.Errors = append(result.Errors, "目标目录结构设置无效")
		return result
	}
	seenDirs := map[string]string{}
	seenLeafIndexes := map[int]struct{}{}
	err = filepath.WalkDir(fsPath(root), func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		clean := displayPath(path)
		if SamePath(clean, root) {
			return nil
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, relErr := filepath.Rel(root, clean)
		if relErr != nil {
			return relErr
		}
		depth := len(strings.Split(rel, string(filepath.Separator)))
		if depth > cfgCap.LevelCount || !feedLevelNameMatches(entry.Name(), cfgCap.LevelNames[depth-1]) {
			return filepath.SkipDir
		}
		if depth < cfgCap.LevelCount {
			return nil
		}
		leaf, parseErr := feedLeafIndex(root, clean, cfgCap)
		if parseErr != nil {
			return filepath.SkipDir
		}
		if _, exists := seenLeafIndexes[leaf]; exists {
			return fmt.Errorf("目标目录存在重复编号的叶目录: %d", leaf)
		}
		seenLeafIndexes[leaf] = struct{}{}
		entries, readErr := os.ReadDir(fsPath(clean))
		if readErr != nil {
			return readErr
		}
		count := 0
		for _, child := range entries {
			if child.IsDir() || child.Type()&os.ModeSymlink != 0 || !IsVideoExt(filepath.Ext(child.Name())) {
				continue
			}
			filePath := filepath.Join(clean, child.Name())
			fileInfo, statErr := child.Info()
			if statErr != nil {
				return statErr
			}
			if !fileInfo.Mode().IsRegular() || !os.SameFile(fileInfo, fileInfo) {
				return fmt.Errorf("目标叶目录的视频不是普通文件: %s", filePath)
			}
			fileRel, relErr := filepath.Rel(root, filePath)
			if relErr != nil {
				return relErr
			}
			result.Files = append(result.Files, VideoFile{SourcePath: filePath, RelPath: fileRel, Name: child.Name(), Ext: strings.ToLower(filepath.Ext(child.Name())), Size: fileInfo.Size(), ModTime: fileInfo.ModTime(), SourceInfo: fileInfo})
			count++
		}
		if count > cfgCap.FilesPerLeaf {
			return fmt.Errorf("叶目录文件数超出上限: %s，实际 %d，上限 %d", clean, count, cfgCap.FilesPerLeaf)
		}
		if leaf > result.LastLeafIndex {
			result.LastLeafIndex = leaf
			result.LastLeafCount = count
		}
		for current := clean; !SamePath(current, root); current = filepath.Dir(current) {
			seenDirs[strings.ToLower(filepath.Clean(current))] = current
		}
		return filepath.SkipDir
	})
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	result.Existing = len(result.Files)
	if result.Existing == 0 {
		result.Errors = append(result.Errors, "符合当前结构的目录中没有已归档视频，请先使用普通归档")
		return result
	}
	for _, dir := range seenDirs {
		result.Dirs = append(result.Dirs, dir)
	}
	sortVideoFilesByTime(result.Files)
	sort.Slice(result.Dirs, func(i, j int) bool { return strings.ToLower(result.Dirs[i]) < strings.ToLower(result.Dirs[j]) })
	return result
}

func feedLevelNameMatches(name, levelName string) bool {
	prefix := levelName + "_"
	if len(name) <= len(prefix) || !strings.EqualFold(name[:len(prefix)], prefix) {
		return false
	}
	index, err := strconv.Atoi(name[len(prefix):])
	return err == nil && index > 0
}
func feedLeafIndex(root, dir string, cfg CapacityConfig) (int, error) {
	name := filepath.Base(dir)
	prefix := cfg.LevelNames[len(cfg.LevelNames)-1] + "_"
	if len(name) < len(prefix) || !strings.EqualFold(name[:len(prefix)], prefix) {
		return 0, fmt.Errorf("受管文件位于不符合当前结构的目录: %s", dir)
	}
	index, err := strconv.Atoi(name[len(prefix):])
	if err != nil || index < 1 {
		return 0, fmt.Errorf("叶目录编号无效: %s", dir)
	}
	expected := filepath.Join(root, filepath.FromSlash(formatPath(cfg.LevelNames, pathIndexes(index, cfg.FoldersPerLevel))))
	if !SamePath(dir, expected) {
		return 0, fmt.Errorf("受管文件目录与原结构不一致: %s，应为 %s", dir, expected)
	}
	return index, nil
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
	capCfg := normalizeCapacityConfig(CapacityConfig{LevelCount: cfg.LevelCount, LevelNames: cfg.LevelNames, FoldersPerLevel: cfg.FoldersPerLevel, FilesPerLeaf: cfg.FilesPerLeaf})
	roomInTail := capCfg.FilesPerLeaf - audit.LastLeafCount
	additionalLeaves := ceilDiv(max(0, len(files)-roomInTail), capCfg.FilesPerLeaf)
	finalLeaf := audit.LastLeafIndex + additionalLeaves
	capCfg.TotalFiles = finalLeaf * capCfg.FilesPerLeaf
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
	plan.LastLeafPath = filepath.Join(cfg.TargetDir, filepath.FromSlash(formatPath(capCfg.LevelNames, pathIndexes(audit.LastLeafIndex, capCfg.FoldersPerLevel))))
	plan.LastLeafFileCount = audit.LastLeafCount
	resolver := newTargetResolver(ctx)
	dirs := map[string]struct{}{}
	for i, file := range files {
		leaf := audit.LastLeafIndex
		if i >= roomInTail {
			leaf += 1 + (i-roomInTail)/capCfg.FilesPerLeaf
		}
		dir := filepath.Join(cfg.TargetDir, filepath.FromSlash(formatPath(capCfg.LevelNames, pathIndexes(leaf, capCfg.FoldersPerLevel))))
		dirs[dir] = struct{}{}
		path, conflict, err := resolver.uniquePath(filepath.Join(dir, file.Name))
		item := MovePlanItem{SourcePath: file.SourcePath, TargetPath: path, Size: file.Size, ModTime: file.ModTime, SourceInfo: file.SourceInfo, Conflict: conflict, Status: "planned"}
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
