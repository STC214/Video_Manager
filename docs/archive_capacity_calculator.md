# 归档容量计算器与多级目录命名预案

## 目标

归档容量计算器用于在正式生成 dry-run 前，帮助用户根据“目标文件总数、目录层数、每层文件夹数、叶目录文件数”快速判断归档结构是否合适。

该模块不移动文件，只做结构容量计算、最后叶目录定位和命名预览。

## 核心输入

### 目标文件总数

来源：

- 扫描完成后自动填入需要移动的视频文件总数。
- 也允许用户手动输入一个模拟数量，用于提前规划目录结构。

字段：

```text
目标文件总数：7429
```

### 结果目录层数

用户输入结果目录层数。

字段：

```text
结果目录层数：3
```

说明：

- 目录层数指目标结构中从根目录下开始生成的分组目录层数。
- 叶目录也是其中一层。
- 例如 `Arc / Season / Episode` 是 3 层。
- 例如 `Season / Episode` 是 2 层。

### 每层结果的文件夹数

根据“结果目录层数”动态生成输入框，每层单独一个输入框。

示例：目录层数为 3 时：

```text
第 1 层文件夹数：5
第 2 层文件夹数：10
第 3 层文件夹数：5
```

规则：

- 第 1 层文件夹数表示目标根目录下最多生成多少个第 1 层目录。
- 第 2 层及之后的文件夹数表示每个上级目录下最多生成多少个当前层目录。
- 最后一层是叶目录层，它的文件夹数表示每个上级目录下最多生成多少个叶目录。
- 程序根据这些输入计算当前配置的最大容量，并判断是否足够容纳目标文件。

### 叶目录文件数

每个叶目录最多包含多少个目标文件。

字段：

```text
叶目录文件数：30
```

说明：

- 每个叶目录最多 N 个文件。
- 只有最后一个叶目录可能少于 N 个文件。

### 每层目录名

每层都提供目录名输入框。

字段示例：

```text
第 1 层目录名：Arc
第 2 层目录名：Season
第 3 层目录名：Episode
```

说明：

- 用户可以选择预案，也可以手动输入每层目录名。
- 目录名只作为生成目标目录名的前缀，不参与内容分类。
- 默认编号采用补零格式，例如 `Arc_01`、`Season_001`、`Episode_001`。

## 核心输出

计算器应显示：

```text
需要移动的目标文件总数：7429
叶目录文件数：30
需要叶目录数量：248
最后一个叶目录：Arc_05/Season_050/Episode_248
最后一个叶目录文件数：19
预计第 1 层目录数量：5
预计第 2 层目录数量：50
预计第 3 层目录数量：248
当前配置最大可容纳文件数：7500
当前配置是否足够：是
```

如果配置不足，应显示：

```text
当前配置是否足够：否
还差叶目录数量：18
还差可容纳文件数：540
建议：增加上层文件夹数、增加叶目录文件数，或增加目录层数。
```

## 计算规则

### 所需叶目录数

```text
requiredLeafDirs = ceil(totalFiles / filesPerLeaf)
```

示例：

```text
ceil(7429 / 30) = 248
```

### 最后一个叶目录文件数

```text
lastLeafFileCount = totalFiles % filesPerLeaf
如果余数为 0，则 lastLeafFileCount = filesPerLeaf
如果 totalFiles 为 0，则 lastLeafFileCount = 0
```

示例：

```text
7429 % 30 = 19
```

### 每层实际目录数量

先计算每层最大容量：

```text
maxLeafDirs = 第 1 层文件夹数 * 第 2 层文件夹数 * ... * 最后一层文件夹数
maxCapacity = maxLeafDirs * filesPerLeaf
```

再从叶目录层向上反推实际需要多少目录：

```text
最后一层实际目录数量 = requiredLeafDirs
上一层实际目录数量 = ceil(requiredLeafDirs / 最后一层文件夹数)
再上一层实际目录数量 = ceil(requiredLeafDirs / (上一层以下所有文件夹数乘积))
```

对于 `Arc / Season / Episode`：

```text
目标文件总数：7429
叶目录文件数：30
第 1 层 Arc 文件夹数：5
第 2 层 Season 文件夹数：10
第 3 层 Episode 文件夹数：5

Episode 数量：248
Season 数量：ceil(248 / 5) = 50
Arc 数量：ceil(50 / 10) = 5
最大容量：5 * 10 * 5 * 30 = 7500
```

### 最后叶目录路径

最后叶目录编号等于 `requiredLeafDirs`。

对于 3 层结构：

```text
leafIndex = requiredLeafDirs
episodeIndex = leafIndex
seasonIndex = ceil(episodeIndex / episodeDirsPerSeason)
arcIndex = ceil(seasonIndex / seasonDirsPerArc)
```

示例：

```text
Episode_248
Season_050
Arc_05
```

