package archive

import (
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const driveRemote = 4

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

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procGetDriveTypeW = kernel32.NewProc("GetDriveTypeW")
)
