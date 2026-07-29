package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"video-manager/file-manager/internal/appconfig"
	"video-manager/file-manager/internal/archive"
)

const (
	appTitle = "File Manager"
	iconID   = 1

	idSourceEdit   = 1001
	idBrowse       = 1002
	idScan         = 1003
	idStartYear    = 1004
	idExtensions   = 1005
	idFilesLeaf    = 1006
	idCalculate    = 1007
	idResult       = 1008
	idPreview      = 1009
	idLog          = 1010
	idTotalFiles   = 1011
	idTargetEdit   = 1012
	idBrowseTarget = 1013
	idDryRun       = 1014
	idMove         = 1015
	idCancel       = 1016
	idProgress     = 1017
	idUndo         = 1018
	idPreviewPrev  = 1019
	idPreviewNext  = 1020
	idFeedEdit     = 1021
	idBrowseFeed   = 1022
	idFeedDryRun   = 1023

	idLeafDirsMonth = 1100

	wmScanComplete     = win.WM_APP + 1
	wmMoveProgress     = win.WM_APP + 2
	wmMoveComplete     = win.WM_APP + 3
	wmUndoComplete     = win.WM_APP + 4
	wmScanProgress     = win.WM_APP + 5
	wmDryRunDone       = win.WM_APP + 6
	wmAppInit          = win.WM_APP + 7
	wmConfigLoaded     = win.WM_APP + 8
	wmCloseReady       = win.WM_APP + 9
	wmDryRunProgress   = win.WM_APP + 10
	wmBrowseDone       = win.WM_APP + 11
	wmManifestChecked  = win.WM_APP + 12
	wmConfigSaveFailed = win.WM_APP + 13

	bifReturnOnlyFSDirs = 0x0001
	bifNewDialogStyle   = 0x0040
	fosNoChangeDir      = 0x00000008
	fosPickFolders      = 0x00000020
	fosForceFileSystem  = 0x00000040
	fosPathMustExist    = 0x00000800
	sigdnFileSystemPath = 0x80058000
	odtButton           = 4
	odsSelected         = 0x0001
	odsDisabled         = 0x0004
	odsFocus            = 0x0010
	iconSmall           = 0
	iconBig             = 1
)

var (
	// BuildVersion is injected by build.ps1 and is shared with the EXE version resource.
	BuildVersion = ""

	className = syscall.StringToUTF16Ptr("FileManagerWindow")

	colorWindow   = rgb(0x10, 0x14, 0x18)
	colorPanel    = rgb(0x17, 0x1D, 0x23)
	colorInput    = rgb(0x0D, 0x11, 0x16)
	colorText     = rgb(0xE6, 0xED, 0xF3)
	colorSubtle   = rgb(0x9A, 0xA7, 0xB2)
	colorBorder   = rgb(0x3A, 0x47, 0x53)
	colorButton   = rgb(0x22, 0x2B, 0x34)
	colorPressed  = rgb(0x1A, 0x22, 0x2A)
	colorDisabled = rgb(0x62, 0x6D, 0x77)
	colorAccent   = rgb(0x4D, 0xA3, 0xFF)

	windowBrush  win.HBRUSH
	panelBrush   win.HBRUSH
	inputBrush   win.HBRUSH
	borderBrush  win.HBRUSH
	buttonBrush  win.HBRUSH
	pressedBrush win.HBRUSH
	font         win.HFONT

	startupTraceOnce sync.Once
	startupTraceCh   = make(chan string, 256)
	appByWindow      sync.Map
	pendingWindowApp *app
)

type app struct {
	hwnd     win.HWND
	instance win.HINSTANCE
	controls map[int]win.HWND

	mu                       sync.Mutex
	scanResult               archive.ScanResult
	currentPlan              archive.MovePlan
	currentPlanConfig        archive.PlanConfig
	currentPlanSourceRoot    string
	moveSummary              archive.MoveSummary
	undoSummary              archive.UndoSummary
	moveStatus               string
	moveDone                 int
	moveTotal                int
	moveNetwork              bool
	moveProgressBusy         bool
	moveCleanupMessages      []string
	moveCancel               context.CancelFunc
	scanCancel               context.CancelFunc
	scanStatus               string
	dryRunCancel             context.CancelFunc
	dryRunPreview            string
	dryRunTSV                string
	dryRunError              string
	dryRunLines              []string
	dryRunPage               int
	dryRunNetworkStage       int
	dryRunFeed               bool
	browsing                 bool
	browseKind               int
	browsePath               string
	browseOldSource          string
	browseOldTarget          string
	browseThreadID           uint32
	browseCancelRequested    bool
	progressTotal            int
	progressBusy             bool
	lastManifest             string
	lastManifestAvailable    bool
	scanning                 bool
	dryRunning               bool
	moving                   bool
	initializing             bool
	logCh                    chan string
	logOnce                  sync.Once
	configWriteMu            sync.Mutex
	configSequence           uint64
	configWritten            uint64
	pendingConfig            appconfig.Config
	pendingManifestAvailable bool
	pendingManifestCheckErr  error
	pendingManifestPath      string
	pendingConfigSaveErr     error
	manifestChecking         bool
	manifestCheckNetwork     bool
	configLoadErr            error
	configEdited             bool
	closing                  bool
	closeSaveStarted         bool
}

func Run() {
	// A Win32 window and its message queue must stay on the same OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	a := &app{
		controls: map[int]win.HWND{},
		logCh:    make(chan string, 512),
	}
	startupTrace(fmt.Sprintf("Run: UI thread locked, thread_id=%d", currentThreadID()))
	a.instance = win.GetModuleHandle(nil)
	setAppUserModelID("STC214.FileManager")
	startupTrace("Run: module handle loaded")
	icc := win.INITCOMMONCONTROLSEX{DwSize: uint32(unsafe.Sizeof(win.INITCOMMONCONTROLSEX{})), DwICC: win.ICC_PROGRESS_CLASS}
	win.InitCommonControlsEx(&icc)
	startupTrace("Run: common controls initialized")

	windowBrush = createSolidBrush(colorWindow)
	panelBrush = createSolidBrush(colorPanel)
	inputBrush = createSolidBrush(colorInput)
	borderBrush = createSolidBrush(colorBorder)
	buttonBrush = createSolidBrush(colorButton)
	pressedBrush = createSolidBrush(colorPressed)
	font = createUIFont()
	bigIcon, bigIconOwned := loadAppIcon(a.instance, win.GetSystemMetrics(win.SM_CXICON))
	smallIcon, smallIconOwned := loadAppIcon(a.instance, win.GetSystemMetrics(win.SM_CXSMICON))
	startupTrace("Run: gdi resources created")
	defer func() {
		win.DeleteObject(win.HGDIOBJ(windowBrush))
		win.DeleteObject(win.HGDIOBJ(panelBrush))
		win.DeleteObject(win.HGDIOBJ(inputBrush))
		win.DeleteObject(win.HGDIOBJ(borderBrush))
		win.DeleteObject(win.HGDIOBJ(buttonBrush))
		win.DeleteObject(win.HGDIOBJ(pressedBrush))
		if font != 0 {
			win.DeleteObject(win.HGDIOBJ(font))
		}
		if bigIconOwned && bigIcon != 0 {
			win.DestroyIcon(bigIcon)
		}
		if smallIconOwned && smallIcon != 0 {
			win.DestroyIcon(smallIcon)
		}
	}()

	var wc win.WNDCLASSEX
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	wc.LpfnWndProc = syscall.NewCallback(wndProc)
	wc.HInstance = a.instance
	wc.HbrBackground = windowBrush
	wc.LpszClassName = className
	wc.HCursor = win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_ARROW))
	wc.HIcon = bigIcon
	wc.HIconSm = smallIcon
	if atom := win.RegisterClassEx(&wc); atom == 0 {
		startupTrace("Run: RegisterClassEx failed")
		return
	}
	startupTrace("Run: window class registered")

	startupTrace("Run: CreateWindowEx begin")
	pendingWindowApp = a
	hwnd := win.CreateWindowEx(
		win.WS_EX_APPWINDOW,
		className,
		syscall.StringToUTF16Ptr(windowTitle()),
		win.WS_OVERLAPPEDWINDOW|win.WS_CLIPCHILDREN,
		win.CW_USEDEFAULT,
		win.CW_USEDEFAULT,
		1060,
		780,
		0,
		0,
		a.instance,
		nil,
	)
	pendingWindowApp = nil
	if hwnd == 0 {
		startupTrace("Run: CreateWindowEx failed")
		return
	}
	startupTrace("Run: CreateWindowEx ok")
	win.SendMessage(hwnd, win.WM_SETICON, iconBig, uintptr(bigIcon))
	win.SendMessage(hwnd, win.WM_SETICON, iconSmall, uintptr(smallIcon))

	enableDarkTitleBar(hwnd)
	startupTrace("Run: dark titlebar set")
	win.ShowWindow(hwnd, win.SW_SHOW)
	win.UpdateWindow(hwnd)
	startupTrace("Run: window shown")

	var msg win.MSG
	startupTrace(fmt.Sprintf("Run: message loop begin, thread_id=%d", currentThreadID()))
	for win.GetMessage(&msg, 0, 0, 0) > 0 {
		win.TranslateMessage(&msg)
		win.DispatchMessage(&msg)
	}
	runtime.KeepAlive(a)
	startupTrace("Run: message loop ended")
}

func windowTitle() string {
	version := strings.TrimSpace(BuildVersion)
	if version == "" {
		return appTitle
	}
	return appTitle + " - " + version
}

func wndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	a := getApp(hwnd)
	switch msg {
	case win.WM_NCCREATE:
		a = pendingWindowApp
		if a == nil {
			return 0
		}
		a.hwnd = hwnd
		appByWindow.Store(hwnd, a)
		return win.DefWindowProc(hwnd, msg, wParam, lParam)
	case win.WM_CREATE:
		if a != nil {
			startupTrace("WM_CREATE: begin")
			a.initializing = true
			startupTrace("WM_CREATE: createControls begin")
			a.createControls()
			startupTrace("WM_CREATE: createControls done")
			win.PostMessage(hwnd, wmAppInit, 0, 0)
		}
	case wmAppInit:
		if a != nil {
			startupTrace("APP_INIT: begin")
			a.initializing = false
			a.recalculate()
			startupTrace("APP_INIT: recalculate done")
			go func() {
				cfg, err := appconfig.Load()
				a.mu.Lock()
				a.pendingConfig = cfg
				a.pendingManifestAvailable = false
				a.configLoadErr = err
				a.mu.Unlock()
				win.PostMessage(a.hwnd, wmConfigLoaded, 0, 0)
			}()
		}
	case wmConfigLoaded:
		if a != nil {
			a.finishConfigLoad()
		}
	case wmManifestChecked:
		if a != nil {
			a.finishManifestCheck()
		}
	case wmConfigSaveFailed:
		if a != nil {
			a.finishConfigSaveFailed()
		}
	case wmCloseReady:
		win.DestroyWindow(hwnd)
	case win.WM_COMMAND:
		if a != nil {
			a.handleCommand(wParam, lParam)
		}
	case win.WM_DRAWITEM:
		if a != nil && a.drawButton((*win.DRAWITEMSTRUCT)(unsafe.Pointer(lParam))) {
			return 1
		}
	case win.WM_GETMINMAXINFO:
		mmi := (*win.MINMAXINFO)(unsafe.Pointer(lParam))
		mmi.PtMinTrackSize.X = 1060
		mmi.PtMinTrackSize.Y = 780
		return 0
	case wmScanComplete:
		if a != nil {
			a.finishScan()
		}
	case wmScanProgress:
		if a != nil {
			a.showScanProgress()
		}
	case wmDryRunDone:
		if a != nil {
			a.finishDryRun()
		}
	case wmDryRunProgress:
		if a != nil {
			a.showDryRunProgress(int(wParam))
		}
	case wmBrowseDone:
		if a != nil {
			a.finishBrowse()
		}
	case wmMoveProgress:
		if a != nil {
			a.showMoveProgress()
		}
	case wmMoveComplete:
		if a != nil {
			a.finishMove()
		}
	case wmUndoComplete:
		if a != nil {
			a.finishUndo()
		}
	case win.WM_CTLCOLORSTATIC:
		hdc := win.HDC(wParam)
		win.SetTextColor(hdc, colorText)
		win.SetBkColor(hdc, colorWindow)
		return uintptr(windowBrush)
	case win.WM_CTLCOLOREDIT, win.WM_CTLCOLORLISTBOX:
		hdc := win.HDC(wParam)
		win.SetTextColor(hdc, colorText)
		win.SetBkColor(hdc, colorInput)
		return uintptr(inputBrush)
	case win.WM_CTLCOLORBTN:
		hdc := win.HDC(wParam)
		win.SetTextColor(hdc, colorText)
		win.SetBkColor(hdc, colorPanel)
		return uintptr(panelBrush)
	case win.WM_ERASEBKGND:
		rect := win.RECT{}
		win.GetClientRect(hwnd, &rect)
		fillRect(win.HDC(wParam), &rect, windowBrush)
		return 1
	case win.WM_CLOSE:
		if a == nil {
			win.DestroyWindow(hwnd)
			return 0
		}
		a.requestClose()
		return 0
	case win.WM_DESTROY:
		if a != nil {
			a.cancelBackgroundTasks()
		}
		win.PostQuitMessage(0)
	case win.WM_NCDESTROY:
		appByWindow.Delete(hwnd)
	}
	return win.DefWindowProc(hwnd, msg, wParam, lParam)
}

