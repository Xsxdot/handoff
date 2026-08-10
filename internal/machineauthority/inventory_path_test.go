// machineauthority 用户路径展开测试。
//
// 职责：
//   - 锁定默认 clone path `~/.handoff/<repo>` 在 owner 机器展开为真实 home
//   - 拒绝无法安全解释的 `~other-user`，避免把波浪号当普通相对目录
//
// 边界：
//   - 只测纯路径函数，不修改进程 HOME，也不执行 clone
package machineauthority

import "testing"

func TestExpandOwnerPath(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		home    string
		want    string
		wantErr bool
	}{
		{name: "default clone path", path: "~/.handoff/repo", home: "/Users/dev", want: "/Users/dev/.handoff/repo"},
		{name: "home itself", path: "~", home: "/Users/dev", want: "/Users/dev"},
		{name: "absolute unchanged", path: "/srv/repo", home: "/Users/dev", want: "/srv/repo"},
		{name: "other user rejected", path: "~root/repo", home: "/Users/dev", wantErr: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := expandOwnerPath(test.path, test.home)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("expandOwnerPath(%q) = %q, %v; want %q", test.path, got, err, test.want)
			}
		})
	}
}
