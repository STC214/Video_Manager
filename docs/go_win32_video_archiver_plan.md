# Go + Win32 视频归档工具企划

## 项目定位

开发一个 Windows 桌面工具，用于对本地或网络挂载目录中的视频文件进行结构化归档。

工具重点解决：

- 视频数量较多，单目录浏览困难。
- 原目录结构混乱，不适合长期维护。
- 希望按固定数量均衡分组，而不是按日期或原目录名整理。
- 需要在正式移动前看到明确的 dry-run 预览。
- 移动完成后希望自动清理空目录。

## 核心原则

- 只按文件系统元数据处理，不读取或识别视频内容。
- 原始目录名不参与分类判断，只在修改时间相同时作为稳定排序依据。
- 源目录内部结构可以混乱；程序递归扫描视频文件后重新分组。
- 默认保留原文件名。
- 不按标题、内容、人物、标签重命名。
- 所有移动操作必须先支持 dry-run。
- 出现重名时不覆盖，自动追加编号。
- 清理目录时只删除空目录。

## 支持的归档模式

### 两层模式

适合中小规模视频目录。

```text
Animation/
  Season_001/
    Episode_001/
    Episode_002/
    Episode_003/
    Episode_004/
  Season_002/
    Episode_005/
    Episode_006/
    Episode_007/
    Episode_008/
```

默认参数：

```text
每个 Episode：最多 25 个视频
每个 Season：4 个 Episode
Season 编号：三位数，例如 Season_001
```

### 三层模式

适合大规模视频目录。

```text
VIDEO/
  Arc_01/
    Season_001/
      Episode_001/
      Episode_002/
      Episode_003/
      Episode_004/
      Episode_005/
  Arc_02/
    Season_011/
      Episode_051/
      Episode_052/
      Episode_053/
      Episode_054/
      Episode_055/
```

默认参数：

```text
每个 Episode：最多 30 个视频
每个 Season：5 个 Episode
每个 Arc：10 个 Season
Season 编号：三位数，例如 Season_001
```

## 视频扩展名范围

默认识别：

```text
.mp4
.mkv
.avi
.mov
.wmv
.flv
.webm
.m4v
.ts
.m2ts
.mpg
.mpeg
.rmvb
.3gp
```

扩展名匹配应大小写不敏感。

## 功能模块

### 1. 目录选择

用户选择源目录。

支持：

- 本地路径。
- SMB 映射盘，例如 `Z:\Videos`。
- SMB / UNC 路径，例如 `\\router\share\Videos`。

首版必须支持软路由、NAS 等设备通过 SMB 协议挂载到 Windows 后形成的盘符路径或 UNC 路径。工具不直接实现 SMB 协议，只通过 Windows 文件系统 API 访问已认证、已挂载或可访问的网络路径。

首版不直接内置 SSH/SFTP 移动能力，避免网络设备权限和编码问题复杂化。网络目录建议先通过 Windows 挂载、映射或 UNC 路径访问后再处理。

### 2. 扫描统计

扫描后显示：

- 视频总数。
- 非视频文件数量。
- 视频扩展名分布。
- 总大小。
- 预计生成的 `Arc`、`Season`、`Episode` 数量。
- 最后一个叶目录预计文件数。

扫描原则：

- 无论原目录名是否混乱，都递归扫描源目录内的视频文件。
- 原目录名不用于分类，只在修改时间相同时参与稳定排序和来源追踪。
- 路径中包含中文、空格和常见标点时应正常识别。

扫描排除规则：

- 如果目标目录位于源目录内，必须排除目标根目录本身。
- 默认排除 `.git`、`System Volume Information`、`$RECYCLE.BIN`。
- 工具日志和清单不是视频文件，不进入移动计划；目标目录位于源目录内时整棵目标目录树会被排除。
- 隐藏目录首版可以扫描，但清理阶段不得删除含隐藏文件的目录。

### 3. 归档参数配置

可配置项：

