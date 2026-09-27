package archive

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type UndoSummary struct {
	Total     int
	Restored  int
	Failed    int
	Cancelled bool
	Error     string
}

func ManifestHasUndoableItems(path string) bool {
	available, _ := CheckManifestUndoable(path)
	return available
}

func CheckManifestUndoable(path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, nil
	}
	items, pendingRecovery, _, err := readManifestState(context.Background(), path)
	return len(items) > 0 || pendingRecovery, err
}

func UndoManifest(ctx context.Context, manifestPath string, onProgress func(MoveProgress)) UndoSummary {
	return UndoManifestWithOptions(ctx, manifestPath, MoveOptions{}, onProgress)
}

func UndoManifestWithOptions(ctx context.Context, manifestPath string, opts MoveOptions, onProgress func(MoveProgress)) UndoSummary {
	if ctx == nil {
		ctx = context.Background()
	}
	summary := UndoSummary{}
	root, err := manifestTargetRoot(manifestPath)
	if err != nil {
		summary.Failed = 1
		summary.Error = err.Error()
		return summary
	}
	if root != "" {
		lock, lockErr := AcquireTargetMoveLock(ctx, root)
		if lockErr != nil {
			summary.Failed = 1
			summary.Error = "cannot lock target for undo: " + lockErr.Error()
			return summary
		}
		defer lock.Close()
	}
	items, _, binding, err := readManifestState(ctx, manifestPath)
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			summary.Cancelled = true
			return summary
		}
		summary.Failed = 1
		summary.Error = err.Error()
		return summary
	}
	summary.Total = len(items)
	sidecar, sidecarErr := readStructureSidecar(ctx, manifestPath)
	if binding == nil {
		binding = sidecar
	} else if sidecar != nil && (binding.Hash != sidecar.Hash || !SamePath(binding.Path, sidecar.Path)) {
		sidecarErr = fmt.Errorf("运行清单与结构意图记录不一致")
	}

	for i := len(items) - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			summary.Cancelled = true
			break
		}
		if opts.ReportItemStart && onProgress != nil {
			onProgress(MoveProgress{
				Index:      summary.Restored + summary.Failed,
				Total:      summary.Total,
				SourcePath: items[i].TargetPath,
				TargetPath: items[i].SourcePath,
				Status:     "processing",
			})
		}
		item := MovePlanItem{
			SourcePath: items[i].TargetPath,
			TargetPath: items[i].SourcePath,
			Size:       items[i].Size,
			ModTime:    items[i].ModTime,
			Status:     "planned",
		}
		var info os.FileInfo
		statErr := retryIOPathsWithMissing(ctx, 3, []string{item.SourcePath}, true, func() error {
			var err error
			info, err = os.Stat(fsPath(item.SourcePath))
			return err
		})
		if os.IsNotExist(statErr) {
			var restoredInfo os.FileInfo
			restoredErr := retryIOPathsWithMissing(ctx, 3, []string{item.TargetPath}, true, func() error {
				var err error
				restoredInfo, err = os.Stat(fsPath(item.TargetPath))
				return err
			})
			if restoredErr == nil && manifestItemMatches(restoredInfo, item) {
				summary.Restored++
				if onProgress != nil {
					onProgress(MoveProgress{Index: summary.Restored + summary.Failed, Total: summary.Total,
						SourcePath: item.SourcePath, TargetPath: item.TargetPath, Status: "already_restored"})
				}
				continue
			}
		}
		if statErr == nil && !manifestItemMatches(info, item) {
			statErr = fmt.Errorf("archived file metadata changed: size %d, time %s; want size %d, time %s",
				info.Size(), info.ModTime().Format(time.RFC3339Nano), item.Size, formatManifestTime(item.ModTime))
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
	if !summary.Cancelled && summary.Failed == 0 && summary.Restored == summary.Total {
		if sidecarErr != nil {
			summary.Failed++
			summary.Error = "文件已恢复，但结构意图记录读取失败: " + sidecarErr.Error()
		} else if binding != nil {
			if err := removeOwnedStructureIntent(ctx, manifestPath, *binding); err != nil {
				summary.Failed++
				summary.Error = "撤销文件已完成，但结构记录清理失败: " + err.Error()
			} else if sidecar != nil {
				if err := removeMatchingStructureSidecar(manifestPath, *sidecar); err != nil {
					summary.Failed++
					summary.Error = "文件已恢复，但结构意图记录清理失败: " + err.Error()
				}
			}
		}
	}
	if !summary.Cancelled && summary.Failed == 0 && summary.Restored == summary.Total {
		if err := removeDeferredStructureMarker(ctx, root); err != nil {
			summary.Failed++
			summary.Error = "deferred structure cleanup failed: " + err.Error()
		}
	}
	return summary
}