func (a *app) createControls() {
	a.label("源目录", 24, 24, 80, 24)
	a.controls[idSourceEdit] = a.edit(idSourceEdit, "", 100, 22, 690, 28, false)
	a.button(idBrowse, "浏览...", 805, 21, 105, 30)
	a.button(idScan, "扫描", 920, 21, 105, 30)

	a.label("目标目录", 24, 60, 80, 24)
	a.controls[idTargetEdit] = a.edit(idTargetEdit, "", 100, 58, 690, 28, false)
	a.button(idBrowseTarget, "浏览...", 805, 57, 105, 30)
	a.button(idDryRun, "生成 Dry-run", 920, 57, 105, 30)
	a.label("投料目录", 24, 96, 80, 24)
	a.controls[idFeedEdit] = a.edit(idFeedEdit, "", 100, 94, 690, 28, false)
	a.button(idBrowseFeed, "浏览...", 805, 93, 105, 30)
	a.button(idFeedDryRun, "投料 Dry-run", 920, 93, 105, 30)
	a.controls[idProgress] = a.progress(idProgress, 100, 132, 580, 20)
	a.button(idCancel, "取消", 700, 127, 100, 30)
	a.button(idMove, "开始移动", 810, 127, 100, 30)
	a.button(idUndo, "撤销最近", 920, 127, 105, 30)

	a.label("按年份 / 季度 / 月份归档容量计算器", 24, 172, 300, 24)
	a.label("目标文件总数", 24, 208, 110, 24)
	a.controls[idTotalFiles] = a.edit(idTotalFiles, "0", 140, 204, 110, 28, true)
	a.controls[idCalculate] = a.button(idCalculate, "重新计算", 900, 203, 125, 30)

	a.label("文件后缀", 24, 246, 80, 24)
	a.controls[idExtensions] = a.edit(idExtensions, ".jpg,.png", 100, 242, 260, 28, false)
	a.label("起始年份", 385, 246, 80, 24)
	a.controls[idStartYear] = a.edit(idStartYear, strconv.Itoa(time.Now().Year()), 465, 242, 75, 28, true)
	a.label("每月叶目录数", 565, 246, 110, 24)
	a.controls[idLeafDirsMonth] = a.edit(idLeafDirsMonth, "4", 680, 242, 70, 28, true)
	a.label("每叶文件数", 775, 246, 100, 24)
	a.controls[idFilesLeaf] = a.edit(idFilesLeaf, "30", 875, 242, 70, 28, true)

	a.label("目录规则", 24, 288, 90, 24)
	a.label("YYYY  →  YYYYS1..S4  →  YYYYMM  →  YYYYMMNN", 115, 288, 360, 24)
	a.label("计算结果", 500, 288, 120, 24)
	a.controls[idResult] = a.textArea(idResult, 500, 315, 525, 145, false)
	a.setReadOnly(a.controls[idResult])

	a.label("预览", 24, 486, 90, 24)
	a.button(idPreviewPrev, "上一页", 360, 480, 68, 28)
	a.button(idPreviewNext, "下一页", 436, 480, 68, 28)
	a.controls[idPreview] = a.textArea(idPreview, 24, 512, 490, 180, true)
	a.setReadOnly(a.controls[idPreview])

	a.label("日志", 526, 486, 90, 24)
	a.controls[idLog] = a.textArea(idLog, 526, 512, 499, 180, true)
	a.setReadOnly(a.controls[idLog])

	a.setText(a.controls[idCalculate], "重新计算")
	a.setActionState(true, false, false, false, false)
}

func (a *app) setActionState(scan, dryRun, move, cancel, undo bool) {
	win.EnableWindow(a.controls[idScan], scan)
	win.EnableWindow(a.controls[idDryRun], dryRun)
	win.EnableWindow(a.controls[idMove], move)
	win.EnableWindow(a.controls[idCancel], cancel)
	win.EnableWindow(a.controls[idUndo], undo)
}

func (a *app) setConfigurationEnabled(enabled bool) {
	ids := []int{
		idSourceEdit, idBrowse, idTargetEdit, idBrowseTarget, idFeedEdit, idBrowseFeed, idFeedDryRun,
		idTotalFiles, idCalculate, idStartYear, idExtensions, idLeafDirsMonth, idFilesLeaf,
	}
	for _, id := range ids {
		win.EnableWindow(a.controls[id], enabled)
	}
}

func (a *app) handleCommand(wParam, lParam uintptr) {
	if a.closing {
		return
	}
	id := int(uint16(wParam))
	code := int(uint16(wParam >> 16))
	if !a.initializing && isConfigurationCommand(id, code) {
		a.configEdited = true
	}

	switch id {
	case idBrowse:
		if code == win.BN_CLICKED {
			current := strings.TrimSpace(a.text(a.controls[idSourceEdit]))
			oldTarget := strings.TrimSpace(a.text(a.controls[idTargetEdit]))
			a.startBrowse(idBrowse, "选择待整理文件源目录", current, current, oldTarget)
		}
	case idBrowseTarget:
		if code == win.BN_CLICKED {
			current := strings.TrimSpace(a.text(a.controls[idTargetEdit]))
			a.startBrowse(idBrowseTarget, "选择归档目标目录", current, "", "")
		}
	case idBrowseFeed:
		if code == win.BN_CLICKED {
			current := strings.TrimSpace(a.text(a.controls[idFeedEdit]))
			a.startBrowse(idBrowseFeed, "选择投料目录", current, "", "")
		}
	case idScan:
		if code == win.BN_CLICKED {
			a.startScan()
		}
	case idDryRun:
		if code == win.BN_CLICKED {
			a.generateDryRun()
		}
	case idFeedDryRun:
		if code == win.BN_CLICKED {
			a.generateFeedDryRun()
		}
	case idMove:
		if code == win.BN_CLICKED {
			a.startMove()
		}
	case idCancel:
		if code == win.BN_CLICKED {
			a.cancelMove()
		}
	case idUndo:
		if code == win.BN_CLICKED {
			a.startUndo()
		}
	case idPreviewPrev:
		if code == win.BN_CLICKED {
			a.changeDryRunPage(-1)
		}
	case idPreviewNext:
		if code == win.BN_CLICKED {
			a.changeDryRunPage(1)
		}
	case idCalculate:
		if code == win.BN_CLICKED {
			a.recalculate()
		}
	default:
		if !a.initializing && lParam != 0 && code == win.EN_CHANGE && id == idFeedEdit {
			a.invalidateFeedDependentState()
		}
		if !a.initializing && lParam != 0 && code == win.EN_CHANGE && (id == idSourceEdit || id == idTargetEdit || id == idExtensions) {
			a.invalidatePathDependentState()
		}
		if !a.initializing && lParam != 0 && code == win.EN_CHANGE && a.isCalculatorInput(id) {
			a.recalculate()
		}
		if !a.initializing && lParam != 0 && code == win.EN_CHANGE && isPlanConfigurationControl(id) {
			a.invalidatePlanForConfigurationChange()
		}
	}
}

func shouldFollowSourceTarget(oldSource, oldTarget string) bool {
	oldSource = strings.TrimSpace(oldSource)
	oldTarget = strings.TrimSpace(oldTarget)
	if oldTarget == "" {
		return true
	}
	if oldSource == "" {
		return false
	}
	expected := filepath.Join(oldSource, "_Archived")
	return archive.SamePath(oldTarget, expected)
}

func (a *app) startBrowse(kind int, title, initialPath, oldSource, oldTarget string) {
	a.mu.Lock()
	if a.browsing || a.scanning || a.dryRunning || a.moving {
		a.mu.Unlock()
		return
	}
	a.browsing = true
	a.browseKind = kind
	a.browsePath = ""
	a.browseOldSource = oldSource
	a.browseOldTarget = oldTarget
	a.browseThreadID = 0
	a.browseCancelRequested = false
	a.configEdited = true
	a.mu.Unlock()

	a.setConfigurationEnabled(false)
	a.setActionState(false, false, false, true, false)
	a.setProgressBusy(true)
	a.log("正在打开目录选择器……")

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		threadID := currentThreadID()
		a.mu.Lock()
		a.browseThreadID = threadID
		a.mu.Unlock()
		// The dialog is unowned so the main window remains available for Cancel and Close.
		path := browseFolder(0, title, initialPath)
		a.mu.Lock()
		if !a.browseCancelRequested {
			a.browsePath = path
		}
		a.mu.Unlock()
		win.PostMessage(a.hwnd, wmBrowseDone, 0, 0)
	}()
}

func (a *app) finishBrowse() {
	a.mu.Lock()
	kind := a.browseKind
	path := a.browsePath
	oldSource := a.browseOldSource
	oldTarget := a.browseOldTarget
	cancelled := a.browseCancelRequested
	a.browsing = false
	a.browseKind = 0
	a.browsePath = ""
	a.browseThreadID = 0
	a.browseCancelRequested = false
	hasScan := len(a.scanResult.Files) > 0
	hasPlan := len(a.currentPlan.Items) > 0 && a.currentPlan.ErrorCount == 0
	hasManifest := a.lastManifestAvailable
	closing := a.closing
	a.mu.Unlock()

	a.setProgressBusy(false)
	a.setProgress(0, 1)
	if closing {
		a.maybeFinishClose()
		return
	}
	a.setConfigurationEnabled(true)
	a.setActionState(true, hasScan, hasPlan, false, hasManifest)
	if cancelled || path == "" {
		a.log("已取消目录选择。")
		return
	}
	if kind == idBrowse {
		a.setText(a.controls[idSourceEdit], path)
		if shouldFollowSourceTarget(oldSource, oldTarget) {
			a.setText(a.controls[idTargetEdit], filepath.Join(path, "_Archived"))
		}
		a.log("已选择源目录: " + path)
		a.log("当前目标目录: " + strings.TrimSpace(a.text(a.controls[idTargetEdit])))
		return
	}
	if kind == idBrowseFeed {
		a.setText(a.controls[idFeedEdit], path)
		a.log("已选择投料目录: " + path)
		return
	}
	a.setText(a.controls[idTargetEdit], path)
	a.log("已选择目标目录: " + path)
}

func (a *app) invalidatePathDependentState() {
	a.mu.Lock()
	if a.scanning || a.dryRunning || a.moving {
		a.mu.Unlock()
		return
	}
	a.scanResult = archive.ScanResult{}
	a.currentPlan = archive.MovePlan{}
	a.currentPlanConfig = archive.PlanConfig{}
	a.currentPlanSourceRoot = ""
	a.dryRunPreview = ""
	a.dryRunLines = nil
	a.dryRunTSV = ""
	a.dryRunError = ""
	hasManifest := a.lastManifestAvailable
	a.mu.Unlock()

	a.setTextNoFlicker(a.controls[idPreview], "路径已变化，请重新扫描并生成 Dry-run。")
	a.setActionState(true, false, false, false, hasManifest)
}