```text
归档模式：两层 / 三层 / 自定义层级
目标根目录：源目录内 / 自定义目录
结果目录层数
每层目录名
每层文件夹数
叶目录文件数
命名预案
是否保留原子目录层级：默认否
是否移动后清理空目录：默认开启
```

容量计算器：

- 显示需要移动的目标文件总数。
- 用户输入结果目录层数。
- 根据目录层数动态显示每层文件夹数输入框。
- 用户输入叶目录文件数。
- 每层目录名可以从预案自动填入，也可以手动输入。
- 实时计算最终需要多少叶目录、最后叶目录路径、最后叶目录文件数和每层实际目录数量。
- 详细规则见 [archive_capacity_calculator.md](archive_capacity_calculator.md)。

目标目录规则：

- 目标目录可以位于源目录内，也可以是自定义目录。
- 目标目录已存在时允许继续生成 dry-run。
- 如果目标目录已存在同名文件，不覆盖，按重名规则追加编号。
- 重复运行时，已归档目标目录必须从扫描范围排除。

### 4. Dry-run 预览

正式移动前生成预览列表：

```text
源路径 -> 目标路径
```

dry-run 可导出为 TSV，建议字段：

```text
status	source	target	size	conflict	error
planned	D:\Video\a.mp4	D:\VIDEO\Arc_01\Season_001\Episode_001\a.mp4	123456	no	
conflict	D:\Video\b.mp4	D:\VIDEO\Arc_01\Season_001\Episode_001\b_dup001.mp4	123456	yes	
error	D:\Video\locked.mp4		123456	no	access denied
```

预览页显示：

- 视频总数。
- 总移动数量。
- 目标目录数量。
- 预计 `Arc`、`Season`、`Episode` 数量。
- 最后一个叶目录预计文件数。
- 重名冲突数量。
- 将清理的空目录数量。
- 错误或不可访问文件数量。

摘要示例：

```text
视频总数：7429
总移动数量：7429
预计 Arc：5
预计 Season：50
预计 Episode：248
最后一个 Episode：19
重名冲突：3
不可访问文件：1
```

结构预览示例：

```text
Arc_01
  Season_001
    Episode_001: 30
    Episode_002: 30
    Episode_003: 30
```

### 5. 正式移动

执行规则：

- 先创建目标目录。
- 再逐个移动视频文件。
- 每移动一个文件记录结果。
- 失败时跳过并记录，不中断全局任务。
- 同一卷内优先使用 rename/move，跨卷自动退化为 copy + verify + delete。
- 跨卷移动首版使用文件大小一致作为校验标准。
- 校验失败时保留源文件，并记录错误。
- 对 SMB / UNC 路径，不能只依赖盘符判断是否同卷；如果直接 move 失败，应自动退化为 copy + verify + delete。
- 网络路径断开、超时或权限不足时，记录对应文件失败并继续处理后续文件。

### 6. 重名处理

如果目标路径已存在，追加编号：

```text
原文件名.mp4
原文件名_dup001.mp4
原文件名_dup002.mp4
```

编号查找应在目标叶目录内完成。

### 7. 空目录清理

移动完成后，自底向上扫描源目录。

只删除：

- 已确认为空的目录。
- 不属于新建目标结构的旧目录。

不删除：

- 非空目录。
- 含隐藏文件的目录。
- 用户排除的目录。
- 新建目标结构。

### 8. 运行清单和恢复

每次正式移动生成一份运行清单，文件名建议：

```text
archive-run-YYYYMMDD-HHMMSS.tsv
```

清单字段建议：

```text
status	source	target	size	conflict	error
```

用途：

- 记录每个文件的移动结果。
- 支持失败后重跑时排查问题。
- 为当前撤销功能提供反向移动依据。
- 用户取消任务时记录已完成项和取消点。

## 路径和网络盘规则

