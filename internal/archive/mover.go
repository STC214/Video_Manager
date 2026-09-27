package archive

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type MoveOptions struct {
	ManifestDir     string
	ReportItemStart bool
	TargetLock      *TargetMoveLock
}

type MoveProgress struct {
	Index      int
	Total      int
	SourcePath string
	TargetPath string
	Status     string
	Error      string
}

type MoveSummary struct {
	Total           int
	Moved           int
	Failed          int
	PendingRecovery bool
	Cancelled       bool
	ManifestPath    string
	Error           string
}

var errTargetCapacityChanged = errors.New("target directory capacity changed after dry-run")

var copyBufferPool = sync.Pool{
	New: func() any {
		buffer := make([]byte, 8*1024*1024)
		return &buffer
	},
}

func ExecuteMovePlan(ctx context.Context, plan MovePlan, opts MoveOptions, onProgress func(MoveProgress)) MoveSummary {
	if ctx == nil {
		ctx = context.Background()
	}
	summary := MoveSummary{Total: len(plan.Items)}
	if len(plan.Items) == 0 {
		return summary
	}
	if opts.TargetLock != nil {
		if !opts.TargetLock.validFor(plan.TargetRoot) {
			summary.Failed = summary.Total
			summary.Error = "target move lock is not held for this target"
			return summary
		}
	} else {
		lock, err := AcquireTargetMoveLock(ctx, plan.TargetRoot)
		if err != nil {
			summary.Failed = summary.Total
			summary.Error = "cannot lock target: " + err.Error()
			return summary
		}
		defer lock.Close()
	}

	if strings.TrimSpace(opts.ManifestDir) == "" {
		opts.ManifestDir = filepath.Join(plan.TargetRoot, "_video-manager")
	}
	if err := retryIOPaths(ctx, 3, []string{opts.ManifestDir}, func() error {
		return os.MkdirAll(fsPath(opts.ManifestDir), 0755)
	}); err != nil {
		summary.Failed = summary.Total
		summary.Error = "cannot create manifest directory: " + err.Error()
		return summary
	}
	var manifest *os.File
	var err error
	summary.ManifestPath, manifest, err = createUniqueTSVFile(ctx, opts.ManifestDir, "archive-run")
	if err != nil {
		summary.ManifestPath = ""
		summary.Failed = summary.Total
		summary.Error = "cannot create manifest: " + err.Error()
		return summary
	}
	writer := bufio.NewWriter(manifest)
	if _, err := fmt.Fprintln(writer, "status\tsource\ttarget\tsize\tconflict\terror\tmod_time_rfc3339_nano"); err != nil {
		_ = manifest.Close()
		summary.Failed = summary.Total
		summary.Error = "cannot initialize manifest: " + err.Error()
		return summary
	}
	if _, err := fmt.Fprintf(writer, "target_root\t%s\t\t0\tfalse\t\t\n", plan.TargetRoot); err != nil {
		_ = manifest.Close()
		summary.Failed = summary.Total
		summary.Error = "cannot record target root: " + err.Error()
		return summary
	}
	if err := flushAndSyncManifest(writer, manifest); err != nil {
		_ = manifest.Close()
		summary.Failed = summary.Total
		summary.Error = "cannot initialize manifest: " + err.Error()
		return summary
	}

	for i, item := range plan.Items {
		if ctx.Err() != nil {
			summary.Cancelled = true
			writeManifest(writer, "cancelled", item)
			break
		}
		if opts.ReportItemStart && onProgress != nil {
			onProgress(MoveProgress{
				Index:      i,
				Total:      len(plan.Items),
				SourcePath: item.SourcePath,
				TargetPath: item.TargetPath,
				Status:     "processing",
			})
		}
		if err := writeManifest(writer, "pending", item); err != nil {
			summary.Error = "cannot write move intent: " + err.Error()
			summary.Failed += len(plan.Items) - i
			break
		}
		if err := flushAndSyncManifest(writer, manifest); err != nil {
			summary.Error = "cannot persist move intent: " + err.Error()
			summary.Failed += len(plan.Items) - i
			break
		}

		capacityErr := checkTargetCapacity(ctx, item.TargetPath, plan.TargetDirFileLimit, plan.ManagedExtensions)
		if capacityErr != nil && ctx.Err() != nil {
			summary.Cancelled = true
			_ = writeManifest(writer, "cancelled", item)
			break
		}
		status := ""
		moveErr := capacityErr
		if moveErr == nil {
			status, moveErr = moveOne(ctx, item)
		}
		var publishedErr *publishedCopyError
		if errors.As(moveErr, &publishedErr) {
			// The already-synced pending row must remain unresolved; recording
			// a regular error row would hide the published target from recovery.
			summary.PendingRecovery = true
			summary.Error = "published copy needs recovery: " + moveErr.Error()
			summary.Failed += len(plan.Items) - i
			break
		}
		if moveErr != nil {
			item.Status = "error"
			item.Error = moveErr.Error()
			summary.Failed++
		} else {
			item.Status = status
			summary.Moved++
		}
		if err := writeManifest(writer, item.Status, item); err != nil {
			summary.Error = "manifest write failed after file operation: " + err.Error()
			summary.Failed += len(plan.Items) - i - 1
			break
		}
		if err := flushAndSyncManifest(writer, manifest); err != nil {
			summary.Error = "manifest persistence failed after file operation: " + err.Error()
			summary.Failed += len(plan.Items) - i - 1
			break
		}
		if onProgress != nil {
			onProgress(MoveProgress{
				Index:      i + 1,
				Total:      len(plan.Items),
				SourcePath: item.SourcePath,
				TargetPath: item.TargetPath,
				Status:     item.Status,
				Error:      item.Error,
			})
		}
		if moveErr != nil && plan.StopOnError {
			summary.Error = "move plan stopped after an item failed: " + moveErr.Error()
			summary.Failed += len(plan.Items) - i - 1
			break
		}
		if capacityErr != nil {
			summary.Error = capacityErr.Error()
			summary.Failed += len(plan.Items) - i - 1
			break
		}
	}
	if err := flushAndSyncManifest(writer, manifest); err != nil && summary.Error == "" {
		summary.Error = "manifest final persistence failed: " + err.Error()
	}
	if err := manifest.Close(); err != nil && summary.Error == "" {
		summary.Error = "manifest close failed: " + err.Error()
	}

	return summary
}

