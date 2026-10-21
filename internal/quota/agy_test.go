//go:build unix

// agy_test.go —— agy CLI 额度适配回归（B453 段 2）。
//
// 职责：用假 agy 可执行文件锁：版本门（< 1.1.11 或无法识别 → 不执行 /usage，调用计数 0）；
// 参数形状 `-p /usage --output-format json --print-timeout 20s`；以账号 home_dir 作 HOME；
// 代理注入子进程环境；超时杀整组（假 CLI 起的孙进程超时后不残留）；未登录/其他非零退出
// 的分类；信封宽容解析（未知字段、前置噪声行、disabled bucket 不计、每模型组 5h/周各成
// 窗口带组名）；指纹取登录身份。
// 边界：信封样本 testdata/agy_usage_200.real.json 是 R2（2026-10-22，agy 1.4.4@linux-01）真机
// /usage 输出：无登录身份字段（指纹留空）、remaining_fraction 有 float 与整数 1 两种。
// disabled bucket、身份字段、未知窗口等形态无真样本，用内联合成用例。真实 agy 不在测试中运行。
package quota

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeAgy 写一个假 agy：--version 打 version；其余调用记参数与环境到 $HOME，再执行 body。
func fakeAgy(t *testing.T, version, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "agy")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then echo '" + version + "'; exit 0; fi\n" +
		"echo \"$@\" >> \"$HOME/calls\"\n" +
		"echo \"HTTPS_PROXY=$HTTPS_PROXY https_proxy=$https_proxy ALL_PROXY=$ALL_PROXY\" >> \"$HOME/env\"\n" +
		body + "\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// usedOf 在运行期算 (1−remaining)×100：常量表达式会按任意精度折叠，与解析器的
// float64 运算差最后一位。
func usedOf(remaining float64) float64 { return clampPercent((1 - remaining) * 100) }

func TestAgyParseUsageTolerant(t *testing.T) {
	sample := readTestdata(t, "agy_usage_200.real.json")
	snap, err := ParseAgyUsage(append([]byte("Loading...\nwarn: something\n"), sample...))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	gw, g5 := mustTime(t, "2026-10-24T13:01:04Z"), mustTime(t, "2026-10-21T20:05:49Z")
	cw, c5 := mustTime(t, "2026-10-28T16:48:16Z"), mustTime(t, "2026-10-21T21:48:16Z")
	assertWindows(t, snap.Windows, []Window{
		{Key: WindowWeekly, Group: "Gemini Models", UsedPercent: usedOf(0.8668959736824036), ResetsAt: &gw},
		{Key: WindowSession, Group: "Gemini Models", UsedPercent: usedOf(0.9410383105278015), ResetsAt: &g5},
		{Key: WindowWeekly, Group: "Claude and GPT models", UsedPercent: 0, ResetsAt: &cw},
		{Key: WindowSession, Group: "Claude and GPT models", UsedPercent: 0, ResetsAt: &c5},
	})
	if id := agyIdentity(sample); id != "" {
		t.Fatalf("真样本无登录身份，应留空，得 %q", id)
	}
	synth := []byte(`{"command":{"data":{"account":{"email":"someone@example.com"},"groups":[{"name":"G","buckets":[` +
		`{"window":"5h","remaining_fraction":0.5},{"window":"5h","remaining_fraction":0,"disabled":true},{"window":"daily","remaining_fraction":0}]}]}}}`)
	if s2, err := ParseAgyUsage(synth); err != nil || len(s2.Windows) != 1 || s2.Windows[0].UsedPercent != 50 {
		t.Fatalf("合成：disabled 与未知窗口应忽略: %+v %v", s2, err)
	}
	if id := agyIdentity(synth); id != "someone@example.com" {
		t.Fatalf("合成：登录身份 = %q", id)
	}
	for name, body := range map[string]string{
		"无 groups": `{"command":{"data":{}}}`,
		"非 JSON":   `hello`,
		"全部禁用":     `{"groups":[{"name":"G","buckets":[{"window":"5h","remaining_fraction":1,"disabled":true}]}]}`,
	} {
		if _, err := ParseAgyUsage([]byte(body)); err == nil {
			t.Fatalf("%s 应解析失败", name)
		}
	}
	_, err = ParseAgyUsage([]byte(`{"error":{"message":"quota service unavailable"}}`))
	var f *Failure
	if !errors.As(err, &f) || f.Category != CategoryUpstream || !strings.Contains(f.Message, "quota service unavailable") {
		t.Fatalf("信封 error 应判 upstream 并带原因: %v", err)
	}
}

