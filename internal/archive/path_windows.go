package archive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// removeOwnedFile marks the open file object for deletion, rather than deleting
// a pathname after an identity check. A concurrent path replacement therefore
// cannot redirect cleanup to another file.
func removeOwnedFile(path string, owned os.FileInfo) error {
	if owned == nil || !owned.Mode().IsRegular() {
		return fmt.Errorf("not an owned regular file: %s", path)
	}
	name, err := windows.UTF16PtrFromString(fsPath(path))
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.DELETE|0x80, // FILE_READ_ATTRIBUTES
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(handle), path)
	defer func() { _ = file.Close() }()
	current, err := file.Stat()
	if err != nil {
		return err
	}
	if !sameOwnedObject(current, owned) {
		return fmt.Errorf("file ownership changed: %s", path)
	}
	if !current.Mode().IsRegular() {
		return fmt.Errorf("file type changed: %s", path)
	}
	deleteFlag := byte(1)
	r1, _, callErr := procSetFileInformationByHandle.Call(uintptr(handle), 4, uintptr(unsafe.Pointer(&deleteFlag)), 1)
	if r1 == 0 {
		return fmt.Errorf("delete owned file %s: %w", path, callErr)
	}
	return file.Close()
}

// removeOwnedEmptyDir removes only the directory object observed during the
// cleanup scan. Windows rejects deletion when it has become non-empty.
func removeOwnedEmptyDir(path string, owned os.FileInfo) error {
	if owned == nil || !owned.IsDir() || owned.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("not an owned directory: %s", path)
	}
	name, err := windows.UTF16PtrFromString(fsPath(path))
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.DELETE|0x80,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(handle), path)
	defer func() { _ = file.Close() }()
	current, err := file.Stat()
	if err != nil {
		return err
	}
	if !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !sameOwnedObject(current, owned) {
		return fmt.Errorf("directory ownership changed: %s", path)
	}
	deleteFlag := byte(1)
	r1, _, callErr := procSetFileInformationByHandle.Call(uintptr(handle), 4, uintptr(unsafe.Pointer(&deleteFlag)), 1)
	if r1 == 0 {
		return fmt.Errorf("delete owned empty directory %s: %w", path, callErr)
	}
	return file.Close()
}

func sameOwnedObject(current, owned os.FileInfo) bool {
	if current == nil || owned == nil || !os.SameFile(current, owned) {
		return false
	}
	a, okA := current.Sys().(*syscall.Win32FileAttributeData)
	b, okB := owned.Sys().(*syscall.Win32FileAttributeData)
	return okA && okB && a.CreationTime == b.CreationTime
}

// renameNoReplace atomically moves a file without replacing a target that
// appeared after planning. os.Rename on Windows uses REPLACE_EXISTING.
func renameNoReplace(source, target string) error {
	from, err := windows.UTF16PtrFromString(fsPath(source))
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(fsPath(target))
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}

const (
	driveRemote        = 4
	errorNotSameDevice = syscall.Errno(17)
)

func fsPath(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || strings.HasPrefix(path, `\\?\`) {
		return path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(path, `\\`)
	}
	if len(path) >= 2 && path[1] == ':' {
		return `\\?\` + path
	}
	return path
}

// FSPath returns a Windows filesystem path with long-path support.
// It is exported for sibling tools that share the archive I/O implementation.
func FSPath(path string) string {
	return fsPath(path)
}

func displayPath(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	if strings.HasPrefix(path, `\\?\`) {
		return strings.TrimPrefix(path, `\\?\`)
	}
	return path
}

// DisplayPath canonicalizes extended Windows paths for comparison and display.
func DisplayPath(path string) string {
	return displayPath(path)
}

func SamePath(left, right string) bool {
	left = displayPath(left)
	right = displayPath(right)
	if left == "." || right == "." || left == "" || right == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func IsLikelyNetworkPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if strings.HasPrefix(path, `\\?\UNC\`) || strings.HasPrefix(path, `\\`) {
		return true
	}
	if len(path) >= 2 && path[1] == ':' {
		root := strings.ToUpper(path[:2]) + `\`
		ret, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(root))))
		return ret == driveRemote
	}
	return false
}

func isCrossDeviceError(err error) bool {
	return errors.Is(err, errorNotSameDevice)
}

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procGetDriveTypeW              = kernel32.NewProc("GetDriveTypeW")
	procSetFileInformationByHandle = kernel32.NewProc("SetFileInformationByHandle")
)
