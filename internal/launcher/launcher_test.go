package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidate 穷举四条规则。它们**就是契约本身**（2026-08-22 需求 B 契约 §2.2），
// 所以这里锁的是规则，不是实现。
func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		list    []Item
		wantErr bool
		// wantIn 是错误文本里必须出现的片段——错误会原样成为 400 的响应体，
		// 「哪一条不合法」必须说得出来，只报「不合法」等于没报
		wantIn string
	}{
		{name: "空列表合法", list: nil},
		{name: "只带 env", list: []Item{{Name: "生产", EnvFile: "prod.env"}}},
		{name: "只带命令", list: []Item{{Name: "跑测试", Command: "go test ./..."}}},
		{name: "两者都带", list: []Item{{Name: "全", EnvFile: "a.env", Command: "ls"}}},
		{
			name: "名字为空", list: []Item{{EnvFile: "a.env"}},
			wantErr: true, wantIn: "名字不能为空",
		},
		{
			name: "名字只有空白", list: []Item{{Name: "   ", Command: "ls"}},
			wantErr: true, wantIn: "名字不能为空",
		},
		{
			name:    "名字重复",
			list:    []Item{{Name: "x", Command: "a"}, {Name: "x", Command: "b"}},
			wantErr: true, wantIn: "重复",
		},
		{
			name: "两者都空", list: []Item{{Name: "空壳"}},
			wantErr: true, wantIn: "至少填一个",
		},
		{
			name: "两者只有空白也算空", list: []Item{{Name: "空壳", EnvFile: "  ", Command: "\t"}},
			wantErr: true, wantIn: "至少填一个",
		},
		{
			name: "env 文件名含分隔符", list: []Item{{Name: "穿越", EnvFile: "../../etc/passwd"}},
			wantErr: true, wantIn: "路径分隔符",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(c.list)
			if c.wantErr {
				if err == nil {
					t.Fatalf("期望被拒，实际通过")
				}
				if !errors.Is(err, ErrInvalid) {
					t.Errorf("错误应包 ErrInvalid，实得 %v", err)
				}
				if c.wantIn != "" && !strings.Contains(err.Error(), c.wantIn) {
					t.Errorf("错误文本应含 %q，实得 %q", c.wantIn, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("期望通过，实得 %v", err)
			}
		})
	}
}

// TestLoadMissingFileIsNotAnError 钉住「文件不存在是正常起点」。
//
// 反面断言配一条正面断言（存了就读得回来），否则这条在 Load 改成
// 「不存在也返回错误」之外的任何走样下照样绿。
func TestLoadMissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("文件不存在应是正常起点，实得错误 %v", err)
	}
	if got != nil {
		t.Errorf("应返回 nil 列表，实得 %+v", got)
	}

	want := []Item{{Name: "生产", EnvFile: "prod.env"}}
	if err := Save(dir, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	back, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(back) != 1 || back[0].Name != "生产" || back[0].EnvFile != "prod.env" {
		t.Errorf("存进去的读不回来：%+v", back)
	}
}

// TestSaveValidatesBeforeWriting 钉住「先校验后落盘」：写坏的配置不该进磁盘。
func TestSaveValidatesBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, []Item{{Name: "空壳"}}); err == nil {
		t.Fatal("两者都空的启动项应被拒")
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(err) {
		t.Error("校验失败时不该留下文件——写坏的配置进了磁盘，症状会拖到下次读取")
	}
}

// TestSaveFilePerm 钉住权限基线：启动项指名了哪份 env 文件，不该松于同目录其余内容。
func TestSaveFilePerm(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, []Item{{Name: "x", Command: "ls"}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	st, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("文件权限应为 0600，实得 %o", perm)
	}
}