// New manifests record their target root before any file intent. For older
// manifests in the standard location, the parent of _video-manager is the root.
func manifestTargetRoot(manifestPath string) (string, error) {
	file, err := os.Open(fsPath(manifestPath))
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("invalid manifest header")
	}
	if scanner.Scan() {
		parts := strings.Split(scanner.Text(), "\t")
		if len(parts) > 0 && parts[0] == "target_root" {
			if len(parts) != 7 || strings.TrimSpace(parts[1]) == "" || parts[2] != "" || parts[3] != "0" {
				return "", fmt.Errorf("invalid manifest target root")
			}
			return parts[1], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	dir := filepath.Dir(manifestPath)
	if strings.EqualFold(filepath.Base(dir), "_video-manager") {
		return filepath.Dir(dir), nil
	}
	return "", nil
}

func readManifestItems(ctx context.Context, path string) ([]MovePlanItem, error) {
	items, _, _, err := readManifestState(ctx, path)
	return items, err
}

func readManifestState(ctx context.Context, path string) ([]MovePlanItem, bool, *structureIntent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var file *os.File
	err := retryIOPathsWithMissing(ctx, 3, []string{path}, true, func() error {
		var openErr error
		file, openErr = os.Open(fsPath(path))
		return openErr
	})
	if err != nil {
		return nil, false, nil, err
	}
	defer file.Close()

	var items []MovePlanItem
	var binding *structureIntent
	pending := make(map[string]MovePlanItem)
	var pendingOrder []string
	completed := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	first := true
	lineNumber := 0
	manifestColumns := 0
	seenTargetRoot := false
manifestLines:
	for scanner.Scan() {
		lineNumber++
		if err := ctx.Err(); err != nil {
			return items, len(pending) > 0, binding, err
		}
		line := scanner.Text()
		if first {
			first = false
			header := strings.Split(line, "\t")
			if (len(header) != 6 && len(header) != 7) || header[0] != "status" || header[1] != "source" ||
				header[2] != "target" || header[3] != "size" {
				return items, len(pending) > 0, binding, fmt.Errorf("invalid manifest header")
			}
			if len(header) == 7 && header[6] != "mod_time_rfc3339_nano" && header[6] != "mod_time_unix_nano" {
				return items, len(pending) > 0, binding, fmt.Errorf("invalid manifest modification-time column")
			}
			manifestColumns = len(header)
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != manifestColumns {
			if len(parts) > 0 && parts[0] != "" && strings.HasPrefix("structure_pending", parts[0]) && !scanner.Scan() && scanner.Err() == nil {
				break manifestLines // A prior version may have left a truncated final intent row.
			}
			return items, len(pending) > 0, binding, fmt.Errorf("invalid manifest row %d: got %d columns, expected %d",
				lineNumber, len(parts), manifestColumns)
		}
		status := parts[0]
		if status == "target_root" {
			if seenTargetRoot || lineNumber != 2 || manifestColumns != 7 || strings.TrimSpace(parts[1]) == "" || parts[2] != "" || parts[3] != "0" {
				return items, len(pending) > 0, binding, fmt.Errorf("invalid target root at manifest row %d", lineNumber)
			}
			seenTargetRoot = true
			continue
		}
		if status == "structure_pending" {
			if manifestColumns != 7 || strings.TrimSpace(parts[1]) == "" || !SamePath(parts[2], structurePath(parts[1])) || parts[3] != "0" {
				return items, len(pending) > 0, binding, fmt.Errorf("invalid structure intent at manifest row %d", lineNumber)
			}
			hashBytes, hashErr := hex.DecodeString(parts[5])
			if hashErr != nil || len(hashBytes) != 32 {
				return items, len(pending) > 0, binding, fmt.Errorf("invalid structure hash at manifest row %d", lineNumber)
			}
			if binding != nil {
				return items, len(pending) > 0, binding, fmt.Errorf("duplicate structure intent at manifest row %d", lineNumber)
			}
			binding = &structureIntent{Root: parts[1], Path: parts[2], Hash: parts[5]}
			continue
		}
		switch status {
		case "pending", "moved", "copied", "error", "cancelled":
		default:
			return items, len(pending) > 0, binding, fmt.Errorf("invalid manifest row %d: unknown status %q", lineNumber, status)
		}
		if (status == "pending" || status == "moved" || status == "copied") &&
			(strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "") {
			return items, len(pending) > 0, binding, fmt.Errorf("invalid manifest row %d: source or target is empty", lineNumber)
		}
		size, sizeErr := strconv.ParseInt(parts[3], 10, 64)
		if sizeErr != nil || size < 0 {
			return items, len(pending) > 0, binding, fmt.Errorf("invalid manifest row %d: invalid size %q", lineNumber, parts[3])
		}
		item := MovePlanItem{
			Status:     status,
			SourcePath: parts[1],
			TargetPath: parts[2],
			Size:       size,
		}
		if manifestColumns == 7 {
			parsed, parseErr := parseManifestTime(parts[6])
			if parseErr != nil {
				return items, len(pending) > 0, binding, fmt.Errorf("invalid manifest row %d: invalid modification time %q", lineNumber, parts[6])
			}
			item.ModTime = parsed
		}
		key := manifestItemKey(item)
		switch status {
		case "pending":
			if _, exists := pending[key]; !exists {
				pendingOrder = append(pendingOrder, key)
			}
			pending[key] = item
		case "moved", "copied":
			if _, exists := completed[key]; !exists {
				items = append(items, item)
				completed[key] = struct{}{}
			}
			delete(pending, key)
		case "error", "cancelled":
			delete(pending, key)
		}
	}
	if first {
		return items, len(pending) > 0, binding, fmt.Errorf("invalid manifest header")
	}
	if err := scanner.Err(); err != nil {
		return items, len(pending) > 0, binding, err
	}

	var recoveryErrors []string
	for _, key := range pendingOrder {
		item, exists := pending[key]
		if !exists {
			continue
		}
		recovered, recoveryErr := recoverPendingManifestItem(ctx, item)
		if recoveryErr != nil {
			recoveryErrors = append(recoveryErrors, recoveryErr.Error())
			continue
		}
		if recovered {
			item.Status = "recovered_pending"
			items = append(items, item)
		}
	}
	if len(recoveryErrors) > 0 {
		return items, true, binding, fmt.Errorf("manifest has ambiguous pending operations: %s", strings.Join(recoveryErrors, "; "))
	}
	return items, false, binding, nil
}

func parseManifestTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC(), nil
	}
	nanos, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, nanos).UTC(), nil
}

