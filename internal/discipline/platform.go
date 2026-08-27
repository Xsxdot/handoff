// platform.go —— 平台不变量恒在层的正文与组装边界。
//
// 职责：持有平台两条底线与收口自查，并把角色/执行者纪律块组装成一个 Block。
// 边界：纯函数，不读配置、不读文件、不写日志、不启动 executor；不复制
// turn.ProtocolRules，提问与 trailer 协议由 executor/turn 负责。
package discipline

import "strings"

const platformInvariantHead = `# 平台不变量（恒在层）

1. 不要派发、不要调用 handoff CLI、不要起任何新的 executor 进程或子任务。
2. 查图使用 go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . <子命令>；也可使用已安装的 codegraph；两者均不可用时再 grep。
3. 没有亲自跑到结果的命令，不许写它的结论。跑了但失败，贴原始报错原文，不要替它归因；不确定就写「未验证」。`

// 落台账要求不在平台层：spec 第 80 行把它移出平台层，第 81 行改由角色层只对
// 产出型角色承载（B229.6 已导入）。平台层若再出现该句，产出型角色会拿到两次、
// review 仍一次；TestComposeEnabledWithEmptyBaseOmitsLedgerFromPlatformLayer 守此。
const platformInvariantTail = `收口前逐条自查：① 有没有把没亲自跑到结果的命令写成结论？② 这一轮碰过 handoff CLI 或起过新 executor 吗？`

// Compose 把 Resolver 产出的角色/执行者纪律块与平台层组装成一个 Block。
//
// 参数：base 为 Resolver.ByName 或 Resolver.For 的结果；platformEnabled 控制平台层。
// 返回：启用时按「平台头部、base 正文、平台尾部」组装；关闭时保留 base。
// 注意：这是唯一的平台正文组装函数，调用方不得自己拼接头、base、尾。
func Compose(base Block, platformEnabled bool) Block {
	baseSource := strings.TrimSpace(base.Source)
	if !platformEnabled {
		source := "平台不变量已关闭"
		if baseSource != "" {
			source += " + " + baseSource
		}
		return Block{Text: base.Text, Source: source}
	}

	parts := []string{strings.TrimSpace(platformInvariantHead)}
	if strings.TrimSpace(base.Text) != "" {
		parts = append(parts, strings.TrimSpace(base.Text))
	}
	parts = append(parts, strings.TrimSpace(platformInvariantTail))
	source := "内置:平台不变量"
	if baseSource != "" {
		source += " + " + baseSource
	}
	return Block{Text: strings.Join(parts, "\n\n"), Source: source}
}
