param(
    [string]$OutputPath = ".\dist\VideoManager.exe"
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$root = $PSScriptRoot
Set-Location $root

$windres = Get-Command windres -ErrorAction SilentlyContinue
if (-not $windres) {
    throw "windres.exe is required to generate the icon and Windows version information."
}

$iconPath = Join-Path $root "assets\app.ico"
if (-not (Test-Path -LiteralPath $iconPath)) {
    throw "Application icon was not found: $iconPath"
}

$outputFullPath = if ([System.IO.Path]::IsPathRooted($OutputPath)) {
    [System.IO.Path]::GetFullPath($OutputPath)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $root $OutputPath))
}
$outputDirectory = Split-Path -Parent $outputFullPath
$resourcePath = Join-Path $root "cmd\video-manager\app.syso"
$temporaryDirectory = Join-Path $root ".build"
$resourceScriptPath = Join-Path $temporaryDirectory "app-version.rc"
$buildMutex = [System.Threading.Mutex]::new($false, "Global\VideoManagerBuildVersionResource")
$buildMutexAcquired = $false

New-Item -ItemType Directory -Force $outputDirectory, $temporaryDirectory | Out-Null

try {
    [void]$buildMutex.WaitOne()
    $buildMutexAcquired = $true

    $buildVersion = Get-Date -Format "yyyyMMdd_HHmm"
    if ($buildVersion -notmatch '^\d{8}_\d{4}$') {
        throw "Unexpected build version format: $buildVersion"
    }

    $year = [int]$buildVersion.Substring(0, 4)
    $month = [int]$buildVersion.Substring(4, 2)
    $day = [int]$buildVersion.Substring(6, 2)
    $hour = [int]$buildVersion.Substring(9, 2)
    $minute = [int]$buildVersion.Substring(11, 2)
    $timeCode = $hour * 100 + $minute
    $resourceIconPath = $iconPath.Replace('\', '/')

    $resourceScript = @"
1 ICON "$resourceIconPath"

1 VERSIONINFO
FILEVERSION $year,$month,$day,$timeCode
PRODUCTVERSION $year,$month,$day,$timeCode
FILEFLAGSMASK 0x3fL
FILEFLAGS 0x0L
FILEOS 0x40004L
FILETYPE 0x1L
FILESUBTYPE 0x0L
BEGIN
  BLOCK "StringFileInfo"
  BEGIN
    BLOCK "040904B0"
    BEGIN
      VALUE "CompanyName", "STC214"
      VALUE "FileDescription", "Video archive organization tool"
      VALUE "FileVersion", "$buildVersion"
      VALUE "InternalName", "VideoManager"
      VALUE "LegalCopyright", "Copyright (C) $year STC214"
      VALUE "OriginalFilename", "VideoManager.exe"
      VALUE "ProductName", "Video Manager"
      VALUE "ProductVersion", "$buildVersion"
    END
  END
  BLOCK "VarFileInfo"
  BEGIN
    VALUE "Translation", 0x0409, 1200
  END
END
"@

    Set-Content -LiteralPath $resourceScriptPath -Value $resourceScript -Encoding ASCII
    & $windres.Source -O coff -o $resourcePath $resourceScriptPath
    if ($LASTEXITCODE -ne 0) {
        throw "windres failed with exit code $LASTEXITCODE"
    }

    $linkerFlags = "-H windowsgui -s -w -X video-manager/internal/ui.BuildVersion=$buildVersion"
    & go build -buildvcs=false -trimpath -ldflags $linkerFlags -o $outputFullPath .\cmd\video-manager
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed with exit code $LASTEXITCODE"
    }

    $versionInfo = [System.Diagnostics.FileVersionInfo]::GetVersionInfo($outputFullPath)
    if ($versionInfo.FileVersion -ne $buildVersion -or $versionInfo.ProductVersion -ne $buildVersion) {
        throw "Built EXE version verification failed. FileVersion=$($versionInfo.FileVersion), ProductVersion=$($versionInfo.ProductVersion)"
    }

    Write-Host "Built: $outputFullPath"
    Write-Host "Version: $buildVersion"
    Write-Host "Product: $($versionInfo.ProductName)"
    Write-Host "Description: $($versionInfo.FileDescription)"
} finally {
    Remove-Item -LiteralPath $resourceScriptPath -Force -ErrorAction SilentlyContinue
    if (Test-Path -LiteralPath $temporaryDirectory) {
        Remove-Item -LiteralPath $temporaryDirectory -Force -ErrorAction SilentlyContinue
    }
    if ($buildMutexAcquired) {
        $buildMutex.ReleaseMutex()
    }
    $buildMutex.Dispose()
}
