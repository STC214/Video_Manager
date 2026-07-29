package archive

import (
	"context"
	"io/fs"
	"testing"
)

func TestRetryReadPathsRetriesMissingNetworkPath(t *testing.T) {
	calls := 0
	err := RetryReadPaths(context.Background(), 2, []string{`\\server\share\files`}, func() error {
		calls++
		if calls == 1 {
			return fs.ErrNotExist
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}
