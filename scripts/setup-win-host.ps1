<#
.SYNOPSIS
    Configures Windows Firewall (TCP/UDP 21118), initializes host config, and optionally registers an interactive logon task.
#>
[CmdletBinding()]
param(
    [string]$DistDir = "",
    [string]$ConfigPath = "",
    [switch]$InitConfig,
    [switch]$RegisterTask
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($DistDir)) {
    $DistDir = Join-Path (Split-Path -Parent $PSScriptRoot) "dist"
}
if ([string]::IsNullOrWhiteSpace($ConfigPath)) {
    $ConfigPath = Join-Path $env:USERPROFILE ".xiaobao-desk\host.json"
}

$hostExe = Join-Path $DistDir "xb-host.exe"
if (-not (Test-Path -LiteralPath $hostExe)) {
    throw "xb-host.exe not found at: $hostExe"
}

$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator
)

function Ensure-FirewallRule {
    param(
        [string]$Name,
        [string]$Protocol,
        [int]$Port,
        [string]$Program
    )
    $existing = Get-NetFirewallRule -DisplayName $Name -ErrorAction SilentlyContinue
    if ($existing) {
        Write-Host "[WinHost] Firewall rule '$Name' already exists."
        return
    }
    if (-not $isAdmin) {
        Write-Warning "[WinHost] Administrator privileges required to create firewall rule '$Name'. Run elevated."
        return
    }
    New-NetFirewallRule -DisplayName $Name `
        -Direction Inbound `
        -Action Allow `
        -Protocol $Protocol `
        -LocalPort $Port `
        -Program $Program `
        -Profile Private,Public `
        -Description "XiaobaoDesk Direct Listener ($Protocol $Port)" | Out-Null
    Write-Host "[WinHost] Created firewall rule '$Name' ($Protocol $Port)."
}

Ensure-FirewallRule -Name "XiaobaoDesk-Host-TCP-21118" -Protocol "TCP" -Port 21118 -Program $hostExe
Ensure-FirewallRule -Name "XiaobaoDesk-Host-UDP-21118" -Protocol "UDP" -Port 21118 -Program $hostExe

if ($InitConfig) {
    $cfgDir = Split-Path -Parent $ConfigPath
    if (-not (Test-Path -LiteralPath $cfgDir)) {
        New-Item -ItemType Directory -Force -Path $cfgDir | Out-Null
    }
    if (-not (Test-Path -LiteralPath $ConfigPath)) {
        Write-Host "[WinHost] Initializing host config at $ConfigPath..."
        & $hostExe --init-config --config $ConfigPath
        if ($LASTEXITCODE -ne 0) {
            throw "xb-host.exe --init-config failed with exit code $LASTEXITCODE"
        }
    } else {
        Write-Host "[WinHost] Existing config preserved at $ConfigPath."
    }
}

if ($RegisterTask) {
    $taskName = "XiaobaoDesk-Host"
    $runLevel = if ($isAdmin) { "Highest" } else { "Limited" }
    if (-not $isAdmin) {
        Write-Warning "[WinHost] Not elevated; registering Scheduled Task '$taskName' with RunLevel=Limited."
    }
    $trayExe = Join-Path $DistDir "xb-tray.exe"
    $execPath = if (Test-Path -LiteralPath $trayExe) { $trayExe } else { $hostExe }
    $execArgs = if ($execPath -eq $trayExe) { "" } else { "--config `"$ConfigPath`" --log `"$(Join-Path (Split-Path -Parent $ConfigPath) 'host.log')`"" }
    $action = if ($execArgs) {
        New-ScheduledTaskAction -Execute $execPath -Argument $execArgs -WorkingDirectory $DistDir
    } else {
        New-ScheduledTaskAction -Execute $execPath -WorkingDirectory $DistDir
    }
    $triggerLogon = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew
    $principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive -RunLevel $runLevel
    Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $triggerLogon -Settings $settings -Principal $principal -Description "XiaobaoDesk Interactive Host" -Force | Out-Null
    Write-Host "[WinHost] Registered Scheduled Task '$taskName' ($execPath, RunLevel=$runLevel)."
}

Write-Host "[WinHost] Setup complete."
