// config 包测试：验证默认值生成、token 落盘持久化、targets 解析与严格键校验。
package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/config"
)

func TestLoadGeneratesDefaultsAndToken(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:7777" {
		t.Fatalf("listen=%s", cfg.Listen)
	}
	if len(cfg.Token) < 16 {
		t.Fatalf("token 未生成: %q", cfg.Token)
	}
	// 二次加载读回同一 token（说明已落盘）
	cfg2, err := config.Load(p)
	if err != nil || cfg2.Token != cfg.Token {
		t.Fatalf("token 未持久化")
	}
}

// TestLoadParsesTargets 验证合法配置（已知键）正常解析：token 与 targets 表。
// 此 fixture 全部为已知键——严格解析（L-1）下必须保持可加载，是回归基线。
func TestLoadParsesTargets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("token: abc123abc123abc1\ntargets:\n  devbox:\n    addr: \"100.1.2.3:7777\"\n    token: \"tk\"\n"), 0o600)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Targets["devbox"].Addr != "100.1.2.3:7777" {
		t.Fatalf("targets 解析失败")
	}
}

// TestLoadRejectsUnknownKeys 覆盖 L-1 严格解析：未知顶层键（旧版配置的
// access_key 等已废弃标量）必须报错，且错误信息带未知键名与迁移提示——
// 旧实现 yaml.Unmarshal 静默忽略未知键，鉴权等行为会在用户不知情时静默改变
// （安全静默降级）。
func TestLoadRejectsUnknownKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("token: abc123abc123abc1\naccess_key: \"AKIA...\"\n"), 0o600)
	_, err := config.Load(p)
	if err == nil {
		t.Fatal("未知键配置应报错，实际成功——严格解析未生效")
	}
	if !strings.Contains(err.Error(), "access_key") {
		t.Fatalf("错误信息应含未知键名 access_key, got %v", err)
	}
	if !strings.Contains(err.Error(), "请删除未知键或升级配置") {
		t.Fatalf("错误信息应含迁移提示, got %v", err)
	}
}

// TestLoadRejectsUnknownTargetKeys 验证 KnownFields 对 targets map value
// （Target 结构）同样生效：目标条目内的未知键同样报错。
func TestLoadRejectsUnknownTargetKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("token: abc123abc123abc1\ntargets:\n  devbox:\n    addr: \"1.2.3.4:5\"\n    secret_key: \"x\"\n"), 0o600)
	_, err := config.Load(p)
	if err == nil {
		t.Fatal("target 条目内未知键应报错，实际成功")
	}
	if !strings.Contains(err.Error(), "secret_key") {
		t.Fatalf("错误信息应含未知键名 secret_key, got %v", err)
	}
}

// TestLoadEmptyFileKeepsDefaults 验证空配置文件按「无内容」处理：
// 保持默认值且不报错（与 yaml.Unmarshal 对空输入的 no-op 语义一致），
// 不能把空文件的 io.EOF 误当解析错误。
func TestLoadEmptyFileKeepsDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, nil, 0o600)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("空文件应正常加载, got %v", err)
	}
	if cfg.Listen != "127.0.0.1:7777" {
		t.Fatalf("listen=%s, want 默认 127.0.0.1:7777", cfg.Listen)
	}
}
