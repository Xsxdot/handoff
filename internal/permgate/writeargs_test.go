package permgate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteArgTargets(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("取不到 home，跳过 ~ 展开用例")
	}
	cases := []struct {
		name string
		cmd  string
		want []string
	}{
		{"管道后的 tee", "echo x | tee /tmp/y", []string{"/tmp/y"}},
		{"tee 带追加标志", "echo x | tee -a /tmp/y", []string{"/tmp/y"}},
		{"tee 多个落点", "echo x | tee a.log /tmp/y", []string{"a.log", "/tmp/y"}},
		{"tee 落到家目录", "echo x | tee ~/.zshrc", []string{filepath.Join(home, ".zshrc")}},
		{"cp 取目的地不取源", "cp a.txt /etc/x", []string{"/etc/x"}},
		{"cp 带递归标志", "cp -r src dst", []string{"dst"}},
		{"cp 的 -t 形态", "cp -t /etc a.txt b.txt", []string{"/etc"}},
		{"cp 的长选项 -t 形态", "cp --target-directory=/etc a.txt", []string{"/etc"}},
		{"mv 取目的地", "mv a ~/b", []string{filepath.Join(home, "b")}},
		{"ln 取目的地", "ln -s /etc/passwd ./link", []string{"./link"}},
		{"install 取目的地", "install -m 755 bin /usr/local/bin/x", []string{"/usr/local/bin/x"}},
		{"dd 的 of= 形态", "dd if=/dev/zero of=/tmp/x bs=1", []string{"/tmp/x"}},
		{"落点带引号且含空格", `cp a.txt "/tmp/two words.txt"`, []string{"/tmp/two words.txt"}},
		{"-- 之后不再当标志", "cp -- -weird.txt /tmp/z", []string{"/tmp/z"}},

		// 以下必须摘不出落点——它们是误伤面，摘出来就会平白升级
		{"命令名不是写命令", "ls /usr/bin/tee /bin", nil},
		{"写命令名只出现在引号里", `git commit -m "cp a /etc/x"`, nil},
		{"写命令名只是别的词的一部分", "go test ./internal/steering/...", nil},
		{"只有一个参数的 cp 不算完整命令", "cp a.txt", nil},
		{"纯重定向不归本函数管", "echo x > /tmp/y", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := WriteArgTargets(c.cmd)
			if len(got) != len(c.want) {
				t.Fatalf("WriteArgTargets(%q) = %v，期望 %v", c.cmd, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("第 %d 个落点 = %q，期望 %q（全部：%v）", i, got[i], c.want[i], got)
				}
			}
		})
	}
}

// TestWriteArgTargetsSegmentsIndependently 钉住分段：复合命令里每一段各判各的，
// 前一段是无害命令不影响后一段被摘出落点。
func TestWriteArgTargetsSegmentsIndependently(t *testing.T) {
	got := WriteArgTargets("go build ./... && cp bin /usr/local/bin/x ; echo done")
	if len(got) != 1 || got[0] != "/usr/local/bin/x" {
		t.Fatalf("复合命令的落点 = %v，期望 [/usr/local/bin/x]", got)
	}
}
