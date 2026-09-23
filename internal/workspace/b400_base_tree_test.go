// b400_base_tree_test.go —— B400 BaseTreeMissingPaths 的真 git 夹具边界锁。
//
// 内部锁声明：workspace 函数不在 spec 两条缝的入口符号上；缝级断言由
// internal/agentd/b400_first_dispatch_test.go 的端到端用例给出。本用例覆盖
// 「在场/缺失/默认分支解析/非法路径」四类边界，不顶替任何缝级断言。
package workspace

import (
	"context"
	"testing"
)

func TestB400BaseTreeMissingPaths(t *testing.T) {
	origin, clone := newOriginAndClone(t)
	commitOnOrigin(t, origin, "docs/superpowers/specs/b400.md", "spec\n")

	cases := []struct {
		name         string
		base         string
		paths        []string
		wantResolved string
		wantMissing  []string
	}{
		{name: "默认线：spec 在场", base: "", paths: []string{"docs/superpowers/specs/b400.md"},
			wantResolved: "main", wantMissing: nil},
		{name: "默认线：plan 缺失", base: "", paths: []string{"docs/superpowers/plans/b400-plan.md"},
			wantResolved: "main", wantMissing: []string{"docs/superpowers/plans/b400-plan.md"}},
		{name: "显式 main：README 在场", base: "main", paths: []string{"README.md"},
			wantResolved: "main", wantMissing: nil},
		{name: "非法路径按缺失处理", base: "main", paths: []string{"../secret"},
			wantResolved: "main", wantMissing: []string{"../secret"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolved, missing, err := BaseTreeMissingPaths(context.Background(), clone, tc.base, tc.paths)
			if err != nil {
				t.Fatalf("BaseTreeMissingPaths: %v", err)
			}
			if resolved != tc.wantResolved {
				t.Fatalf("resolved = %q，want %q", resolved, tc.wantResolved)
			}
			if len(missing) != len(tc.wantMissing) {
				t.Fatalf("missing = %v，want %v", missing, tc.wantMissing)
			}
			for i := range missing {
				if missing[i] != tc.wantMissing[i] {
					t.Fatalf("missing = %v，want %v", missing, tc.wantMissing)
				}
			}
		})
	}
}
