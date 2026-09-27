package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lxn/win"
	"video-manager/internal/appconfig"
	"video-manager/internal/archive"
)

func sameFeedAudit(left, right archive.FeedAudit) bool {
	if left.Existing != right.Existing || left.LastLeafIndex != right.LastLeafIndex || left.LastLeafCount != right.LastLeafCount || left.RequiresAdoption != right.RequiresAdoption || len(left.Files) != len(right.Files) || len(left.Dirs) != len(right.Dirs) {
		return false
	}
	for i := range left.Dirs {
		if !archive.SamePath(left.Dirs[i], right.Dirs[i]) {
			return false
		}
	}
	for i := range left.Files {
		a, b := left.Files[i], right.Files[i]
		if !archive.SamePath(a.SourcePath, b.SourcePath) || a.Size != b.Size || !a.ModTime.Equal(b.ModTime) || !archive.SameVideoIdentity(a, b) {
			return false
		}
	}
	return true
}

func (a *app) generateFeedDryRun() {
	feedRoot := strings.TrimSpace(a.text(a.controls[idFeedEdit]))
	targetRoot := strings.TrimSpace(a.text(a.controls[idTargetEdit]))
	if feedRoot == "" || targetRoot == "" {
		a.log("请先选择投料目录和目标目录。")
		return
	}
	feedAbs, feedErr := filepath.Abs(feedRoot)
	targetAbs, targetErr := filepath.Abs(targetRoot)
	if feedErr != nil || targetErr != nil {
		a.log("投料或目标路径无效。")
		return
	}
	feedKey := strings.ToLower(filepath.Clean(feedAbs))
	targetKey := strings.ToLower(filepath.Clean(targetAbs))
	if feedKey == targetKey || strings.HasPrefix(feedKey, targetKey+string(filepath.Separator)) || strings.HasPrefix(targetKey, feedKey+string(filepath.Separator)) {
		a.log("投料目录和目标目录不能互相包含。")
		return
	}
	cfg := a.planConfig(0)
	if err := archive.ValidateLevelNames(cfg.LevelNames); err != nil {
		a.log("目录名配置无效: " + err.Error())
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	if a.dryRunning || a.scanning || a.moving || a.browsing {
		a.mu.Unlock()
		cancel()
		a.log("已有任务进行中，请等待完成或取消。")
		return
	}
	a.dryRunning, a.dryRunCancel = true, cancel
	a.currentPlan = archive.MovePlan{}
	a.currentPlanConfig = archive.PlanConfig{}
	a.currentPlanFeed = false
	a.dryRunLines, a.dryRunTSV, a.dryRunError = nil, "", ""
	a.dryRunNetwork = archive.IsLikelyNetworkPath(feedRoot) || archive.IsLikelyNetworkPath(targetRoot)
	a.dryRunIsFeed = true
	a.mu.Unlock()
	a.setConfigurationEnabled(false)
	a.setActionState(false, false, false, true, false)
	a.setProgress(0, 4)
	a.setTextNoFlicker(a.controls[idPreview], "正在扫描投料并审计原结构……")
	a.log("开始生成投料 Dry-run。")
	go func() {
		finish := func(errText string, plan archive.MovePlan, audit archive.FeedAudit, lines []string, tsv string) {
			a.mu.Lock()
			a.currentPlan, a.currentPlanConfig = plan, cfg
			a.currentPlanSource, a.currentPlanFeed, a.currentFeedAudit = feedRoot, true, audit
			a.dryRunLines, a.dryRunTSV, a.dryRunError = lines, tsv, errText
			a.dryRunning, a.dryRunCancel = false, nil
			a.mu.Unlock()
			win.PostMessage(a.hwnd, wmDryRunDone, 0, 0)
		}
		if err := archive.CheckReadableDirForReadContext(ctx, feedRoot); err != nil {
			finish("投料目录不可读取: "+err.Error(), archive.MovePlan{}, archive.FeedAudit{}, nil, "")
			return
		}
		scan := archive.ScanVideos(ctx, feedRoot, nil)
		if ctx.Err() != nil || scan.Cancelled {
			finish("投料 Dry-run 已取消。", archive.MovePlan{}, archive.FeedAudit{}, nil, "")
			return
		}
		if scan.ErrorCount > 0 || len(scan.Files) == 0 {
			finish(fmt.Sprintf("投料扫描结果：视频 %d 个，读取错误 %d 个。", len(scan.Files), scan.ErrorCount), archive.MovePlan{}, archive.FeedAudit{}, nil, "")
			return
		}
		a.postDryRunProgress(1)
		plan, audit := archive.BuildFeedPlanContext(ctx, scan.Files, cfg)
		if len(audit.Errors) > 0 {
			finish("现有目标结构与当前设置不一致: "+audit.Errors[0], plan, audit, nil, "")
			return
		}
		if ctx.Err() != nil {
			finish("投料 Dry-run 已取消。", archive.MovePlan{}, audit, nil, "")
			return
		}
		a.postDryRunProgress(2)
		lines := []string{fmt.Sprintf("投料 Dry-run：现有 %d，新增 %d，完成后 %d；重名 %d，错误 %d。", audit.Existing, len(scan.Files), plan.FinalTargetFiles, plan.ConflictCount, plan.ErrorCount), fmt.Sprintf("已核对原结构参数和 %d 个现有结构目录；实际末尾叶目录编号 %d，已有受管视频 %d/%d。", len(audit.Dirs), audit.LastLeafIndex, audit.LastLeafCount, cfg.FilesPerLeaf), "现有文件保持原位；允许历史叶目录未满，新文件只从实际末尾叶目录继续追加。", ""}
		if audit.RequiresAdoption {
			lines = append(lines, "旧归档未保存原设定：首次投料时需确认当前参数就是原参数；确认后将绑定到目标目录。")
		}
		if plan.AutoExpanded {
			lines = append(lines, fmt.Sprintf("第 1 层目录数自动扩为 %d。", plan.EffectiveFolders[0]))
		}
		for _, item := range plan.Items {
			lines = append(lines, item.SourcePath+" -> "+item.TargetPath)
		}
		dirs, errs := archive.PreviewEmptyDirs(ctx, feedRoot, []string{targetRoot})
		lines = append(lines, fmt.Sprintf("投料后预计可清理空源目录 %d 个，预览错误 %d 个。", len(dirs), len(errs)))
		a.postDryRunProgress(3)
		dataDir, err := appconfig.DataDir()
		if err != nil {
			finish("投料 TSV 数据目录不可用: "+err.Error(), plan, audit, lines, "")
			return
		}
		tsv, err := archive.ExportMovePlanTSVContext(ctx, plan, filepath.Join(dataDir, "dry-runs"))
		if err != nil {
			finish("投料 TSV 导出失败: "+err.Error(), plan, audit, lines, "")
			return
		}
		a.postDryRunProgress(4)
		finish("", plan, audit, lines, tsv)
	}()
}
