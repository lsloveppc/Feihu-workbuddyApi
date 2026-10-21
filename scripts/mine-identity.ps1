<#
.SYNOPSIS
  绯狐 · 真机身份迁移：把本机官方 WorkBuddy 客户端的设备标识注入 auths/*.json。

.DESCRIPTION
  解决的问题
  ----------
  上游做通道真实性校验（11-128 "request illegal"）时，设备标识是账号级、可持久化
  的强信号。网关在此之前一律用 uid 哈希**伪造** machineId/qimei36，于是「一个在
  真机上登录过的账号，出站时却报另一台设备」——这是最容易被判成非官方客户端的
  形态（真机与网关两台设备同时活跃）。

  本脚本把真机的设备标识搬进 auth 文件，让网关以「同一台设备」的身份出站：
    - machine_id ← <data-dir>\device-id          （36 位 UUID）
    - qimei36    ← <data-dir>\qimei-cache.json   （腾讯 QIMEI36）
    - device_token（可选）← 手工指定，见下

  三者都只写「有真值」的键，缺哪个就跳过哪个——伪造比缺失更容易被识别。

.PARAMETER AuthDir
  auths 目录（默认 .\auths，与 config.json 的 auth_dir 一致）。

.PARAMETER DataDir
  官方客户端数据目录（默认自动探测 ~\.workbuddy）。

.PARAMETER DeviceToken
  可选：手工提供 X-Device-Token。真实来源在官方客户端的加密 blob（.workbuddy\keyblob，
  Electron OSCrypt 封装），需要 DPAPI 解封，本脚本不做自动提取。

.PARAMETER DryRun
  只打印将写入的内容，不改文件。

.EXAMPLE
  .\scripts\mine-identity.ps1 -DryRun
  .\scripts\mine-identity.ps1 -AuthDir .\auths
#>
[CmdletBinding()]
param(
    [string]$AuthDir = ".\auths",
    [string]$DataDir = "",
    [string]$DeviceToken = "",
    # Only 只处理文件名/uid 匹配该子串的 auth。多账号环境务必用它：
    # 真机标识只应注入「确实在这台机器上登录过」的那个账号。
    [string]$Only = "",
    # Force 多账号时强行全部注入（默认拒绝，见下方风控说明）。
    [switch]$Force,
    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'

function Find-DataDir {
    param([string]$Explicit)
    if ($Explicit -and (Test-Path $Explicit)) { return (Resolve-Path $Explicit).Path }
    $cands = @(
        (Join-Path $env:USERPROFILE ".workbuddy"),
        (Join-Path $env:APPDATA "WorkBuddy"),
        (Join-Path $env:LOCALAPPDATA "WorkBuddy")
    )
    foreach ($c in $cands) {
        if ($c -and (Test-Path (Join-Path $c "device-id"))) { return $c }
        if ($c -and (Test-Path (Join-Path $c "qimei-cache.json"))) { return $c }
    }
    return ""
}

$dir = Find-DataDir -Explicit $DataDir
if (-not $dir) {
    Write-Host "未找到官方客户端数据目录。用 -DataDir 指定（通常是 %USERPROFILE%\.workbuddy）" -ForegroundColor Red
    exit 2
}
Write-Host "官方客户端数据目录: $dir" -ForegroundColor Cyan

# --- 采集真机标识 ---------------------------------------------------------
$machineId = ""
$idFile = Join-Path $dir "device-id"
if (Test-Path $idFile) { $machineId = (Get-Content $idFile -Raw).Trim() }

$qimei = ""
$qFile = Join-Path $dir "qimei-cache.json"
if (Test-Path $qFile) {
    try {
        $q = Get-Content $qFile -Raw | ConvertFrom-Json
        if ($q.qimei36) { $qimei = [string]$q.qimei36 }
    } catch { Write-Host "qimei-cache.json 解析失败，跳过 qimei36" -ForegroundColor Yellow }
}

$ver = ""
$lFile = Join-Path $dir "last-launch.json"
if (Test-Path $lFile) {
    try { $launch = Get-Content $lFile -Raw | ConvertFrom-Json; $ver = [string]$launch.version } catch { }
}

Write-Host ("  machine_id : {0}" -f $(if ($machineId) { $machineId } else { "(未找到)" }))
Write-Host ("  qimei36    : {0}" -f $(if ($qimei) { $qimei } else { "(未找到)" }))
Write-Host ("  客户端版本 : {0}" -f $(if ($ver) { $ver } else { "(未找到)" }))
Write-Host ("  device_token: {0}" -f $(if ($DeviceToken) { "已提供" } else { "(未提供，跳过)" }))

if (-not $machineId -and -not $qimei -and -not $DeviceToken) {
    Write-Host "没有任何真机标识可注入，退出（不写空值）。" -ForegroundColor Yellow
    exit 1
}

# --- 注入 auths ----------------------------------------------------------
if (-not (Test-Path $AuthDir)) {
    Write-Host "auths 目录不存在: $AuthDir" -ForegroundColor Red
    exit 2
}
$files = Get-ChildItem $AuthDir -Filter "workbuddy*.json" -File
if (-not $files) { Write-Host "auths 目录下没有 workbuddy*.json" -ForegroundColor Yellow; exit 1 }

if ($Only) {
    $files = @($files | Where-Object { $_.Name -like "*$Only*" })
    if (-not $files) { Write-Host "-Only '$Only' 没匹配到任何 auth 文件" -ForegroundColor Red; exit 1 }
}

# 多账号防护（重要）：把同一台设备的 machine_id/qimei36 注入多个账号，等于告诉上游
# 「一台设备关联 N 个账号」—— 这正是批量小号最典型的关联特征，比设备标识缺失更危险。
# 所以默认拒绝，要求显式 -Only 指定真机登录过的那个账号；确实要共用本机才加 -Force。
if (-not $Only -and $files.Count -gt 1 -and -not $Force) {
    Write-Host ""
    Write-Host "检测到 $($files.Count) 个账号，已拒绝全部注入。" -ForegroundColor Yellow
    Write-Host "原因：同一 device-id / qimei36 注入多个账号 = 上游视角「一台设备关联多个账号」，" -ForegroundColor Yellow
    Write-Host "      这是批量小号的典型关联特征。" -ForegroundColor Yellow
    Write-Host "做法：用 -Only <uid片段> 只注入真机登录过的那个账号；确实要多账号共用本机再加 -Force。" -ForegroundColor Yellow
    exit 3
}

$changed = 0
foreach ($f in $files) {
    $raw = [IO.File]::ReadAllText($f.FullName, [Text.Encoding]::UTF8)
    $obj = $raw | ConvertFrom-Json
    $names = @($obj.PSObject.Properties | ForEach-Object { $_.Name })
    $mode = if ($names -contains "auth") { "nested" } else { "flat" }

    # 转成有序字典再序列化：保持原有键顺序（auth/account 在前），新增键追加到尾部。
    $dict = [ordered]@{}
    foreach ($p in $obj.PSObject.Properties) { $dict[$p.Name] = $p.Value }

    $added = @()
    if ($machineId -and -not ($names -contains "machine_id")) { $dict["machine_id"] = $machineId; $added += "machine_id" }
    if ($qimei -and -not ($names -contains "qimei36")) { $dict["qimei36"] = $qimei; $added += "qimei36" }
    if ($DeviceToken -and -not ($names -contains "device_token")) { $dict["device_token"] = $DeviceToken; $added += "device_token" }

    if ($added.Count -eq 0) {
        Write-Host "  跳过 $($f.Name)（已含全部真机标识）" -ForegroundColor DarkGray
        continue
    }

    $out = $dict | ConvertTo-Json -Depth 8
    if ($DryRun) {
        Write-Host "`n[DRY] $($f.Name) ($mode) 将新增: $($added -join ', ')" -ForegroundColor DarkGray
        Write-Host $out -ForegroundColor DarkGray
    } else {
        # 原子替换：tmp + rename（与 Go 侧 SaveAtomic 同一语义）
        $tmp = $f.FullName + ".tmp"
        [IO.File]::WriteAllText($tmp, $out, (New-Object System.Text.UTF8Encoding($false)))
        Move-Item -Force $tmp $f.FullName
        Write-Host "  已注入 $($f.Name) ($mode): $($added -join ', ')" -ForegroundColor Green
        $changed++
    }
}

Write-Host ""
if ($DryRun) {
    Write-Host "DRY RUN：未改动任何文件。去掉 -DryRun 执行写入。" -ForegroundColor Cyan
} else {
    Write-Host "完成：$changed 个 auth 文件已注入真机标识。" -ForegroundColor Cyan
    Write-Host "注意：auth 文件里存的是明文凭证，注入后请勿外传该目录。" -ForegroundColor Yellow
}
