# Video Manager 文档入口

## 项目目标

Video Manager 是一个已经实现的 Windows 桌面视频归档工具，用于把大量零散视频按固定数量整理到稳定、可排序、可预览的目录结构中。

项目不做视频内容识别、标题识别、转码或标签管理。首版只基于文件系统元数据处理视频文件，并在正式移动前提供 dry-run 预览。

## 当前状态

当前项目已经包含可运行的 Go / Win32 实现、核心自动化测试和正式 Windows GUI 构建脚本。

当前已实现：

- 递归扫描视频文件并统计数量、大小和扩展名。
- 扫描在后台执行，支持忙碌进度条、节流日志和取消扫描。
- 归档容量计算器和多层目录命名预案。
- dry-run 移动计划生成、空目录预览和 TSV 导出，已后台化避免 UI 卡死。
- 正式移动文件，优先 rename，失败后使用 copy + verify + delete。
- 大文件复制按块执行，并在块边界响应取消。
- 目标重名自动追加 `_dup001`。
- 运行清单 TSV。
- 移动完成后清理源目录空文件夹。
- 移动前确认弹窗和移动进度条。
- 扫描使用不确定进度条，移动和撤销使用确定进度条，并减少进度条重绘闪动。
- 移动链路的长路径封装；本地路径轻量重试，SMB / UNC / 网络映射盘使用更长退避重试。
- dry-run 阶段显示计划清理的空目录预览。
- Dry-run 预览分页显示，完整明细导出 TSV。
- Dry-run TSV 写入程序目录 `data/dry-runs/`，预演阶段不创建或改写归档目标目录。
- UI 日志保留最近 200 行，完整日志异步写入 `data/logs/`。
- 目录名非法字符、源目录可读性、目标目录可访问性会提前校验。
- 浏览按钮使用资源管理器风格的 `IFileOpenDialog` 文件夹选择器，支持地址栏、侧边栏、现代导航和当前路径定位；旧系统创建失败时兼容回退。
- 程序目录 `data/config.json` 在后台自动保存和加载常用配置，不阻塞 Win32 消息循环。
- 基于运行清单撤销最近一次成功移动的文件。
- 深色 Win32 UI、深色标题栏和 EXE 图标资源。
- 标题栏与任务栏显式使用独立大小的应用图标，并通过稳定 AppUserModelID 避免任务栏身份回退。
- 构建时自动生成精确到分钟的版本号，标题栏与 Windows 文件属性使用相同版本和应用信息。

当前残余限制：

- 目录选择使用系统 Shell 的资源管理器式对话框，失效的 SMB 最近位置或第三方 Shell 扩展仍可能拖慢该系统对话框。
- SMB 单次系统 I/O 调用本身可能阻塞，程序可在任务边界和复制块边界响应取消，但不能强行中断所有 Windows 底层阻塞。

已完成的文档：

- [docs/animation_archive_naming.md](docs/animation_archive_naming.md)：两层 `Season / Episode` 归档命名规范。
- [docs/video_archive_structure_plan.md](docs/video_archive_structure_plan.md)：三层 `Arc / Season / Episode` 大规模归档结构方案。
- [docs/archive_capacity_calculator.md](docs/archive_capacity_calculator.md)：归档容量计算器、动态层级输入和 10 套多级目录命名预案。
- [docs/go_win32_video_archiver_plan.md](docs/go_win32_video_archiver_plan.md)：Go + Win32 桌面工具企划与实现边界。
- [docs/ui_freeze_risks_and_style.md](docs/ui_freeze_risks_and_style.md)：Go + Win32 UI 卡死风险清单与深色主题规范。
- [docs/test_cases.md](docs/test_cases.md)：当前自动化测试与人工验收应覆盖的核心场景。

## 推荐阅读顺序

1. 先读 [docs/animation_archive_naming.md](docs/animation_archive_naming.md)，了解中小规模目录的两层结构。
2. 再读 [docs/video_archive_structure_plan.md](docs/video_archive_structure_plan.md)，了解大规模目录的三层结构。
3. 读 [docs/archive_capacity_calculator.md](docs/archive_capacity_calculator.md)，了解如何自动计算目录层级、容量和命名预案。
4. 最后读 [docs/go_win32_video_archiver_plan.md](docs/go_win32_video_archiver_plan.md)，了解工具功能、UI、数据结构和安全策略。
5. 实现 UI 前读 [docs/ui_freeze_risks_and_style.md](docs/ui_freeze_risks_and_style.md)，避免 Win32 窗口卡死并统一深色主题。

