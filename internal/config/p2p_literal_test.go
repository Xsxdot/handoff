package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

// C8：缺省 STUN 地址只在 config 定义——传输层与组装点的生产源码不得写死任何一条。
func TestNoSTUNLiteralOutsideConfig(t *testing.T) {
	var hosts []string
	for _, u := range config.DefaultP2PSTUN() {
		hostport := strings.TrimPrefix(u, "stun:")
		hosts = append(hosts, hostport[:strings.LastIndex(hostport, ":")])
	}
	for _, dir := range []string{"../p2p", "../relay", "../targetclient", "../mobilecore", "../../cmd"} {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, h := range hosts {
				if strings.Contains(string(b), h) {
					t.Errorf("%s 写死了 STUN 地址 %s（缺省值只许在 internal/config/p2p.go）", path, h)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