func flushAndSyncManifest(writer *bufio.Writer, manifest *os.File) error {
	if writer == nil || manifest == nil {
		return fmt.Errorf("manifest writer is unavailable")
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	return retryIOPaths(context.Background(), 3, []string{manifest.Name()}, manifest.Sync)
}

func checkTargetCapacity(ctx context.Context, targetPath string, limit int, extensions []string) error {
	if limit <= 0 || len(extensions) == 0 {
		return nil
	}
	dir := filepath.Dir(targetPath)
	var entries []os.DirEntry
	err := retryIOPaths(ctx, 3, []string{dir}, func() error {
		var readErr error
		entries, readErr = os.ReadDir(fsPath(dir))
		return readErr
	})
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: cannot verify %s: %v", errTargetCapacityChanged, dir, err)
	}
	normalized := make([]string, 0, len(extensions))
	for _, value := range extensions {
		ext := strings.ToLower(strings.TrimSpace(value))
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		normalized = append(normalized, ext)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		for _, ext := range normalized {
			if len(name) > len(ext) && strings.HasSuffix(name, ext) {
				count++
				break
			}
		}
	}
	if count >= limit {
		return fmt.Errorf("%w: %s already contains %d matching files (limit %d)", errTargetCapacityChanged, dir, count, limit)
	}
	return nil
}

