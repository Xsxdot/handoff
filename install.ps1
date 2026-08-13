#!/usr/bin/env pwsh
# handoff 的 Windows 一行安装脚本。
#
# 用法：irm https://handoff.gosuper.dev/install.ps1 | iex
#
# 职责：
#   - 探测架构，从 GitHub Release 拉对应的 zip，校验 sha256，装到 %LOCALAPPDATA%\Programs\handoff
#
# 边界：
#   - 只在「本机还没有 handoff」时用一次；后续换版走 handoff upgrade
#   - **不改 PATH、不写服务、不提权**——与 install.sh 的边界逐条一致
#   - Windows 上 handoff 只能当协调者：agentd 的进程承载层在非 unix 平台
#     尚未实现（backlog B37），派发目标必须是一台 macOS/Linux 执行机
#
# 环境变量：
#   HANDOFF_INSTALL_DIR  覆盖安装目录
#   HANDOFF_INSTALL_LIB  设为 1 时只定义函数不执行主流程（供 install_test.ps1 用）
#
# 兼容性：必须同时在 Windows 自带的 PowerShell 5.1 与 PowerShell 7 上可用。
#
# 本文件**必须带 UTF-8 BOM**（首三字节 EF BB BF），别把它当成编辑器噪音删掉：
# PowerShell 5.1 读 .ps1 时，没有 BOM 就按系统 ANSI 代码页解码，而不是 UTF-8。
# 在中文 Windows（cp936/GBK）上，注释里的中文会被按 GBK 拆成双字节——GBK 的
# 前导字节会把紧跟其后的 ASCII 字符（引号、右括号、换行）一并吞掉，于是整个
# 脚本变成语法错误、一行都跑不了。英文 Windows（cp1252）字节一一对应不吞字符，
# 只是中文显示成乱码，照样能跑——所以 CI 的 windows-latest 验不出这条，
# 只有真机（zh-CN）会炸。BOM 一加，5.1 与 7 都按 UTF-8 解码。
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$Repo = 'Xsxdot/handoff'

# Write-Log 输出到 stderr：stdout 留给可能被管道消费的内容。
function Write-Log([string]$Message) {
    [Console]::Error.WriteLine($Message)
}

# Stop-Install 打印失败原因后抛出。
#
# 每个失败分支都必须经它退出——脚本挂掉时用户能看到的只有这一行，
# 缺上下文的「安装失败」等于让用户去猜网络、权限还是架构。
function Stop-Install([string]$Message) {
    throw "handoff 安装失败：$Message"
}

# Get-HandoffArch 把处理器架构归一成 Release 资产用的 arch 名。
#
# 返回：amd64 或 arm64
#
# 注意：不在矩阵内的架构一律抛错。静默装一个跑不起来的二进制，
# 比当场报错糟得多——症状会推迟到运行时才出现，且看不出根因。
function Get-HandoffArch {
    switch ($env:PROCESSOR_ARCHITECTURE) {
        'AMD64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        default { Stop-Install "不支持的架构 $($env:PROCESSOR_ARCHITECTURE)（仅 AMD64/ARM64）" }
    }
}

# Get-HandoffInstallDir 解析安装目录。
#
# 默认 %LOCALAPPDATA%\Programs\handoff——这是 Windows 上的用户级安装惯例，
# 无需管理员权限。install.sh 用的 ~/.local/bin 在 Windows 上没有工具认。
function Get-HandoffInstallDir {
    if ($env:HANDOFF_INSTALL_DIR) { return $env:HANDOFF_INSTALL_DIR }
    return (Join-Path $env:LOCALAPPDATA 'Programs\handoff')
}

# Get-LatestTag 解析 releases/latest 的重定向，取最新 tag。
#
# 返回：形如 v0.1.0
#
# why（不打 api.github.com）：匿名 API 限流 60 次/小时/IP，安装这条路径
# 不该被限流影响。重定向没有限流。
#
# why（两套取 URL 的写法）：PowerShell 5.1 的 BaseResponse 是 HttpWebResponse
# （用 .ResponseUri），7 上是 HttpResponseMessage（用 .RequestMessage.RequestUri）。
# 只写 7 的写法会让脚本在绝大多数 Windows 机器上第一步就挂。
function Get-LatestTag {
    $url = "https://github.com/$Repo/releases/latest"
    try {
        $resp = Invoke-WebRequest -Uri $url -UseBasicParsing -MaximumRedirection 10
    } catch {
        Stop-Install "取最新版本失败：连不上 github.com（$($_.Exception.Message)）"
    }
    $final = $null
    if ($resp.BaseResponse.PSObject.Properties.Name -contains 'ResponseUri') {
        $final = $resp.BaseResponse.ResponseUri.AbsoluteUri
    } elseif ($resp.BaseResponse.PSObject.Properties.Name -contains 'RequestMessage') {
        $final = $resp.BaseResponse.RequestMessage.RequestUri.AbsoluteUri
    }
    if (-not $final) { Stop-Install '取最新版本失败：无法从响应里取出最终地址' }
    $tag = $final.Split('/')[-1]
    # 仓库一个 release 都没有时，GitHub 重定向到 .../releases，末段不是版本号
    if ($tag -notmatch '^v') { Stop-Install "取最新版本失败：$Repo 还没有任何 release（重定向到 $final）" }
    return $tag
}

