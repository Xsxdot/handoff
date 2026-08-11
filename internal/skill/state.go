// state.go —— 各落点相对于当前二进制内嵌内容的一致性。
//
// 职责：
//   - Status：逐个落点读出实际内容，与 content 比 sha256，报 in_sync / stale / missing
//
// 边界：
//   - **只报有，不报无**：落点不存在只说 missing，绝不断言「你没装 skill」。
//     agent 可能从我们表外的位置读到它，而一条会说谎的诊断命令比没有更糟
//   - 不修复：发现不一致只报告，同步是 handoff skill install
package skill

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
)

// Status 报告每个落点的一致性。
//
// 参数：
//   - content: 当前二进制内嵌的 SKILL.md 全文，作为比对基准
//   - home: 用户 home 目录
//
// 返回：
//   - 每个落点的状态，顺序与 Install 一致
//   - err: 恒为 nil（保留返回值是为了让调用方的签名不必因将来加 I/O 而变）；
//     单个落点读失败落到该 Site 的 Note 上，不让一处坏掉的落点吃掉整份报告
func Status(content, home string) ([]Site, error) {
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
	for _, rel := range agentDirs {
		// 经软链读到的就是基准副本；落点是实体目录时读到的是它自己那份
		sites = append(sites, check(filepath.Join(home, rel, skillDirName, fileName)))
	}
	return sites, nil
}

// InSync 判断全部**存在的**落点是否都与 content 一致。
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