func moveOne(ctx context.Context, item MovePlanItem) (string, error) {
	if item.Status == "error" {
		return "error", errors.New(item.Error)
	}
	if strings.TrimSpace(item.SourcePath) == "" || strings.TrimSpace(item.TargetPath) == "" {
		return "error", fmt.Errorf("source or target path is empty")
	}

	var sourceInfo os.FileInfo
	err := retryIOPathsWithMissing(ctx, 3, []string{item.SourcePath}, true, func() error {
		var statErr error
		sourceInfo, statErr = os.Stat(fsPath(item.SourcePath))
		return statErr
	})
	if err != nil {
		return "error", err
	}
	if !os.SameFile(sourceInfo, sourceInfo) {
		return "error", fmt.Errorf("cannot capture source file identity")
	}
	if item.SourceInfo != nil && !sameOwnedObject(sourceInfo, item.SourceInfo) {
		return "error", fmt.Errorf("source file identity changed after dry-run")
	}
	if sourceInfo.Size() != item.Size {
		return "error", fmt.Errorf("source file size changed after dry-run: got %d, planned %d", sourceInfo.Size(), item.Size)
	}
	if !item.ModTime.IsZero() && !sourceInfo.ModTime().Equal(item.ModTime) {
		return "error", fmt.Errorf("source file modification time changed after dry-run: got %s, planned %s",
			sourceInfo.ModTime().Format(time.RFC3339Nano), item.ModTime.Format(time.RFC3339Nano))
	}
	if err := retryIOPaths(ctx, 3, []string{item.TargetPath}, func() error {
		return os.MkdirAll(fsPath(filepath.Dir(item.TargetPath)), 0755)
	}); err != nil {
		return "error", err
	}

	if err := ctx.Err(); err != nil {
		return "error", err
	}
	renameErr := renameNoReplace(item.SourcePath, item.TargetPath)
	if renameErr == nil {
		return "moved", nil
	}
	if !isCrossDeviceError(renameErr) {
		if err := retryIOPathsWithMissing(ctx, 2, []string{item.SourcePath, item.TargetPath}, true, func() error {
			return renameNoReplace(item.SourcePath, item.TargetPath)
		}); err == nil {
			return "moved", nil
		}
	}

	if err := copyVerifyDelete(ctx, item.SourcePath, item.TargetPath, sourceInfo); err != nil {
		return "error", err
	}
	return "copied", nil
}

func copyVerifyDelete(ctx context.Context, sourcePath, targetPath string, sourceInfo os.FileInfo) error {
	return copyVerifyDeleteWithHooks(ctx, sourcePath, targetPath, sourceInfo, nil, os.Chtimes)
}