# Test-HandoffChecksum 比对文件的 sha256 与 checksums.txt 里的声明。
#
# 参数：
#   - Path: 待校验的文件
#   - ChecksumsText: checksums.txt 全文
#   - Name: 资产的裸文件名
#
# 返回：校验通过返回 $true；条目缺失或不符抛错（不返回 $false——
# 让调用方忘记检查返回值就装上一个坏包，是这里最不能接受的失败模式）
function Test-HandoffChecksum([string]$Path, [string]$ChecksumsText, [string]$Name) {
    $want = $null
    foreach ($line in $ChecksumsText -split "`n") {
        $f = $line.Trim() -split '\s+'
        if ($f.Count -ne 2) { continue }
        # sha256sum 在二进制模式下会给文件名加 * 前缀，一并容忍
        if ($f[1].TrimStart('*') -eq $Name) { $want = $f[0]; break }
    }
    if (-not $want) { Stop-Install "checksums.txt 里没有 $Name 的条目" }
    $got = (Get-FileHash -Path $Path -Algorithm SHA256).Hash.ToLower()
    if ($got -ne $want.ToLower()) {
        Stop-Install "校验失败：期望 $want，实得 $got。不安装，下载物已清理"
    }
    return $true
}

# Write-NextSteps 打印装完之后该做什么。
function Write-NextSteps([string]$Dir) {
    Write-Log ''
    Write-Log '下一步   handoff init'
    Write-Log '         Windows 上 handoff 只能当协调者，init 会带你配对一台远程执行机。'
    Write-Log '         agentd 在 Windows 上跑不起来（backlog B37），本机不能当执行机。'
    if (($env:PATH -split ';') -notcontains $Dir) {
        Write-Log ''
        Write-Log "注意：$Dir 不在 PATH 里。在 PowerShell 里跑下面这行把它加上（只需一次）："
        Write-Log "  [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ';$Dir', 'User')"
        Write-Log '（本脚本不会去改你的 PATH）'
    }
}

# Invoke-Main 是安装主流程。
function Invoke-Main {
    $arch = Get-HandoffArch
    $tag = Get-LatestTag
    $dir = Get-HandoffInstallDir
    $zip = "handoff_${tag}_windows_${arch}.zip"
    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("handoff-install-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp -Force | Out-Null
    try {
        Write-Log "handoff $tag  windows_$arch"
        $zipPath = Join-Path $tmp $zip
        Invoke-WebRequest -Uri "https://github.com/$Repo/releases/download/$tag/$zip" -OutFile $zipPath -UseBasicParsing
        $sums = (Invoke-WebRequest -Uri "https://github.com/$Repo/releases/download/$tag/checksums.txt" -UseBasicParsing).Content
        Test-HandoffChecksum -Path $zipPath -ChecksumsText $sums -Name $zip | Out-Null

        Expand-Archive -Path $zipPath -DestinationPath $tmp -Force
        $exe = Join-Path $tmp 'handoff.exe'
        if (-not (Test-Path $exe)) { Stop-Install "包内没有 handoff.exe" }

        New-Item -ItemType Directory -Path $dir -Force | Out-Null
        $dest = Join-Path $dir 'handoff.exe'
        Copy-Item -Path $exe -Destination $dest -Force
        Write-Log "已安装 $dest  $tag"

        # 顺手把 skill 装给本机各家 agent。**必须调刚装好的那个文件**——
        # skill 内嵌在二进制里，调旧的就装旧的。
        # 失败不算安装失败：二进制已经装好了，skill 少一份不影响 CLI 可用。
        try {
            & $dest skill install
        } catch {
            Write-Log "注意：skill 安装失败，可稍后手动跑 `"$dest`" skill install"
        }

        Write-NextSteps -Dir $dir
    } finally {
        Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

# 被 install_test.ps1 dot-source 时只定义函数，不执行主流程
if ($env:HANDOFF_INSTALL_LIB -ne '1') { Invoke-Main }