- 支持本地盘符路径、SMB 映射盘路径和 UNC 路径。
- 路径中允许中文、空格和常见标点。
- 实现中应集中处理长路径，避免 Windows 传统 260 字符路径限制。
- 运行前应检查源目录是否可访问，无法访问时阻止扫描并提示。
- dry-run 和正式移动之间，如果网络路径断开，应在正式移动阶段重新检查源文件。
- 对网络盘不做特殊内容假设；只要 Windows 文件 API 可读写，就按普通文件处理。
- SMB 设备可能不完整支持某些文件属性，首版只依赖路径、文件大小、修改时间和目录枚举能力。
- 对 UNC 路径和 Windows 识别为 remote drive 的映射盘，I/O 创建目录、移动、复制打开、删除、清理枚举等步骤使用更长退避重试，给软路由 / NAS 唤醒和短暂抖动留出恢复时间。

## 界面设计

UI 卡死风险和视觉主题细节见 [ui_freeze_risks_and_style.md](ui_freeze_risks_and_style.md)。

首版 UI 采用深色、时髦、克制的工具型风格。窗口标题栏不能保留系统默认白色标题栏，应适配深色主题。

### 主界面布局

```text
[源目录选择] [浏览...]

归档模式：
( ) 两层 Season/Episode
( ) 三层 Arc/Season/Episode

参数：
目标文件总数：[7429]
结果目录层数：[3]
第 1 层目录名：[Arc]     文件夹数：[5]
第 2 层目录名：[Season]  文件夹数：[10]
第 3 层目录名：[Episode] 文件夹数：[5]
叶目录文件数：[30]
命名预案：[Arc / Season / Episode v]

计算结果：
需要叶目录：
最后叶目录：
最后叶目录文件数：
每层实际目录数量：
当前配置最大容量：

[扫描] [生成 Dry-run] [开始移动]

统计区域：
视频总数：
预计 Episode：
预计 Season：
预计 Arc：
预计最后叶目录数量：

预览列表：
源路径 | 目标路径 | 状态

日志区域：
...
```

### 状态设计

任务状态：

```text
Idle
Scanning
PreviewReady
Moving
Completed
CompletedWithErrors
Cancelled
```

按钮启用规则：

- 未扫描前禁用 `生成 Dry-run` 和 `开始移动`。
- 未生成 dry-run 前禁用 `开始移动`。
- 移动中禁用参数修改。
- 移动中允许取消，但取消后保留已完成结果。

## Go + Win32 技术方案

### 推荐技术栈

```text
语言：Go
界面：Win32 API
构建：项目根目录 build.ps1（自动生成标题栏和 EXE 属性版本信息）
配置：JSON
日志：纯文本 .log
```

Win32 封装可选：

- `github.com/lxn/win`
- 或项目已有的 Win32 调用封装方式。

### 核心包划分

```text
cmd/video-archiver/
  main.go

internal/archive/
  scanner.go      # 扫描视频文件
  planner.go      # 生成归档计划
  capacity.go     # 计算归档容量和层级结果
  mover.go        # 执行移动
  cleanup.go      # 清理空目录
  naming.go       # Arc/Season/Episode 命名
  conflict.go     # 重名处理

internal/ui/
  window.go
  controls.go
  events.go
  progress.go

internal/config/
  config.go

internal/logging/
  logger.go
```

## 核心数据结构

```go
type ArchiveMode int

const (
    ModeTwoLevel ArchiveMode = iota
    ModeThreeLevel
)

type ArchiveConfig struct {
    SourceDir           string
    TargetDir           string
    Mode                ArchiveMode
    LevelCount          int
    LevelNames          []string
    FoldersPerLevel     []int
    FilesPerEpisode     int
    EpisodesPerSeason   int
    SeasonsPerArc       int
    CleanupEmptyDirs    bool
    KeepOriginalSubdirs bool
    NamingPreset        string
}

type VideoFile struct {
    SourcePath string
    RelPath    string
    Name       string
    Ext        string
    Size       int64
    ModTime    time.Time
}

type MovePlanItem struct {
    SourcePath string
    TargetPath string
    Size       int64
    Conflict   bool
    Status     string
    Error      string
}

type CapacityConfig struct {
    TotalFiles        int
    LevelCount        int
    LevelNames        []string
    FoldersPerLevel   []int
    FilesPerLeaf      int
    NamingPreset      string
}

type CapacityResult struct {
    RequiredLeafDirs   int
    LastLeafPath       string
    LastLeafFileCount  int
    ActualDirsPerLevel []int
    MaxCapacity        int
    Enough             bool
}
```

