// redirect_test.go —— 重定向落点提取的逐形态断言。
//
// 每加一条形态就在这里加一行：漏掉一种写法就是一条静默放行的通道。
package permgate

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRedirectTargets(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("取 home 失败: %v", err)
	}
	cases := []struct {
		name string
		cmd  string
		want []string
	}{
		{"空串", "", nil},
		{"无重定向", "go build ./...", nil},
		{"覆盖写带空格", "echo hi > /tmp/x", []string{"/tmp/x"}},
		{"覆盖写无空格", "echo hi >/tmp/x", []string{"/tmp/x"}},
		{"追加写", "echo hi >> /etc/hosts", []string{"/etc/hosts"}},
		{"追加写无空格", "echo hi>>/etc/hosts", []string{"/etc/hosts"}},
		{"强制覆盖 >|", "echo hi >| /tmp/x", []string{"/tmp/x"}},
		{"带 fd 号", "cmd 2> err.log", []string{"err.log"}},
		{"合并重定向 &>", "cmd &> /tmp/all", []string{"/tmp/all"}},
		{"合并追加 &>>", "cmd &>> /tmp/all", []string{"/tmp/all"}},
		{"相对落点", "echo hi > out.txt", []string{"out.txt"}},
		{"多个落点", "a > b | c > d", []string{"b", "d"}},
		{"落点带引号", `echo x > "/etc/foo bar"`, []string{"/etc/foo bar"}},
		{"落点带单引号", "echo x > '/tmp/y'", []string{"/tmp/y"}},
		{"家目录展开", "echo x >> ~/.zshrc", []string{filepath.Join(home, ".zshrc")}},
		{"裸家目录", "echo x > ~", []string{home}},

		// 以下都不是文件写入，必须一条都不产出
		{"fd 复制 2>&1", "go test ./... 2>&1", nil},
		{"fd 复制 >&2", "echo err >&2", nil},
		{"fd 关闭 >&-", "cmd >&-", nil},
		{"引号内的尖括号", `echo "a > b"`, nil},
		{"单引号内的尖括号", "echo 'a > b'", nil},
		{"字符串里的箭头", `grep "x->y" file.txt`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedirectTargets(c.cmd)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("RedirectTargets(%q) = %v，期望 %v", c.cmd, got, c.want)
			}
		})
	}
}

func TestIsDiscardTarget(t *testing.T) {
	yes := []string{"/dev/null", "/dev/stdout", "/dev/stderr", "/dev/tty", "/dev/fd/3"}
	for _, p := range yes {
		if !IsDiscardTarget(p) {
			t.Fatalf("%s 必须判为丢弃落点——否则 `go test > /dev/null` 每次都升级人工", p)
		}
	}
	no := []string{"/dev/sda", "/tmp/null", "/etc/passwd", "out.txt", ""}
	for _, p := range no {
		if IsDiscardTarget(p) {
			t.Fatalf("%s 不得判为丢弃落点", p)
		}
	}
}
