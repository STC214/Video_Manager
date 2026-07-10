package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const invalidWindowsNameChars = `<>:"/\|?*`

func ValidateLevelNames(names []string) error {
	for i, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return fmt.Errorf("第 %d 层目录名不能为空", i+1)
		}
		if strings.ContainsAny(trimmed, invalidWindowsNameChars) {
			return fmt.Errorf("第 %d 层目录名包含 Windows 非法字符: %s", i+1, invalidWindowsNameChars)
		}
		if strings.HasSuffix(trimmed, ".") {
			return fmt.Errorf("第 %d 层目录名不能以英文句点结尾", i+1)
		}
	}
	return nil
}

func CheckReadableDir(path string) error {
	return CheckReadableDirContext(context.Background(), path)
}

func CheckReadableDirContext(ctx context.Context, path string) error {
	var info os.FileInfo
	err := retryIOPaths(ctx, 3, []string{path}, func() error {
		var statErr error
		info, statErr = os.Stat(fsPath(path))
		return statErr
	})
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("不是目录")
	}
	var entries []os.DirEntry
	err = retryIOPaths(ctx, 3, []string{path}, func() error {
		var readErr error
		entries, readErr = os.ReadDir(fsPath(path))
		return readErr
	})
	if err != nil {
		return err
	}
	_ = entries
	return nil
}

func CheckTargetRoot(path string) error {
	return CheckTargetRootContext(context.Background(), path)
}

func CheckTargetRootContext(ctx context.Context, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("目标目录不能为空")
	}
	if targetInfo, err := os.Stat(fsPath(path)); err == nil {
		if !targetInfo.IsDir() {
			return fmt.Errorf("目标路径已存在但不是目录")
		}
		return CheckReadableDirContext(ctx, path)
	}
	var lastErr error
	for parent := filepath.Dir(filepath.Clean(path)); parent != "." && parent != path; parent = filepath.Dir(parent) {
		info, err := os.Stat(fsPath(parent))
		if err != nil && IsLikelyNetworkPath(parent) && filepath.Dir(parent) == parent {
			err = retryIOPaths(ctx, 3, []string{parent}, func() error {
				var statErr error
				info, statErr = os.Stat(fsPath(parent))
				return statErr
			})
		}
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("可用上级路径不是目录: %s", parent)
			}
			return CheckReadableDirContext(ctx, parent)
		}
		lastErr = err
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
	}
	return lastErr
}