func (a *app) invalidateFeedDependentState() {
	a.mu.Lock()
	if a.scanning || a.dryRunning || a.moving {
		a.mu.Unlock()
		return
	}
	a.currentPlan = archive.MovePlan{}
	a.currentPlanConfig = archive.PlanConfig{}
	a.currentPlanSourceRoot = ""
	a.dryRunPreview = ""
	a.dryRunLines = nil
	a.dryRunTSV = ""
	a.dryRunError = ""
	hasScan := len(a.scanResult.Files) > 0
	hasManifest := a.lastManifestAvailable
	a.mu.Unlock()

	a.setTextNoFlicker(a.controls[idPreview], "投料目录已变化，请重新生成投料 Dry-run。")
	a.setActionState(true, hasScan, false, false, hasManifest)
}

func isPlanConfigurationControl(id int) bool {
	return id == idStartYear || id == idLeafDirsMonth || id == idFilesLeaf
}

func (a *app) invalidatePlanForConfigurationChange() {
	a.mu.Lock()
	if a.scanning || a.dryRunning || a.moving || len(a.currentPlan.Items) == 0 {
		a.mu.Unlock()
		return
	}
	a.currentPlan = archive.MovePlan{}
	a.currentPlanConfig = archive.PlanConfig{}
	a.currentPlanSourceRoot = ""
	hasScan := len(a.scanResult.Files) > 0
	hasManifest := a.lastManifestAvailable
	a.mu.Unlock()

	a.setTextNoFlicker(a.controls[idPreview], "归档结构配置已变化，请重新生成 Dry-run。")
	a.setActionState(true, hasScan, false, false, hasManifest)
}

func samePlanConfig(left, right archive.PlanConfig) bool {
	if !archive.SamePath(left.TargetDir, right.TargetDir) ||
		left.StartYear != right.StartYear ||
		left.LeafDirsPerMonth != right.LeafDirsPerMonth ||
		left.FilesPerLeaf != right.FilesPerLeaf ||
		len(left.Extensions) != len(right.Extensions) {
		return false
	}
	for i := range left.Extensions {
		if left.Extensions[i] != right.Extensions[i] {
			return false
		}
	}
	return true
}

func isConfigurationCommand(id, code int) bool {
	if code != win.EN_CHANGE {
		return false
	}
	return id == idSourceEdit || id == idFeedEdit || id == idTargetEdit || id == idExtensions ||
		id == idTotalFiles || id == idStartYear || id == idLeafDirsMonth || id == idFilesLeaf
}

func (a *app) isCalculatorInput(id int) bool {
	return id == idStartYear || id == idLeafDirsMonth || id == idFilesLeaf || id == idTotalFiles
}

func (a *app) startScan() {
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	if a.scanning {
		a.mu.Unlock()
		cancel()
		return
	}
	a.scanning = true
	a.scanCancel = cancel
	a.scanStatus = "scan started"
	a.mu.Unlock()

	source := strings.TrimSpace(a.text(a.controls[idSourceEdit]))
	target := strings.TrimSpace(a.text(a.controls[idTargetEdit]))
	extensions, extensionErr := archive.ParseExtensions(a.text(a.controls[idExtensions]))
	if extensionErr != nil {
		a.log(extensionErr.Error())
		a.mu.Lock()
		a.scanning = false
		a.scanCancel = nil
		a.mu.Unlock()
		cancel()
		return
	}
	if source == "" {
		a.log("请先选择或输入源目录。")
		a.mu.Lock()
		a.scanning = false
		a.scanCancel = nil
		a.mu.Unlock()
		cancel()
		return
	}
	if target != "" && archive.SamePath(source, target) {
		a.log("源目录和目标目录不能相同，请重新选择目标目录。")
		a.mu.Lock()
		a.scanning = false
		a.scanCancel = nil
		a.mu.Unlock()
		cancel()
		return
	}
	a.log("开始扫描: " + source)
	if archive.IsLikelyNetworkPath(source) {
		a.log("检测到网络路径，扫描和访问校验会给予更长等待时间。")
	}
	a.setConfigurationEnabled(false)
	a.setActionState(false, false, false, true, false)
	a.setProgressBusy(true)

	go func() {
		if err := archive.CheckReadableDirForReadContext(ctx, source); err != nil {
			a.mu.Lock()
			a.scanResult = archive.ScanResult{SourceDir: source, ErrorCount: 1, Errors: []string{err.Error()}}
			a.currentPlan = archive.MovePlan{}
			a.currentPlanConfig = archive.PlanConfig{}
			a.scanning = false
			a.scanCancel = nil
			a.mu.Unlock()
			win.PostMessage(a.hwnd, wmScanComplete, 0, 0)
			return
		}
		var excluded []string
		if target != "" {
			excluded = append(excluded, target)
		}
		lastPost := time.Now().Add(-time.Second)
		result := archive.ScanFilesWithProgress(ctx, source, excluded, extensions, func(progress archive.ScanProgress) {
			if time.Since(lastPost) < time.Second {
				return
			}
			lastPost = time.Now()
			a.mu.Lock()
			a.scanStatus = fmt.Sprintf("扫描中: 已访问 %d, 目标文件 %d, 非目标文件 %d, 当前 %s",
				progress.Visited, progress.MatchedCount, progress.IgnoredCount, progress.CurrentPath)
			a.mu.Unlock()
			win.PostMessage(a.hwnd, wmScanProgress, 0, 0)
		})
		a.mu.Lock()
		a.scanResult = result
		a.currentPlan = archive.MovePlan{}
		a.currentPlanConfig = archive.PlanConfig{}
		a.scanning = false
		a.scanCancel = nil
		a.mu.Unlock()
		win.PostMessage(a.hwnd, wmScanComplete, 0, 0)
	}()
}

func (a *app) finishScan() {
	defer a.maybeFinishClose()
	a.mu.Lock()
	result := a.scanResult
	hasManifest := a.lastManifestAvailable
	a.mu.Unlock()
	hasUsableScan := len(result.Files) > 0 && result.ErrorCount == 0 && !result.Cancelled
	a.setConfigurationEnabled(true)
	a.setActionState(true, hasUsableScan, false, false, hasManifest)

	a.setText(a.controls[idCalculate], "重新计算")
	a.setText(a.controls[idTotalFiles], strconv.Itoa(result.MatchedCount))
	if result.Cancelled {
		a.log(fmt.Sprintf("扫描已取消: 已找到目标文件 %d 个, 非目标文件 %d 个, 错误 %d 个",
			result.MatchedCount, result.IgnoredCount, result.ErrorCount))
	} else {
		a.log(fmt.Sprintf("扫描完成: 目标文件 %d 个, 非目标文件 %d 个, 错误 %d 个, 总大小 %.2f GB",
			result.MatchedCount, result.IgnoredCount, result.ErrorCount, float64(result.TotalSize)/1024/1024/1024))
	}
	if len(result.Files) > 0 {
		a.log("归档排序: 按文件最后修改时间从早到晚；时间相同时按相对路径和文件名排序。")
	}
	for _, scanErr := range result.Errors {
		a.log("扫描错误: " + scanErr)
	}
	a.setProgressBusy(false)
	a.setProgress(1, 1)
	a.recalculate()
	if len(result.Files) == 0 && !result.Cancelled {
		a.log("当前源目录未扫描到目标文件，无法生成 Dry-run: " + result.SourceDir)
		a.setTextNoFlicker(a.controls[idPreview], "未扫描到目标文件。\r\n\r\n当前源目录："+result.SourceDir+"\r\n\r\n请选择包含目标文件的源目录后重新扫描。")
	}
}

func (a *app) generateDryRun() {
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	if a.dryRunning || a.scanning || a.moving {
		a.mu.Unlock()
		cancel()
		a.log("已有任务进行中，请等待完成或取消。")
		return
	}
	result := a.scanResult
	a.mu.Unlock()
	if result.Cancelled {
		cancel()
		a.log("上次扫描已取消，结果不完整；请重新扫描后再生成 Dry-run。")
		return
	}
	if result.ErrorCount > 0 {
		cancel()
		a.log(fmt.Sprintf("上次扫描有 %d 个读取错误，结果不完整；请排除错误并重新扫描。", result.ErrorCount))
		return
	}
	if len(result.Files) == 0 {
		cancel()
		a.log("没有可生成 dry-run 的扫描结果，请先扫描包含目标文件的目录。")
		return
	}
	if strings.TrimSpace(a.text(a.controls[idTargetEdit])) == "" {
		cancel()
		a.log("请先选择或输入目标目录。")
		return
	}
	if archive.SamePath(a.text(a.controls[idSourceEdit]), a.text(a.controls[idTargetEdit])) {
		cancel()
		a.log("源目录和目标目录不能相同，无法生成 Dry-run。")
		return
	}

	cfg := a.planConfig(len(result.Files))
	if err := archive.ValidateConfigForFiles(cfg, len(result.Files)); err != nil {
		cancel()
		a.log("归档配置无效: " + err.Error())
		return
	}
	a.mu.Lock()
	a.dryRunning = true
	a.dryRunCancel = cancel
	a.dryRunFeed = false
	a.currentPlan = archive.MovePlan{}
	a.currentPlanConfig = archive.PlanConfig{}
	a.currentPlanSourceRoot = ""
	a.dryRunPreview = ""
	a.dryRunTSV = ""
	a.dryRunError = ""
	a.dryRunLines = nil
	a.dryRunPage = 0
	a.mu.Unlock()
	sourceRoot := strings.TrimSpace(a.text(a.controls[idSourceEdit]))
	targetRoot := strings.TrimSpace(a.text(a.controls[idTargetEdit]))
	sourceNetwork := archive.IsLikelyNetworkPath(sourceRoot)
	targetNetwork := archive.IsLikelyNetworkPath(targetRoot)
	networkStage := 0
	if targetNetwork {
		networkStage = 2
	}
	if sourceNetwork {
		networkStage = 3
	}
	networkRead := networkStage > 0
	a.mu.Lock()
	a.dryRunNetworkStage = networkStage
	a.mu.Unlock()
	if networkRead {
		a.log("检测到网络路径，Dry-run 校验会给予 SMB/UNC 更长等待时间。")
	}

	a.setConfigurationEnabled(false)
	a.setActionState(false, false, false, true, false)
	if networkRead {
		a.setProgressBusy(true)
	} else {
		a.setProgress(0, 4)
	}
	a.setTextNoFlicker(a.controls[idPreview], "正在生成 Dry-run，请等待阶段进度完成……")
	a.log("开始生成 Dry-run。")

	go func() {
		if err := archive.CheckTargetRootContext(ctx, cfg.TargetDir); err != nil {
			a.mu.Lock()
			a.dryRunError = "目标目录不可访问: " + err.Error()
			a.dryRunning = false
			a.dryRunCancel = nil
			a.mu.Unlock()
			win.PostMessage(a.hwnd, wmDryRunDone, 0, 0)
			return
		}
		a.postDryRunProgress(1)
		plan := archive.BuildMovePlanContext(ctx, result.Files, cfg)
		if ctx.Err() == nil {
			a.postDryRunProgress(2)
		}
		lines := []string{
			fmt.Sprintf("Dry-run: %d 个文件, 目标目录 %d 个, 重名冲突 %d 个, 错误 %d 个",
				len(plan.Items), plan.TargetDirCount, plan.ConflictCount, plan.ErrorCount),
			"排序规则: 文件最后修改时间从早到晚，越早的文件进入编号越小的叶目录。",
			fmt.Sprintf("目录规则: 从 %04d 年开始，每月 %d 个叶目录，每叶最多 %d 个文件。", cfg.StartYear, cfg.LeafDirsPerMonth, cfg.FilesPerLeaf),
			fmt.Sprintf("续排结果: 涉及叶目录原有匹配文件 %d 个，最后叶目录 %s，规划后含 %d 个匹配文件。",
				plan.ExistingTargetFiles, emptyDash(plan.LastLeafPath), plan.LastLeafFileCount),
			"",
		}
		if plan.ErrorCount > 0 {
			lines = append(lines, "计划错误明细:")
			lines = append(lines, planErrorMessages(plan, 20)...)
			lines = append(lines, "")
		}
		for i := 0; i < len(plan.Items); i++ {
			item := plan.Items[i]
			if item.Error != "" {
				lines = append(lines, "ERROR: "+item.SourcePath+" -> "+item.TargetPath+" | "+item.Error)
			} else {
				lines = append(lines, item.SourcePath+" -> "+item.TargetPath)
			}
		}
		if sourceRoot != "" && ctx.Err() == nil {
			dirs, errs := archive.PreviewEmptyDirs(ctx, sourceRoot, []string{targetRoot})
			lines = append(lines, "", fmt.Sprintf("计划清理空目录: %d 个，预览错误 %d 个", len(dirs), len(errs)))
			cleanupLimit := 30
			if len(dirs) < cleanupLimit {
				cleanupLimit = len(dirs)
			}
			for i := 0; i < cleanupLimit; i++ {
				lines = append(lines, "DELETE EMPTY DIR: "+dirs[i])
			}
			if len(dirs) > cleanupLimit {
				lines = append(lines, fmt.Sprintf("... 还有 %d 个空目录未显示", len(dirs)-cleanupLimit))
			}
		}
		if ctx.Err() == nil {
			a.postDryRunProgress(3)
		}

		tsvPath := ""
		errText := ""
		if ctx.Err() != nil {
			errText = "Dry-run 已取消。"
		} else {
			dataDir, dataErr := appconfig.DataDir()
			if dataErr != nil {
				errText = "Dry-run 数据目录不可用: " + dataErr.Error()
			} else if path, exportErr := archive.ExportMovePlanTSVContext(ctx, plan, filepath.Join(dataDir, "dry-runs")); exportErr != nil {
				errText = "Dry-run TSV 导出失败: " + exportErr.Error()
			} else {
				tsvPath = path
				a.postDryRunProgress(4)
			}
		}

		a.mu.Lock()
		if ctx.Err() == nil {
			a.currentPlan = plan
			a.currentPlanConfig = cfg
			a.currentPlanSourceRoot = sourceRoot
		}
		a.dryRunLines = lines
		a.dryRunTSV = tsvPath
		a.dryRunError = errText
		a.dryRunning = false
		a.dryRunCancel = nil
		a.mu.Unlock()
		win.PostMessage(a.hwnd, wmDryRunDone, 0, 0)
	}()
}

