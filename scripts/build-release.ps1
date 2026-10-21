<#
.SYNOPSIS
  绯狐 · 本地发布构建：一次产出 Windows / Linux 可分发压缩包 + 校验和。

.DESCRIPTION
  这是本仓库的「发布线」，替代原项目的 GitHub Actions 矩阵构建。
  不依赖远程 CI、不推送任何镜像仓库，产物全部落在本地 dist/ 目录。

  产物结构（每个平台一个包）：
    feihu-v<version>-<os>-<arch>.zip|tar.gz
      ├─ feihu[.exe]          主服务（网关 + 管理面板）
      ├─ login[.exe]          OAuth 设备授权（加账号）
      ├─ credit[.exe]         积分/额度命令行工具
      ├─ signin[.exe]         签到命令行工具
      ├─ config.example.json  配置样例
      ├─ README.md            文档
      └─ scripts/             运维工具（通道探针、真机身份迁移）

  版本号取自 cmd/server/main.go 的 appVersion，避免两处漂移。

.PARAMETER Version
  覆盖版本号（默认读源码 appVersion，形如 2.0.0-feihu）。

.PARAMETER Targets
  目标平台，默认 windows/amd64、linux/amd64、linux/arm64。

.PARAMETER NoArchive
  只构建二进制，不打包压缩包（调试用）。

.EXAMPLE
  .\scripts\build-release.ps1
  .\scripts\build-release.ps1 -Targets @('windows/amd64')
#>
[CmdletBinding()]
param(
    [string]$Version = "",
    [string[]]$Targets = @('windows/amd64', 'linux/amd64', 'linux/arm64'),
    [switch]$NoArchive,
    # Strip 用 -ldflags "-s -w" 剥离符号表（体积减约 30%）。默认关闭：
    # 实测本机安全软件会在 300ms 内隔离 strip 后的 PE（熵高被启发式判可疑），
    # 产物直接消失且不报错 —— 需要 strip 时先给 dist 目录加杀软排除。
    [switch]$Strip
)

$ErrorActionPreference = 'Stop'

# --- 定位仓库根与 Go ---------------------------------------------------------
$Root = Split-Path -Parent $PSScriptRoot
if (-not (Test-Path (Join-Path $Root 'go.mod'))) {
    throw "未找到 go.mod，脚本应位于 <repo>/scripts/ 下"
}
Set-Location $Root

$GoExe = $null
foreach ($c in @('go', 'G:\_tools\go-sdk\go\bin\go.exe', "$env:LOCALAPPDATA\Programs\Go\bin\go.exe")) {
    $cmd = Get-Command $c -ErrorAction SilentlyContinue
    if ($cmd) { $GoExe = $cmd.Source; break }
    if (Test-Path $c) { $GoExe = $c; break }
}
if (-not $GoExe) { throw "找不到 go 可执行文件：把 Go 加入 PATH 或用已知路径" }
Write-Host "Go: $GoExe" -ForegroundColor Cyan

# --- 版本号 -----------------------------------------------------------------
if (-not $Version) {
    $mainGo = Join-Path $Root 'cmd\server\main.go'
    $m = Select-String -Path $mainGo -Pattern 'appVersion\s*=\s*"([^"]+)"'
    if (-not $m) { throw "无法从 $mainGo 解析 appVersion" }
    $Version = $m.Matches[0].Groups[1].Value
}
Write-Host "版本: $Version" -ForegroundColor Cyan

# 文件名只取语义化部分：appVersion 常带 -feihu 这类后缀，直接用会出现
# feihu-v2.0.0-feihu-windows-amd64.zip 这种品牌重复的包名。
$FileVersion = $Version -replace '-.*$', ''

# --- 命令清单（cmd/* 下每个含 main 的包）-------------------------------------
$Commands = @('server', 'login', 'credit', 'signin')
$BinName = @{
    'server' = 'feihu'
    'login'  = 'login'
    'credit' = 'credit'
    'signin' = 'signin'
}

$DistRoot = Join-Path $Root 'dist'
$StageRoot = Join-Path $DistRoot "_stage"
if (Test-Path $DistRoot) { Remove-Item $DistRoot -Recurse -Force }
New-Item -ItemType Directory -Force -Path $DistRoot | Out-Null

$Summary = @()