func TestAgyProbeRunsUsageWithHomeAndProxy(t *testing.T) {
	samplePath, _ := filepath.Abs(filepath.Join("testdata", "agy_usage_200.real.json"))
	bin := fakeAgy(t, "agy version 1.4.4", "cat '"+samplePath+"'")
	home := t.TempDir()
	a := &Agy{Binary: bin}
	snap, err := a.Probe(context.Background(), Request{Kind: KindAgy, Home: home, Proxy: "socks5://127.0.0.1:7897"})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	calls, _ := os.ReadFile(filepath.Join(home, "calls"))
	if strings.TrimSpace(string(calls)) != "-p /usage --output-format json --print-timeout 20s" {
		t.Fatalf("参数形状 = %q", calls)
	}
	env, _ := os.ReadFile(filepath.Join(home, "env"))
	if !strings.Contains(string(env), "HTTPS_PROXY=socks5://127.0.0.1:7897 https_proxy=socks5://127.0.0.1:7897 ALL_PROXY=socks5://127.0.0.1:7897") {
		t.Fatalf("代理未注入子进程环境: %q", env)
	}
	if len(snap.Windows) != 4 || snap.Fingerprint != "" {
		t.Fatalf("读数/指纹: %+v", snap)
	}
}

func TestAgyVersionGateSkipsUsage(t *testing.T) {
	for _, v := range []string{"agy 1.1.10", "1.0.99", "dev-build"} {
		t.Run(v, func(t *testing.T) {
			bin := fakeAgy(t, v, "echo '{}'")
			home := t.TempDir()
			_, err := (&Agy{Binary: bin}).Probe(context.Background(), Request{Kind: KindAgy, Home: home})
			var f *Failure
			if !errors.As(err, &f) || f.Category != CategoryVersionTooOld || !strings.Contains(f.Message, "1.1.11") {
				t.Fatalf("旧版/无法识别版本应判 version_too_old: %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(home, "calls")); !os.IsNotExist(statErr) {
				t.Fatal("版本门未过却执行了 /usage（会白花额度）")
			}
		})
	}
	bin := fakeAgy(t, "agy 1.1.11", "echo '{\"groups\":[{\"name\":\"G\",\"buckets\":[{\"window\":\"5h\",\"remaining_fraction\":0.5}]}]}'")
	if _, err := (&Agy{Binary: bin}).Probe(context.Background(), Request{Kind: KindAgy, Home: t.TempDir()}); err != nil {
		t.Fatalf("恰好 1.1.11 应放行: %v", err)
	}
}

func TestAgyTimeoutKillsProcessGroup(t *testing.T) {
	bin := fakeAgy(t, "agy 1.4.4", "sleep 30 &\necho $! > \"$HOME/child\"\nwait")
	home := t.TempDir()
	start := time.Now()
	_, err := (&Agy{Binary: bin, Timeout: 500 * time.Millisecond}).Probe(context.Background(), Request{Kind: KindAgy, Home: home})
	var f *Failure
	if !errors.As(err, &f) || f.Category != CategoryTimeout {
		t.Fatalf("超时应判 timeout: %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("超时后未及时返回: %s", time.Since(start))
	}
	raw, rerr := os.ReadFile(filepath.Join(home, "child"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("孙进程 %d 超时后仍存活：进程组未被整体杀掉", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAgyFailureClassification(t *testing.T) {
	_, err := (&Agy{Binary: filepath.Join(t.TempDir(), "no-such-agy")}).Probe(context.Background(), Request{Kind: KindAgy, Home: t.TempDir()})
	var f *Failure
	if !errors.As(err, &f) || f.Category != CategoryCLIMissing {
		t.Fatalf("找不到 agy 应判 cli_missing: %v", err)
	}
	bin := fakeAgy(t, "agy 1.4.4", "echo 'Error: not logged in, please run agy login' >&2; exit 1")
	_, err = (&Agy{Binary: bin}).Probe(context.Background(), Request{Kind: KindAgy, Home: t.TempDir()})
	if !errors.As(err, &f) || f.Category != CategoryCredentialMissing || !strings.Contains(f.Message, "agy login") {
		t.Fatalf("未登录应判 credential_missing 并指路: %v", err)
	}
	bin = fakeAgy(t, "agy 1.4.4", "echo 'boom' >&2; exit 2")
	_, err = (&Agy{Binary: bin}).Probe(context.Background(), Request{Kind: KindAgy, Home: t.TempDir()})
	if !errors.As(err, &f) || f.Category != CategoryUpstream || !strings.Contains(f.Message, "boom") {
		t.Fatalf("其他非零退出应判 upstream 带 stderr 尾部: %v", err)
	}
}