func (a *app) generateFeedDryRun() {
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	if a.dryRunning || a.scanning || a.moving {
		a.mu.Unlock()
		cancel()
		a.log("已有任务进行中，请等待完成或取消。")
		return
	}
	a.mu.Unlock()

	feedRoot := strings.TrimSpace(a.text(a.controls[idFeedEdit]))
	targetRoot := strings.TrimSpace(a.text(a.controls[idTargetEdit]))
	if feedRoot == "" || targetRoot == "" {
		cancel()
		a.log("请先选择投料目录和目标目录。")
		return
	}
	if archive.SamePath(feedRoot, targetRoot) {
		cancel()
		a.log("投料目录和目标目录不能相同。")
		return
	}
	if pathIsSameOrInside(feedRoot, targetRoot) {
		cancel()
		a.log("投料目录不能位于目标目录内部，否则会被目标审计当作待重排文件。")
		return
	}
	extensions, extensionErr := archive.ParseExtensions(a.text(a.controls[idExtensions]))
	if extensionErr != nil {
		cancel()
		a.log(extensionErr.Error())
		return
	}
	cfg := a.planConfig(1)
	cfg.Extensions = extensions
	if err := archive.ValidateConfigForFiles(cfg, 1); err != nil {
		cancel()
		a.log("归档配置无效: " + err.Error())
		return
	}

	a.mu.Lock()
	a.dryRunning = true
	a.dryRunCancel = cancel
	a.dryRunFeed = true
	a.currentPlan = archive.MovePlan{}
	a.currentPlanConfig = archive.PlanConfig{}
	a.currentPlanSourceRoot = ""
	a.dryRunPreview = ""
	a.dryRunTSV = ""
	a.dryRunError = ""
	a.dryRunLines = nil
	a.dryRunPage = 0
	networkRead := archive.IsLikelyNetworkPath(feedRoot) || archive.IsLikelyNetworkPath(targetRoot)
	if networkRead {
		a.dryRunNetworkStage = 4
	} else {
		a.dryRunNetworkStage = 0
	}
	a.mu.Unlock()

	a.setConfigurationEnabled(false)
	a.setActionState(false, false, false, true, false)
	if networkRead {
		a.setProgressBusy(true)
		a.log("检测到网络路径，正在后台读取投料和目标目录；UI 可继续响应，也可取消。")
	} else {
		a.setProgress(0, 4)
	}
	a.setTextNoFlicker(a.controls[idPreview], "正在扫描投料并审计目标目录，请稍候…")
	a.log("开始生成投料 Dry-run。")

	go func() {
		fail := func(message string) {
			a.mu.Lock()
			a.dryRunError = message
			a.dryRunning = false
			a.dryRunCancel = nil
			a.mu.Unlock()
			win.PostMessage(a.hwnd, wmDryRunDone, 0, 0)
		}
		if err := archive.CheckReadableDirForReadContext(ctx, feedRoot); err != nil {
			fail("投料目录不可访问: " + err.Error())
			return
		}
		excluded := []string{}
		if pathIsSameOrInside(targetRoot, feedRoot) {
			excluded = append(excluded, targetRoot)
		}
		feedScan := archive.ScanFilesWithProgress(ctx, feedRoot, excluded, extensions, nil)
		if ctx.Err() != nil || feedScan.Cancelled {
			fail("投料 Dry-run 已取消。")
			return
		}
		if feedScan.ErrorCount > 0 {
			fail(fmt.Sprintf("投料目录扫描存在 %d 个读取错误: %s", feedScan.ErrorCount, strings.Join(feedScan.Errors, "; ")))
			return
		}
		if len(feedScan.Files) == 0 {
			fail("投料目录中没有符合当前后缀设置的文件。")
			return
		}
		a.postDryRunProgress(1)

		if err := archive.CheckTargetRootContext(ctx, targetRoot); err != nil {
			fail("目标目录不可访问: " + err.Error())
			return
		}
		plan := archive.BuildFeedPlanContext(ctx, feedScan.Files, cfg)
		if ctx.Err() != nil {
			fail("投料 Dry-run 已取消。")
			return
		}
		a.postDryRunProgress(2)

		auditState := "正确，仅续排投料文件"
		if plan.ErrorCount > 0 {
			auditState = "审计失败，已禁止正式移动"
		} else if plan.Rebalanced {
			auditState = "不符合数量/目录规则，将先重排现有目标文件，再追加投料"
		}
		lines := []string{
			fmt.Sprintf("投料 Dry-run: 投料文件 %d 个，目标已有匹配文件 %d 个，移动操作 %d 项，错误 %d 项。",
				len(feedScan.Files), plan.ExistingTargetFiles, len(plan.Items), plan.ErrorCount),
			"目标审计: " + auditState,
			"审计说明: " + plan.AuditMessage,
			"排序规则: 需要重排时，现有目标文件按修改时间从老到新重排；投料文件随后按修改时间从老到新追加。",
			fmt.Sprintf("目录规则: 从 %04d 年开始，每月 %d 个叶目录，每叶最多 %d 个文件。", cfg.StartYear, cfg.LeafDirsPerMonth, cfg.FilesPerLeaf),
			fmt.Sprintf("规划后最后叶目录: %s，其中匹配文件 %d 个。", emptyDash(plan.LastLeafPath), plan.LastLeafFileCount),
			"",
		}
		if plan.ErrorCount > 0 {
			lines = append(lines, "计划错误明细:")
			lines = append(lines, planErrorMessages(plan, 20)...)
			lines = append(lines, "")
		}
		for _, item := range plan.Items {
			prefix := ""
			if plan.StagingRoot != "" && pathIsSameOrInside(item.TargetPath, plan.StagingRoot) {
				prefix = "STAGE: "
			}
			if item.Error != "" {
				prefix = "ERROR: "
			}
			lines = append(lines, prefix+item.SourcePath+" -> "+item.TargetPath)
		}
		dirs, errs := archive.PreviewEmptyDirs(ctx, feedRoot, excluded)
		lines = append(lines, "", fmt.Sprintf("投料后计划清理空目录: %d 个，预览错误 %d 个。", len(dirs), len(errs)))
		for i := 0; i < len(dirs) && i < 30; i++ {
			lines = append(lines, "DELETE EMPTY DIR: "+dirs[i])
		}
		a.postDryRunProgress(3)

		tsvPath := ""
		errText := ""
		dataDir, dataErr := appconfig.DataDir()
		if dataErr != nil {
			errText = "Dry-run 数据目录不可用: " + dataErr.Error()
		} else if path, exportErr := archive.ExportMovePlanTSVContext(ctx, plan, filepath.Join(dataDir, "dry-runs")); exportErr != nil {
			errText = "Dry-run TSV 导出失败: " + exportErr.Error()
		} else {
			tsvPath = path
			a.postDryRunProgress(4)
		}

		a.mu.Lock()
		if ctx.Err() == nil {
			a.currentPlan = plan
			a.currentPlanConfig = cfg
			a.currentPlanSourceRoot = feedRoot
		}
		a.dryRunLines = lines
		a.dryRunTSV = tsvPath
		a.dryRunError = errText
		a.dryRunning = false
		a.dryRunCancel = nil
		a.mu.Unlock()
		win.PostMessage(a.hwnd, wmDryRunDone, 0, 0)
	}()
}

