package archive

import (
	"fmt"
	"strings"
)

type PlanConfig struct {
	TargetDir        string
	StartYear        int
	LeafDirsPerMonth int
	FilesPerLeaf     int
	PathTemplate     string
	Extensions       []string
}

type CapacityResult struct {
	TotalFiles        int
	RequiredLeafDirs  int
	RequiredMonths    int
	RequiredQuarters  int
	RequiredYears     int
	LastLeafPath      string
	LastLeafFileCount int
	FilesPerMonth     int
	FilesPerYear      int
	PreviewPaths      []string
}

func NormalizePlanConfig(cfg PlanConfig) PlanConfig {
	if cfg.StartYear < 1000 {
		cfg.StartYear = 2000
	}
	if cfg.StartYear > 9999 {
		cfg.StartYear = 9999
	}
	if cfg.LeafDirsPerMonth <= 0 {
		cfg.LeafDirsPerMonth = 4
	}
	if cfg.FilesPerLeaf <= 0 {
		cfg.FilesPerLeaf = 30
	}
	if strings.TrimSpace(cfg.PathTemplate) == "" {
		cfg.PathTemplate = DefaultPathTemplate
	} else {
		cfg.PathTemplate = strings.TrimSpace(cfg.PathTemplate)
	}
	return cfg
}

func CalculateCapacity(totalFiles int, cfg PlanConfig) CapacityResult {
	cfg = NormalizePlanConfig(cfg)
	if totalFiles < 0 {
		totalFiles = 0
	}
	result := CapacityResult{
		TotalFiles:    totalFiles,
		FilesPerMonth: saturatedMultiply(cfg.LeafDirsPerMonth, cfg.FilesPerLeaf),
	}
	result.FilesPerYear = saturatedMultiply(result.FilesPerMonth, 12)
	if totalFiles == 0 {
		return result
	}
	result.RequiredLeafDirs = ceilDiv(totalFiles, cfg.FilesPerLeaf)
	result.RequiredMonths = ceilDiv(result.RequiredLeafDirs, cfg.LeafDirsPerMonth)
	result.RequiredQuarters = ceilDiv(result.RequiredMonths, 3)
	result.RequiredYears = ceilDiv(result.RequiredMonths, 12)
	result.LastLeafFileCount = totalFiles % cfg.FilesPerLeaf
	if result.LastLeafFileCount == 0 {
		result.LastLeafFileCount = cfg.FilesPerLeaf
	}
	result.LastLeafPath = CalendarLeafPathForConfig(cfg, result.RequiredLeafDirs)
	limit := result.RequiredLeafDirs
	if limit > 5 {
		limit = 5
	}
	for i := 1; i <= limit; i++ {
		result.PreviewPaths = append(result.PreviewPaths, CalendarLeafPathForConfig(cfg, i))
	}
	if result.RequiredLeafDirs > limit {
		result.PreviewPaths = append(result.PreviewPaths, "...", result.LastLeafPath)
	}
	return result
}

func CalendarLeafPath(startYear, leafIndex, leafDirsPerMonth int) string {
	return CalendarLeafPathForConfig(PlanConfig{StartYear: startYear, LeafDirsPerMonth: leafDirsPerMonth}, leafIndex)
}

func ValidateConfig(cfg PlanConfig) error {
	cfg.TargetDir = strings.TrimSpace(cfg.TargetDir)
	if cfg.TargetDir == "" {
		return fmt.Errorf("目标目录不能为空")
	}
	if cfg.StartYear < 1000 || cfg.StartYear > 9999 {
		return fmt.Errorf("起始年份必须是 1000 到 9999 的四位数")
	}
	if cfg.LeafDirsPerMonth < 1 || cfg.LeafDirsPerMonth > 99 {
		return fmt.Errorf("每月叶目录数必须在 1 到 99 之间，以保持两位叶目录编号")
	}
	if cfg.FilesPerLeaf < 1 || cfg.FilesPerLeaf > 1000000 {
		return fmt.Errorf("每个叶目录文件数必须在 1 到 1000000 之间")
	}
	if err := ValidatePathTemplate(cfg.PathTemplate); err != nil {
		return err
	}
	return nil
}

func ValidateConfigForFiles(cfg PlanConfig, totalFiles int) error {
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	result := CalculateCapacity(totalFiles, cfg)
	if result.RequiredYears > 0 && cfg.StartYear+result.RequiredYears-1 > 9999 {
		return fmt.Errorf("当前文件数量会使归档年份超过 9999，请增大每月叶目录数或每叶文件数")
	}
	return nil
}

func ceilDiv(a, b int) int {
	if a <= 0 || b <= 0 {
		return 0
	}
	return (a-1)/b + 1
}

func saturatedMultiply(a, b int) int {
	if a <= 0 || b <= 0 {
		return 0
	}
	maxInt := int(^uint(0) >> 1)
	if a > maxInt/b {
		return maxInt
	}
	return a * b
}