func manifestItemKey(item MovePlanItem) string {
	return strings.ToLower(displayPath(item.SourcePath)) + "\x00" +
		strings.ToLower(displayPath(item.TargetPath)) + "\x00" + strconv.FormatInt(item.Size, 10)
}

func recoverPendingManifestItem(ctx context.Context, item MovePlanItem) (bool, error) {
	var sourceInfo os.FileInfo
	sourceErr := retryIOPathsWithMissing(ctx, 3, []string{item.SourcePath}, true, func() error {
		var statErr error
		sourceInfo, statErr = os.Stat(fsPath(item.SourcePath))
		return statErr
	})
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var targetInfo os.FileInfo
	targetErr := retryIOPathsWithMissing(ctx, 3, []string{item.TargetPath}, true, func() error {
		var statErr error
		targetInfo, statErr = os.Stat(fsPath(item.TargetPath))
		return statErr
	})
	sourceExists := sourceErr == nil
	targetExists := targetErr == nil
	if sourceErr != nil && !os.IsNotExist(sourceErr) {
		return false, fmt.Errorf("cannot inspect pending source %s: %w", item.SourcePath, sourceErr)
	}
	if targetErr != nil && !os.IsNotExist(targetErr) {
		return false, fmt.Errorf("cannot inspect pending target %s: %w", item.TargetPath, targetErr)
	}
	if !sourceExists && targetExists && manifestItemMatches(targetInfo, item) {
		return true, nil
	}
	if sourceExists && !targetExists && manifestItemMatches(sourceInfo, item) {
		return false, nil
	}
	return false, fmt.Errorf("cannot determine pending move state: %s -> %s", item.SourcePath, item.TargetPath)
}

func manifestItemMatches(info os.FileInfo, item MovePlanItem) bool {
	if info == nil || info.IsDir() || info.Size() != item.Size {
		return false
	}
	return item.ModTime.IsZero() || info.ModTime().Equal(item.ModTime)
}

func formatManifestTime(value time.Time) string {
	if value.IsZero() {
		return "(not recorded)"
	}
	return value.Format(time.RFC3339Nano)
}
