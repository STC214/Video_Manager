package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func BuildMovePlanContext(ctx context.Context, files []VideoFile, cfg PlanConfig) MovePlan {
	result := MovePlan{}
	if len(files) == 0 {
		return result
	}
	cfg = NormalizePlanConfig(cfg)
	files = append([]VideoFile(nil), files...)
	sortFiles(files)
	if len(cfg.Extensions) == 0 {
		for _, file := range files {
			ext := file.Ext
			if ext == "" {
				ext = filepath.Ext(file.Name)
			}
			cfg.Extensions = append(cfg.Extensions, ext)
		}
	}
	cfg.Extensions = normalizeExtensions(cfg.Extensions)
	if err := ValidateConfigForFiles(cfg, len(files)); err != nil {
		result.TargetRoot = filepath.Clean(strings.TrimSpace(cfg.TargetDir))
		for _, file := range files {
			result.Items = append(result.Items, MovePlanItem{SourcePath: file.SourcePath, Size: file.Size, ModTime: file.ModTime, Status: "error", Error: err.Error()})
		}
		result.ErrorCount = len(result.Items)
		return result
	}
	capacity := CalculateCapacity(len(files), cfg)
	result.RequiredLeafDirs = capacity.RequiredLeafDirs
	result.ConfiguredCapacity = capacity.FilesPerYear
	result.EffectiveCapacity = saturatedMultiply(capacity.RequiredYears, capacity.FilesPerYear)
	result.EffectiveFolders = EffectiveFolderCounts(cfg, capacity.RequiredYears)
	result.TargetDirFileLimit = cfg.FilesPerLeaf
	result.ManagedExtensions = append([]string(nil), cfg.Extensions...)
	targetRoot := filepath.Clean(strings.TrimSpace(cfg.TargetDir))
	result.TargetRoot = targetRoot
	if targetRoot == "." || targetRoot == "" {
		for _, file := range files {
			result.Items = append(result.Items, MovePlanItem{SourcePath: file.SourcePath, Size: file.Size, ModTime: file.ModTime, Status: "error", Error: "target directory is empty"})
		}
		result.ErrorCount = len(result.Items)
		return result
	}
	resolver := newTargetResolver(ctx)
	targetDirs := map[string]struct{}{}
	leafIndex := 1
	filesInLeaf := -1
	maxLeafDirs := MaximumLeafDirs(cfg)
	for _, file := range files {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		var capacityErr error
		for {
			if leafIndex > maxLeafDirs {
				capacityErr = fmt.Errorf("目标目录现有文件占用容量后，归档年份将超过 9999")
				break
			}
			if filesInLeaf < 0 {
				targetDir := filepath.Join(targetRoot, CalendarLeafPathForConfig(cfg, leafIndex))
				filesInLeaf, capacityErr = resolver.matchingFileCount(targetDir, cfg.Extensions)
				if capacityErr != nil {
					break
				}
				result.ExistingTargetFiles += filesInLeaf
			}
			if filesInLeaf < cfg.FilesPerLeaf {
				break
			}
			leafIndex++
			filesInLeaf = -1
		}
		if capacityErr != nil {
			result.ErrorCount++
			result.Items = append(result.Items, MovePlanItem{
				SourcePath: file.SourcePath,
				Size:       file.Size,
				ModTime:    file.ModTime,
				Status:     "error",
				Error:      "target directory capacity check failed: " + capacityErr.Error(),
			})
			continue
		}
		targetDir := filepath.Join(targetRoot, CalendarLeafPathForConfig(cfg, leafIndex))
		targetDirs[targetDir] = struct{}{}
		managedExt, _ := matchingExtension(file.Name, cfg.Extensions)
		targetPath, conflict, err := resolver.uniquePath(filepath.Join(targetDir, file.Name), managedExt)
		if err != nil {
			result.ErrorCount++
			result.Items = append(result.Items, MovePlanItem{SourcePath: file.SourcePath, TargetPath: filepath.Join(targetDir, file.Name), Size: file.Size, ModTime: file.ModTime, Status: "error", Error: "target directory check failed: " + err.Error()})
			continue
		}
		if conflict {
			result.ConflictCount++
		}
		result.Items = append(result.Items, MovePlanItem{SourcePath: file.SourcePath, TargetPath: targetPath, Size: file.Size, ModTime: file.ModTime, Conflict: conflict, Status: "planned"})
		filesInLeaf++
		result.LastLeafPath = CalendarLeafPathForConfig(cfg, leafIndex)
		result.LastLeafFileCount = filesInLeaf
	}
	result.RequiredLeafDirs = leafIndex
	groups := EffectiveLeafDirsPerPeriod(cfg)
	layout, _ := AnalyzePathTemplate(cfg.PathTemplate)
	requiredPeriods := ceilDiv(leafIndex, groups)
	requiredYears := ceilDiv(requiredPeriods, layout.PeriodsPerYear)
	result.EffectiveCapacity = saturatedMultiply(requiredYears, capacity.FilesPerYear)
	result.EffectiveFolders = EffectiveFolderCounts(cfg, requiredYears)
	result.TargetDirCount = len(targetDirs)
	return result
}

