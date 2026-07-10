package archive

import (
	"fmt"
	"math"
	"strings"
)

type CapacityConfig struct {
	TotalFiles      int
	LevelCount      int
	LevelNames      []string
	FoldersPerLevel []int
	FilesPerLeaf    int
	NamingPreset    string
}

type CapacityResult struct {
	RequiredLeafDirs   int
	LastLeafPath       string
	LastLeafFileCount  int
	ActualDirsPerLevel []int
	MaxCapacity        int
	Enough             bool
	MissingLeafDirs    int
	MissingCapacity    int
	PreviewPaths       []string
}

type NamingPreset struct {
	Name   string
	Levels []string
}

var NamingPresets = []NamingPreset{
	{Name: "Arc / Season / Episode", Levels: []string{"Arc", "Season", "Episode"}},
	{Name: "Volume / Part / Batch", Levels: []string{"Volume", "Part", "Batch"}},
	{Name: "Library / Shelf / Box", Levels: []string{"Library", "Shelf", "Box"}},
	{Name: "Group / Set / Pack", Levels: []string{"Group", "Set", "Pack"}},
	{Name: "Zone / Rack / Slot", Levels: []string{"Zone", "Rack", "Slot"}},
	{Name: "Collection / Series / Item", Levels: []string{"Collection", "Series", "Item"}},
	{Name: "Archive / Chapter / Node", Levels: []string{"Archive", "Chapter", "Node"}},
	{Name: "Tier / Block / Unit", Levels: []string{"Tier", "Block", "Unit"}},
	{Name: "Bin / Case / File", Levels: []string{"Bin", "Case", "File"}},
	{Name: "Level / Folder / Leaf", Levels: []string{"Level", "Folder", "Leaf"}},
}

func DefaultLevelNames(levelCount int, preset NamingPreset) []string {
	if levelCount < 1 {
		levelCount = 1
	}
	names := make([]string, levelCount)
	if levelCount <= len(preset.Levels) {
		copy(names, preset.Levels[len(preset.Levels)-levelCount:])
		return names
	}

	prefixCount := levelCount - len(preset.Levels)
	for i := 0; i < prefixCount; i++ {
		names[i] = fmt.Sprintf("Level%d", i+1)
	}
	copy(names[prefixCount:], preset.Levels)
	return names
}

func CalculateCapacity(cfg CapacityConfig) CapacityResult {
	cfg = normalizeCapacityConfig(cfg)
	result := CapacityResult{}

	if cfg.TotalFiles == 0 {
		result.ActualDirsPerLevel = make([]int, cfg.LevelCount)
		result.MaxCapacity = product(cfg.FoldersPerLevel) * cfg.FilesPerLeaf
		result.Enough = true
		return result
	}

	result.RequiredLeafDirs = ceilDiv(cfg.TotalFiles, cfg.FilesPerLeaf)
	result.LastLeafFileCount = cfg.TotalFiles % cfg.FilesPerLeaf
	if result.LastLeafFileCount == 0 {
		result.LastLeafFileCount = cfg.FilesPerLeaf
	}

	maxLeafDirs := product(cfg.FoldersPerLevel)
	result.MaxCapacity = saturatedMultiply(maxLeafDirs, cfg.FilesPerLeaf)
	result.Enough = result.MaxCapacity >= cfg.TotalFiles
	if !result.Enough {
		result.MissingLeafDirs = result.RequiredLeafDirs - maxLeafDirs
		result.MissingCapacity = cfg.TotalFiles - result.MaxCapacity
	}

	result.ActualDirsPerLevel = actualDirsPerLevel(result.RequiredLeafDirs, cfg.FoldersPerLevel)
	indexes := pathIndexes(result.RequiredLeafDirs, cfg.FoldersPerLevel)
	result.LastLeafPath = formatPath(cfg.LevelNames, indexes)
	result.PreviewPaths = previewPaths(cfg, result.RequiredLeafDirs, 5)

	return result
}

