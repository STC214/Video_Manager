package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ScanResult struct {
	SourceDir    string
	Files        []VideoFile
	MatchedCount int
	IgnoredCount int
	TotalSize    int64
	ExtCounts    map[string]int
	ErrorCount   int
	Errors       []string
	Cancelled    bool
}

type ScanProgress struct {
	Visited      int
	MatchedCount int
	IgnoredCount int
	CurrentPath  string
}

func ParseExtensions(value string) ([]string, error) {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '，' || r == '；' || r == '|' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	seen := map[string]struct{}{}
	var result []string
	for _, field := range fields {
		ext := strings.ToLower(strings.TrimSpace(field))
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		if strings.ContainsAny(ext, `\/:*?"<>`) || len(ext) == 1 {
			return nil, fmt.Errorf("无效文件后缀: %s", field)
		}
		if _, exists := seen[ext]; exists {
			continue
		}
		seen[ext] = struct{}{}
		result = append(result, ext)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("请至少设置一个文件后缀，例如 .jpg,.png")
	}
	sort.Slice(result, func(i, j int) bool {
		if len(result[i]) != len(result[j]) {
			return len(result[i]) > len(result[j])
		}
		return result[i] < result[j]
	})
	return result, nil
}

func ScanFilesWithProgress(ctx context.Context, sourceDir string, excludedRoots, extensions []string, onProgress func(ScanProgress)) ScanResult {
	result := ScanResult{SourceDir: sourceDir, ExtCounts: map[string]int{}}
	if ctx == nil {
		ctx = context.Background()
	}
	extensions = normalizeExtensions(extensions)
	sourceDir = DisplayPath(sourceDir)
	excluded := normalizeExcluded(excludedRoots)
	visited := 0
	var walkDir func(string)
	walkDir = func(dir string) {
		if ctx.Err() != nil {
			result.Cancelled = true
			return
		}
		dir = DisplayPath(dir)
		visited++
		if shouldSkipDir(dir, sourceDir, excluded) {
			return
		}
		reportProgress(onProgress, visited, result, dir)

		var entries []os.DirEntry
		err := RetryIOPaths(ctx, 3, []string{dir}, func() error {
			var readErr error
			entries, readErr = os.ReadDir(FSPath(dir))
			return readErr
		})
		if err != nil {
			if ctx.Err() != nil {
				result.Cancelled = true
				return
			}
			result.ErrorCount++
			result.Errors = appendLimited(result.Errors, dir+": "+err.Error(), 20)
			return
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				result.Cancelled = true
				return
			}
			path := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				walkDir(path)
				if result.Cancelled {
					return
				}
				continue
			}
			visited++
			ext, matched := matchingExtension(entry.Name(), extensions)
			if !matched {
				result.IgnoredCount++
				reportProgress(onProgress, visited, result, path)
				continue
			}
			var info os.FileInfo
			statErr := RetryIOPaths(ctx, 3, []string{path}, func() error {
				var infoErr error
				info, infoErr = entry.Info()
				return infoErr
			})
			if statErr != nil {
				if ctx.Err() != nil {
					result.Cancelled = true
					return
				}
				result.ErrorCount++
				result.Errors = appendLimited(result.Errors, path+": "+statErr.Error(), 20)
				continue
			}
			rel, relErr := filepath.Rel(sourceDir, path)
			if relErr != nil {
				rel = entry.Name()
			}
			file := VideoFile{SourcePath: path, RelPath: rel, Name: entry.Name(), Ext: ext, Size: info.Size(), ModTime: info.ModTime()}
			result.Files = append(result.Files, file)
			result.MatchedCount++
			result.TotalSize += file.Size
			result.ExtCounts[ext]++
			reportProgress(onProgress, visited, result, path)
		}
	}
	walkDir(sourceDir)
	sortFiles(result.Files)
	return result
}

func sortFiles(files []VideoFile) {
	sort.SliceStable(files, func(i, j int) bool {
		a, b := files[i], files[j]
		if !a.ModTime.Equal(b.ModTime) {
			return a.ModTime.Before(b.ModTime)
		}
		if cmp := strings.Compare(strings.ToLower(a.RelPath), strings.ToLower(b.RelPath)); cmp != 0 {
			return cmp < 0
		}
		return strings.ToLower(a.SourcePath) < strings.ToLower(b.SourcePath)
	})
}

func normalizeExcluded(paths []string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, path := range paths {
		if value := strings.ToLower(DisplayPath(path)); value != "" && value != "." {
			result[value] = struct{}{}
		}
	}
	return result
}

func normalizeExtensions(extensions []string) []string {
	seen := make(map[string]struct{}, len(extensions))
	result := make([]string, 0, len(extensions))
	for _, value := range extensions {
		ext := strings.ToLower(strings.TrimSpace(value))
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		if _, exists := seen[ext]; exists {
			continue
		}
		seen[ext] = struct{}{}
		result = append(result, ext)
	}
	sort.Slice(result, func(i, j int) bool {
		if len(result[i]) != len(result[j]) {
			return len(result[i]) > len(result[j])
		}
		return result[i] < result[j]
	})
	return result
}

func matchingExtension(name string, extensions []string) (string, bool) {
	lowerName := strings.ToLower(name)
	for _, ext := range extensions {
		if strings.HasSuffix(lowerName, ext) && len(lowerName) > len(ext) {
			return ext, true
		}
	}
	return "", false
}

func shouldSkipDir(path, source string, excluded map[string]struct{}) bool {
	if path != source {
		switch strings.ToLower(filepath.Base(path)) {
		case ".git", "system volume information", "$recycle.bin":
			return true
		}
	}
	_, found := excluded[strings.ToLower(filepath.Clean(path))]
	return found
}

func reportProgress(fn func(ScanProgress), visited int, result ScanResult, path string) {
	if fn != nil {
		fn(ScanProgress{Visited: visited, MatchedCount: result.MatchedCount, IgnoredCount: result.IgnoredCount, CurrentPath: path})
	}
}

func appendLimited(items []string, item string, limit int) []string {
	if len(items) < limit {
		return append(items, item)
	}
	return items
}
