// b439_s6_test.go —— B439 S6：本机旧别名（local/本机/hostname）判据与拒收文案。
//
// 对象是承重规则：有自名时旧别名可被拒并点名改成 name:；无自名时没有可改成的
// 名字，判据放行（S5）。config 是自名判据的唯一来源（F2）。
package config

import (
	"os"
	"strings"
	"testing"
)

func TestB439IsLegacyLocalAlias(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("取 hostname: %v", err)
	}
	for _, alias := range []string{"local", "本机", host, " 本机 "} {
		if !IsLegacyLocalAlias(alias) {
			t.Fatalf("%q 应判为本机旧别名", alias)
		}
	}
	for _, notAlias := range []string{"", "linux-01", "mac-02", " "} {
		if IsLegacyLocalAlias(notAlias) {
			t.Fatalf("%q 不应判为本机旧别名", notAlias)
		}
	}
}

func TestB439LegacyLocalAliasError(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("取 hostname: %v", err)
	}
	named := &Config{Name: "linux-01"}
	unnamed := &Config{}
	cases := []struct {
		name    string
		cfg     *Config
		target  string
		wantErr bool
	}{
		{"有自名 local 拒", named, "local", true},
		{"有自名 本机 拒", named, "本机", true},
		{"有自名 hostname 拒", named, host, true},
		{"有自名 省略放行", named, "", false},
		{"有自名 自名放行", named, "linux-01", false},
		{"有自名 他名放行", named, "mac-02", false},
		{"无自名 local 放行(S5)", unnamed, "local", false},
		{"无自名 hostname 放行(S5)", unnamed, host, false},
		{"nil 配置放行", nil, "local", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotErr := c.cfg.LegacyLocalAliasError(c.target)
			if c.wantErr != (gotErr != nil) {
				t.Fatalf("LegacyLocalAliasError(%q) err=%v，wantErr=%v", c.target, gotErr, c.wantErr)
			}
			if c.wantErr && !strings.Contains(gotErr.Error(), "linux-01") {
				t.Fatalf("拒收文案应点名改成 %q，实得: %v", "linux-01", gotErr)
			}
		})
	}
}
