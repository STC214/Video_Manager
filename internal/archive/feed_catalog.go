package archive

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EnsureFeedArchiveCSV records the pre-feed managed files once. Run TSV files
// record subsequent additions; this baseline is never rewritten by a feed.
// The caller must hold the target move lock and pass a fresh successful audit.
func EnsureFeedArchiveCSV(ctx context.Context, cfg PlanConfig, audit FeedAudit, lock *TargetMoveLock) (string, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	path := filepath.Join(cfg.TargetDir, "_video-manager", "archive.csv")
	if err := ctx.Err(); err != nil {
		return path, false, err
	}
	if lock == nil || !lock.validFor(cfg.TargetDir) {
		return path, false, fmt.Errorf("目标目录未加锁，不能建立投料档案")
	}
	if len(audit.Errors) > 0 || audit.Existing == 0 || audit.Existing != len(audit.Files) {
		return path, false, fmt.Errorf("目标结构审计未通过，不能建立投料档案")
	}
	if info, err := os.Lstat(fsPath(path)); err == nil {
		if !info.Mode().IsRegular() {
			return path, false, fmt.Errorf("投料档案路径不是普通文件: %s", path)
		}
		return path, false, nil
	} else if !os.IsNotExist(err) {
		return path, false, err
	}
	files := append([]VideoFile(nil), audit.Files...)
	sort.Slice(files, func(i, j int) bool {
		left, right := strings.ToLower(files[i].RelPath), strings.ToLower(files[j].RelPath)
		if left == right {
			return files[i].RelPath < files[j].RelPath
		}
		return left < right
	})
	var data bytes.Buffer
	writer := csv.NewWriter(&data)
	writer.UseCRLF = true
	if err := writer.Write([]string{"relative_path", "size_bytes", "modified_at_rfc3339_nano"}); err != nil {
		return path, false, err
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return path, false, err
		}
		if !filepath.IsLocal(file.RelPath) || !SamePath(filepath.Join(cfg.TargetDir, file.RelPath), file.SourcePath) {
			return path, false, fmt.Errorf("投料档案文件路径不属于目标目录: %s", file.SourcePath)
		}
		info, err := os.Stat(fsPath(file.SourcePath))
		if err != nil {
			return path, false, err
		}
		if !info.Mode().IsRegular() || file.SourceInfo == nil || !sameOwnedObject(file.SourceInfo, info) || info.Size() != file.Size || !info.ModTime().Equal(file.ModTime) {
			return path, false, fmt.Errorf("目标文件在建档前已变化: %s", file.SourcePath)
		}
		// The ./ prefix prevents spreadsheet applications from interpreting a
		// configurable first directory name as a formula.
		rel := "./" + filepath.ToSlash(file.RelPath)
		if err := writer.Write([]string{rel, strconv.FormatInt(file.Size, 10), file.ModTime.UTC().Format(time.RFC3339Nano)}); err != nil {
			return path, false, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return path, false, err
	}
	if err := publishNoReplace(ctx, path, data.Bytes()); err != nil {
		if info, statErr := os.Lstat(fsPath(path)); statErr == nil && info.Mode().IsRegular() {
			return path, false, nil // Another process published a file first.
		}
		return path, false, err
	}
	return path, true, nil
}
