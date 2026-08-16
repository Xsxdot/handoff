package shell

import (
	"os"
	"path/filepath"
	"testing"
)

// 全新机器：配置文件根本不存在 → 必须判为未配置。
// 这条是本文件最重要的一条：config.Load 此时返回默认配置且 err==nil，
// 照着 err 判断会把新机器误判成「已配置」。
func TestResolveTreatsMissingFileAsUnconfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	_, state, err := Resolve(path)
	if err != nil {
		t.Fatalf("Resolve 不该报错（文件不存在是正常情况）: %v", err)
	}
	if state != StateUnconfigured {
		t.Fatalf("state = %v, want StateUnconfigured", state)
	}
}

// 文件在、但 token 是空的（例如手工建了个空壳配置）→ 同样算未配置。
func TestResolveTreatsEmptyTokenAsUnconfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("listen: 127.0.0.1:7777\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, state, err := Resolve(path)
	if err != nil {
		t.Fatalf("Resolve 报错: %v", err)
	}
	if state != StateUnconfigured {
		t.Fatalf("state = %v, want StateUnconfigured（token 为空）", state)
	}
}

func TestResolveReturnsEndpointWhenConfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "listen: 127.0.0.1:9999\ntoken: abc123\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ep, state, err := Resolve(path)
	if err != nil {
		t.Fatalf("Resolve 报错: %v", err)
	}
	if state != StateConfigured {
		t.Fatalf("state = %v, want StateConfigured", state)
	}
	if ep.Addr != "127.0.0.1:9999" || ep.Token != "abc123" {
		t.Fatalf("endpoint = %+v, want {127.0.0.1:9999 abc123}", ep)
	}
}