func normalizeCapacityConfig(cfg CapacityConfig) CapacityConfig {
	if cfg.LevelCount < 1 {
		cfg.LevelCount = 1
	}
	if cfg.TotalFiles < 0 {
		cfg.TotalFiles = 0
	}
	if cfg.FilesPerLeaf <= 0 {
		cfg.FilesPerLeaf = 30
	}

	if len(cfg.LevelNames) < cfg.LevelCount {
		preset := NamingPresets[0]
		defaults := DefaultLevelNames(cfg.LevelCount, preset)
		names := make([]string, cfg.LevelCount)
		copy(names, cfg.LevelNames)
		for i := len(cfg.LevelNames); i < cfg.LevelCount; i++ {
			names[i] = defaults[i]
		}
		cfg.LevelNames = names
	} else {
		cfg.LevelNames = cfg.LevelNames[:cfg.LevelCount]
	}
	for i := range cfg.LevelNames {
		cfg.LevelNames[i] = sanitizeLevelName(cfg.LevelNames[i], i)
	}

	if len(cfg.FoldersPerLevel) < cfg.LevelCount {
		values := make([]int, cfg.LevelCount)
		copy(values, cfg.FoldersPerLevel)
		for i := len(cfg.FoldersPerLevel); i < cfg.LevelCount; i++ {
			values[i] = 5
		}
		cfg.FoldersPerLevel = values
	} else {
		cfg.FoldersPerLevel = cfg.FoldersPerLevel[:cfg.LevelCount]
	}
	for i := range cfg.FoldersPerLevel {
		if cfg.FoldersPerLevel[i] <= 0 {
			cfg.FoldersPerLevel[i] = 1
		}
	}

	return cfg
}

func actualDirsPerLevel(requiredLeafDirs int, foldersPerLevel []int) []int {
	count := len(foldersPerLevel)
	actual := make([]int, count)
	for level := 0; level < count; level++ {
		capBelow := product(foldersPerLevel[level+1:])
		if capBelow == 0 {
			capBelow = 1
		}
		actual[level] = ceilDiv(requiredLeafDirs, capBelow)
		if actual[level] > product(foldersPerLevel[:level+1]) {
			actual[level] = product(foldersPerLevel[:level+1])
		}
	}
	return actual
}

func pathIndexes(leafIndex int, foldersPerLevel []int) []int {
	indexes := make([]int, len(foldersPerLevel))
	for level := 0; level < len(foldersPerLevel); level++ {
		capBelow := product(foldersPerLevel[level+1:])
		if capBelow == 0 {
			capBelow = 1
		}
		indexes[level] = ceilDiv(leafIndex, capBelow)
	}
	return indexes
}

func previewPaths(cfg CapacityConfig, requiredLeafDirs, limit int) []string {
	if requiredLeafDirs <= 0 {
		return nil
	}
	count := int(math.Min(float64(limit), float64(requiredLeafDirs)))
	paths := make([]string, 0, count+1)
	for i := 1; i <= count; i++ {
		paths = append(paths, formatPath(cfg.LevelNames, pathIndexes(i, cfg.FoldersPerLevel)))
	}
	if requiredLeafDirs > count {
		paths = append(paths, formatPath(cfg.LevelNames, pathIndexes(requiredLeafDirs, cfg.FoldersPerLevel)))
	}
	return paths
}

func formatPath(names []string, indexes []int) string {
	parts := make([]string, len(names))
	for i := range names {
		width := 3
		if i == 0 && len(names) >= 3 {
			width = 2
		}
		parts[i] = fmt.Sprintf("%s_%0*d", names[i], width, indexes[i])
	}
	return strings.Join(parts, "/")
}

func sanitizeLevelName(name string, index int) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Sprintf("Level%d", index+1)
	}
	replacer := strings.NewReplacer("<", "", ">", "", ":", "", "\"", "", "/", "", "\\", "", "|", "", "?", "", "*", "")
	name = strings.TrimSpace(replacer.Replace(name))
	if name == "" {
		return fmt.Sprintf("Level%d", index+1)
	}
	return name
}

func ceilDiv(a, b int) int {
	if b <= 0 {
		return 0
	}
	result := a / b
	if a%b != 0 {
		result++
	}
	return result
}

func product(values []int) int {
	if len(values) == 0 {
		return 1
	}
	total := 1
	for _, value := range values {
		if value <= 0 {
			continue
		}
		total = saturatedMultiply(total, value)
	}
	return total
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
