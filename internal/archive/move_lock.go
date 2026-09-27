package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// TargetMoveLock serializes archive writes to one target, including the final
// target audit and structure binding. The byte-range lock also works between
// separate processes using the same Windows/SMB filesystem.
type TargetMoveLock struct {
	root string
	file *os.File
	mu   sync.Mutex
}

func AcquireTargetMoveLock(ctx context.Context, root string) (*TargetMoveLock, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("target root is empty")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Join(root, "_video-manager", ".move.lock")
	if err := os.MkdirAll(fsPath(filepath.Dir(path)), 0755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(fsPath(path), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	lock := &TargetMoveLock{root: root, file: file}
	for {
		var overlap windows.Overlapped
		err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlap)
		if err == nil {
			if ctx.Err() != nil {
				_ = lock.Close()
				return nil, ctx.Err()
			}
			return lock, nil
		}
		if err != windows.ERROR_LOCK_VIOLATION && err != windows.ERROR_IO_PENDING {
			_ = file.Close()
			return nil, fmt.Errorf("lock target %s: %w", root, err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (lock *TargetMoveLock) validFor(root string) bool {
	if lock == nil {
		return false
	}
	lock.mu.Lock()
	defer lock.mu.Unlock()
	return lock.file != nil && SamePath(lock.root, root)
}

func (lock *TargetMoveLock) Close() error {
	if lock == nil {
		return nil
	}
	lock.mu.Lock()
	defer lock.mu.Unlock()
	if lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	var overlap windows.Overlapped
	err := windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlap)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
