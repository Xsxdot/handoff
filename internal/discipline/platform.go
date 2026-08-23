// platform.go —— 平台不变量恒在层的正文与组装边界。
//
// 职责：持有平台四条底线与收口自查，并把角色/执行者纪律块组装成一个 Block。
// 边界：纯函数，不读配置、不读文件、不写日志、不启动 executor；不复制
// turn.ProtocolRules，提问与 trailer 协议由 executor/turn 负责。
package discipline

import "strings"

const platformInvariantHead = `# 平台不变量（恒在层）

1. 不要派发、不要调用 handoff CLI（只读本地图数据的 handoff graph 子命令除外）、不要起任何新的 executor 进程或子任务。
2. 没有亲自跑到结果的命令，不许写它的结论。跑了但失败，贴原始报错原文，不要替它归因；不确定就写「未验证」。
3. 每确立一个事实就往台账文件追加一行——提交、跑过的命令与原始输出、放弃的尝试、做出的判断。不要攒到回合结束再写：回合可能不会有结束。
4. 按协议输出 trailer 收口。`

const platformInvariantTail = `收口前逐条自查：① 有没有把没亲自跑到结果的命令写成结论？② 台账是边干边追加的吗？③ 这一轮碰过 handoff CLI 或起过新 executor 吗？`

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
