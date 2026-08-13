// install.go —— 把 skill 内容装到本机各家 agent。
//
// 职责：
//   - 写基准副本到 <home>/.handoff/skill/SKILL.md
//   - 给**存在的** agent 目录建软链指向基准副本
//   - 返回每个落点的实际处置，供命令层如实打印
//
// 边界：
//   - 不改任何 agent 的配置文件（四家都按约定自动扫描 skills 目录）
//   - **agent 的 home 目录不存在就跳过，不代为创建**：给没装 codex 的机器
//     造一个 ~/.codex，下次那台机器真装了 codex 时会拿到我们凭空造的半截结构
//   - 不含 go:embed：内容与 home 都是入参，测试给临时目录与任意字符串即可，
//     不需要构建产物
//   - 不装到远端：skill 服务于协调者，协调者在本机（spec 非目标）
package skill

import (
	"fmt"
	"os"
	"path/filepath"
)

// 落点状态。
const (
	StateInstalled = "installed" // 本次已写入/已建链
	StateSkipped   = "skipped"   // agent 目录不存在，跳过（Note 说明理由）
	StateInSync    = "in_sync"   // 内容与当前二进制内嵌的一致
	StateStale     = "stale"     // 存在但内容不一致
	StateMissing   = "missing"   // 落点不存在
)

// Site 是一个落点及其状态。
//
// Note 只在需要解释时非空（跳过的理由、读取失败的原因）。
type Site struct {
	Path  string
	State string
	Note  string
}

// agentDirs 是各家 agent 的 skills 目录（相对 home）。
//
// 顺序即打印顺序。加新 agent 时只改这里——落点表随二进制一起更新，
// 这正是「我们自己装就知道装到了哪」的前提（spec D6）。
var agentDirs = []string{
	".claude/skills",
	".codex/skills",
	".config/opencode/skills",
	".grok/skills",
}

// skillDirName 是软链在各家 skills 目录下的名字。
const skillDirName = "handoff"

// fileName 是基准副本里的文件名。
const fileName = "SKILL.md"

// BasePath 返回基准副本目录。
//
// 为什么用副本而不是让四家都软链到仓库：仓库切分支/移动时四个 agent 会
// 一起失效。代价是改动后要重新同步，而这正由 upgrade 与一行安装自动完成。
func BasePath(home string) string { return filepath.Join(home, ".handoff", "skill") }

// Install 把 content 装到本机。
//
// 参数：
//   - content: SKILL.md 的全文（生产由 go:embed 注入）
//   - home: 用户 home 目录（测试注入临时目录）
//
// 返回：
//   - 每个落点的处置结果，顺序为 [基准副本, .claude, .codex, opencode, .grok]
//   - err: 只有基准副本写失败才返回错误——那是这个功能的地基；
//     单个 agent 落点失败记进 Site.Note 继续，不因为一家没装成就全盘失败
func Install(content, home string) ([]Site, error) {
	base := BasePath(home)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("创建基准副本目录 %s: %w", base, err)
	}
	target := filepath.Join(base, fileName)
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("写基准副本 %s: %w", target, err)
	}
	sites := []Site{{Path: target, State: StateInstalled}}

	for _, rel := range agentDirs {
		dir := filepath.Join(home, rel)
		parent := filepath.Dir(dir)
		if _, err := os.Stat(parent); err != nil {
			sites = append(sites, Site{
				Path: filepath.Join(dir, skillDirName), State: StateSkipped,
				Note: parent + " 不存在（该 agent 未安装）",
			})
			continue
		}
		link := filepath.Join(dir, skillDirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			sites = append(sites, Site{Path: link, State: StateSkipped, Note: "创建目录失败: " + err.Error()})
			continue
		}
		// 先删再建：目标可能是上一次装的软链，也可能是手工放的实体目录
		if err := os.RemoveAll(link); err != nil {
			sites = append(sites, Site{Path: link, State: StateSkipped, Note: "清理旧落点失败: " + err.Error()})
			continue
		}
		if err := os.Symlink(base, link); err != nil {
			sites = append(sites, Site{Path: link, State: StateSkipped, Note: "建软链失败: " + err.Error()})
			continue
		}
		sites = append(sites, Site{Path: link, State: StateInstalled})
	}
	return sites, nil
}