## 归档算法

1. 扫描源目录下所有视频文件。
2. 排除目标根目录和默认排除目录。
3. 按稳定顺序排序：

```text
最后修改时间升序（从早到晚）
时间相同时按相对路径升序
再按文件名和完整源路径升序
文件名升序
```

4. 计算第 `n` 个文件所在的 `Episode`：

```text
episodeIndex = n / FilesPerEpisode + 1
```

5. 计算 `Season`：

```text
seasonIndex = (episodeIndex - 1) / EpisodesPerSeason + 1
```

6. 三层模式下计算 `Arc`：

```text
arcIndex = (seasonIndex - 1) / SeasonsPerArc + 1
```

7. 生成目标路径。
8. 处理重名冲突。
9. 输出 dry-run。
10. 用户确认后执行移动。
11. 写入运行清单和日志。

## 安全与错误处理

- 移动前检查源路径是否存在。
- 目标目录创建失败时跳过对应文件。
- 移动失败时记录错误，不删除源文件。
- 跨盘复制后需要校验文件大小一致再删除源文件。
- 清理目录前再次检查是否为空。
- 所有操作写入日志，便于回看。
- 取消任务时不回滚已完成文件，但必须保留运行清单。

## 首版交付范围

首版只做：

- 本地目录扫描。
- 视频文件识别。
- 两层/三层结构 dry-run。
- 正式移动。
- 重名处理。
- 空目录清理。
- 基础 Win32 窗口和进度显示。

当前实现补充：

- 已实现自定义层级容量计算器，不再只限固定两层或三层。
- 已实现 dry-run TSV、正式运行清单 TSV 和基于清单撤销最近移动。
- 扫描、dry-run、正式移动、撤销和空目录清理均保持后台执行，UI 线程只接收状态并刷新控件。
- 扫描阶段使用不确定进度条；移动和撤销阶段使用确定进度条。
- 目标路径、运行清单和移动链路已接入长路径封装，目录选择使用资源管理器风格的 `IFileOpenDialog` 和 `IShellItem` 文件系统路径。
- 当前预览控件为分页文本预览，完整 dry-run 明细通过 TSV 导出保存。
- 完整运行日志异步写入程序目录 `data/logs/`。
- 目录名非法字符、源目录可读性和目标目录可访问性已增加前置校验。
- Dry-run 目标冲突检查按叶目录枚举并缓存，支持取消，避免在 SMB 上逐文件重复查询。
- 正式移动必须先成功创建运行清单；清单不可写时不会开始移动文件。
- 撤销前核对归档文件大小，完整撤销后清除当前可撤销状态。
- UI 已修复按钮裁切和控件重叠，结果、预览和日志使用真正的多行滚动控件。
- Win32 消息循环通过 `runtime.LockOSThread()` 固定在窗口创建线程。
- 配置文件加载和保存已后台化，关闭窗口时不会在窗口过程内同步等待磁盘或网络文件 I/O。
- 正式移动前复核计划中的源文件大小与修改时间，变化文件不会按旧 Dry-run 计划移动。

首版不做：

- 视频转码。
- 内容识别。
- 缩略图生成。
- 标签管理。
- 数据库索引。
- SSH/SFTP 直连。

## 后续可扩展方向

- 保存多个归档配置预设。
- 支持文件 hash 去重检查。
- 支持跳过小文件或指定扩展名。
- 支持任务暂停和恢复。
- 支持任务配置自定义