func pathIsSameOrInside(path, root string) bool {
	path = archive.DisplayPath(strings.TrimSpace(path))
	root = archive.DisplayPath(strings.TrimSpace(root))
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (a *app) finishDryRun() {
	defer a.maybeFinishClose()
	a.mu.Lock()
	preview := a.dryRunPreview
	lines := append([]string(nil), a.dryRunLines...)
	tsvPath := a.dryRunTSV
	errText := a.dryRunError
	itemCount := len(a.currentPlan.Items)
	planErrors := a.currentPlan.ErrorCount
	finalTargetFiles := a.currentPlan.FinalTargetFiles
	hasManifest := a.lastManifestAvailable
	hasScan := len(a.scanResult.Files) > 0
	networkStage := a.dryRunNetworkStage
	a.dryRunNetworkStage = 0
	a.dryRunFeed = false
	a.mu.Unlock()
	a.setConfigurationEnabled(true)
	a.setActionState(true, hasScan, errText == "" && itemCount > 0 && planErrors == 0, false, hasManifest)
	if finalTargetFiles > 0 {
		a.setText(a.controls[idTotalFiles], strconv.Itoa(finalTargetFiles))
		a.recalculate()
	}
	if networkStage > 0 {
		a.setProgressBusy(false)
		if errText != "" {
			a.setProgress(0, 4)
		}
	}

	if len(lines) > 0 {
		a.setDryRunPreviewPage(lines, 0)
	} else if preview != "" {
		a.setTextNoFlicker(a.controls[idPreview], preview)
	}
	if errText != "" {
		a.setTextNoFlicker(a.controls[idPreview], "Dry-run 未完成。\r\n\r\n"+errText)
		a.log(errText)
		return
	}
	if planErrors > 0 {
		a.mu.Lock()
		plan := a.currentPlan
		a.mu.Unlock()
		messages := planErrorMessages(plan, 1)
		firstError := "未知错误"
		if len(messages) > 0 {
			firstError = messages[0]
		}
		a.log(fmt.Sprintf("Dry-run 存在 %d 个错误项，开始移动保持禁用；首个错误: %s", planErrors, firstError))
		return
	}
	a.mu.Lock()
	plan := a.currentPlan
	a.mu.Unlock()
	existingResult := a.text(a.controls[idResult])
	if index := strings.Index(existingResult, "\r\nDry-run 续排:"); index >= 0 {
		existingResult = existingResult[:index]
	}
	existingResult += fmt.Sprintf("\r\nDry-run 续排: 已计入 %d 个既有目标文件\r\n规划后最后叶目录: %s\r\n该叶目录目标文件数: %d",
		plan.ExistingTargetFiles, emptyDash(plan.LastLeafPath), plan.LastLeafFileCount)
	a.setText(a.controls[idResult], existingResult)
	a.log(fmt.Sprintf("Dry-run 已生成: %d 个移动项。", itemCount))
	if tsvPath != "" {
		a.log("Dry-run TSV: " + tsvPath)
	}
}

func (a *app) postDryRunProgress(done int) {
	win.PostMessage(a.hwnd, wmDryRunProgress, uintptr(done), 0)
}

func (a *app) showDryRunProgress(done int) {
	a.mu.Lock()
	networkStage := a.dryRunNetworkStage
	feed := a.dryRunFeed
	a.mu.Unlock()
	if networkStage > 0 && done < networkStage {
		status := dryRunProgressMessage(done)
		if feed {
			status = feedDryRunProgressMessage(done)
		}
		if status != "" {
			a.log(status + " 正在读取网络目录……")
		}
		return
	}
	a.setProgress(done, 4)
	status := dryRunProgressMessage(done)
	if feed {
		status = feedDryRunProgressMessage(done)
	}
	if status != "" {
		a.log(status)
	}
}

func feedDryRunProgressMessage(done int) string {
	switch done {
	case 1:
		return "投料 Dry-run 进度 1/4: 投料目录扫描完成。"
	case 2:
		return "投料 Dry-run 进度 2/4: 目标审计与移动计划生成完成。"
	case 3:
		return "投料 Dry-run 进度 3/4: 空目录预览完成。"
	case 4:
		return "投料 Dry-run 进度 4/4: TSV 导出完成。"
	default:
		return ""
	}
}

func dryRunProgressMessage(done int) string {
	switch done {
	case 1:
		return "Dry-run 进度 1/4: 目标目录校验完成。"
	case 2:
		return "Dry-run 进度 2/4: 移动计划生成完成。"
	case 3:
		return "Dry-run 进度 3/4: 空目录预览完成。"
	case 4:
		return "Dry-run 进度 4/4: TSV 导出完成。"
	default:
		return ""
	}
}

func planErrorMessages(plan archive.MovePlan, limit int) []string {
	if limit <= 0 {
		return nil
	}
	seen := make(map[string]struct{})
	messages := make([]string, 0, min(limit, plan.ErrorCount))
	for _, item := range plan.Items {
		message := strings.TrimSpace(item.Error)
		if message == "" {
			continue
		}
		if _, exists := seen[message]; exists {
			continue
		}
		seen[message] = struct{}{}
		messages = append(messages, message)
		if len(messages) == limit {
			break
		}
	}
	return messages
}

const dryRunPreviewPageSize = 200

func (a *app) changeDryRunPage(delta int) {
	a.mu.Lock()
	lines := append([]string(nil), a.dryRunLines...)
	page := a.dryRunPage + delta
	a.mu.Unlock()
	if len(lines) == 0 {
		return
	}
	a.setDryRunPreviewPage(lines, page)
}

func (a *app) setDryRunPreviewPage(lines []string, page int) {
	if len(lines) == 0 {
		a.setTextNoFlicker(a.controls[idPreview], "")
		return
	}
	pages := (len(lines) + dryRunPreviewPageSize - 1) / dryRunPreviewPageSize
	page = clamp(page, 0, pages-1)
	start := page * dryRunPreviewPageSize
	end := start + dryRunPreviewPageSize
	if end > len(lines) {
		end = len(lines)
	}
	header := fmt.Sprintf("Dry-run 预览第 %d/%d 页，每页最多 %d 行。完整明细请看 TSV。\r\n\r\n", page+1, pages, dryRunPreviewPageSize)
	a.setTextNoFlicker(a.controls[idPreview], header+strings.Join(lines[start:end], "\r\n"))
	a.mu.Lock()
	a.dryRunPage = page
	a.dryRunPreview = ""
	a.mu.Unlock()
}

func (a *app) startMove() {
	a.mu.Lock()
	if a.moving || a.scanning || a.dryRunning {
		a.mu.Unlock()
		return
	}
	plan := a.currentPlan
	planCfg := a.currentPlanConfig
	a.mu.Unlock()
	currentCfg := a.planConfig(len(plan.Items))
	if err := archive.ValidateConfigForFiles(currentCfg, 1); err != nil {
		a.log("归档配置无效: " + err.Error())
		return
	}
	if len(plan.Items) == 0 {
		a.log("请先生成 dry-run。")
		return
	}
	if plan.ErrorCount > 0 {
		a.log("当前 dry-run 存在错误项，请修正后重新生成。")
		return
	}
	if !samePlanConfig(planCfg, currentCfg) {
		a.log("归档结构配置或目标目录已变化，请重新生成 dry-run。")
		a.invalidatePlanForConfigurationChange()
		return
	}
	confirmText := fmt.Sprintf("即将移动 %d 个文件。\r\n目标目录：%s\r\n\r\n确认开始正式移动？", len(plan.Items), plan.TargetRoot)
	if plan.Rebalanced {
		confirmText = fmt.Sprintf("目标目录数量或结构不符合当前规则。\r\n将先重排 %d 个现有目标文件，再追加投料；共执行 %d 次移动。\r\n\r\n目标目录：%s\r\n\r\n确认开始？",
			plan.ExistingTargetFiles, len(plan.Items), plan.TargetRoot)
	}
	if win.MessageBox(a.hwnd, syscall.StringToUTF16Ptr(confirmText), syscall.StringToUTF16Ptr("确认移动"), win.MB_YESNO|win.MB_ICONWARNING|win.MB_DEFBUTTON2) != win.IDYES {
		a.log("已取消正式移动。")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	if a.moving || a.scanning || a.dryRunning {
		a.mu.Unlock()
		cancel()
		return
	}
	a.moveCancel = cancel
	a.moving = true
	a.moveStatus = "开始移动..."
	a.moveCleanupMessages = nil
	a.mu.Unlock()

	a.setConfigurationEnabled(false)
	a.setActionState(false, false, false, true, false)
	a.setProgressBusy(false)
	a.setProgress(0, len(plan.Items))
	a.log("开始正式移动文件。")

	sourceRoot := a.currentPlanSourceRoot
	if strings.TrimSpace(sourceRoot) == "" {
		sourceRoot = strings.TrimSpace(a.text(a.controls[idSourceEdit]))
	}
	targetRoot := strings.TrimSpace(a.text(a.controls[idTargetEdit]))
	networkMove := archive.IsLikelyNetworkPath(sourceRoot) || archive.IsLikelyNetworkPath(targetRoot) || archive.IsLikelyNetworkPath(plan.TargetRoot)
	a.mu.Lock()
	a.moveNetwork = networkMove
	a.moveProgressBusy = false
	a.mu.Unlock()
	if networkMove {
		a.mu.Lock()
		a.moveProgressBusy = true
		a.moveStatus = "正在初始化网络运行清单……"
		a.mu.Unlock()
		a.setProgressBusy(true)
		a.log("检测到网络路径，移动、复制和删除会使用更长重试等待；取消会在当前 I/O 或复制块结束后生效。")
	}
	if plan.FeedFileCount > 0 {
		a.mu.Lock()
		a.moveProgressBusy = true
		a.moveStatus = "正式移动前正在重新审计目标目录……"
		a.mu.Unlock()
		a.setProgressBusy(true)
		a.log("正式移动前正在重新审计目标目录并核对 Dry-run 快照。")
	}

	go func() {
		if plan.FeedFileCount > 0 {
			if err := archive.ValidateFeedPlanPreflight(ctx, plan, planCfg); err != nil {
				summary := archive.MoveSummary{Total: len(plan.Items), Failed: len(plan.Items), Error: "投料前重新审计失败: " + err.Error()}
				if ctx.Err() != nil {
					summary.Failed = 0
					summary.Cancelled = true
					summary.Error = ""
				}
				a.mu.Lock()
				a.moveSummary = summary
				a.moving = false
				a.moveCancel = nil
				a.moveProgressBusy = false
				a.mu.Unlock()
				win.PostMessage(a.hwnd, wmMoveComplete, 0, 0)
				return
			}
			a.mu.Lock()
			a.moveProgressBusy = networkMove
			if networkMove {
				a.moveStatus = "投料前复审通过，正在初始化网络运行清单……"
			} else {
				a.moveStatus = "投料前目标目录复审通过，开始正式移动。"
			}
			a.moveDone = 0
			a.moveTotal = len(plan.Items)
			a.mu.Unlock()
			win.PostMessage(a.hwnd, wmMoveProgress, 0, 0)
		}
		lastPost := time.Now().Add(-time.Second)
		summary := archive.ExecuteMovePlan(ctx, plan, archive.MoveOptions{ReportItemStart: networkMove}, func(progress archive.MoveProgress) {
			if progress.Status != "processing" && time.Since(lastPost) < 250*time.Millisecond && progress.Index != progress.Total {
				return
			}
			lastPost = time.Now()
			a.mu.Lock()
			a.moveProgressBusy = a.moveNetwork && progress.Status == "processing"
			if progress.Status == "processing" {
				a.moveStatus = fmt.Sprintf("正在处理网络文件 %d/%d: %s", progress.Index+1, progress.Total, progress.TargetPath)
			} else if progress.Error != "" {
				a.moveStatus = fmt.Sprintf("移动进度 %d/%d: %s - %s", progress.Index, progress.Total, progress.Status, progress.Error)
			} else {
				a.moveStatus = fmt.Sprintf("移动进度 %d/%d: %s", progress.Index, progress.Total, progress.TargetPath)
			}
			a.moveDone = progress.Index
			a.moveTotal = progress.Total
			a.mu.Unlock()
			win.PostMessage(a.hwnd, wmMoveProgress, 0, 0)
		})
		if !summary.Cancelled && summary.Error == "" && summary.Moved > 0 && sourceRoot != "" {
			if archive.IsLikelyNetworkPath(sourceRoot) {
				a.mu.Lock()
				a.moveProgressBusy = true
				a.moveStatus = "正在读取网络源目录并清理空目录……"
				a.mu.Unlock()
				win.PostMessage(a.hwnd, wmMoveProgress, 0, 0)
			}
			removed, errs := archive.CleanupEmptyDirs(ctx, sourceRoot, []string{targetRoot})
			if len(errs) > 0 {
				a.mu.Lock()
				a.moveStatus = fmt.Sprintf("空目录清理完成: 删除 %d 个，错误 %d 个", removed, len(errs))
				a.moveCleanupMessages = append(a.moveCleanupMessages, a.moveStatus)
				for i := 0; i < len(errs) && i < 3; i++ {
					a.moveCleanupMessages = append(a.moveCleanupMessages, "空目录清理错误: "+errs[i])
				}
				a.mu.Unlock()
			}
		}
		if !summary.Cancelled && summary.Error == "" && summary.Failed == 0 && plan.StagingRoot != "" {
			if archive.IsLikelyNetworkPath(plan.StagingRoot) {
				a.mu.Lock()
				a.moveProgressBusy = true
				a.moveStatus = "正在读取网络目标目录并清理投料临时目录……"
				a.mu.Unlock()
				win.PostMessage(a.hwnd, wmMoveProgress, 0, 0)
			}
			removed, errs := archive.CleanupFeedStaging(ctx, plan.StagingRoot)
			if len(errs) > 0 {
				a.mu.Lock()
				a.moveStatus = fmt.Sprintf("投料临时目录清理完成: 删除 %d 个，错误 %d 个。", removed, len(errs))
				a.moveCleanupMessages = append(a.moveCleanupMessages, a.moveStatus)
				for i := 0; i < len(errs) && i < 3; i++ {
					a.moveCleanupMessages = append(a.moveCleanupMessages, "投料临时目录清理错误: "+errs[i])
				}
				a.mu.Unlock()
			}
		}
		a.mu.Lock()
		a.moveSummary = summary
		a.moving = false
		a.moveCancel = nil
		a.mu.Unlock()
		win.PostMessage(a.hwnd, wmMoveComplete, 0, 0)
	}()
}

func (a *app) cancelActiveTask() {
	a.mu.Lock()
	browsing := a.browsing
	scanCancel := a.scanCancel
	scanning := a.scanning
	dryRunCancel := a.dryRunCancel
	dryRunning := a.dryRunning
	cancel := a.moveCancel
	moving := a.moving
	a.mu.Unlock()
	if browsing {
		a.cancelBrowse()
		return
	}
	if moving && cancel != nil {
		cancel()
		a.log("正在取消，当前文件操作结束后停止。")
		return
	}
	if scanning && scanCancel != nil {
		scanCancel()
		a.log("正在取消扫描。")
		return
	}
	if dryRunning && dryRunCancel != nil {
		dryRunCancel()
		a.log("正在取消 Dry-run。")
	}
}

func (a *app) cancelBrowse() {
	a.mu.Lock()
	if !a.browsing {
		a.mu.Unlock()
		return
	}
	a.browseCancelRequested = true
	a.mu.Unlock()
	a.log("正在关闭目录选择器……")

	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		timeout := time.NewTimer(5 * time.Second)
		defer timeout.Stop()
		for {
			a.mu.Lock()
			browsing := a.browsing
			threadID := a.browseThreadID
			a.mu.Unlock()
			if !browsing {
				return
			}
			if threadID != 0 {
				closeThreadWindows(threadID)
			}
			select {
			case <-ticker.C:
			case <-timeout.C:
				return
			}
		}
	}()
}

func (a *app) cancelMove() {
	a.cancelActiveTask()
}

func (a *app) cancelBackgroundTasks() {
	a.mu.Lock()
	cancels := []context.CancelFunc{a.scanCancel, a.dryRunCancel, a.moveCancel}
	a.mu.Unlock()
	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
}

func (a *app) requestClose() {
	if a.closing {
		return
	}
	a.closing = true
	a.cancelBackgroundTasks()
	a.cancelBrowse()
	a.maybeFinishClose()
}

func (a *app) maybeFinishClose() {
	if !a.closing || a.closeSaveStarted {
		return
	}
	a.mu.Lock()
	active := a.scanning || a.dryRunning || a.moving
	a.mu.Unlock()
	if active {
		return
	}
	a.closeSaveStarted = true
	sequence, cfg := a.configSaveRequest()
	go func() {
		if err := a.writeConfig(sequence, cfg); err != nil {
			a.reportConfigSaveError(err)
		}
		win.PostMessage(a.hwnd, wmCloseReady, 0, 0)
	}()
}

func (a *app) showScanProgress() {
	a.mu.Lock()
	status := a.scanStatus
	a.mu.Unlock()
	if status != "" {
		a.log(status)
	}
}

func (a *app) showMoveProgress() {
	a.mu.Lock()
	status := a.moveStatus
	done := a.moveDone
	total := a.moveTotal
	busy := a.moveProgressBusy
	a.mu.Unlock()
	if status != "" {
		a.log(status)
	}
	if busy {
		a.setProgressBusy(true)
	} else if total > 0 {
		a.setProgress(done, total)
	}
}

func (a *app) finishMove() {
	defer a.maybeFinishClose()
	a.mu.Lock()
	summary := a.moveSummary
	cleanupMessages := append([]string(nil), a.moveCleanupMessages...)
	a.moveCleanupMessages = nil
	a.currentPlan = archive.MovePlan{}
	a.currentPlanConfig = archive.PlanConfig{}
	a.currentPlanSourceRoot = ""
	lastManifest, newManifest := manifestAfterMove(a.lastManifest, summary)
	a.lastManifest = lastManifest
	if newManifest {
		a.lastManifestAvailable = true
	}
	hasManifest := a.lastManifestAvailable
	a.moveNetwork = false
	a.moveProgressBusy = false
	a.mu.Unlock()
	a.setConfigurationEnabled(true)
	a.setActionState(true, false, false, false, hasManifest)
	a.setProgress(summary.Moved+summary.Failed, summary.Total)
	a.log(fmt.Sprintf("移动完成: 总数 %d, 成功 %d, 失败 %d, 已取消 %s", summary.Total, summary.Moved, summary.Failed, yesNo(summary.Cancelled)))
	if summary.Error != "" {
		a.log("移动安全检查错误: " + summary.Error)
	}
	for _, message := range cleanupMessages {
		a.log(message)
	}
	if newManifest {
		a.saveConfigAsync()
		a.log("运行清单: " + summary.ManifestPath)
	} else if summary.ManifestPath != "" {
		a.log("本次未成功移动文件，运行清单仅用于诊断，不会设为最近可撤销清单: " + summary.ManifestPath)
	}
}

func manifestAfterMove(existing string, summary archive.MoveSummary) (string, bool) {
	if summary.ManifestPath != "" && summary.Moved > 0 {
		return summary.ManifestPath, true
	}
	return existing, false
}

func (a *app) startUndo() {
	a.mu.Lock()
	manifest := a.lastManifest
	if manifest == "" {
		manifest = a.moveSummary.ManifestPath
	}
	if a.moving || a.scanning || a.dryRunning {
		a.mu.Unlock()
		a.log("后台任务进行中，不能撤销。")
		return
	}
	a.mu.Unlock()

	if strings.TrimSpace(manifest) == "" {
		a.log("没有可撤销的运行清单。")
		return
	}
	confirmText := "将根据最近一次运行清单撤销已成功移动的文件。\r\n\r\n" + manifest + "\r\n\r\n确认撤销？"
	if win.MessageBox(a.hwnd, syscall.StringToUTF16Ptr(confirmText), syscall.StringToUTF16Ptr("确认撤销"), win.MB_YESNO|win.MB_ICONWARNING|win.MB_DEFBUTTON2) != win.IDYES {
		a.log("已取消撤销。")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	networkUndo := archive.IsLikelyNetworkPath(manifest) ||
		archive.IsLikelyNetworkPath(a.text(a.controls[idSourceEdit])) ||
		archive.IsLikelyNetworkPath(a.text(a.controls[idTargetEdit]))
	a.mu.Lock()
	a.moving = true
	a.moveCancel = cancel
	a.moveStatus = "开始撤销..."
	a.moveNetwork = networkUndo
	a.moveProgressBusy = networkUndo
	if networkUndo {
		a.moveStatus = "正在读取网络运行清单……"
	}
	a.mu.Unlock()

	a.setConfigurationEnabled(false)
	a.setActionState(false, false, false, true, false)
	a.setProgressBusy(networkUndo)
	a.setProgress(0, 1)
	a.log("开始撤销最近一次移动。")

	go func() {
		lastPost := time.Now().Add(-time.Second)
		summary := archive.UndoManifestWithOptions(ctx, manifest, archive.MoveOptions{ReportItemStart: networkUndo}, func(progress archive.MoveProgress) {
			if progress.Status != "processing" && time.Since(lastPost) < 250*time.Millisecond && progress.Index != progress.Total {
				return
			}
			lastPost = time.Now()
			a.mu.Lock()
			a.moveProgressBusy = a.moveNetwork && progress.Status == "processing"
			if progress.Status == "processing" {
				a.moveStatus = fmt.Sprintf("正在处理网络撤销 %d/%d: %s", progress.Index+1, progress.Total, progress.TargetPath)
			} else if progress.Error != "" {
				a.moveStatus = fmt.Sprintf("撤销进度 %d/%d: %s", progress.Index, progress.Total, progress.Error)
			} else {
				a.moveStatus = fmt.Sprintf("撤销进度 %d/%d: %s", progress.Index, progress.Total, progress.TargetPath)
			}
			a.moveDone = progress.Index
			a.moveTotal = progress.Total
			a.mu.Unlock()
			win.PostMessage(a.hwnd, wmMoveProgress, 0, 0)
		})
		a.mu.Lock()
		a.moveStatus = fmt.Sprintf("撤销完成: 总数 %d, 成功 %d, 失败 %d, 已取消 %s", summary.Total, summary.Restored, summary.Failed, yesNo(summary.Cancelled))
		if summary.Error != "" {
			a.moveStatus += "，错误: " + summary.Error
		}
		a.undoSummary = summary
		a.moving = false
		a.moveCancel = nil
		a.mu.Unlock()
		win.PostMessage(a.hwnd, wmUndoComplete, 0, 0)
	}()
}

func (a *app) finishUndo() {
	defer a.maybeFinishClose()
	a.mu.Lock()
	summary := a.undoSummary
	if summary.Total > 0 && summary.Restored == summary.Total && summary.Failed == 0 && !summary.Cancelled {
		a.lastManifest = ""
		a.lastManifestAvailable = false
	}
	hasPendingUndo := a.lastManifestAvailable
	a.moveNetwork = false
	a.moveProgressBusy = false
	a.mu.Unlock()
	a.setConfigurationEnabled(true)
	a.setActionState(true, false, false, false, hasPendingUndo)
	a.showMoveProgress()
	if !hasPendingUndo {
		a.saveConfigAsync()
	}
}

func (a *app) recalculate() {
	cfg := a.planConfig(a.currentTotalFiles())
	result := archive.CalculateCapacity(a.currentTotalFiles(), cfg)
	resultText := fmt.Sprintf(
		"目标文件总数: %d\r\n需要叶目录: %d\r\n涉及月份 / 季度 / 年份: %d / %d / %d\r\n最后叶目录: %s\r\n最后叶目录文件数: %d\r\n每月容量: %d\r\n每年容量: %d",
		result.TotalFiles,
		result.RequiredLeafDirs,
		result.RequiredMonths,
		result.RequiredQuarters,
		result.RequiredYears,
		emptyDash(result.LastLeafPath),
		result.LastLeafFileCount,
		result.FilesPerMonth,
		result.FilesPerYear,
	)
	a.setText(a.controls[idResult], resultText)
	a.setTextNoFlicker(a.controls[idPreview], strings.Join(result.PreviewPaths, "\r\n"))
}

func (a *app) finishConfigLoad() {
	a.mu.Lock()
	cfg := a.pendingConfig
	manifestAvailable := a.pendingManifestAvailable
	err := a.configLoadErr
	a.mu.Unlock()
	startupTrace("APP_INIT: config load done")
	if err != nil || a.configEdited || a.closing {
		return
	}
	a.initializing = true
	a.applyConfig(cfg, manifestAvailable)
	a.initializing = false
	a.recalculate()
	a.startManifestCheck(cfg.LastManifest)
}

func (a *app) startManifestCheck(manifest string) {
	manifest = strings.TrimSpace(manifest)
	if manifest == "" {
		return
	}
	network := archive.IsLikelyNetworkPath(manifest)
	a.mu.Lock()
	a.manifestChecking = true
	a.manifestCheckNetwork = network
	a.pendingManifestPath = manifest
	a.pendingManifestAvailable = false
	a.pendingManifestCheckErr = nil
	a.mu.Unlock()
	if network {
		a.setProgressBusy(true)
		a.log("正在后台检查网络运行清单，配置已先行加载……")
	}
	go func() {
		available, err := archive.CheckManifestUndoable(manifest)
		a.mu.Lock()
		a.pendingManifestAvailable = available
		a.pendingManifestCheckErr = err
		a.manifestChecking = false
		a.mu.Unlock()
		win.PostMessage(a.hwnd, wmManifestChecked, 0, 0)
	}()
}

func (a *app) finishManifestCheck() {
	a.mu.Lock()
	manifest := a.pendingManifestPath
	available := a.pendingManifestAvailable
	checkErr := a.pendingManifestCheckErr
	network := a.manifestCheckNetwork
	active := a.browsing || a.scanning || a.dryRunning || a.moving
	sameManifest := archive.SamePath(a.lastManifest, manifest)
	closing := a.closing
	if sameManifest {
		a.lastManifestAvailable = available
	}
	a.pendingManifestPath = ""
	a.pendingManifestCheckErr = nil
	a.manifestCheckNetwork = false
	a.mu.Unlock()

	if closing {
		return
	}
	if network && !active {
		a.setProgressBusy(false)
		a.setProgress(0, 1)
	}
	if !sameManifest {
		return
	}
	win.EnableWindow(a.controls[idUndo], available && !active)
	if checkErr != nil {
		if available {
			a.log("运行清单存在需要人工确认的恢复状态，撤销时将再次校验: " + checkErr.Error())
		} else {
			a.log("无法检查最近运行清单，撤销暂不可用: " + checkErr.Error())
		}
		return
	}
	if available {
		a.log("最近运行清单检查完成，可以撤销。")
	}
}

func (a *app) applyConfig(cfg appconfig.Config, manifestAvailable bool) {
	a.setText(a.controls[idSourceEdit], cfg.SourceDir)
	a.setText(a.controls[idFeedEdit], cfg.FeedDir)
	a.setText(a.controls[idTargetEdit], cfg.TargetDir)
	if strings.TrimSpace(cfg.Extensions) != "" {
		a.setText(a.controls[idExtensions], cfg.Extensions)
	}
	if cfg.StartYear >= 1000 && cfg.StartYear <= 9999 {
		a.setText(a.controls[idStartYear], strconv.Itoa(cfg.StartYear))
	}
	if cfg.LeafDirsPerMonth > 0 {
		a.setText(a.controls[idLeafDirsMonth], strconv.Itoa(cfg.LeafDirsPerMonth))
	}
	if cfg.FilesPerLeaf > 0 {
		a.setText(a.controls[idFilesLeaf], strconv.Itoa(cfg.FilesPerLeaf))
	}
	a.lastManifest = cfg.LastManifest
	a.lastManifestAvailable = manifestAvailable
	win.EnableWindow(a.controls[idUndo], a.lastManifestAvailable)
}

func (a *app) configSnapshot() appconfig.Config {
	return appconfig.Config{
		SourceDir:        strings.TrimSpace(a.text(a.controls[idSourceEdit])),
		FeedDir:          strings.TrimSpace(a.text(a.controls[idFeedEdit])),
		TargetDir:        strings.TrimSpace(a.text(a.controls[idTargetEdit])),
		Extensions:       strings.TrimSpace(a.text(a.controls[idExtensions])),
		StartYear:        clamp(a.intFromControl(a.controls[idStartYear], time.Now().Year()), 1000, 9999),
		LeafDirsPerMonth: clamp(a.intFromControl(a.controls[idLeafDirsMonth], 4), 1, 99),
		FilesPerLeaf:     clamp(a.intFromControl(a.controls[idFilesLeaf], 30), 1, 1000000),
		LastManifest:     a.lastManifest,
	}
}

func (a *app) saveConfigAsync() {
	sequence, cfg := a.configSaveRequest()
	go func() {
		if err := a.writeConfig(sequence, cfg); err != nil {
			a.reportConfigSaveError(err)
		}
	}()
}

func (a *app) configSaveRequest() (uint64, appconfig.Config) {
	a.configSequence++
	return a.configSequence, a.configSnapshot()
}

func (a *app) writeConfig(sequence uint64, cfg appconfig.Config) error {
	a.configWriteMu.Lock()
	defer a.configWriteMu.Unlock()
	if sequence < a.configWritten {
		return nil
	}
	// Mark the newest attempted snapshot before writing so an older goroutine
	// cannot overwrite it if this save fails.
	a.configWritten = sequence
	if err := appconfig.Save(cfg); err != nil {
		return err
	}
	return nil
}

func (a *app) reportConfigSaveError(err error) {
	if err == nil {
		return
	}
	a.mu.Lock()
	a.pendingConfigSaveErr = err
	a.mu.Unlock()
	win.PostMessage(a.hwnd, wmConfigSaveFailed, 0, 0)
}

func (a *app) finishConfigSaveFailed() {
	a.mu.Lock()
	err := a.pendingConfigSaveErr
	a.pendingConfigSaveErr = nil
	a.mu.Unlock()
	if err != nil {
		a.log("配置保存失败: " + err.Error())
	}
}

func (a *app) currentTotalFiles() int {
	return clamp(a.intFromControl(a.controls[idTotalFiles], 0), 0, 1000000000)
}

func (a *app) planConfig(totalFiles int) archive.PlanConfig {
	extensions, _ := archive.ParseExtensions(a.text(a.controls[idExtensions]))
	return archive.PlanConfig{
		TargetDir:        strings.TrimSpace(a.text(a.controls[idTargetEdit])),
		StartYear:        clamp(a.intFromControl(a.controls[idStartYear], time.Now().Year()), 1000, 9999),
		LeafDirsPerMonth: clamp(a.intFromControl(a.controls[idLeafDirsMonth], 4), 1, 99),
		FilesPerLeaf:     clamp(a.intFromControl(a.controls[idFilesLeaf], 30), 1, 1000000),
		Extensions:       extensions,
	}
}

func (a *app) label(text string, x, y, w, h int32) win.HWND {
	hwnd := win.CreateWindowEx(0, syscall.StringToUTF16Ptr("STATIC"), syscall.StringToUTF16Ptr(text),
		win.WS_CHILD|win.WS_VISIBLE|win.SS_LEFT, x, y, w, h, a.hwnd, 0, a.instance, nil)
	a.applyFont(hwnd)
	return hwnd
}

func (a *app) edit(id int, text string, x, y, w, h int32, number bool) win.HWND {
	style := uint32(win.WS_CHILD | win.WS_VISIBLE | win.WS_TABSTOP | win.WS_BORDER | win.ES_AUTOHSCROLL)
	if number {
		style |= win.ES_NUMBER
	}
	hwnd := win.CreateWindowEx(0, syscall.StringToUTF16Ptr("EDIT"), syscall.StringToUTF16Ptr(text),
		style, x, y, w, h, a.hwnd, win.HMENU(uintptr(id)), a.instance, nil)
	a.applyFont(hwnd)
	return hwnd
}

func (a *app) textArea(id int, x, y, w, h int32, horizontalScroll bool) win.HWND {
	style := uint32(win.WS_CHILD | win.WS_VISIBLE | win.WS_TABSTOP | win.WS_BORDER | win.WS_VSCROLL |
		win.ES_MULTILINE | win.ES_AUTOVSCROLL | win.ES_NOHIDESEL)
	if horizontalScroll {
		style |= win.WS_HSCROLL | win.ES_AUTOHSCROLL
	}
	hwnd := win.CreateWindowEx(0, syscall.StringToUTF16Ptr("EDIT"), nil,
		style, x, y, w, h, a.hwnd, win.HMENU(uintptr(id)), a.instance, nil)
	a.applyFont(hwnd)
	win.SendMessage(hwnd, win.EM_SETLIMITTEXT, 8*1024*1024, 0)
	return hwnd
}

func (a *app) button(id int, text string, x, y, w, h int32) win.HWND {
	hwnd := win.CreateWindowEx(0, syscall.StringToUTF16Ptr("BUTTON"), syscall.StringToUTF16Ptr(text),
		win.WS_CHILD|win.WS_VISIBLE|win.WS_TABSTOP|win.BS_OWNERDRAW, x, y, w, h, a.hwnd, win.HMENU(uintptr(id)), a.instance, nil)
	a.applyFont(hwnd)
	a.controls[id] = hwnd
	return hwnd
}

func (a *app) combo(id int, x, y, w, h int32) win.HWND {
	hwnd := win.CreateWindowEx(0, syscall.StringToUTF16Ptr("COMBOBOX"), nil,
		win.WS_CHILD|win.WS_VISIBLE|win.WS_TABSTOP|win.CBS_DROPDOWNLIST,
		x, y, w, h, a.hwnd, win.HMENU(uintptr(id)), a.instance, nil)
	a.applyFont(hwnd)
	return hwnd
}

func (a *app) progress(id int, x, y, w, h int32) win.HWND {
	hwnd := win.CreateWindowEx(0, syscall.StringToUTF16Ptr("msctls_progress32"), nil,
		win.WS_CHILD|win.PBS_SMOOTH,
		x, y, w, h, a.hwnd, win.HMENU(uintptr(id)), a.instance, nil)
	win.SendMessage(hwnd, win.PBM_SETBKCOLOR, 0, uintptr(colorInput))
	win.SendMessage(hwnd, win.PBM_SETBARCOLOR, 0, uintptr(colorAccent))
	return hwnd
}

func (a *app) setProgressBusy(busy bool) {
	hwnd := a.controls[idProgress]
	if hwnd == 0 || a.progressBusy == busy {
		return
	}
	a.progressBusy = busy
	if busy {
		style := win.GetWindowLong(hwnd, win.GWL_STYLE)
		win.SetWindowLong(hwnd, win.GWL_STYLE, style|int32(win.PBS_MARQUEE))
		showWindow(hwnd, true)
		win.SendMessage(hwnd, win.PBM_SETMARQUEE, 1, 35)
		return
	}
	win.SendMessage(hwnd, win.PBM_SETMARQUEE, 0, 0)
	style := win.GetWindowLong(hwnd, win.GWL_STYLE)
	win.SetWindowLong(hwnd, win.GWL_STYLE, style&^int32(win.PBS_MARQUEE))
}

func (a *app) setProgress(done, total int) {
	hwnd := a.controls[idProgress]
	if hwnd == 0 {
		return
	}
	showWindow(hwnd, true)
	a.setProgressBusy(false)
	if total <= 0 {
		total = 1
	}
	if done < 0 {
		done = 0
	}
	if done > total {
		done = total
	}
	if a.progressTotal != total {
		a.progressTotal = total
		win.SendMessage(hwnd, win.PBM_SETRANGE32, 0, uintptr(total))
	}
	win.SendMessage(hwnd, win.PBM_SETPOS, uintptr(done), 0)
}

func (a *app) setReadOnly(hwnd win.HWND) {
	win.SendMessage(hwnd, win.EM_SETREADONLY, 1, 0)
}

func (a *app) drawButton(item *win.DRAWITEMSTRUCT) bool {
	if item == nil || item.CtlType != odtButton {
		return false
	}
	brush := buttonBrush
	if item.ItemState&odsSelected != 0 {
		brush = pressedBrush
	}
	fillRect(item.HDC, &item.RcItem, borderBrush)
	inner := item.RcItem
	inner.Left++
	inner.Top++
	inner.Right--
	inner.Bottom--
	fillRect(item.HDC, &inner, brush)

	textColor := colorText
	if item.ItemState&odsDisabled != 0 {
		textColor = colorDisabled
	}
	win.SetTextColor(item.HDC, textColor)
	win.SetBkMode(item.HDC, win.TRANSPARENT)
	oldFont := win.SelectObject(item.HDC, win.HGDIOBJ(font))
	text := syscall.StringToUTF16(a.text(item.HwndItem))
	win.DrawTextEx(item.HDC, &text[0], int32(len(text)-1), &inner,
		win.DT_CENTER|win.DT_VCENTER|win.DT_SINGLELINE|win.DT_NOPREFIX, nil)
	if oldFont != 0 {
		win.SelectObject(item.HDC, oldFont)
	}
	if item.ItemState&odsFocus != 0 {
		focus := inner
		focus.Left += 2
		focus.Top += 2
		focus.Right -= 2
		focus.Bottom -= 2
		win.DrawFocusRect(item.HDC, &focus)
	}
	return true
}

func (a *app) applyFont(hwnd win.HWND) {
	if font != 0 {
		// The parent is hidden during control creation; paint everything once on ShowWindow.
		win.SendMessage(hwnd, win.WM_SETFONT, uintptr(font), 0)
	}
}

func (a *app) text(hwnd win.HWND) string {
	length := getWindowTextLength(hwnd)
	buf := make([]uint16, length+1)
	getWindowText(hwnd, &buf[0], int32(len(buf)))
	return syscall.UTF16ToString(buf)
}

func (a *app) setText(hwnd win.HWND, text string) {
	setWindowText(hwnd, text)
}

func (a *app) setTextNoFlicker(hwnd win.HWND, text string) {
	if hwnd == 0 {
		return
	}
	win.SendMessage(hwnd, win.WM_SETREDRAW, 0, 0)
	setWindowText(hwnd, text)
	win.SendMessage(hwnd, win.WM_SETREDRAW, 1, 0)
	win.InvalidateRect(hwnd, nil, true)
}

func (a *app) intFromControl(hwnd win.HWND, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(a.text(hwnd)))
	if err != nil {
		return fallback
	}
	return value
}

func (a *app) log(message string) {
	if a.logCh != nil {
		a.logOnce.Do(func() {
			go a.runLogWriter()
		})
		select {
		case a.logCh <- time.Now().Format("2006-01-02 15:04:05") + "\t" + message:
		default:
		}
	}
	hwnd := a.controls[idLog]
	if hwnd == 0 {
		return
	}
	const maxLogLines = 200
	a.setTextNoFlicker(hwnd, prependLogMessage(a.text(hwnd), message, maxLogLines))
}

func prependLogMessage(old, message string, limit int) string {
	if limit <= 0 {
		return ""
	}
	lines := []string{message}
	if old != "" {
		lines = append(lines, strings.Split(old, "\r\n")...)
	}
	if len(lines) > limit {
		lines = lines[:limit]
	}
	return strings.Join(lines, "\r\n")
}

func (a *app) runLogWriter() {
	dataDir, err := appconfig.DataDir()
	if err != nil {
		return
	}
	logDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return
	}
	path := filepath.Join(logDir, "file-manager-"+time.Now().Format("20060102")+".log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer file.Close()
	for line := range a.logCh {
		_, _ = file.WriteString(line + "\r\n")
	}
}

func startupTrace(message string) {
	if os.Getenv("FILE_MANAGER_STARTUP_TRACE") != "1" {
		return
	}
	startupTraceOnce.Do(func() {
		go runStartupTraceWriter()
	})
	select {
	case startupTraceCh <- time.Now().Format("2006-01-02 15:04:05.000") + "\t" + message:
	default:
	}
}

func runStartupTraceWriter() {
	dataDir, err := appconfig.DataDir()
	if err != nil {
		return
	}
	logDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return
	}
	path := filepath.Join(logDir, "startup.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer file.Close()
	for line := range startupTraceCh {
		_, _ = file.WriteString(line + "\r\n")
	}
}

func getApp(hwnd win.HWND) *app {
	value, ok := appByWindow.Load(hwnd)
	if !ok {
		return nil
	}
	return value.(*app)
}

func sendString(hwnd win.HWND, msg uint32, text string) {
	win.SendMessage(hwnd, msg, 0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(text))))
}