type targetDirState struct {
	names map[string]struct{}
	files []string
}

type targetResolver struct {
	ctx      context.Context
	dirs     map[string]targetDirState
	dirErrs  map[string]error
	reserved map[string]struct{}
	ignored  map[string]struct{}
}

func newTargetResolver(ctx context.Context) *targetResolver {
	return newTargetResolverIgnoring(ctx, nil)
}

func newTargetResolverIgnoring(ctx context.Context, ignoredPaths []string) *targetResolver {
	if ctx == nil {
		ctx = context.Background()
	}
	ignored := make(map[string]struct{}, len(ignoredPaths))
	for _, path := range ignoredPaths {
		ignored[strings.ToLower(DisplayPath(path))] = struct{}{}
	}
	return &targetResolver{
		ctx:      ctx,
		dirs:     map[string]targetDirState{},
		dirErrs:  map[string]error{},
		reserved: map[string]struct{}{},
		ignored:  ignored,
	}
}

func (r *targetResolver) uniquePath(path, managedExt string) (string, bool, error) {
	dir := filepath.Dir(path)
	if err := r.loadDir(dir); err != nil {
		return "", false, err
	}
	if !r.exists(path) {
		r.reserve(path)
		return path, false, nil
	}
	name := filepath.Base(path)
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if managedExt != "" && strings.HasSuffix(strings.ToLower(name), managedExt) {
		ext = name[len(name)-len(managedExt):]
		base = name[:len(name)-len(managedExt)]
	}
	for i := 1; ; i++ {
		candidate := filepath.Join(dir, base+"_dup"+leftPad3(i)+ext)
		if !r.exists(candidate) {
			r.reserve(candidate)
			return candidate, true, nil
		}
	}
}

func (r *targetResolver) loadDir(dir string) error {
	key := strings.ToLower(DisplayPath(dir))
	if err, ok := r.dirErrs[key]; ok {
		return err
	}
	if _, ok := r.dirs[key]; ok {
		return nil
	}
	state := targetDirState{names: map[string]struct{}{}}
	var entries []os.DirEntry
	err := RetryIOPaths(r.ctx, 3, []string{dir}, func() error {
		var readErr error
		entries, readErr = os.ReadDir(FSPath(dir))
		return readErr
	})
	if os.IsNotExist(err) {
		if ancestorErr := r.validateTargetDirAncestors(dir); ancestorErr != nil {
			r.dirErrs[key] = ancestorErr
			return ancestorErr
		}
		r.dirs[key] = state
		return nil
	}
	if err != nil {
		r.dirErrs[key] = err
		return err
	}
	for _, entry := range entries {
		if _, ignored := r.ignored[strings.ToLower(DisplayPath(filepath.Join(dir, entry.Name())))]; ignored {
			continue
		}
		name := strings.ToLower(entry.Name())
		state.names[name] = struct{}{}
		if !entry.IsDir() {
			state.files = append(state.files, name)
		}
	}
	r.dirs[key] = state
	return nil
}

func (r *targetResolver) validateTargetDirAncestors(dir string) error {
	current := filepath.Clean(dir)
	for {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		var info os.FileInfo
		err := RetryIOPaths(r.ctx, 3, []string{current}, func() error {
			var statErr error
			info, statErr = os.Stat(FSPath(current))
			return statErr
		})
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("target path component is not a directory: %s", current)
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func (r *targetResolver) exists(path string) bool {
	if _, ok := r.reserved[strings.ToLower(path)]; ok {
		return true
	}
	state := r.dirs[strings.ToLower(DisplayPath(filepath.Dir(path)))]
	_, ok := state.names[strings.ToLower(filepath.Base(path))]
	return ok
}

func (r *targetResolver) reserve(path string) {
	r.reserved[strings.ToLower(path)] = struct{}{}
}

func (r *targetResolver) matchingFileCount(dir string, extensions []string) (int, error) {
	if err := r.loadDir(dir); err != nil {
		return 0, err
	}
	state := r.dirs[strings.ToLower(DisplayPath(dir))]
	count := 0
	for _, name := range state.files {
		if _, matched := matchingExtension(name, extensions); matched {
			count++
		}
	}
	return count, nil
}

func leftPad3(value int) string {
	text := strconv.Itoa(value)
	return strings.Repeat("0", max(0, 3-len(text))) + text
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
