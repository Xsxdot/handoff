// install.go —— 把 skill 内容装到本机各家 agent。
//
// 职责：
//   - 写基准副本到 <home>/.handoff/skill/SKILL.md
//   - 在**存在的** agent 目录里各写一份副本
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
	StateInstalled = "installed" // 本次已写入副本
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

// skillDirName 是落点目录在各家 skills 目录下的名字。
const skillDirName = "handoff"

// fileName 是基准副本里的文件名。
const fileName = "SKILL.md"

// BasePath 返回基准副本目录。
//
// 为什么四家各存一份副本、而不是软链到这里或软链到仓库：软链到仓库会在仓库
// 切分支/移动时让四个 agent 一起失效；软链到基准副本看似便宜，买到的
// 「改一处生效四处」却是零收益——内容来自 go:embed，Install 每次运行本来
// 就全量重写所有落点，没人手改基准副本（手改了也会被 Status 判成 stale）。
// 而它的代价是实打实的：Windows 上建目录软链需要管理员特权，非特权时四个
// 落点全部装不上，症状还是静默半残（B84）。副本形态全平台一条路径。
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
		site := filepath.Join(dir, skillDirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			sites = append(sites, Site{Path: site, State: StateSkipped, Note: "创建目录失败: " + err.Error()})
			continue
		}
		// 先删再建：目标可能是上一次装的软链（老形态），也可能是手工放的实体目录。
		// RemoveAll 对软链只摘链、不动链指向的基准副本——迁移正是靠这条语义
		if err := os.RemoveAll(site); err != nil {
			sites = append(sites, Site{Path: site, State: StateSkipped, Note: "清理旧落点失败: " + err.Error()})
			continue
		}
		if err := os.MkdirAll(site, 0o755); err != nil {
			sites = append(sites, Site{Path: site, State: StateSkipped, Note: "创建落点目录失败: " + err.Error()})
			continue
		}
		if err := os.WriteFile(filepath.Join(site, fileName), []byte(content), 0o644); err != nil {
			sites = append(sites, Site{Path: site, State: StateSkipped, Note: "写落点副本失败: " + err.Error()})
			continue
		}
		sites = append(sites, Site{Path: site, State: StateInstalled})
	}
	return sites, nil
}