func copyVerifyDeleteWithHooks(ctx context.Context, sourcePath, targetPath string, sourceInfo os.FileInfo, afterCopy func(), setTimes func(string, time.Time, time.Time) error) error {
	if sourceInfo.Mode().Perm()&0200 == 0 {
		return fmt.Errorf("source is read-only; cross-device copy would leave the original undeletable")
	}
	var source *os.File
	err := retryIOPathsWithMissing(ctx, 3, []string{sourcePath}, true, func() error {
		var openErr error
		source, openErr = os.Open(fsPath(sourcePath))
		return openErr
	})
	if err != nil {
		return err
	}
	openedSourceInfo, err := source.Stat()
	if err != nil {
		_ = source.Close()
		return err
	}
	if !sameOwnedObject(openedSourceInfo, sourceInfo) || openedSourceInfo.Size() != sourceInfo.Size() ||
		!openedSourceInfo.ModTime().Equal(sourceInfo.ModTime()) {
		_ = source.Close()
		return fmt.Errorf("source file changed before copy")
	}
	// Keep the final name invisible until the copy and metadata are verified.
	// A failed operation only removes its own unique staging file.
	target, err := os.CreateTemp(fsPath(filepath.Dir(targetPath)), ".video-manager-copy-*.tmp")
	if err != nil {
		_ = source.Close()
		return err
	}
	stagingPath := target.Name()
	stagingInfo, err := target.Stat()
	if err != nil {
		_ = source.Close()
		_ = target.Close()
		return err
	}
	if !os.SameFile(stagingInfo, stagingInfo) {
		_ = source.Close()
		_ = target.Close()
		return fmt.Errorf("cannot capture staging file identity")
	}
	defer removeOwnedStagingFile(stagingPath, stagingInfo)
	copiedHash := sha256.New()
	_, copyErr := copyWithContext(ctx, target, io.TeeReader(source, copiedHash))
	sourceCloseErr := source.Close()
	closeErr := target.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if sourceCloseErr != nil {
		return sourceCloseErr
	}
	if afterCopy != nil {
		afterCopy()
	}

	var targetInfo os.FileInfo
	err = retryIOPathsWithMissing(ctx, 3, []string{stagingPath}, true, func() error {
		var statErr error
		targetInfo, statErr = os.Stat(fsPath(stagingPath))
		return statErr
	})
	if err != nil {
		return err
	}
	if targetInfo.Size() != sourceInfo.Size() {
		return fmt.Errorf("copy verify failed: source size %d, target size %d", sourceInfo.Size(), targetInfo.Size())
	}
	var currentSourceInfo os.FileInfo
	err = retryIOPathsWithMissing(ctx, 3, []string{sourcePath}, true, func() error {
		var statErr error
		currentSourceInfo, statErr = os.Stat(fsPath(sourcePath))
		return statErr
	})
	if err != nil {
		return fmt.Errorf("source revalidation after copy failed: %w", err)
	}
	if currentSourceInfo.Size() != sourceInfo.Size() || !currentSourceInfo.ModTime().Equal(sourceInfo.ModTime()) ||
		!sameOwnedObject(currentSourceInfo, openedSourceInfo) {
		return fmt.Errorf("source file changed during copy: size %d -> %d, modification time %s -> %s",
			sourceInfo.Size(), currentSourceInfo.Size(), sourceInfo.ModTime().Format(time.RFC3339Nano), currentSourceInfo.ModTime().Format(time.RFC3339Nano))
	}
	if err := verifyCopiedBytes(ctx, sourcePath, stagingPath, openedSourceInfo, copiedHash.Sum(nil)); err != nil {
		return err
	}
	if err := retryIOPathsWithMissing(ctx, 3, []string{stagingPath}, true, func() error {
		return setTimes(fsPath(stagingPath), sourceInfo.ModTime(), sourceInfo.ModTime())
	}); err != nil {
		return fmt.Errorf("cannot preserve target modification time: %w", err)
	}
	var finalTargetInfo os.FileInfo
	if err := retryIOPathsWithMissing(ctx, 3, []string{stagingPath}, true, func() error {
		var statErr error
		finalTargetInfo, statErr = os.Stat(fsPath(stagingPath))
		return statErr
	}); err != nil {
		return fmt.Errorf("cannot verify preserved target metadata: %w", err)
	}
	if finalTargetInfo.Size() != sourceInfo.Size() || !finalTargetInfo.ModTime().Equal(sourceInfo.ModTime()) {
		return fmt.Errorf("target metadata preservation failed: size %d, time %s; want size %d, time %s",
			finalTargetInfo.Size(), finalTargetInfo.ModTime().Format(time.RFC3339Nano),
			sourceInfo.Size(), sourceInfo.ModTime().Format(time.RFC3339Nano))
	}
	if err := syncExistingFile(ctx, stagingPath); err != nil {
		return fmt.Errorf("cannot persist preserved target metadata: %w", err)
	}
	if err := os.Chmod(fsPath(stagingPath), sourceInfo.Mode()); err != nil {
		return fmt.Errorf("cannot preserve target mode: %w", err)
	}
	if current, err := os.Stat(fsPath(stagingPath)); err != nil || current.Mode().Perm() != sourceInfo.Mode().Perm() {
		return fmt.Errorf("cannot verify preserved target mode: %v", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := renameNoReplace(stagingPath, targetPath); err != nil {
		return fmt.Errorf("cannot publish verified copy: %w", err)
	}
	sourceDeleteErr := retryIOPathsWithMissing(ctx, 3, []string{sourcePath}, true, func() error {
		return removeOwnedFile(sourcePath, openedSourceInfo)
	})
	if sourceDeleteErr != nil && !os.IsNotExist(sourceDeleteErr) {
		return handlePublishedCopyDeleteFailure(sourceDeleteErr, targetPath, stagingInfo)
	}
	return nil
}

func verifyCopiedBytes(ctx context.Context, sourcePath, stagingPath string, sourceInfo os.FileInfo, copiedHash []byte) error {
	for _, path := range []string{sourcePath, stagingPath} {
		file, err := os.Open(fsPath(path))
		if err != nil {
			return fmt.Errorf("copy hash verification open %s: %w", path, err)
		}
		if path == sourcePath {
			current, statErr := file.Stat()
			if statErr != nil || !sameOwnedObject(current, sourceInfo) {
				_ = file.Close()
				return fmt.Errorf("source identity changed during copy hash verification: %v", statErr)
			}
		}
		h := sha256.New()
		_, copyErr := copyWithContext(ctx, h, file)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("copy hash verification read %s: %w", path, copyErr)
		}
		if closeErr != nil {
			return closeErr
		}
		if !bytes.Equal(h.Sum(nil), copiedHash) {
			return fmt.Errorf("copy hash verification failed: %s", path)
		}
	}
	return nil
}

type publishedCopyError struct{ error }

func handlePublishedCopyDeleteFailure(sourceErr error, targetPath string, owned os.FileInfo) error {
	if err := removeOwnedFile(targetPath, owned); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("source deletion failed; published copy already absent: %w", sourceErr)
		}
		return &publishedCopyError{fmt.Errorf("source deletion failed (%v); published target cleanup failed: %w", sourceErr, err)}
	}
	return fmt.Errorf("source deletion failed; published copy removed: %w", sourceErr)
}

