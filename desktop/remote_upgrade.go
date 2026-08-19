// 本文件负责「从桌面端升级执行机」（spec D5）。
//
// 职责：
//   - streamCommand 跑一条命令并把 stdout+stderr 逐行回调出去
//   - runRemoteUpgrade 用它跑 handoff upgrade --now 并把输出流进升级面板
//
// 边界（承重）：
//   - **不重建 upgrade 的编排。** 七种结论、两道闸、部分失败不中断、逐行报告
//     全在 CLI 里（cmd/upgrade.go）。这里只负责起进程和显示，多写一行判断
//     逻辑就是在造第二套会与 CLI 分叉的实现
//   - **不解析输出内容。** 是不是闸一导致的失败交给用户自己看——输出是给人看
//     的中文表格，解析输出会在格式一改时静默失效（spec §7.2）
//   - 不调起真实终端：GUI 进程的 PATH 与登录 shell 不同（B71 同源教训），
//     Windows 上还要回答「哪个终端」，且失败时用户被丢在 shell 里没有指引
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

// remoteUpgradeTimeout 是整条升级的上限。
//
// 10 分钟：upgrade --now 要逐台机器下载并推送资产，机器多、网络慢时确实要
// 这么久。设短了会在最后一台上被砍断，而那台的状态是「推了一半」。
const remoteUpgradeTimeout = 10 * time.Minute

// streamCommand 跑一条命令，把 stdout 与 stderr 合并后逐行送给 onLine。
//
// 返回：
//   - 退出码。**命令跑起来了但退出非零时 error 为 nil** —— 调用方必须能分清
//     「跑不起来」（error 非 nil）和「跑了但失败」（error 为 nil、code 非 0），
//     这两种的处置完全不同
//   - error 只在「起不来」「读不了输出」这类情况下非 nil
//
// 注意：
//   - stderr 必须合并进来。handoff 的警告与更新提示都走 stderr，只收 stdout
//     会漏掉恰恰最该看见的那几行
//   - 逐行送出而不是攒到最后：单台机器可能要几十秒，攒着显示会让用户以为卡死
func streamCommand(ctx context.Context, c *exec.Cmd, onLine func(string)) (int, error) {
	// 薄壳是 GUI 进程：不压这一下，Windows 上每跑一条命令都会闪黑窗口
	hideConsole(c)
	pipe, err := c.StdoutPipe()
	if err != nil {
		return -1, fmt.Errorf("接管 stdout: %w", err)
	}
	c.Stderr = c.Stdout
	if err := c.Start(); err != nil {
		return -1, fmt.Errorf("启动 %s: %w", c.Path, err)
	}
	sc := bufio.NewScanner(pipe)
	// upgrade 的表格行不长，但错误原文可能很长（含远端 fetch 的 stderr 原样回显）
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		onLine(sc.Text())
	}
	scanErr := sc.Err()
	waitErr := c.Wait()
	if scanErr != nil {
		return -1, fmt.Errorf("读取输出: %w", scanErr)
	}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		// 跑了但失败：不是 error，退出码才是结论
		return ee.ExitCode(), nil
	}
	if waitErr != nil {
		return -1, fmt.Errorf("等待命令结束: %w", waitErr)
	}
	return 0, nil
}

// runRemoteUpgrade 跑 handoff upgrade --now 并把输出流进升级面板。
//
// 参数：
//   - force: 带上 --force 越过闸一（活跃任务）。**不越过闸二**（非托管），
//     那是 agentd 侧的硬拒绝
//
// 注意：exec 的是 ResolveBinPath 解出来的那份——也就是刚刚被同步过的、
// 版本已知的那一份，不是 PATH 上碰运气找到的。
func runRemoteUpgrade(force bool) {
	p := openUpgradePanel(trayApp)
	p.State("running", "正在升级所有机器")
	p.OnForceRetry(func() { runRemoteUpgrade(true) })

	bin, err := shell.ResolveBinPath("")
	if err != nil {
		logger.Error("定位 handoff 失败，无法升级执行机", "cause", err)
		p.State("fail", "定位 handoff 失败")
		p.Line(err.Error())
		return
	}
	args := []string{"upgrade", "--now"}
	if force {
		args = append(args, "--force")
	}
	logger.Info("开始升级执行机", "bin", bin, "args", args)
	p.Line("$ " + bin + " " + strings.Join(args, " "))

	ctx, cancel := context.WithTimeout(context.Background(), remoteUpgradeTimeout)
	defer cancel()
	code, err := streamCommand(ctx, exec.CommandContext(ctx, bin, args...), p.Line)
	switch {
	case err != nil:
		logger.Error("升级执行机：命令起不来", "bin", bin, "cause", err)
		p.State("fail", "命令无法启动")
		p.Line(err.Error())
	case code != 0:
		logger.Warn("升级执行机：有机器没升成", "exit_code", code)
		p.State("fail", fmt.Sprintf("有机器没升成（退出码 %d）", code))
	default:
		logger.Info("升级执行机完成", "force", force)
		p.State("ok", "所有机器已是最新")
	}
}
