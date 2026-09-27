package archive

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func PreviewEmptyDirs(ctx context.Context, root string, protectedRoots []string) ([]string, []string) {
	root = displayPath(root)
	protected := normalizeExcluded(protectedRoots)
	dirs, errors := collectDirsWithRetry(ctx, root, protected)

	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) > len(dirs[j])
	})

	emptyDirs := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if ctx.Err() != nil {
			break
		}
		var entries []os.DirEntry
		err := retryIOPathsWithMissing(ctx, 3, []string{dir}, true, func() error {
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
	root = displayPath(root)
	protected := normalizeExcluded(protectedRoots)
	dirs, errors := collectDirsWithRetry(ctx, root, protected)

	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) > len(dirs[j])
	})

	removed := 0
	for _, dir := range dirs {
		if ctx.Err() != nil {
			break
		}
		var entries []os.DirEntry
		err := retryIOPathsWithMissing(ctx, 3, []string{dir}, true, func() error {
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
		owned, err := os.Lstat(fsPath(dir))
		if err != nil {
			errors = appendLimited(errors, dir+": "+err.Error(), 20)
			continue
		}
		if !os.SameFile(owned, owned) {
			errors = appendLimited(errors, dir+": cannot capture directory identity", 20)
			continue
		}
		if err := retryIOPaths(ctx, 3, []string{dir}, func() error {
			return removeOwnedEmptyDir(dir, owned)
		}); err != nil {
			errors = appendLimited(errors, dir+": "+err.Error(), 20)
			continue
		}
		removed++
	}

	return removed, errors
}

func collectDirsWithRetry(ctx context.Context, root string, protected map[string]struct{}) ([]string, []string) {
	if ctx == nil {
		ctx = context.Background()
	}
	var dirs []string
	var errors []string
	var walk func(string)
	walk = func(dir string) {
		if ctx.Err() != nil {
			return
		}
		clean := displayPath(dir)
		if clean != root {
			if _, ok := protected[strings.ToLower(clean)]; ok {
				return
			}
			dirs = append(dirs, clean)
		}
		var entries []os.DirEntry
		err := retryIOPathsWithMissing(ctx, 3, []string{clean}, true, func() error {
			var readErr error
			entries, readErr = os.ReadDir(fsPath(clean))
			return readErr
		})
		if err != nil {
			if ctx.Err() == nil {
				errors = appendLimited(errors, clean+": "+err.Error(), 20)
			}
			return
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return
			}
			if entry.IsDir() {
				walk(filepath.Join(clean, entry.Name()))
			}
		}
	}
	walk(root)
	return dirs, errors
}