func removeOwnedStagingFile(path string, original os.FileInfo) {
	_ = removeOwnedFile(path, original)
}

func syncExistingFile(ctx context.Context, path string) error {
	return retryIOPathsWithMissing(ctx, 3, []string{path}, true, func() error {
		file, err := os.OpenFile(fsPath(path), os.O_RDWR, 0)
		if err != nil {
			return err
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		if syncErr != nil {
			return syncErr
		}
		return closeErr
	})
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	// Moves are sequential, so this improves SMB throughput without
	// multiplying memory use across concurrent copies. Reuse the allocation
	// so batches of small files do not create an 8 MiB garbage object each.
	buffer := copyBufferPool.Get().(*[]byte)
	buf := *buffer
	defer copyBufferPool.Put(buffer)
	var written int64
	for {
		if ctx != nil && ctx.Err() != nil {
			return written, ctx.Err()
		}
		nr, er := src.Read(buf)
		if nr > 0 {
			if ctx != nil && ctx.Err() != nil {
				return written, ctx.Err()
			}
			nw, ew := dst.Write(buf[:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				return written, ew
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				return written, nil
			}
			return written, er
		}
	}
}

func writeManifest(writer *bufio.Writer, status string, item MovePlanItem) error {
	if writer == nil {
		return fmt.Errorf("manifest writer is unavailable")
	}
	_, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%d\t%t\t%s\t%s\n",
		status,
		escapeTSV(item.SourcePath),
		escapeTSV(item.TargetPath),
		item.Size,
		item.Conflict,
		escapeTSV(item.Error),
		manifestModTimeField(item.ModTime),
	)
	return err
}

func manifestModTimeField(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func escapeTSV(value string) string {
	value = strings.ReplaceAll(value, "\t", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}
