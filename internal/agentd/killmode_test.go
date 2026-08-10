package agentd

import (
	"errors"
	"testing"
)

func TestKillModeDetectsUnsafeUnit(t *testing.T) {
	readFile := func(string) ([]byte, error) {
		return []byte("0::/system.slice/handoff-agentd.service\n"), nil
	}
	lookup := func(unit string) (string, error) {
		if unit != "handoff-agentd.service" {
			t.Fatalf("unit 解析错误: %q", unit)
		}
		return "control-group", nil
	}
	unit, mode, ok := killModeFromCgroup(readFile, lookup)
	if !ok {
		t.Fatal("systemd 托管场景必须识别出来")
	}
	if unit != "handoff-agentd.service" || mode != "control-group" {
		t.Fatalf("unit=%q mode=%q", unit, mode)
	}
}

func TestKillModeSilentWhenNotUnderSystemd(t *testing.T) {
	// 非 systemd（macOS、docker、直接 shell 起）：cgroup 文件不存在或不含 .service
	readFile := func(string) ([]byte, error) { return nil, errors.New("no such file") }
	if _, _, ok := killModeFromCgroup(readFile, nil); ok {
		t.Fatal("非 systemd 场景不应报告 unit——误报会让 macOS 用户每次启动都看到无关警告")
	}
	readFile2 := func(string) ([]byte, error) { return []byte("0::/user.slice/session-3.scope\n"), nil }
	if _, _, ok := killModeFromCgroup(readFile2, nil); ok {
		t.Fatal("cgroup 路径不含 .service 时不应报告 unit")
	}
}

func TestKillModeSafeWhenProcess(t *testing.T) {
	readFile := func(string) ([]byte, error) {
		return []byte("0::/system.slice/handoff-agentd.service\n"), nil
	}
	lookup := func(string) (string, error) { return "process", nil }
	_, mode, ok := killModeFromCgroup(readFile, lookup)
	if !ok || mode != "process" {
		t.Fatalf("ok=%v mode=%q", ok, mode)
	}
}
