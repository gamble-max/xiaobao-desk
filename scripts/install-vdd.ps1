<#
.SYNOPSIS
    Installs the Virtual Display Driver (MttVDD) from drivers/vdd/*.zip and applies touch-friendly mobile resolutions.
.DESCRIPTION
    Run as Administrator. Extracts vdd.zip and nefcon.zip into C:\VirtualDisplayDriver\stage\,
    copies vdd_settings.xml (UTF-8 without BOM), sets HKLM:\SOFTWARE\MikeTheTech\VirtualDisplayDriver\VDDPATH,
    installs Root\MttVDD, and switches back to the primary internal display until a remote session extends it.
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator
)
if (-not $isAdmin) {
    throw "Please run scripts\install-vdd.ps1 from an Administrator PowerShell."
}

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$rootDir = Split-Path -Parent $scriptDir
$vddRoot = Join-Path $rootDir 'drivers\vdd'
$vddZip = Join-Path $vddRoot 'vdd.zip'
$nefconZip = Join-Path $vddRoot 'nefcon.zip'
$settingsSrc = Join-Path $vddRoot 'vdd_settings.xml'

if (-not (Test-Path -LiteralPath $vddZip)) { throw "Missing $vddZip" }
if (-not (Test-Path -LiteralPath $nefconZip)) { throw "Missing $nefconZip" }
if (-not (Test-Path -LiteralPath $settingsSrc)) { throw "Missing $settingsSrc" }

$targetCfgDir = 'C:\VirtualDisplayDriver'
$stageDir = Join-Path $targetCfgDir 'stage'
$vddExtract = Join-Path $stageDir 'vdd'
$nefconExtract = Join-Path $stageDir 'nefcon'

New-Item -ItemType Directory -Path $targetCfgDir -Force | Out-Null
New-Item -ItemType Directory -Path $vddExtract -Force | Out-Null
New-Item -ItemType Directory -Path $nefconExtract -Force | Out-Null

Expand-Archive -Path $vddZip -DestinationPath $vddExtract -Force
Expand-Archive -Path $nefconZip -DestinationPath $nefconExtract -Force

# Copy vdd_settings.xml without UTF-8 BOM
$xmlText = [System.IO.File]::ReadAllText($settingsSrc, [System.Text.Encoding]::UTF8)
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
[System.IO.File]::WriteAllText((Join-Path $targetCfgDir 'vdd_settings.xml'), $xmlText, $utf8NoBom)

& icacls.exe $targetCfgDir /grant "*S-1-5-19:(OI)(CI)RX" "*S-1-5-32-545:(OI)(CI)RX" | Out-Null

$regPath = 'HKLM:\SOFTWARE\MikeTheTech\VirtualDisplayDriver'
if (-not (Test-Path $regPath)) {
    New-Item -Path $regPath -Force | Out-Null
}
Set-ItemProperty -Path $regPath -Name 'VDDPATH' -Value $targetCfgDir -Type String

$infFile = Get-ChildItem -Path $vddExtract -Recurse -Filter 'MttVDD.inf' | Select-Object -First 1
$catFile = Get-ChildItem -Path $vddExtract -Recurse -Filter 'mttvdd.cat' | Select-Object -First 1
$nefconc = Get-ChildItem -Path $nefconExtract -Recurse -Filter 'nefconc.exe' | Select-Object -First 1

if (-not $infFile) { throw "MttVDD.inf not found after extracting vdd.zip" }
if (-not $nefconc) { throw "nefconc.exe not found after extracting nefcon.zip" }

if ($catFile) {
    $sig = Get-AuthenticodeSignature -FilePath $catFile.FullName
    if ($sig.SignerCertificate) {
        $thumb = $sig.SignerCertificate.Thumbprint
        $existingCert = Get-ChildItem -Path 'Cert:\LocalMachine\TrustedPublisher' | Where-Object { $_.Thumbprint -eq $thumb }
        if (-not $existingCert) {
            $store = New-Object System.Security.Cryptography.X509Certificates.X509Store('TrustedPublisher', 'LocalMachine')
            $store.Open('ReadWrite')
            $store.Add($sig.SignerCertificate)
            $store.Close()
            Write-Host "[VDD] Added driver certificate ($thumb) to TrustedPublisher."
        }
    }
}

Write-Host "[VDD] Installing Root\MttVDD via nefconc..."
& $nefconc.FullName install $infFile.FullName "Root\MttVDD" --no-duplicates --remove-duplicates
if ($LASTEXITCODE -ne 0 -and $LASTEXITCODE -ne 3010) {
    Write-Host "[VDD] Falling back to pnputil /add-driver /install..."
    & pnputil.exe /add-driver $infFile.FullName /install
}

Start-Sleep -Milliseconds 600
$dev = Get-PnpDevice -Class Display -ErrorAction SilentlyContinue | Where-Object { $_.HardwareID -contains 'Root\MttVDD' } | Select-Object -First 1
if ($dev) {
    Disable-PnpDevice -InstanceId $dev.InstanceId -Confirm:$false -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 500
    Enable-PnpDevice -InstanceId $dev.InstanceId -Confirm:$false -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 2
}

Start-Process -FilePath 'DisplaySwitch.exe' -ArgumentList '/internal' -Wait -ErrorAction SilentlyContinue
Write-Host "[VDD] Virtual Display Driver installed and ready."
