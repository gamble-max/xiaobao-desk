<#
.SYNOPSIS
    Serverless Local Pairing & APK Distribution (Zero Cloud Server Required).
.DESCRIPTION
    Reads local host.json + host-fingerprint.txt, auto-discovers LAN / Tailscale / ZeroTier IPv4
    and global IPv6 addresses, and starts a temporary local HTTP server on port 21119 so your
    Android phone on the same Wi-Fi / Tailscale / ZeroTier network can download XiaobaoDesk.apk
    and import the serverless direct profile in one tap.
#>
[CmdletBinding()]
param(
    [int]$HttpPort = 21119,
    [int]$TimeoutMinutes = 30,
    [string]$ExtraDirectAddress = ""
)

$ErrorActionPreference = 'Stop'

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$rootDir = Split-Path -Parent $scriptDir
$apkPath = Join-Path $rootDir 'dist\XiaobaoDesk.apk'
$cfgDir = Join-Path $env:USERPROFILE '.xiaobao-desk'
$hostCfgPath = Join-Path $cfgDir 'host.json'
$fpPath = Join-Path $cfgDir 'host-fingerprint.txt'

if (-not (Test-Path -LiteralPath $hostCfgPath)) {
    throw "Host config not found at $hostCfgPath. Run scripts\setup-win-host.ps1 -InitConfig first."
}

$hostCfg = ([System.IO.File]::ReadAllText($hostCfgPath, [System.Text.Encoding]::UTF8)) | ConvertFrom-Json
$fp = if (Test-Path -LiteralPath $fpPath) {
    ([System.IO.File]::ReadAllText($fpPath, [System.Text.Encoding]::UTF8)).Trim()
} else {
    ""
}
if ([string]::IsNullOrWhiteSpace($fp) -and (Test-Path -LiteralPath (Join-Path $rootDir 'dist\xb-host.exe'))) {
    $fp = (& (Join-Path $rootDir 'dist\xb-host.exe') --config $hostCfgPath --print-fingerprint).Trim()
}

# Collect local direct addresses (LAN IPv4, Tailscale/ZeroTier IPv4, Global IPv6)
$addrs = New-Object System.Collections.Generic.List[string]
$lanUrlIp = $null

$ipProps = Get-NetIPAddress -AddressState Preferred -ErrorAction SilentlyContinue | Where-Object {
    $_.IPAddress -ne '127.0.0.1' -and $_.IPAddress -ne '::1' -and
    -not $_.IPAddress.StartsWith('169.254.') -and -not $_.IPAddress.StartsWith('fe80:')
}

foreach ($ip in $ipProps) {
    if ($ip.AddressFamily -eq 'InterNetwork') {
        $addrs.Add("$($ip.IPAddress):21118")
        if (-not $lanUrlIp -and ($ip.IPAddress.StartsWith('192.168.') -or $ip.IPAddress.StartsWith('10.') -or $ip.IPAddress.StartsWith('100.') -or $ip.IPAddress.StartsWith('172.'))) {
            $lanUrlIp = $ip.IPAddress
        }
    } elseif ($ip.AddressFamily -eq 'InterNetworkV6') {
        $cleanV6 = $ip.IPAddress.Split('%')[0]
        $addrs.Add("[$cleanV6]:21118")
    }
}

if (-not [string]::IsNullOrWhiteSpace($ExtraDirectAddress)) {
    $addrs.Insert(0, $ExtraDirectAddress.Trim())
}
if (-not $lanUrlIp) {
    $lanUrlIp = '127.0.0.1'
}

$directList = ($addrs | Select-Object -Unique) -join ', '

$profileObj = [ordered]@{
    name            = $env:COMPUTERNAME
    directAddress   = $directList
    hubAddress      = [string]$hostCfg.hub_addr
    hostId          = [string]$hostCfg.host_id
    certFingerprint = $fp
    password        = [string]$hostCfg.host_password
    superScreen     = $false
    fps             = 120
    codec           = 'hevc'
    bitrateMbps     = 16
}
$profileJson = $profileObj | ConvertTo-Json -Depth 3 -Compress
$b64 = [Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes($profileJson))
$b64Url = $b64.Replace('+', '-').Replace('/', '_').TrimEnd('=')

$intentHref = "intent://import?data=$b64Url#Intent;scheme=xiaobaodesk;package=com.xiaobao.desk;end"
$fallbackHref = "xiaobaodesk://import?data=$b64Url"

$rngBytes = New-Object byte[] 8
$rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
$rng.GetBytes($rngBytes)
$rng.Dispose()
$token = ([BitConverter]::ToString($rngBytes)).Replace('-', '').ToLower()

