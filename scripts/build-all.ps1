<#
.SYNOPSIS
    Builds XiaobaoDesk Windows host, media core, tray, Linux hub, and Android APK into dist/.
#>
[CmdletBinding()]
param(
    [switch]$SkipAndroid,
    [switch]$BuildLinuxHub
)

$ErrorActionPreference = 'Stop'
$rootDir = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$distDir = Join-Path $rootDir 'dist'
New-Item -ItemType Directory -Force -Path $distDir | Out-Null

$goExe = (Get-Command go -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source)
if (-not $goExe) {
    $localGo = Join-Path $rootDir '.toolchains\go\bin\go.exe'
    if (Test-Path $localGo) { $goExe = $localGo }
}
if (-not $goExe) { throw "Go toolchain not found in PATH." }

Write-Host "[1/4] Building win-host (xb-host.exe)..."
Push-Location (Join-Path $rootDir 'win-host')
& $goExe build -o (Join-Path $distDir 'xb-host.exe') .\cmd\xb-host
if ($LASTEXITCODE -ne 0) { throw "xb-host build failed" }
Pop-Location

Write-Host "[2/4] Building tray (xb-tray.exe)..."
$csc = "$env:WINDIR\Microsoft.NET\Framework64\v4.0.30319\csc.exe"
if (Test-Path $csc) {
    & $csc /nologo /target:winexe /out:(Join-Path $distDir 'xb-tray.exe') (Join-Path $rootDir 'win-host\tray\Program.cs')
    if ($LASTEXITCODE -ne 0) { throw "xb-tray build failed" }
} else {
    Write-Warning "csc.exe not found; skipping xb-tray.exe"
}

Write-Host "[3/4] Building win-media (xb-media-core.exe)..."
& cmd.exe /c "cd /d `"$(Join-Path $rootDir 'win-media')`" && call build.bat"
if ($LASTEXITCODE -ne 0) { throw "xb-media-core build failed" }

if ($BuildLinuxHub) {
    Write-Host "[Optional] Building Linux amd64 xb-hub..."
    Push-Location (Join-Path $rootDir 'hub')
    $env:GOOS = 'linux'; $env:GOARCH = 'amd64'
    & $goExe build -o (Join-Path $distDir 'xb-hub-linux-amd64') .\cmd\xb-hub
    Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
    if ($LASTEXITCODE -ne 0) { throw "xb-hub build failed" }
    Pop-Location
}

if (-not $SkipAndroid) {
    Write-Host "[4/4] Building Android APK..."
    Push-Location (Join-Path $rootDir 'android')
    & .\gradlew.bat assembleDebug --no-daemon
    if ($LASTEXITCODE -ne 0) { throw "Android build failed" }
    Copy-Item -Force 'app\build\outputs\apk\debug\app-debug.apk' (Join-Path $distDir 'XiaobaoDesk.apk')
    Pop-Location
}

Write-Host "Build complete. Outputs in $distDir"
