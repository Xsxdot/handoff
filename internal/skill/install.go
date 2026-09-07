// install.go —— 把 skill 内容装到本机各家 agent。
//
// 职责：
//   - 写基准副本到 <home>/.handoff/skill/SKILL.md
//   - 在存在的 agent 目录里各写一份副本
//   - 返回每个落点的实际处置，供命令层如实打印
//
// 边界：
//   - 不改任何 agent 的配置文件（五家都按约定自动扫描 skills 目录）
//   - agent 的 home 目录不存在就跳过，不代为创建：给没装 codex 的机器
//     造一个 ~/.codex，下次那台机器真装了 codex 时会拿到我们凭空造的半截结构
//   - 不含 go:embed：内容与 home 都是入参，测试给临时目录与任意字符串即可，
//     不需要构建产物
//   - 不装到远端：skill 服务于协调者，协调者在本机（spec 非目标）
package skill

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/executor"
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

// skillDirName 是落点目录在各家 skills 目录下的名字。
const skillDirName = "handoff"

// fileName 是基准副本里的文件名。
const fileName = "SKILL.md"

// BasePath 返回基准副本目录。
func BasePath(home string) string { return filepath.Join(home, ".handoff", "skill") }

// Install 把 content 装到本机。
//
// 参数：
//   - content: SKILL.md 的全文（生产由 go:embed 注入）
//   - home: 用户 home 目录（测试注入临时目录）
//   - providers: 各家 agent 的 Skills 能力提供方
//
// 返回：
//   - 每个落点的处置结果，首项为基准副本，随后按 providers 顺序追加
//   - err: 只有基准副本写失败才返回错误——那是这个功能的地基；
//     单个 agent 落点失败记进 Site.Note 继续，不因为一家没装成就全盘失败
func Install(content, home string, providers []executor.Skills) ([]Site, error) {
	base := BasePath(home)
	if err := os.MkdirAll(base, 0o755); err != nil {
		slog.Default().Error("创建基准副本目录失败", "path", base, "cause", err)
		return nil, fmt.Errorf("创建基准副本目录 %s: %w", base, err)
	}
	target := filepath.Join(base, fileName)
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		slog.Default().Error("写基准副本失败", "path", target, "cause", err)
		return nil, fmt.Errorf("写基准副本 %s: %w", target, err)
	}
	sites := []Site{{Path: target, State: StateInstalled}}
	slog.Default().Info("skill 基准副本已写入", "path", target)

	ctx := context.Background()
	for _, p := range providers {
		if p == nil {
			continue
		}
		got, err := p.Install(ctx, executor.SkillInstallReq{Home: home, Content: content})
		if err != nil {
			// 一家失败继续：把 err 记进 Note，不 return
			sites = append(sites, Site{Path: "", State: StateSkipped, Note: err.Error()})
			slog.Default().Warn("skill 提供方安装失败", "cause", err)
			continue
		}
		for _, s := range got {
			sites = append(sites, Site{Path: s.Path, State: s.State, Note: s.Note})
			if s.State == StateSkipped {
				slog.Default().Warn("skill 落点跳过", "path", s.Path, "reason", s.Note)
			} else {
				slog.Default().Info("skill 落点已写入", "path", s.Path, "state", s.State)
			}
		}
	}
	return sites, nil
}
