// selfname_test.go —— B439 自名判据的词法与解析测试（先红后绿的对象是
// 承重规则：保留字/空白/斜杠、省略=自名、无自名回退地址判据）。
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSelfName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"空串=未配置，合法", "", false},
		{"普通名合法", "linux-01", false},
		{"含连字符合法", "macbook-pro", false},
		{"local 保留字", "local", true},
		{"本机 保留字", "本机", true},
		{"含空白", "linux 01", true},
		{"含制表符", "linux\t01", true},
		{"含斜杠", "team/linux-01", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateSelfName(c.input)
			if c.wantErr && err == nil {
				t.Fatalf("ValidateSelfName(%q) 应报错", c.input)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("ValidateSelfName(%q) 不应报错: %v", c.input, err)
			}
		})
	}
}

// TestConfigResolveTarget 锁住身份值/本机判据：有自名走 name 判（省略或 ==
// 自名 → 本机，身份值=自名）；无自名回退地址判据（本机身份值=空串，S5）。
func TestConfigResolveTarget(t *testing.T) {
	named := &Config{
		Name: "linux-01", Listen: "127.0.0.1:7777",
		Targets: map[string]Target{"macbook-pro": {Addr: "100.0.0.2:7777"}},
	}
	unnamed := &Config{
		Listen: "127.0.0.1:7777",
		Targets: map[string]Target{
			"self-box":    {Addr: "127.0.0.1:7777"},
			"macbook-pro": {Addr: "100.0.0.2:7777"},
		},
	}
	cases := []struct {
		name         string
		cfg          *Config
		target       string
		wantIdentity string
		wantLocal    bool
	}{
		{"有自名：省略 → 自名", named, "", "linux-01", true},
		{"有自名：==自名 → 本机", named, "linux-01", "linux-01", true},
		{"有自名：远端名保留", named, "macbook-pro", "macbook-pro", false},
		{"有自名：旧别名不是本机", named, "local", "local", false},
		{"无自名：省略 → 本机空身份", unnamed, "", "", true},
		{"无自名：地址自指 → 本机空身份", unnamed, "self-box", "", true},
		{"无自名：远端名保留", unnamed, "macbook-pro", "macbook-pro", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			identity, isLocal := c.cfg.ResolveTarget(c.target)
			if identity != c.wantIdentity || isLocal != c.wantLocal {
				t.Fatalf("ResolveTarget(%q) = (%q, %v)，期望 (%q, %v)",
					c.target, identity, isLocal, c.wantIdentity, c.wantLocal)
			}
		})
	}
}

func TestConfigIsSelfName(t *testing.T) {
	named := &Config{Name: "linux-01"}
	unnamed := &Config{}
	if !named.IsSelfName("") || !named.IsSelfName("linux-01") || named.IsSelfName("macbook-pro") {
		t.Fatal("有自名时：省略/自名 为本机，他名不是")
	}
	if unnamed.IsSelfName("") || unnamed.IsSelfName("linux-01") {
		t.Fatal("无自名时没有名字判据，恒为 false（回退地址判据）")
	}
}

// TestLoadParsesNameAndAdvertise 钉住新顶层键解析与 omitempty 落盘（旧版
// agentd 读到未知键即启动失败，空值不得写盘）。
func TestLoadParsesNameAndAdvertise(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	body := "name: linux-01\nadvertise: 100.81.114.119:7777\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "linux-01" || cfg.Advertise != "100.81.114.119:7777" {
		t.Fatalf("name/advertise 未解析: %+v", cfg)
	}
}

func TestLoadRejectsReservedSelfName(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("name: local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("name: local 必须被 Load 拒绝")
	}
}

// 未配置 name/advertise 时 Save 不得落这两个键（omitempty 硬要求）。
func TestSelfNameOmitEmptyOnSave(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	cfg := Defaults()
	cfg.Listen = "127.0.0.1:7777"
	if err := Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "name:") || strings.Contains(string(b), "advertise:") {
		t.Fatalf("未配置 name/advertise 不得落盘:\n%s", b)
	}
}
