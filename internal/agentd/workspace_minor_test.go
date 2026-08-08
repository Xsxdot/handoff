// workspace_minor_test.go —— 第二轮审查第七节 Minor 的回归测试（审阅路由侧）。
//
// 职责：覆盖 ReadFile 对特殊文件与超限截断的处理——两者都是「executor 能影响、
// 审核者会被误导」的路径。
//
// 边界：不触真实工作区，全部在 t.TempDir() 里造仓库。
package agentd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadFileTruncationIsMarked 验证超过上限的文件在返回内容里带显式截断标记。
//
// 无标记的截断会让审核者把「第 1MiB 处」当成文件末尾去推理——它看到的最后
// 一行既不是真正的末行，也没有任何提示说明后面还有内容。
func TestReadFileTruncationIsMarked(t *testing.T) {
	repo := t.TempDir()
	big := strings.Repeat("a", maxRunOutput+1024)
	if err := os.WriteFile(filepath.Join(repo, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatalf("写大文件: %v", err)
	}

	got, err := ReadFile(repo, "big.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if !strings.Contains(got, "已截断") {
		t.Errorf("超限文件未带截断标记，审核者会把截断处当文件末尾（长度 %d）", len(got))
	}
	if !strings.HasPrefix(got, strings.Repeat("a", 1024)) {
		t.Error("截断标记不应污染文件正文开头")
	}
}
