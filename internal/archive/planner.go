package archive

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type PlanConfig struct {
	TargetDir       string
	LevelCount      int
	LevelNames      []string
	FoldersPerLevel []int
	FilesPerLeaf    int
}

func ExportMovePlanTSV(plan MovePlan, outputDir string) (string, error) {
	return ExportMovePlanTSVContext(context.Background(), plan, outputDir)
}

func ExportMovePlanTSVContext(ctx context.Context, plan MovePlan, outputDir string) (string, error) {
	if strings.TrimSpace(outputDir) == "" {
		outputDir = filepath.Join(plan.TargetRoot, "_video-manager")
	}
	if err := retryIOPaths(ctx, 3, []string{outputDir}, func() error {
		return os.MkdirAll(fsPath(outputDir), 0755)
	}); err != nil {
		return "", err
	}
	path, file, err := createUniqueTSVFile(ctx, outputDir, "dry-run")
	if err != nil {
		return "", err
	}

	writer := bufio.NewWriter(file)
	if _, err := fmt.Fprintln(writer, "status\tsource\ttarget\tsize\tconflict\terror"); err != nil {
		_ = file.Close()
		return "", err
	}
	for _, item := range plan.Items {
		if err := writeManifest(writer, item.Status, item); err != nil {
			_ = file.Close()
			return "", err
		}
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func createUniqueTSVFile(ctx context.Context, dir, prefix string) (string, *os.File, error) {
	stamp := time.Now().Format("20060102-150405")
	for i := 0; i < 1000; i++ {
		name := prefix + "-" + stamp + ".tsv"
		if i > 0 {
			name = fmt.Sprintf("%s-%s-%03d.tsv", prefix, stamp, i)
		}
		path := filepath.Join(dir, name)
		file, err := os.OpenFile(fsPath(path), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
		if err == nil {
			return path, file, nil
		}
		if os.IsExist(err) {
			continue
		}
		err = retryIOPaths(ctx, 3, []string{path}, func() error {
			var openErr error
			file, openErr = os.OpenFile(fsPath(path), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
			return openErr
		})
		if err == nil {
			return path, file, nil
		}
		if !os.IsExist(err) {
			return "", nil, err
		}
	}
	return "", nil, fmt.Errorf("cannot allocate unique %s TSV filename", prefix)
}

type MovePlanItem struct {
	SourcePath string
	TargetPath string
	Size       int64
	ModTime    time.Time
	Conflict   bool
	Status     string
	Error      string
}

type MovePlan struct {
	Items            []MovePlanItem
	TargetRoot       string
	TargetDirCount   int
	ConflictCount    int
	ErrorCount       int
	RequiredLeafDirs int
}

func BuildMovePlan(files []VideoFile, cfg PlanConfig) MovePlan {
	return BuildMovePlanContext(context.Background(), files, cfg)
}

func BuildMovePlanContext(ctx context.Context, files []VideoFile, cfg PlanConfig) MovePlan {
	result := MovePlan{}
	if len(files) == 0 {
		return result
	}
	files = append([]VideoFile(nil), files...)
	sortVideoFilesByTime(files)

	capCfg := CapacityConfig{
		TotalFiles:      len(files),
		LevelCount:      cfg.LevelCount,
		LevelNames:      cfg.LevelNames,
		FoldersPerLevel: cfg.FoldersPerLevel,
		FilesPerLeaf:    cfg.FilesPerLeaf,
	}
	capCfg = normalizeCapacityConfig(capCfg)
	capResult := CalculateCapacity(capCfg)
	result.RequiredLeafDirs = capResult.RequiredLeafDirs
	if !capResult.Enough {
		for _, file := range files {
			result.Items = append(result.Items, MovePlanItem{
				SourcePath: file.SourcePath,
				Size:       file.Size,
				ModTime:    file.ModTime,
				Status:     "error",
				Error:      "capacity is not enough for all files",
			})
		}
		result.ErrorCount = len(result.Items)
		return result
	}

	targetRoot := filepath.Clean(strings.TrimSpace(cfg.TargetDir))
	result.TargetRoot = targetRoot
	if targetRoot == "." || targetRoot == "" {
		for _, file := range files {
			result.Items = append(result.Items, MovePlanItem{
				SourcePath: file.SourcePath,
				Size:       file.Size,
				ModTime:    file.ModTime,
				Status:     "error",
				Error:      "target directory is empty",
			})
		}
		result.ErrorCount = len(result.Items)
		return result
	}

	resolver := newTargetResolver(ctx)
	targetDirs := map[string]struct{}{}
	for index, file := range files {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		leafIndex := index/capCfg.FilesPerLeaf + 1
		leafPath := formatPath(capCfg.LevelNames, pathIndexes(leafIndex, capCfg.FoldersPerLevel))
		targetDir := filepath.Join(targetRoot, filepath.FromSlash(leafPath))
		targetDirs[targetDir] = struct{}{}

		targetPath, conflict, err := resolver.uniquePath(filepath.Join(targetDir, file.Name))
		if err != nil {
			result.ErrorCount++
			result.Items = append(result.Items, MovePlanItem{
				SourcePath: file.SourcePath,
				TargetPath: filepath.Join(targetDir, file.Name),
				Size:       file.Size,
				ModTime:    file.ModTime,
				Status:     "error",
				Error:      "target directory check failed: " + err.Error(),
			})
			continue
		}
		if conflict {
			result.ConflictCount++
		}
		result.Items = append(result.Items, MovePlanItem{
			SourcePath: file.SourcePath,
			TargetPath: targetPath,
			Size:       file.Size,
			ModTime:    file.ModTime,
			Conflict:   conflict,
			Status:     "planned",
		})
	}
	result.TargetDirCount = len(targetDirs)
	return result
}

type targetResolver struct {
	ctx      context.Context
	dirs     map[string]map[string]struct{}
	dirErrs  map[string]error
	reserved map[string]struct{}
}

func newTargetResolver(ctx context.Context) *targetResolver {
	return &targetResolver{
		ctx:      ctx,
		dirs:     map[string]map[string]struct{}{},
		dirErrs:  map[string]error{},
		reserved: map[string]struct{}{},
	}
}

func (r *targetResolver) uniquePath(path string) (string, bool, error) {
	dir := filepath.Dir(path)
	if err := r.loadDir(dir); err != nil {
		return "", false, err
	}
	if !r.exists(path) {
		r.reserve(path)
		return path, false, nil
	}

	ext := filepath.Ext(path)
	base := strings.TrimSuffix(filepath.Base(path), ext)
	for i := 1; ; i++ {
		candidate := filepath.Join(dir, base+"_dup"+leftPad3(i)+ext)
		if !r.exists(candidate) {
			r.reserve(candidate)
			return candidate, true, nil
		}
	}
}

func (r *targetResolver) loadDir(dir string) error {
	key := strings.ToLower(filepath.Clean(dir))
	if err, ok := r.dirErrs[key]; ok {
		return err
	}
	if _, ok := r.dirs[key]; ok {
		return nil
	}
	names := map[string]struct{}{}
	entries, err := os.ReadDir(fsPath(dir))
	if os.IsNotExist(err) {
		r.dirs[key] = names
		return nil
	}
	if err != nil {
		err = retryIOPaths(r.ctx, 3, []string{dir}, func() error {
			var readErr error
			entries, readErr = os.ReadDir(fsPath(dir))
			return readErr
		})
	}
	if err != nil {
		r.dirErrs[key] = err
		return err
	}
	for _, entry := range entries {
		names[strings.ToLower(entry.Name())] = struct{}{}
	}
	r.dirs[key] = names
	return nil
}

func (r *targetResolver) exists(path string) bool {
	if _, ok := r.reserved[strings.ToLower(path)]; ok {
		return true
	}
	names := r.dirs[strings.ToLower(filepath.Clean(filepath.Dir(path)))]
	_, ok := names[strings.ToLower(filepath.Base(path))]
	return ok
}

func (r *targetResolver) reserve(path string) {
	r.reserved[strings.ToLower(path)] = struct{}{}
}

func leftPad3(value int) string {
	return strings.Repeat("0", max(0, 3-len(strconv.Itoa(value)))) + strconv.Itoa(value)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
