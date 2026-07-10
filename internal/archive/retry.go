package archive

import (
	"context"
	"os"
	"time"
)

func retryIO(ctx context.Context, attempts int, fn func() error) error {
	return retryIOPaths(ctx, attempts, nil, fn)
}

func retryIOPaths(ctx context.Context, attempts int, paths []string, fn func() error) error {
	delays := []time.Duration{150 * time.Millisecond, 300 * time.Millisecond, 450 * time.Millisecond}
	if hasNetworkPath(paths) {
		if attempts < 8 {
			attempts = 8
		}
		delays = []time.Duration{
			1 * time.Second,
			2 * time.Second,
			4 * time.Second,
			6 * time.Second,
			8 * time.Second,
			10 * time.Second,
			12 * time.Second,
		}
	}
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for i := 0; i < attempts; i++ {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		err = fn()
		if err == nil {
			return nil
		}
		if os.IsNotExist(err) || os.IsExist(err) {
			return err
		}
		if i+1 < attempts {
			delay := delays[len(delays)-1]
			if i < len(delays) {
				delay = delays[i]
			}
			if ctx != nil {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(delay):
				}
			} else {
				time.Sleep(delay)
			}
		}
	}
	return err
}

func hasNetworkPath(paths []string) bool {
	for _, path := range paths {
		if IsLikelyNetworkPath(path) {
			return true
		}
	}
	return false
}