## 归档模式概览

### 两层模式

适合中小规模视频目录。

```text
Animation/
  Season_001/
    Episode_001/
    Episode_002/
```

默认参数：

```text
每个 Episode 最多 25 个视频
每个 Season 包含 4 个 Episode
```

### 三层模式

适合大规模视频目录。

```text
VIDEO/
  Arc_01/
    Season_001/
      Episode_001/
      Episode_002/
```

默认参数：

```text
每个 Episode 最多 30 个视频
每个 Season 包含 5 个 Episode
每个 Arc 包含 10 个 Season
```

## 统一规则

- 目录编号使用补零格式，保证 Windows 资源管理器中排序稳定。
- `Episode` 和 `Season` 默认使用三位数，例如 `Episode_001`、`Season_001`。
- `Arc` 默认使用两位数，例如 `Arc_01`。
- 源目录内部原有目录名不参与分类判断；程序只递归扫描文件，再按稳定顺序重新分组。
- 文件按最后修改时间从早到晚分配；越早的文件进入编号越小的叶目录。修改时间相同时，再按相对路径和文件名升序保证结果稳定。
- 正式移动前会复核源文件大小和修改时间；扫描或 Dry-run 后发生变化的文件不会按旧计划移动。
- 每个叶目录的文件数是“最多 N 个”，只有最后一个叶目录可能少于 N 个。
- 默认保留原文件名，不按内容重命名。
- 目标重名时追加 `_dup001`、`_dup002`。
- 清理阶段只删除确认为空的旧目录。
- 不处理非视频文件，除非用户明确开启相关规则。

## 文件系统支持

首版必须支持 Windows 能正常访问的文件系统路径，包括：

- 本地磁盘路径，例如 `D:\Videos`。
- 软路由或 NAS 通过 SMB 挂载后的盘符路径，例如 `Z:\Videos`。
- SMB / UNC 路径，例如 `\\router\share\Videos`。

工具不直接实现 SMB 协议，只通过 Windows 文件系统 API 访问已认证、已挂载或可访问的网络路径。

## 构建

正式 EXE 必须从项目根目录使用统一脚本构建：

```powershell
.\build.ps1
```

默认输出为 `dist\VideoManager.exe`。脚本会按本机当前时间生成 `yyyyMM_HHmm` 格式版本号，例如 `202607_1035`，并同时写入：

- 窗口标题栏：`Video Manager - 202607_1035`。
- Windows 文件属性中的文件版本和产品版本。
- 产品名称、文件说明、公司、版权和原始文件名等应用信息。

不要使用裸 `go build` 生成正式发布文件，因为裸构建不会刷新 Win32 版本资源和标题栏版本。

维护验证命令：

```powershell
go test ./...
go test -race ./...
go vet -unsafeptr=false ./...
```

Win32 窗口过程必须把 `lParam` 转换为系统提供的结构体指针，因此标准 `go vet ./...` 的 `unsafeptr` 分析会对 `WM_DRAWITEM` 和 `WM_GETMINMAXINFO` 两处 ABI 转换给出预期告警；其余 vet 规则仍需通过。

启动阶段默认不创建日志文件，任务日志在首次使用时懒加载。需要诊断启动阶段时，可临时设置 `VIDEO_MANAGER_STARTUP_TRACE=1`，轨迹会写入 `data/logs/startup.log`。

## 仓库内容

仓库保留：

- `cmd/`、`internal/` 和 `tools/` 下的 Go 源码与测试。
- `assets/app.ico` 正式应用图标。
- `build.ps1` 正式构建入口。
- `README.md` 和 `docs/` 项目文档。

以下内容由 `.gitignore` 排除，不应提交：

- `dist/` 中的 EXE、运行配置、日志、Dry-run TSV 和其他运行数据。
- 构建生成的 `cmd/video-manager/app.syso`、`.build/` 和本地 Go 缓存。
- `.agents/`、`.codex/`、编辑器配置和操作系统元数据。
- 已转换完成且构建不再依赖的根目录原始图片 `ico.png`。

干净检出后运行 `.\build.ps1` 会重新生成 Win32 资源和 `dist/VideoManager.exe`。

## 维护约束

归档核心逻辑继续保持在无 UI 的 Go 包中，并用 [docs/test_cases.md](docs/test_cases.md) 中的场景验证扫描、规划、重名处理、dry-run、移动、撤销和清理逻辑。UI 层只负责配置、预览、进度和日志展示；Win32 消息循环必须固定在同一系统线程，文件和网络 I/O 不得进入 UI 线程。
