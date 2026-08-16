// 本文件实现「在访达中显示」（Reveal in Finder，B108）：平台能力位与
// POST /api/workspaces/reveal 端点。
//
// 职责：
//   - 报告本平台是否支持这个动作（经 /api/status 与 /api/machines 上报）
//   - 校验「调用方在本机、路径在工作树内」后执行 `open -R`
//
// 边界：
//   - **不接 ?machine= 转发**。转发正是这个端点要拒绝的那件事——在别人的
//     机器上弹一个没人看的 Finder 窗口。理由见 spec §3.2
//   - 不做任何写操作；这是一个只读的、给人看的动作
package agentd

import "runtime"

// revealSupportedOS 是本平台是否支持「在访达中显示」。
//
// 为什么是 var 而不是 const：**唯一理由是测试缝**。写成 const 的话 false 分支
// 只有在非 macOS 机器上才跑得到，等于永远不测——与 hostguard.go 的
// nicRefreshGap / localIPsFn 同一条理由。
var revealSupportedOS = runtime.GOOS == "darwin"
