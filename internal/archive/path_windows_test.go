package archive

import (
	"fmt"
	"syscall"
	"testing"
)

func TestIsCrossDeviceErrorRecognizesWrappedWindowsError(t *testing.T) {
	err := fmt.Errorf("rename failed: %w", syscall.Errno(17))
	if !isCrossDeviceError(err) {
		t.Fatal("wrapped ERROR_NOT_SAME_DEVICE was not recognized")
	}
	if isCrossDeviceError(syscall.Errno(5)) {
		t.Fatal("access denied was incorrectly classified as cross-device")
	}
}

func TestIsLikelyNetworkPathUNC(t *testing.T) {
	if !IsLikelyNetworkPath(`\\router\share\Videos`) {
		t.Fatal("expected UNC path to be treated as network path")
	}
	if !IsLikelyNetworkPath(`\\?\UNC\router\share\Videos`) {
		t.Fatal("expected extended UNC path to be treated as network path")
	}
}

func TestIsLikelyNetworkPathLocalRelative(t *testing.T) {
	if IsLikelyNetworkPath(`Videos\Local`) {
		t.Fatal("expected relative path to be treated as local")
	}
}

func TestDisplayPathTrimsExtendedPrefix(t *testing.T) {
	if got := displayPath(`\\?\C:\Videos\a.mp4`); got != `C:\Videos\a.mp4` {
		t.Fatalf("unexpected local display path: %q", got)
	}
	if got := displayPath(`\\?\UNC\router\share\Videos`); got != `\\router\share\Videos` {
		t.Fatalf("unexpected UNC display path: %q", got)
	}
}

func TestSamePathNormalizesWindowsPaths(t *testing.T) {
	if !SamePath(`Z:\Videos\Source\`, `z:\videos\source`) {
		t.Fatal("expected paths with case and trailing slash differences to match")
	}
	if !SamePath(`\\?\Z:\Videos\Source`, `Z:\Videos\Source`) {
		t.Fatal("expected extended and display paths to match")
	}
	if SamePath(`Z:\Videos\Source`, `Z:\Videos\Target`) {
		t.Fatal("different paths must not match")
	}
}
