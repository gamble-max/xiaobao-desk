<#
.SYNOPSIS
    Builds XiaobaoDesk v1.5.0 turnkey artifacts (XiaobaoDesk-Setup.exe, XiaobaoDesk.apk, xb-hub-linux-amd64/arm64) into dist/.
#>
[CmdletBinding()]
param(
    [switch]$SkipAndroid
)

$ErrorActionPreference = 'Stop'
$rootDir = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$distDir = Join-Path $rootDir 'dist'
$stageDir = Join-Path $distDir '_payload_stage'
New-Item -ItemType Directory -Force -Path $distDir, $stageDir, (Join-Path $stageDir 'vdd') | Out-Null

$goExe = (Get-Command go -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source)
if (-not $goExe) {
    $localGo = Join-Path $rootDir '.toolchains\go\bin\go.exe'
    if (Test-Path $localGo) { $goExe = $localGo }
}
if (-not $goExe) { throw "Go toolchain not found in PATH." }

Write-Host "[1/5] Building xb-hub (Windows & Linux amd64/arm64)..."
Push-Location (Join-Path $rootDir 'hub')
& $goExe build -o (Join-Path $stageDir 'xb-hub.exe') .\cmd\xb-hub
if ($LASTEXITCODE -ne 0) { throw "xb-hub.exe build failed" }
$env:GOOS = 'linux'; $env:GOARCH = 'amd64'
& $goExe build -o (Join-Path $distDir 'xb-hub-linux-amd64') .\cmd\xb-hub
if ($LASTEXITCODE -ne 0) { throw "xb-hub-linux-amd64 build failed" }
$env:GOARCH = 'arm64'
& $goExe build -o (Join-Path $distDir 'xb-hub-linux-arm64') .\cmd\xb-hub
Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
if ($LASTEXITCODE -ne 0) { throw "xb-hub-linux-arm64 build failed" }
Pop-Location

Write-Host "[2/5] Building xb-host.exe..."
Push-Location (Join-Path $rootDir 'win-host')
& $goExe build -o (Join-Path $stageDir 'xb-host.exe') .\cmd\xb-host
if ($LASTEXITCODE -ne 0) { throw "xb-host.exe build failed" }
Pop-Location

Write-Host "[3/5] Building xb-media-core.exe..."
& cmd.exe /c "cd /d `"$(Join-Path $rootDir 'win-media')`" && call build.bat"
if ($LASTEXITCODE -ne 0) { throw "xb-media-core.exe build failed" }
Move-Item -Force (Join-Path $distDir 'xb-media-core.exe') (Join-Path $stageDir 'xb-media-core.exe')

Write-Host "[4/5] Packaging XiaobaoDesk.exe & XiaobaoDesk-Setup.exe..."
$csc = "$env:WINDIR\Microsoft.NET\Framework64\v4.0.30319\csc.exe"
if (-not (Test-Path $csc)) { throw "csc.exe not found at $csc" }
& $csc /nologo /target:winexe /r:System.Windows.Forms.dll,System.Drawing.dll,System.Web.Extensions.dll /out:(Join-Path $stageDir 'XiaobaoDesk.exe') (Join-Path $rootDir 'win-installer\ControlPanelProgram.cs')
if ($LASTEXITCODE -ne 0) { throw "XiaobaoDesk.exe build failed" }

Add-Type -AssemblyName System.IO.Compression.FileSystem
$tmpVdd = Join-Path $distDir '_tmp_vdd'
$tmpNef = Join-Path $distDir '_tmp_nef'
Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $tmpVdd, $tmpNef
[System.IO.Compression.ZipFile]::ExtractToDirectory((Join-Path $rootDir 'drivers\vdd\vdd.zip'), $tmpVdd)
[System.IO.Compression.ZipFile]::ExtractToDirectory((Join-Path $rootDir 'drivers\vdd\nefcon.zip'), $tmpNef)
Get-ChildItem -Path $tmpVdd -Recurse -File | Where-Object { $_.Name -in @('MttVDD.inf', 'MttVDD.dll', 'mttvdd.cat') } | ForEach-Object { Copy-Item $_.FullName (Join-Path $stageDir 'vdd') -Force }
$nefconc = Get-ChildItem -Path $tmpNef -Recurse -Filter 'nefconc.exe' | Where-Object { $_.FullName -match 'x64' } | Select-Object -First 1
Copy-Item $nefconc.FullName (Join-Path $stageDir 'vdd\nefconc.exe') -Force
Copy-Item (Join-Path $rootDir 'drivers\vdd\vdd_settings.xml') (Join-Path $stageDir 'vdd\vdd_settings.xml') -Force

$payloadZip = Join-Path $distDir '_payload.zip'
Remove-Item -Force -ErrorAction SilentlyContinue $payloadZip
[System.IO.Compression.ZipFile]::CreateFromDirectory($stageDir, $payloadZip)

& $csc /nologo /target:winexe /win32manifest:(Join-Path $rootDir 'win-installer\setup.manifest') /r:System.Windows.Forms.dll,System.Drawing.dll,System.IO.Compression.dll,System.IO.Compression.FileSystem.dll /resource:"$payloadZip,XiaobaoDeskSetup.payload.zip" /out:(Join-Path $distDir 'XiaobaoDesk-Setup.exe') (Join-Path $rootDir 'win-installer\SetupProgram.cs')
if ($LASTEXITCODE -ne 0) { throw "XiaobaoDesk-Setup.exe build failed" }

Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $stageDir, $tmpVdd, $tmpNef, $payloadZip

if (-not $SkipAndroid) {
    Write-Host "[5/5] Building XiaobaoDesk.apk..."
    Push-Location (Join-Path $rootDir 'android')
    & .\gradlew.bat assembleDebug --no-daemon
    if ($LASTEXITCODE -ne 0) { throw "Android APK build failed" }
    Copy-Item -Force 'app\build\outputs\apk\debug\app-debug.apk' (Join-Path $distDir 'XiaobaoDesk.apk')
    Pop-Location
}

Write-Host "Release artifacts ready in $distDir"