func showWindow(hwnd win.HWND, show bool) {
	if show {
		win.ShowWindow(hwnd, win.SW_SHOW)
	} else {
		win.ShowWindow(hwnd, win.SW_HIDE)
	}
}

func clamp(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func yesNo(value bool) string {
	if value {
		return "是"
	}
	return "否"
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return filepath.FromSlash(value)
}

func rgb(r, g, b byte) win.COLORREF {
	return win.COLORREF(uint32(r) | uint32(g)<<8 | uint32(b)<<16)
}

func createUIFont() win.HFONT {
	var lf win.LOGFONT
	lf.LfHeight = -16
	lf.LfWeight = win.FW_NORMAL
	lf.LfCharSet = win.DEFAULT_CHARSET
	lf.LfOutPrecision = win.OUT_DEFAULT_PRECIS
	lf.LfClipPrecision = win.CLIP_DEFAULT_PRECIS
	lf.LfQuality = win.CLEARTYPE_QUALITY
	lf.LfPitchAndFamily = win.DEFAULT_PITCH | win.FF_DONTCARE
	copy(lf.LfFaceName[:], syscall.StringToUTF16("Microsoft YaHei UI"))
	return win.CreateFontIndirect(&lf)
}

func loadAppIcon(instance win.HINSTANCE, size int32) (win.HICON, bool) {
	if size <= 0 {
		size = 32
	}
	handle := win.LoadImage(instance, win.MAKEINTRESOURCE(iconID), win.IMAGE_ICON, size, size, 0)
	if handle != 0 {
		return win.HICON(handle), true
	}
	return win.LoadIcon(0, win.MAKEINTRESOURCE(win.IDI_APPLICATION)), false
}

func setAppUserModelID(id string) {
	if strings.TrimSpace(id) == "" {
		return
	}
	procSetCurrentProcessExplicitAppUserModelID.Call(
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(id))),
	)
}

