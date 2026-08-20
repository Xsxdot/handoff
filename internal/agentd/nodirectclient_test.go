// 本文件是一道机械守卫：internal/agentd 内不许再出现 client.New( 直连构造。
//
// 为什么需要它：relay 机器在控制台一律显示「已断开」这个 bug，**不会让任何
// 既有测试变红**——直连机器一切正常。下一个人新增第七处扇出时，照样可能顺手
// 写 client.New(t.Addr, t.Token)，而且照样一路绿灯合进去。
//
// 边界：这是字符串扫描，不是类型检查。它只回答「有没有人绕过池」，不回答
// 「走池的用法对不对」——后者由各调用点自己的用例负责。
package agentd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allowDirectClientNew 是白名单：这些文件允许出现 client.New(。
//
// 目前为空——agentd 包内没有任何一处该直连构造。加白名单前先问一句：
// 这个调用点为什么不能走池？
var allowDirectClientNew = map[string]bool{}

// TestNoDirectClientNewInAgentd 扫描本包源码，发现直连构造即失败。
func TestNoDirectClientNewInAgentd(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取包目录失败: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") || allowDirectClientNew[name] {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", name, err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if hasDirectClientNew(line) {
				t.Errorf("%s:%d 直连构造了 agentd 客户端：%s\n"+
					"agentd 侧一律走 s.pool.For(name)（internal/targetclient）。"+
					"直连构造对 relay 形态的机器恒失败——它们没有 addr，"+
					"client.New(\"\") 会退化成一个没有 Host 的 URL。",
					name, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// hasDirectClientNew 区分 client.New( 与 targetclient.New(。
//
// 只用 strings.Contains 会把后者的后缀误报成前者；按标识符边界扫描仍保持
// 这道守卫的简单源码检查性质，同时避免让工厂本身成为自己的违规项。
func hasDirectClientNew(line string) bool {
	const needle = "client.New("
	for start := 0; start < len(line); {
		i := strings.Index(line[start:], needle)
		if i < 0 {
			return false
		}
		i += start
		if i == 0 || !isIdentifierByte(line[i-1]) {
			return true
		}
		start = i + len(needle)
	}
	return false
}

func isIdentifierByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}
