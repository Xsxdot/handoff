// no_direct_httptest_test.go —— 防止 agentd 测试绕过 B234 fixture helper。
// 职责：扫描 internal/agentd 的 Go 测试源码，禁止直接构造 NewServer/NewUnstartedServer。
// 边界：只审源码迁移纪律，不替代真实 HTTP/WS/client.New 拨号接缝测试。
package agentd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoDirectHttptestServers(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(body)
		for _, forbidden := range []string{"httptest.New" + "Server(", "httptest.NewUnstarted" + "Server("} {
			if index := strings.Index(text, forbidden); index >= 0 {
				line := 1 + strings.Count(text[:index], "\n")
				return fmt.Errorf("%s:%d 仍直接调用 %s，请改用 internal/testhttp", path, line, forbidden)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
