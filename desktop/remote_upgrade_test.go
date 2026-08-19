//go:build !windows

// 本文件覆盖桌面端命令流的逐行传输、stderr 合流与退出码语义。
//
// Windows 不使用 sh -c：这里验的是 streamCommand 的平台无关语义，Windows
// 侧由 Task 12 的真机走查覆盖。
package main

import (
	"context"
	"os/exec"
	"slices"
	"testing"
	"time"
)

// TestStreamCommandDeliversEachLine 钉住输出是**逐行流式**送出的，不是攒到最后。
//
// 为什么承重：handoff upgrade --now 会逐台机器处理，一台可能要几十秒。攒到
// 最后再显示，用户会以为面板卡死了——而这正是他最想看到进度的时刻。
func TestStreamCommandDeliversEachLine(t *testing.T) {
	var got []string
	started := time.Now()
	var firstLineAt time.Duration
	code, err := streamCommand(context.Background(),
		exec.Command("sh", "-c", "echo a; sleep 1; echo b; echo c"),
		func(line string) {
			got = append(got, line)
			if line == "a" {
				firstLineAt = time.Since(started)
			}
		})
	if err != nil {
		t.Fatalf("streamCommand 出错：%v", err)
	}
	if code != 0 {
		t.Errorf("退出码 = %d，想要 0", code)
	}
	want := []string{"a", "b", "c"}
	if !slices.Equal(got, want) {
		t.Errorf("收到 %v，想要 %v", got, want)
	}
	if firstLineAt >= 900*time.Millisecond {
		t.Errorf("第一行直到命令结束后才到达，耗时 %s；想要流式回调", firstLineAt)
	}
}

// TestStreamCommandReportsNonZeroExit 钉住非零退出码被如实带出来。
//
// 面板靠它决定亮不亮「带 --force 重试」——判据是退出码，不是解析输出文本
// （spec §7.2：解析中文表格是脆的，失效方式还是静默的）。
func TestStreamCommandReportsNonZeroExit(t *testing.T) {
	var got []string
	code, err := streamCommand(context.Background(),
		exec.Command("sh", "-c", "echo 失败了; exit 3"),
		func(line string) { got = append(got, line) })
	if err != nil {
		t.Fatalf("非零退出不该作为 error 返回（那样调用方分不清「跑不起来」和「跑了但失败」）：%v", err)
	}
	if code != 3 {
		t.Errorf("退出码 = %d，想要 3", code)
	}
	if !slices.Contains(got, "失败了") {
		t.Errorf("非零退出时输出也必须送到：%v", got)
	}
}

// TestStreamCommandMergesStderr 钉住 stderr 也进面板。
//
// handoff 的更新提示、警告都走 stderr（cmd/root.go 的 maybeNotifyUpdate 就是
// 一例）。只收 stdout 会让面板漏掉恰恰最需要看见的那几行。
func TestStreamCommandMergesStderr(t *testing.T) {
	var got []string
	if _, err := streamCommand(context.Background(),
		exec.Command("sh", "-c", "echo out; echo err 1>&2"),
		func(line string) { got = append(got, line) }); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "err") {
		t.Errorf("stderr 没被收进来：%v", got)
	}
}

// TestStreamCommandFailsWhenBinaryMissing 钉住「跑不起来」返回 error。
func TestStreamCommandFailsWhenBinaryMissing(t *testing.T) {
	_, err := streamCommand(context.Background(),
		exec.Command("这个命令肯定不存在-handoff-test"), func(string) {})
	if err == nil {
		t.Fatal("命令不存在时必须返回 error")
	}
}
