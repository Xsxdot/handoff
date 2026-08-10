// 本地工作区完整性校验的测试：porcelain 分类、文件名排版、校验入口。
package cmd

import (
	"reflect"
	"testing"
)

// TestClassifyLocalDirty 穷举 git status --porcelain 的行形态。
//
// 判别力所在：「已暂存改动」「重命名」「冲突」三行——把它们错分成未跟踪
// （或整行丢弃）的实现会在这里翻红，而只测「工作区改动 + 未跟踪」的用例
// 对那种实现照样绿。
func TestClassifyLocalDirty(t *testing.T) {
	cases := []struct {
		name          string
		porcelain     string
		wantTracked   []string
		wantUntracked []string
	}{
		{"干净", "", nil, nil},
		{"只有未跟踪", "?? scratch.md\n?? tmp.log\n", nil, []string{"scratch.md", "tmp.log"}},
		{"工作区改动", " M cmd/dispatch.go\n", []string{"cmd/dispatch.go"}, nil},
		{"已暂存改动", "M  cmd/dispatch.go\n", []string{"cmd/dispatch.go"}, nil},
		{"新增已暂存", "A  cmd/new.go\n", []string{"cmd/new.go"}, nil},
		{"删除", " D README.md\n", []string{"README.md"}, nil},
		{"重命名取新名", "R  old.go -> new.go\n", []string{"new.go"}, nil},
		{"冲突", "UU merge.go\n", []string{"merge.go"}, nil},
		{"混合", " M a.go\n?? b.txt\n", []string{"a.go"}, []string{"b.txt"}},
		{"含空格文件名保留引号", " M \"a b.go\"\n", []string{`"a b.go"`}, nil},
		{"空行忽略", " M a.go\n\n", []string{"a.go"}, nil},
		{"过短行忽略", "X\n M a.go\n", []string{"a.go"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tracked, untracked := classifyLocalDirty(c.porcelain)
			if !reflect.DeepEqual(tracked, c.wantTracked) {
				t.Errorf("tracked = %#v, want %#v", tracked, c.wantTracked)
			}
			if !reflect.DeepEqual(untracked, c.wantUntracked) {
				t.Errorf("untracked = %#v, want %#v", untracked, c.wantUntracked)
			}
		})
	}
}
