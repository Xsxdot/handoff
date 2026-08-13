#!/usr/bin/env pwsh
# install.ps1 的单元测试：只测能纯函数化的部分（架构归一、安装目录、校验和）。
#
# 用法：pwsh -File install_test.ps1  /  powershell.exe -File install_test.ps1
# 全通过时静默退出 0；有失败时逐条打印期望/实得并退 1。
#
# 边界：不测下载与安装本身——那需要真实 Release，属真机验证。
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
Remove-Item -Path $probe -Force -ErrorAction SilentlyContinue

if ($script:Fails -gt 0) {
    [Console]::Error.WriteLine("$($script:Fails) 条失败")
    exit 1
}
exit 0