foreach ($t in $Targets) {
    $os, $arch = $t.Split('/')
    $tag = "$os-$arch"
    $stage = Join-Path $StageRoot $tag
    New-Item -ItemType Directory -Force -Path $stage | Out-Null
    Write-Host "`n=== 构建 $tag ===" -ForegroundColor Yellow

    $env:GOOS = $os
    $env:GOARCH = $arch
    $env:CGO_ENABLED = '0'
    $ext = if ($os -eq 'windows') { '.exe' } else { '' }

    foreach ($cmd in $Commands) {
        $out = Join-Path $stage ("{0}{1}" -f $BinName[$cmd], $ext)
        $buildArgs = @('build', '-trimpath')
        if ($Strip) { $buildArgs += @('-ldflags', '-s -w') }
        $buildArgs += @('-o', $out, "./cmd/$cmd")
        & $GoExe @buildArgs
        if ($LASTEXITCODE -ne 0) { throw "构建失败: $cmd ($tag)" }
        $size = [math]::Round((Get-Item $out).Length / 1MB, 1)
        Write-Host ("  {0,-14} {1} MB" -f (Split-Path $out -Leaf), $size)
    }

    # 随包文件
    Copy-Item (Join-Path $Root 'config.example.json') $stage
    Copy-Item (Join-Path $Root 'README.md') $stage
    Copy-Item (Join-Path $Root 'scripts') $stage -Recurse
    # 清掉本地残留（__pycache__ 等），不要把开发垃圾打进发布包
    Get-ChildItem (Join-Path $stage 'scripts') -Recurse -Directory -Filter '__pycache__' -ErrorAction SilentlyContinue |
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue

    Write-Host "  [诊断] stage 顶层文件:" -ForegroundColor DarkGray
    Get-ChildItem $stage -File | ForEach-Object { Write-Host ("    {0} {1}" -f $_.Name, $_.Length) -ForegroundColor DarkGray }

    if (-not $NoArchive) {
        $name = "feihu-v$FileVersion-$tag"
        if ($os -eq 'windows') {
            $zip = Join-Path $DistRoot "$name.zip"
            # 不用 Compress-Archive：PowerShell 5.1 的实现对 ~10MB 级文件会静默失败
            # （实测 feihu.exe 10.1MB 被漏掉、也不报错，产出的包缺主程序）。
            # .NET 的 CreateFromDirectory 没有这个限制。
            Add-Type -AssemblyName System.IO.Compression.FileSystem
            [IO.Compression.ZipFile]::CreateFromDirectory(
                $stage, $zip, [IO.Compression.CompressionLevel]::Optimal, $false)
            $Summary += [pscustomobject]@{ 包 = "$name.zip"; MB = [math]::Round((Get-Item $zip).Length / 1MB, 1) }
        } else {
            $tar = Join-Path $DistRoot "$name.tar.gz"
            tar -czf $tar -C $stage .
            if ($LASTEXITCODE -ne 0) { throw "打包失败: $tar" }
            $Summary += [pscustomobject]@{ 包 = "$name.tar.gz"; MB = [math]::Round((Get-Item $tar).Length / 1MB, 1) }
        }
    }
}

# --- 校验和 -----------------------------------------------------------------
$archives = Get-ChildItem $DistRoot -File | Where-Object { $_.Extension -in '.zip', '.gz' }
if ($archives) {
    $sumFile = Join-Path $DistRoot 'SHA256SUMS.txt'
    $lines = foreach ($f in $archives) {
        $h = (Get-FileHash $f.FullName -Algorithm SHA256).Hash.ToLower()
        "$h  $($f.Name)"
    }
    [IO.File]::WriteAllLines($sumFile, $lines, (New-Object System.Text.UTF8Encoding($false)))
    Write-Host "`n校验和: $sumFile" -ForegroundColor Cyan
}

Remove-Item $StageRoot -Recurse -Force -ErrorAction SilentlyContinue

Write-Host "`n=== 发布产物 ($DistRoot) ===" -ForegroundColor Green
Get-ChildItem $DistRoot -File | Select-Object Name, @{n = 'MB'; e = { [math]::Round($_.Length / 1MB, 1) } } | Format-Table -AutoSize

Write-Host "版本 $Version —— 共 $($Targets.Count) 个平台。" -ForegroundColor Green
