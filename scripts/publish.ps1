<#
.SYNOPSIS
  绯狐 · 一键发布到 GitHub：建仓 → 替换 owner 占位 → 推送 → 发 Release 附二进制。

.DESCRIPTION
  前置：本机已 `gh auth login`（或设置 GH_TOKEN 环境变量）。

  它做的事：
    1. 用 gh api 取当前登录账号，把仓库里所有 lsloveppc 占位替换成真实 owner
       （README / LICENSE / CI 注释里的仓库地址）；
    2. 若远程仓库不存在则创建，存在则复用；
    3. 推送当前分支；
    4. 打 tag 并用 dist/ 下的压缩包发 Release（tag 与源码 appVersion 自动对齐）。

  可重复执行：owner 已替换过则跳过替换，仓库已存在则只推进，tag 已存在则跳过发布。

.PARAMETER RepoName
  仓库名，默认 feihu。

.PARAMETER Visibility
  public（默认，供别人下载）或 private。

.PARAMETER SkipRelease
  只推代码，不发 Release。

.PARAMETER DryRun
  只打印将执行的步骤，不改任何东西。

.EXAMPLE
  .\scripts\publish.ps1 -DryRun
  .\scripts\publish.ps1
#>
[CmdletBinding()]
param(
    [string]$RepoName = "feihu",
    [ValidateSet('public', 'private')][string]$Visibility = 'public',
    [string]$Description = "绯狐 · 个人自用的 CodeBuddy/WorkBuddy 多账号网关（OpenAI 兼容 + 管理面板）",
    [switch]$SkipRelease,
    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

function Find-Gh {
    foreach ($c in @('gh', 'G:\_tools\gh\bin\gh.exe')) {
        $cmd = Get-Command $c -ErrorAction SilentlyContinue
        if ($cmd) { return $cmd.Source }
        if (Test-Path $c) { return $c }
    }
    throw "找不到 gh CLI。装一个：winget install GitHub.cli，或设置 GH_TOKEN 后手动执行 git push"
}
$Gh = Find-Gh

# --- 1. 认证与 owner --------------------------------------------------------
$Owner = & $Gh api user --jq '.login' 2>$null
if ($LASTEXITCODE -ne 0 -or -not $Owner) {
    throw "gh 未登录。先运行：& '$Gh' auth login"
}
$Owner = $Owner.Trim()
Write-Host "GitHub 账号: $Owner" -ForegroundColor Cyan
Write-Host "目标仓库: $Owner/$RepoName ($Visibility)" -ForegroundColor Cyan

# --- 2. 替换 owner 占位 -----------------------------------------------------
$Placeholder = 'lsloveppc'
$files = git grep -l $Placeholder 2>$null
if ($files) {
    Write-Host "替换 owner 占位：$($files -join ', ')" -ForegroundColor Yellow
    if (-not $DryRun) {
        $enc = New-Object System.Text.UTF8Encoding($false)
        foreach ($f in $files) {
            $t = [IO.File]::ReadAllText((Resolve-Path $f), [Text.Encoding]::UTF8)
            [IO.File]::WriteAllText((Resolve-Path $f), $t.Replace($Placeholder, $Owner), $enc)
        }
    }
} else {
    Write-Host "owner 占位已替换过，跳过" -ForegroundColor DarkGray
}

# --- 3. 提交替换 ------------------------------------------------------------
if (-not $DryRun -and $files) {
    git add -A
    git -c user.name='feihu' -c user.email='feihu@localhost' commit -q -m "chore: 仓库地址绑定实际账号 ($Owner/$RepoName)" 2>$null
    if ($LASTEXITCODE -ne 0) { Write-Host "（无改动可提交）" -ForegroundColor DarkGray }
}

# --- 4. 建仓 / 复用 --------------------------------------------------------
$exists = & $Gh repo view "$Owner/$RepoName" --json name --jq '.name' 2>$null
if ($LASTEXITCODE -ne 0 -or -not $exists) {
    Write-Host "创建仓库 $Owner/$RepoName ..." -ForegroundColor Yellow
    if (-not $DryRun) {
        & $Gh repo create "$Owner/$RepoName" "--$Visibility" --description $Description --source=. --remote=origin
        if ($LASTEXITCODE -ne 0) { throw "创建仓库失败" }
    }
} else {
    Write-Host "仓库已存在，复用" -ForegroundColor DarkGray
    if (-not $DryRun) {
        $remotes = git remote
        if ($remotes -notcontains 'origin') {
            git remote add origin "https://github.com/$Owner/$RepoName.git"
        } else {
            git remote set-url origin "https://github.com/$Owner/$RepoName.git"
        }
    }
}

# --- 5. 推送 ---------------------------------------------------------------
$Branch = git branch --show-current
if (-not $Branch) { $Branch = 'master' }
Write-Host "推送分支 $Branch ..." -ForegroundColor Yellow
if (-not $DryRun) {
    git push -u origin $Branch
    if ($LASTEXITCODE -ne 0) { throw "推送失败（检查 gh 的 git 凭证：gh auth setup-git）" }
}

# --- 6. Release ------------------------------------------------------------
if (-not $SkipRelease) {
    $m = Select-String -Path (Join-Path $Root 'cmd\server\main.go') -Pattern 'appVersion\s*=\s*"([^"]+)"'
    $AppVersion = $m.Matches[0].Groups[1].Value
    $FileVersion = $AppVersion -replace '-.*$', ''
    $Tag = "v$FileVersion"

    $dist = Join-Path $Root 'dist'
    if (-not (Test-Path $dist)) {
        Write-Host "dist/ 不存在，先跑 .\scripts\build-release.ps1" -ForegroundColor Yellow
        if (-not $DryRun) { & (Join-Path $PSScriptRoot 'build-release.ps1') }
    }
    $assets = Get-ChildItem $dist -File | Where-Object { $_.Extension -in '.zip', '.gz', '.txt' } | Select-Object -ExpandProperty FullName

    $tagExists = & $Gh release view $Tag --json tagName --jq '.tagName' 2>$null
    if ($tagExists) {
        Write-Host "Release $Tag 已存在，跳过（要重发先删：gh release delete $Tag）" -ForegroundColor Yellow
    } else {
        Write-Host "发布 Release $Tag（$($assets.Count) 个附件）..." -ForegroundColor Yellow
        if (-not $DryRun) {
            $notes = Join-Path $Root 'RELEASE_NOTES.md'
            $noteArgs = if (Test-Path $notes) { @('--notes-file', $notes) } else { @('--notes', "绯狐 $AppVersion") }
            & $Gh release create $Tag @assets --title "绯狐 $FileVersion" @noteArgs --target $Branch
            if ($LASTEXITCODE -ne 0) { throw "发布 Release 失败" }
        }
    }
}

Write-Host "`n完成。" -ForegroundColor Green
if (-not $DryRun) {
    Write-Host "仓库: https://github.com/$Owner/$RepoName" -ForegroundColor Green
    Write-Host "下载: https://github.com/$Owner/$RepoName/releases" -ForegroundColor Green
}
