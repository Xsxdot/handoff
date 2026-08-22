// 本文件把 codegraph 命令树以 graph 别名挂进 handoff（deprecated 委托别名）。
//
// 刀 0 搬迁后 codegraph 的 canonical 家在 charter 仓（github.com/Xsxdot/charter/graph），
// 命令树由 graph/cli 构造——与 codegraph 二进制同一构造，行为同版本等价
// （契约见 charter 仓 docs/contracts/2026-08-22-codegraph-extraction-contract.md §4）。
//
// 边界：
//   - 本文件只做挂载与 deprecated 标注，不含任何子命令逻辑；
//   - deprecated 只进帮助文本（Short），禁用 cobra 的 Deprecated 字段——
//     该字段会向运行时输出打告警，污染 JSON 消费管道与 SessionStart hook 的 summary 注入。
package cmd

import (
	graphcli "github.com/Xsxdot/charter/graph/cli"
)

func init() {
	graphCmd := graphcli.New("graph")
	graphCmd.Short = "[deprecated：请改用 codegraph 二进制] " + graphCmd.Short
	rootCmd.AddCommand(graphCmd)
}
