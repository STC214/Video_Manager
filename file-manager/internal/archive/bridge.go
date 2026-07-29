package archive

import (
	"context"

	core "video-manager/internal/archive"
)

type VideoFile = core.VideoFile
type MovePlanItem = core.MovePlanItem
type MovePlan = core.MovePlan
type MoveOptions = core.MoveOptions
type MoveProgress = core.MoveProgress
type MoveSummary = core.MoveSummary
type UndoSummary = core.UndoSummary

var (
	SamePath                 = core.SamePath
	IsLikelyNetworkPath      = core.IsLikelyNetworkPath
	CheckReadableDirContext  = core.CheckReadableDirContext
	CheckTargetRootContext   = core.CheckTargetRootContext
	ManifestHasUndoableItems = core.ManifestHasUndoableItems
	PreviewEmptyDirs         = core.PreviewEmptyDirs
	CleanupEmptyDirs         = core.CleanupEmptyDirs
	ExecuteMovePlan          = core.ExecuteMovePlan
	UndoManifest             = core.UndoManifest
	FSPath                   = core.FSPath
	DisplayPath              = core.DisplayPath
	RetryIOPaths             = core.RetryIOPaths
)

func ExportMovePlanTSVContext(ctx context.Context, plan MovePlan, outputDir string) (string, error) {
	return core.ExportMovePlanTSVContext(ctx, plan, outputDir)
}
