package archive

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func PreviewEmptyDirs(ctx context.Context, root string, protectedRoots []string) ([]string, []string) {
	var dirs []string
	var errors []string
	root = filepath.Clean(root)
	walkRoot := fsPath(root)
	protected := normalizeExcluded(protectedRoots)

	_ = filepath.WalkDir(walkRoot, func(path string, d os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			errors = appendLimited(errors, displayPath(path)+": "+err.Error(), 20)
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		clean := displayPath(path)
		if clean != root {
			if _, ok := protected[strings.ToLower(clean)]; ok {
				return filepath.SkipDir
			}
			dirs = append(dirs, clean)
		}
		return nil
	})

	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) > len(dirs[j])
	})

	emptyDirs := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if ctx.Err() != nil {
			break
		}
		var entries []os.DirEntry
		err := retryIOPaths(ctx, 3, []string{dir}, func() error {
			var readErr error
			entries, readErr = os.ReadDir(fsPath(dir))
			return readErr
		})
		if err != nil {
			errors = appendLimited(errors, dir+": "+err.Error(), 20)
			continue
		}
		if len(entries) == 0 {
			emptyDirs = append(emptyDirs, dir)
		}
	}
	return emptyDirs, errors
}

func CleanupEmptyDirs(ctx context.Context, root string, protectedRoots []string) (int, []string) {
	var dirs []string
	var errors []string
	root = filepath.Clean(root)
	walkRoot := fsPath(root)
	protected := normalizeExcluded(protectedRoots)

	_ = filepath.WalkDir(walkRoot, func(path string, d os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			errors = appendLimited(errors, displayPath(path)+": "+err.Error(), 20)
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		clean := displayPath(path)
		if clean != root {
			if _, ok := protected[strings.ToLower(clean)]; ok {
				return filepath.SkipDir
			}
			dirs = append(dirs, clean)
		}
		return nil
	})

	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) > len(dirs[j])
	})

	removed := 0
	for _, dir := range dirs {
		if ctx.Err() != nil {
			break
		}
		var entries []os.DirEntry
		err := retryIOPaths(ctx, 3, []string{dir}, func() error {
			var readErr error
			entries, readErr = os.ReadDir(fsPath(dir))
			return readErr
		})
		if err != nil {
			errors = appendLimited(errors, dir+": "+err.Error(), 20)
			continue
		}
		if len(entries) != 0 {
			continue
		}
		if err := retryIOPaths(ctx, 3, []string{dir}, func() error {
			return os.Remove(fsPath(dir))
		}); err != nil {
			errors = appendLimited(errors, dir+": "+err.Error(), 20)
			continue
		}
		removed++
	}

	return removed, errors
}
