package config

// b452_artifacts_test.go —— B452 artifacts.retention 三态解析（缺省 168h、显式 0 = 不清、正值原样）。
import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestB452ArtifactsRetentionParse(t *testing.T) {
	cases := map[string]time.Duration{
		"":                               DefaultArtifactsRetention,
		"artifacts:\n  retention: 2m\n":  2 * time.Minute,
		"artifacts:\n  retention: 0s\n":  0,
		"artifacts:\n  retention: -1h\n": 0,
	}
	for body, want := range cases {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte("token: x\n"+body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load(%q): %v", body, err)
		}
		if got := cfg.ArtifactsRetention(); got != want {
			t.Fatalf("%q: retention=%s want %s", body, got, want)
		}
	}
	// 未配置时 Save 不得把 artifacts 段写盘（旧版 agentd 严格解析会被新键顶死）
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "artifacts") {
		t.Fatalf("未配置的 artifacts 段不应落盘:\n%s", raw)
	}
}
