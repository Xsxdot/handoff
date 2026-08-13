#!/usr/bin/env pwsh
# install.ps1 的单元测试：只测能纯函数化的部分（架构归一、安装目录、校验和）。
#
# 用法：pwsh -File install_test.ps1  /  powershell.exe -File install_test.ps1
# 全通过时静默退出 0；有失败时逐条打印期望/实得并退 1。
#
# 边界：不测下载与安装本身——那需要真实 Release，属真机验证。
#
# 本文件**必须带 UTF-8 BOM**：PowerShell 5.1 无 BOM 时按系统 ANSI 代码页解码，
# 中文 Windows 上会把脚本解成语法错误。`TestInstallTestPs1CarriesUTF8BOM` 守着
# 这条，删掉 BOM 会让测试变红。
#
# **install.ps1 恰恰相反**：它必须无 BOM 且纯 ASCII，因为它主要经
# `irm ... | iex` 消费，而 PS 5.1 不把 U+FEFF 当空白，BOM 会粘进首个 token。
# 两个文件规则相反不是笔误，原委见 install.ps1 头部那段。本文件只从磁盘跑，
# 从不进 iex，所以留 BOM 是安全的。
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$env:HANDOFF_INSTALL_LIB = '1'
. (Join-Path $PSScriptRoot 'install.ps1')

$script:Fails = 0

# Assert-Equal <说明> <期望> <实得>
function Assert-Equal([string]$What, $Expected, $Actual) {
    if ("$Expected" -ne "$Actual") {
        [Console]::Error.WriteLine("FAIL  $What`n      期望 $Expected`n      实得 $Actual")
        $script:Fails++
    }
}

# Assert-Throws <说明> <脚本块>：脚本块必须抛错，否则算失败。
function Assert-Throws([string]$What, [scriptblock]$Block) {
    try {
        & $Block
        [Console]::Error.WriteLine("FAIL  $What`n      期望抛错，实际正常返回")
        $script:Fails++
    } catch {
        # 预期内
    }
}

# 两个受支持的架构都要归一正确
$env:PROCESSOR_ARCHITECTURE = 'AMD64'
Assert-Equal 'AMD64 归一' 'amd64' (Get-HandoffArch)
$env:PROCESSOR_ARCHITECTURE = 'ARM64'
Assert-Equal 'ARM64 归一' 'arm64' (Get-HandoffArch)

# 32 位与其他架构不在矩阵内，必须拒绝而不是装一个跑不起来的包
$env:PROCESSOR_ARCHITECTURE = 'x86'
Assert-Throws 'x86 必须被拒绝' { Get-HandoffArch }

# 安装目录：默认值与环境变量覆盖
$env:HANDOFF_INSTALL_DIR = ''
Assert-Equal '默认安装目录' (Join-Path $env:LOCALAPPDATA 'Programs\handoff') (Get-HandoffInstallDir)
$env:HANDOFF_INSTALL_DIR = 'C:\tmp\hf'
Assert-Equal '安装目录可被环境变量覆盖' 'C:\tmp\hf' (Get-HandoffInstallDir)
$env:HANDOFF_INSTALL_DIR = ''

# 校验和：相符放行、不符拒绝、条目缺失拒绝
$probe = Join-Path ([System.IO.Path]::GetTempPath()) ("hf-probe-" + [Guid]::NewGuid().ToString('N'))
Set-Content -Path $probe -Value 'handoff' -NoNewline
$real = (Get-FileHash -Path $probe -Algorithm SHA256).Hash.ToLower()
Assert-Equal '校验和相符时放行' $true (Test-HandoffChecksum -Path $probe -ChecksumsText "$real  a.zip" -Name 'a.zip')
Assert-Equal 'sha256sum 的 * 前缀也认' $true (Test-HandoffChecksum -Path $probe -ChecksumsText "$real *a.zip" -Name 'a.zip')
Assert-Throws '校验和不符必须拒绝' {
    Test-HandoffChecksum -Path $probe -ChecksumsText ('0' * 64 + '  a.zip') -Name 'a.zip'
}
Assert-Throws '条目缺失必须拒绝' {
    Test-HandoffChecksum -Path $probe -ChecksumsText "$real  b.zip" -Name 'a.zip'
}

# 响应体解码：GitHub 把 checksums.txt 也按 application/octet-stream 发，于是
# Invoke-WebRequest 的 .Content 给的是 **byte[]** 而不是字符串（5.1 与 7 都一样）。
# 拿 byte[] 去 -split 只会得到一个无用对象，条目永远查不到——2026-08-13 真机实测：
# 每一次安装都死在「checksums.txt 里没有 ... 的条目」，而条目其实好端端在那儿。
#
# 这三条是那个缺陷的回归：前两条钉住解码本身，第三条走完整条查表路径，
# 保证 byte[] 形态的 checksums.txt 能被查到。
$sumLine = "$real  a.zip"
Assert-Equal 'byte[] 响应体必须被解成字符串' $sumLine `
    (ConvertTo-HandoffText ([System.Text.Encoding]::UTF8.GetBytes($sumLine)))
Assert-Equal '已是字符串时原样返回' $sumLine (ConvertTo-HandoffText $sumLine)
Assert-Equal 'byte[] 形态的 checksums.txt 仍能查到条目' $true `
    (Test-HandoffChecksum -Path $probe `
        -ChecksumsText (ConvertTo-HandoffText ([System.Text.Encoding]::UTF8.GetBytes($sumLine))) `
        -Name 'a.zip')

Remove-Item -Path $probe -Force -ErrorAction SilentlyContinue

if ($script:Fails -gt 0) {
    [Console]::Error.WriteLine("$($script:Fails) 条失败")
    exit 1
}
exit 0
