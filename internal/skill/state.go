// state.go —— 各落点相对于当前二进制内嵌内容的一致性。
//
// 职责：
//   - Status：逐个落点读出实际内容，与 content 比 sha256，报 in_sync / stale / missing
//
// 边界：
//   - 只报有，不报无：落点不存在只说 missing，绝不断言「你没装 skill」。
//     agent 可能从我们表外的位置读到它，而一条会说谎的诊断命令比没有更糟
//   - 不修复：发现不一致只报告，同步是 handoff skill install
package skill

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/executor"
)

// Status 报告每个落点的一致性。
//
// 参数：
//   - content: 当前二进制内嵌的 SKILL.md 全文，作为比对基准
//   - home: 用户 home 目录
//   - providers: 各家 agent 的 Skills 能力提供方
//
// 返回：
//   - 每个落点的状态，顺序与 Install 一致
//   - err: 恒为 nil
func Status(content, home string, providers []executor.Skills) ([]Site, error) {
	want := sha256.Sum256([]byte(content))
	check := func(p string) Site {
		b, err := os.ReadFile(p)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return Site{Path: p, State: StateMissing}
		case err != nil:
			return Site{Path: p, State: StateMissing, Note: "读取失败: " + err.Error()}
		}
		got := sha256.Sum256(b)
		if got == want {
			return Site{Path: p, State: StateInSync}
		}
		return Site{Path: p, State: StateStale}
	}

	sites := []Site{check(filepath.Join(BasePath(home), fileName))}
	ctx := context.Background()
	for _, p := range providers {
		if p == nil {
			continue
		}
		got, err := p.Inspect(ctx, home)
		if err != nil {
			sites = append(sites, Site{Path: "", State: StateMissing, Note: err.Error()})
			continue
		}
		for _, s := range got {
			if s.State == StateMissing || s.State == StateSkipped {
				sites = append(sites, Site{Path: s.Path, State: s.State, Note: s.Note})
				continue
			}
			sites = append(sites, check(s.Path))
		}
	}
	return sites, nil
}

// InSync 判断全部存在的落点是否都与 content 一致。
//
// missing 不算不一致：那家 agent 没装，本来就不该有落点。
func InSync(sites []Site) bool {
	for _, s := range sites {
		if s.State == StateStale {
			return false
		}
	}
	return true
}