$hasApk = Test-Path -LiteralPath $apkPath
$apkBtnHtml = if ($hasApk) {
    "<a class='btn btn-primary' href='/d/$token/XiaobaoDesk.apk'>① 下载安装 XiaobaoDesk.apk</a>"
} else {
    "<p style='color:#fbbf24'>提示：未检测到 dist\XiaobaoDesk.apk，若手机已安装 App 可直接点击下方按钮导入配置。</p>"
}

$html = @"
<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>XiaobaoDesk 本地零服务端配对</title>
<style>
  body{margin:0;background:#0b1020;color:#e8eefc;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;display:flex;justify-content:center;align-items:center;min-height:100vh;padding:20px;box-sizing:border-box}
  .card{width:100%;max-width:440px;background:#141b33;border:1px solid #26325b;border-radius:18px;padding:24px;box-shadow:0 16px 40px rgba(0,0,0,.45)}
  h1{margin:0 0 8px;font-size:21px}
  .badge{display:inline-block;padding:4px 10px;border-radius:999px;font-size:12px;background:#1d2b53;color:#8db4ff;margin-bottom:16px}
  .btn{display:block;text-align:center;text-decoration:none;padding:15px 16px;border-radius:12px;font-weight:600;font-size:16px;margin:12px 0}
  .btn-primary{background:#3b82f6;color:#fff}
  .btn-success{background:#10b981;color:#052e16}
  .btn-sub{display:block;text-align:center;color:#93a7d1;font-size:13px;margin-top:8px;text-decoration:underline}
  p{color:#a7b6d9;font-size:14px;line-height:1.6}
</style>
</head>
<body>
  <div class="card">
    <h1>XiaobaoDesk 本地直连配对</h1>
    <div class="badge">无服务端模式 · 局域网 / IPv6 / 组网直连</div>
    $apkBtnHtml
    <p>点击下方按钮将本机直连地址、TLS 证书指纹与连接密码离线导入手机客户端：</p>
    <a class="btn btn-success" href="$intentHref">② 一键离线导入并连接 ($env:COMPUTERNAME)</a>
    <a class="btn-sub" href="$fallbackHref">备用跳转链接（若上方按钮无反应点此）</a>
  </div>
</body>
</html>
"@
$htmlBytes = [System.Text.Encoding]::UTF8.GetBytes($html)

$listener = New-Object System.Net.HttpListener
$listener.Prefixes.Add("http://+:$HttpPort/")
try {
    $listener.Start()
} catch {
    $listener = New-Object System.Net.HttpListener
    $listener.Prefixes.Add("http://${lanUrlIp}:$HttpPort/")
    $listener.Start()
}

Write-Host ""
Write-Host "=== XiaobaoDesk Serverless Local Pairing ==="
Write-Host "Open on your Android phone browser (same Wi-Fi / Tailscale / ZeroTier):"
Write-Host "  http://${lanUrlIp}:$HttpPort/d/$token"
Write-Host "Direct addresses included: $directList"
Write-Host "Press Ctrl+C to stop (auto-stops in $TimeoutMinutes minutes)."
Write-Host ""

$deadline = (Get-Date).AddMinutes($TimeoutMinutes)
while ($listener.IsListening -and (Get-Date) -lt $deadline) {
    $async = $listener.BeginGetContext($null, $null)
    if (-not $async.AsyncWaitHandle.WaitOne(1000)) {
        continue
    }
    $ctx = $listener.EndGetContext($async)
    $path = $ctx.Request.Url.AbsolutePath.TrimEnd('/')
    $resp = $ctx.Response
    try {
        if ($path -eq "/d/$token") {
            $resp.StatusCode = 200
            $resp.ContentType = "text/html; charset=utf-8"
            $resp.ContentLength64 = $htmlBytes.Length
            $resp.OutputStream.Write($htmlBytes, 0, $htmlBytes.Length)
        } elseif ($hasApk -and $path -eq "/d/$token/XiaobaoDesk.apk") {
            $apkBytes = [System.IO.File]::ReadAllBytes($apkPath)
            $resp.StatusCode = 200
            $resp.ContentType = "application/vnd.android.package-archive"
            $resp.AddHeader("Content-Disposition", 'attachment; filename="XiaobaoDesk.apk"')
            $resp.ContentLength64 = $apkBytes.Length
            $resp.OutputStream.Write($apkBytes, 0, $apkBytes.Length)
        } else {
            $resp.StatusCode = 404
        }
    } finally {
        $resp.Close()
    }
}
$listener.Stop()
