package archive

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type UndoSummary struct {
	Total     int
	Restored  int
	Failed    int
	Cancelled bool
	Error     string
}

func UndoManifest(ctx context.Context, manifestPath string, onProgress func(MoveProgress)) UndoSummary {
	summary := UndoSummary{}
	items, err := readManifestItems(manifestPath)
	if err != nil {
		summary.Failed = 1
		summary.Error = err.Error()
		return summary
	}
	summary.Total = len(items)

	for i := len(items) - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			summary.Cancelled = true
			break
		}
		item := MovePlanItem{
			SourcePath: items[i].TargetPath,
			TargetPath: items[i].SourcePath,
			Size:       items[i].Size,
			Status:     "planned",
		}
		var info os.FileInfo
		statErr := retryIOPaths(ctx, 3, []string{item.SourcePath}, func() error {
			var err error
			info, err = os.Stat(fsPath(item.SourcePath))
			return err
		})
		if os.IsNotExist(statErr) {
			var restoredInfo os.FileInfo
			restoredErr := retryIOPaths(ctx, 3, []string{item.TargetPath}, func() error {
				var err error
				restoredInfo, err = os.Stat(fsPath(item.TargetPath))
				return err
			})
			if restoredErr == nil && restoredInfo.Size() == item.Size {
				summary.Restored++
				if onProgress != nil {
					onProgress(MoveProgress{Index: summary.Restored + summary.Failed, Total: summary.Total,
						SourcePath: item.SourcePath, TargetPath: item.TargetPath, Status: "already_restored"})
				}
				continue
			}
		}
		if statErr == nil && info.Size() != item.Size {
			statErr = fmt.Errorf("archived file size changed: got %d, want %d", info.Size(), item.Size)
		}
		if statErr != nil {
			summary.Failed++
			if onProgress != nil {
				onProgress(MoveProgress{Index: summary.Restored + summary.Failed, Total: summary.Total,
					SourcePath: item.SourcePath, TargetPath: item.TargetPath, Status: "error", Error: statErr.Error()})
			}
			continue
		}
		status, err := moveOne(ctx, item)
		if err != nil {
			summary.Failed++
			if onProgress != nil {
				onProgress(MoveProgress{
					Index:      summary.Restored + summary.Failed,
					Total:      summary.Total,
					SourcePath: item.SourcePath,
					TargetPath: item.TargetPath,
					Status:     "error",
					Error:      err.Error(),
				})
			}
			continue
		}
		summary.Restored++
		if onProgress != nil {
			onProgress(MoveProgress{
				Index:      summary.Restored + summary.Failed,
				Total:      summary.Total,
				SourcePath: item.SourcePath,
				TargetPath: item.TargetPath,
				Status:     status,
			})
		}
	}
	return summary
}

func readManifestItems(path string) ([]MovePlanItem, error) {
	file, err := os.Open(fsPath(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var items []MovePlanItem
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			first = false
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 6 {
			continue
		}
		status := parts[0]
		if status != "moved" && status != "copied" {
			continue
		}
		size, _ := strconv.ParseInt(parts[3], 10, 64)
		items = append(items, MovePlanItem{
			Status:     status,
			SourcePath: parts[1],
			TargetPath: parts[2],
			Size:       size,
		})
	}
	return items, scanner.Err()
}