最终路径：

```text
Arc_05/Season_050/Episode_248
```

## UI 行为

### 动态输入框

当用户修改“结果目录层数”时：

- 自动增减“每层文件夹数”输入框。
- 自动增减“每层目录名”输入框。
- 保留仍然存在的层级输入值。
- 新增层级使用当前命名预案的默认名称。

示例：

```text
结果目录层数：[3]

层级设置：
第 1 层：目录名 [Arc]     文件夹数 [5]
第 2 层：目录名 [Season]  文件夹数 [10]
第 3 层：目录名 [Episode] 文件夹数 [5]
叶目录文件数：[30]
```

### 实时计算

以下字段变化时应重新计算：

- 目标文件总数。
- 结果目录层数。
- 每层文件夹数。
- 叶目录文件数。
- 每层目录名。
- 命名预案。

计算应为轻量同步逻辑；如果未来加入复杂预览树生成，应按 UI 卡死风险文档要求节流或后台化。

### 预览输出

至少显示：

- 所需叶目录数量。
- 最后叶目录完整路径。
- 最后叶目录文件数。
- 每层实际目录数量。
- 当前配置最大容量。
- 当前配置是否足够。
- 前几个目录名示例。

示例：

```text
Arc_01/Season_001/Episode_001
Arc_01/Season_001/Episode_002
Arc_01/Season_001/Episode_003
...
Arc_05/Season_050/Episode_248
```

## 多级目录命名预案

用户不知道如何命名时，可以从以下 10 个预案中选择。选择预案后，程序自动填充每层目录名；用户仍可手动修改。

### 预案 1：Arc / Season / Episode

适合大规模普通视频归档。

```text
Arc
Season
Episode
```

示例：

```text
Arc_01/Season_001/Episode_001
```

### 预案 2：Volume / Part / Batch

适合中性、偏资料库风格的归档。

```text
Volume
Part
Batch
```

示例：

```text
Volume_01/Part_001/Batch_001
```

### 预案 3：Library / Shelf / Box

适合把视频当成馆藏资料管理。

```text
Library
Shelf
Box
```

示例：

```text
Library_01/Shelf_001/Box_001
```

### 预案 4：Group / Set / Pack

适合通用文件批次归档。

```text
Group
Set
Pack
```

示例：

```text
Group_01/Set_001/Pack_001
```

### 预案 5：Zone / Rack / Slot

适合偏仓储、位置编号风格的归档。

```text
Zone
Rack
Slot
```

示例：

```text
Zone_01/Rack_001/Slot_001
```

### 预案 6：Collection / Series / Item

适合收藏类视频整理。

```text
Collection
Series
Item
```

示例：

```text
Collection_01/Series_001/Item_001
```

### 预案 7：Archive / Chapter / Node

适合偏技术、偏归档系统风格。

```text
Archive
Chapter
Node
```

示例：

```text
Archive_01/Chapter_001/Node_001
```

### 预案 8：Tier / Block / Unit

适合强调层级和块状分布的结构。

```text
Tier
Block
Unit
```

示例：

```text
Tier_01/Block_001/Unit_001
```

### 预案 9：Bin / Case / File

适合简单实用的箱柜式归档。

```text
Bin
Case
File
```

示例：

```text
Bin_01/Case_001/File_001
```

### 预案 10：Level / Folder / Leaf

适合最直白的结构测试和通用默认值。

```text
Level
Folder
Leaf
```

示例：

```text
Level_01/Folder_001/Leaf_001
```

## 层数和预案适配规则

- 如果用户选择 1 层，只使用预案中的最后一层名称。
- 如果用户选择 2 层，使用预案中的后两层名称。
- 如果用户选择 3 层，使用预案中的三层名称。
- 如果用户选择超过 3 层，前面新增层级使用 `Level`、`Level2`、`Level3` 等通用名称，最后三层使用预案名称。
- 用户手动修改目录名后，不再强制覆盖，除非重新选择预案。

## 数据结构建议

```go
type CapacityConfig struct {
    TotalFiles        int
    LevelCount        int
    LevelNames        []string
    FoldersPerLevel   []int
    FilesPerLeaf      int
    NamingPreset      string
}

type CapacityResult struct {
    RequiredLeafDirs      int
    LastLeafPath          string
    LastLeafFileCount     int
    ActualDirsPerLevel    []int
    MaxCapacity           int
    Enough                bool
    MissingLeafDirs       int
    MissingCapacity       int
    PreviewPaths          []string
}
```

## 校验规则

- 目标文件总数必须大于等于 0。
- 结果目录层数必须大于等于 1。
- 叶目录文件数必须大于 0。
- 每层文件夹数必须大于 0。
- 目录名不能为空。
- 目录名不能包含 Windows 路径非法字符：`< > : " / \ | ? *`。
- 目录名首尾空格应自动修剪。