func enableDarkTitleBar(hwnd win.HWND) {
	dwm := syscall.NewLazyDLL("dwmapi.dll")
	proc := dwm.NewProc("DwmSetWindowAttribute")
	value := int32(1)
	// DWMWA_USE_IMMERSIVE_DARK_MODE is 20 on current Windows, 19 on older builds.
	proc.Call(uintptr(hwnd), 20, uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value))
	proc.Call(uintptr(hwnd), 19, uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value))
}

type comObject struct {
	vtbl *[29]uintptr
}

var (
	clsidFileOpenDialog = win.CLSID{Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE, Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidFileOpenDialog   = win.IID{Data1: 0xD57C7288, Data2: 0xD4AD, Data3: 0x4768, Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
	iidShellItem        = win.IID{Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE, Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
)

func browseFolder(owner win.HWND, dialogTitle, initialPath string) string {
	hr := win.CoInitializeEx(nil, win.COINIT_APARTMENTTHREADED)
	if hr == win.S_OK || hr == win.S_FALSE {
		defer win.CoUninitialize()
	}
	if win.SUCCEEDED(hr) {
		if path, available := browseFolderModern(owner, dialogTitle, initialPath); available {
			return path
		}
	}
	return browseFolderLegacy(owner, dialogTitle)
}

func browseFolderModern(owner win.HWND, dialogTitle, initialPath string) (string, bool) {
	var dialogPointer unsafe.Pointer
	hr := win.CoCreateInstance(&clsidFileOpenDialog, nil, win.CLSCTX_INPROC_SERVER, &iidFileOpenDialog, &dialogPointer)
	if win.FAILED(hr) || dialogPointer == nil {
		return "", false
	}
	dialog := (*comObject)(dialogPointer)
	defer releaseCOM(dialog)

	var options uint32
	if win.FAILED(callCOM(dialog, 10, uintptr(unsafe.Pointer(&options)))) {
		return "", false
	}
	options |= fosNoChangeDir | fosPickFolders | fosForceFileSystem | fosPathMustExist
	if win.FAILED(callCOM(dialog, 9, uintptr(options))) {
		return "", false
	}
	if strings.TrimSpace(dialogTitle) != "" {
		title := syscall.StringToUTF16Ptr(dialogTitle)
		_ = callCOM(dialog, 17, uintptr(unsafe.Pointer(title)))
	}
	if item := shellItemFromPath(initialPath); item != nil {
		_ = callCOM(dialog, 12, uintptr(unsafe.Pointer(item)))
		releaseCOM(item)
	}

	// Once the modern dialog is shown, cancellation or a Shell error should not open a second dialog.
	if win.FAILED(callCOM(dialog, 3, uintptr(owner))) {
		return "", true
	}
	var resultPointer unsafe.Pointer
	if win.FAILED(callCOM(dialog, 20, uintptr(unsafe.Pointer(&resultPointer)))) || resultPointer == nil {
		return "", true
	}
	item := (*comObject)(resultPointer)
	defer releaseCOM(item)

	var pathPointer *uint16
	if win.FAILED(callCOM(item, 5, sigdnFileSystemPath, uintptr(unsafe.Pointer(&pathPointer)))) || pathPointer == nil {
		return "", true
	}
	defer win.CoTaskMemFree(uintptr(unsafe.Pointer(pathPointer)))
	return windows.UTF16PtrToString(pathPointer), true
}

func shellItemFromPath(path string) *comObject {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	pathPointer := syscall.StringToUTF16Ptr(path)
	var itemPointer unsafe.Pointer
	ret, _, _ := procSHCreateItemFromParsingName.Call(
		uintptr(unsafe.Pointer(pathPointer)),
		0,
		uintptr(unsafe.Pointer(&iidShellItem)),
		uintptr(unsafe.Pointer(&itemPointer)),
	)
	if win.FAILED(win.HRESULT(ret)) || itemPointer == nil {
		return nil
	}
	return (*comObject)(itemPointer)
}

func callCOM(object *comObject, method int, args ...uintptr) win.HRESULT {
	if object == nil || object.vtbl == nil || method < 0 || method >= len(object.vtbl) {
		return win.HRESULT(-2147467261)
	}
	params := make([]uintptr, 1, len(args)+1)
	params[0] = uintptr(unsafe.Pointer(object))
	params = append(params, args...)
	ret, _, _ := syscall.SyscallN(object.vtbl[method], params...)
	return win.HRESULT(ret)
}

func releaseCOM(object *comObject) {
	if object == nil || object.vtbl == nil {
		return
	}
	_, _, _ = syscall.SyscallN(object.vtbl[2], uintptr(unsafe.Pointer(object)))
}

func browseFolderLegacy(owner win.HWND, dialogTitle string) string {

	var bi win.BROWSEINFO
	title := syscall.StringToUTF16Ptr(dialogTitle)
	bi.HwndOwner = owner
	bi.LpszTitle = title
	bi.UlFlags = bifReturnOnlyFSDirs | bifNewDialogStyle

	pidl := win.SHBrowseForFolder(&bi)
	if pidl == 0 {
		return ""
	}
	defer win.CoTaskMemFree(pidl)

	buf := make([]uint16, 32768)
	if procSHGetPathFromIDListEx.Find() == nil {
		ret, _, _ := procSHGetPathFromIDListEx.Call(uintptr(pidl), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0)
		if ret == 0 {
			return ""
		}
	} else if !win.SHGetPathFromIDList(pidl, &buf[0]) {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

var (
	user32                                      = syscall.NewLazyDLL("user32.dll")
	gdi32                                       = syscall.NewLazyDLL("gdi32.dll")
	shell32                                     = syscall.NewLazyDLL("shell32.dll")
	procGetWindowText                           = user32.NewProc("GetWindowTextW")
	procGetTextLength                           = user32.NewProc("GetWindowTextLengthW")
	procSetWindowText                           = user32.NewProc("SetWindowTextW")
	procFillRect                                = user32.NewProc("FillRect")
	procEnumThreadWindows                       = user32.NewProc("EnumThreadWindows")
	procCreateSolidBrush                        = gdi32.NewProc("CreateSolidBrush")
	procSHGetPathFromIDListEx                   = shell32.NewProc("SHGetPathFromIDListEx")
	procSHCreateItemFromParsingName             = shell32.NewProc("SHCreateItemFromParsingName")
	procSetCurrentProcessExplicitAppUserModelID = shell32.NewProc("SetCurrentProcessExplicitAppUserModelID")
	procGetCurrentThreadID                      = syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentThreadId")
	enumCloseWindowCallback                     = syscall.NewCallback(enumCloseWindow)
)

func currentThreadID() uint32 {
	ret, _, _ := procGetCurrentThreadID.Call()
	return uint32(ret)
}

func closeThreadWindows(threadID uint32) bool {
	if threadID == 0 {
		return false
	}
	found := false
	procEnumThreadWindows.Call(
		uintptr(threadID),
		enumCloseWindowCallback,
		uintptr(unsafe.Pointer(&found)),
	)
	return found
}

func enumCloseWindow(hwnd win.HWND, lParam uintptr) uintptr {
	if hwnd != 0 {
		*(*bool)(unsafe.Pointer(lParam)) = true
		win.PostMessage(hwnd, win.WM_CLOSE, 0, 0)
	}
	return 1
}

func createSolidBrush(color win.COLORREF) win.HBRUSH {
	ret, _, _ := procCreateSolidBrush.Call(uintptr(color))
	return win.HBRUSH(ret)
}

func fillRect(hdc win.HDC, rect *win.RECT, brush win.HBRUSH) {
	procFillRect.Call(uintptr(hdc), uintptr(unsafe.Pointer(rect)), uintptr(brush))
}

func getWindowTextLength(hwnd win.HWND) int32 {
	ret, _, _ := procGetTextLength.Call(uintptr(hwnd))
	return int32(ret)
}

func getWindowText(hwnd win.HWND, buffer *uint16, maxCount int32) int32 {
	ret, _, _ := procGetWindowText.Call(uintptr(hwnd), uintptr(unsafe.Pointer(buffer)), uintptr(maxCount))
	return int32(ret)
}

func setWindowText(hwnd win.HWND, text string) {
	procSetWindowText.Call(uintptr(hwnd), uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(text))))
}
