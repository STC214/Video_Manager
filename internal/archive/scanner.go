package archive

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type VideoFile struct {
	SourcePath string
	RelPath    string
	Name       string
	Ext        string
	Size       int64
	ModTime    time.Time
}

type ScanResult struct {
	SourceDir     string
	Files         []VideoFile
	VideoCount    int
	NonVideoCount int
	TotalSize     int64
	ExtCounts     map[string]int
	ErrorCount    int
	Errors        []string
	Cancelled     bool
}

type ScanProgress struct {
	Visited       int
	VideoCount    int
	NonVideoCount int
	CurrentPath   string
}

var videoExts = map[string]struct{}{
	".mp4": {}, ".mkv": {}, ".avi": {}, ".mov": {}, ".wmv": {}, ".flv": {},
	".webm": {}, ".m4v": {}, ".ts": {}, ".m2ts": {}, ".mpg": {}, ".mpeg": {},
	".rmvb": {}, ".3gp": {},
}

func ScanVideos(ctx context.Context, sourceDir string, excludedRoots []string) ScanResult {
	return ScanVideosWithProgress(ctx, sourceDir, excludedRoots, nil)
}

func ScanVideosWithProgress(ctx context.Context, sourceDir string, excludedRoots []string, onProgress func(ScanProgress)) ScanResult {
	result := ScanResult{
		SourceDir: sourceDir,
		ExtCounts: map[string]int{},
	}
	sourceDir = filepath.Clean(sourceDir)
	walkRoot := fsPath(sourceDir)
	excluded := normalizeExcluded(excludedRoots)
	visited := 0

	_ = filepath.WalkDir(walkRoot, func(path string, d os.DirEntry, err error) error {
		if ctx.Err() != nil {
			result.Cancelled = true
			return ctx.Err()
		}
		visited++
		if err != nil {
			result.ErrorCount++
			result.Errors = appendLimited(result.Errors, displayPath(path)+": "+err.Error(), 20)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		cleanPath := displayPath(path)
		if d.IsDir() {
			if shouldSkipDir(cleanPath, sourceDir, excluded) {
				return filepath.SkipDir
			}
			reportScanProgress(onProgress, visited, result, cleanPath)
			return nil
		}

		ext := strings.ToLower(filepath.Ext(d.Name()))
		if _, ok := videoExts[ext]; !ok {
			result.NonVideoCount++
			reportScanProgress(onProgress, visited, result, cleanPath)
			return nil
		}

		info, statErr := d.Info()
		if statErr != nil {
			result.ErrorCount++
			result.Errors = appendLimited(result.Errors, cleanPath+": "+statErr.Error(), 20)
			return nil
		}
		rel, relErr := filepath.Rel(sourceDir, cleanPath)
		if relErr != nil {
			rel = d.Name()
		}
		file := VideoFile{
			SourcePath: cleanPath,
			RelPath:    rel,
			Name:       d.Name(),
			Ext:        ext,
			Size:       info.Size(),
			ModTime:    info.ModTime(),
		}
		result.Files = append(result.Files, file)
		result.VideoCount++
		result.TotalSize += file.Size
		result.ExtCounts[ext]++
		reportScanProgress(onProgress, visited, result, cleanPath)
		return nil
	})

	sortVideoFilesByTime(result.Files)

	return result
}

func sortVideoFilesByTime(files []VideoFile) {
	sort.SliceStable(files, func(i, j int) bool {
		a, b := files[i], files[j]
		if !a.ModTime.Equal(b.ModTime) {
			return a.ModTime.Before(b.ModTime)
		}
		aRel := strings.ToLower(a.RelPath)
		bRel := strings.ToLower(b.RelPath)
		if aRel != bRel {
			return aRel < bRel
		}
		aName := strings.ToLower(a.Name)
		bName := strings.ToLower(b.Name)
		if aName != bName {
			return aName < bName
		}
		return strings.ToLower(a.SourcePath) < strings.ToLower(b.SourcePath)
	})
}

func reportScanProgress(onProgress func(ScanProgress), visited int, result ScanResult, path string) {
	if onProgress == nil {
		return
	}
	onProgress(ScanProgress{
		Visited:       visited,
		VideoCount:    result.VideoCount,
		NonVideoCount: result.NonVideoCount,
		CurrentPath:   path,
	})
}

func IsVideoExt(ext string) bool {
	_, ok := videoExts[strings.ToLower(ext)]
	return ok
}

func normalizeExcluded(paths []string) map[string]struct{} {
	excluded := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = strings.ToLower(filepath.Clean(strings.TrimSpace(path)))
		if path != "." && path != "" {
			excluded[path] = struct{}{}
		}
	}
	return excluded
}

func shouldSkipDir(path, sourceDir string, excluded map[string]struct{}) bool {
	name := strings.ToLower(filepath.Base(path))
	if path != sourceDir {
		switch name {
		case ".git", "system volume information", "$recycle.bin":
			return true
		}
	}
	_, ok := excluded[strings.ToLower(path)]
	return ok
}

func appendLimited(items []string, item string, limit int) []string {
	if len(items) >= limit {
		return items
	}
	return append(items, item)
}
