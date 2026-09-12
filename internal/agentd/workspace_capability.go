// workspace_capability.go —— 工作区 Capability 的 agentd 持有面。
//
// 职责：暴露「未注入工作区」哨兵给 handler 映射。生产实现由
// internal/workspace.NewCapability 提供；Manager 侧 SetWorkspace /
// AssembleResultRef 在 internal/orchestration。
// 边界：本文件不定义适配器、不写账本、不授权 push。
package agentd

import "errors"

// ErrWorkspaceUnavailable 表示编排侧未注入工作区能力。
// nil 不得解释成跳过 EnsureRepoUsable 或脏检查——调用方必须失败。
var ErrWorkspaceUnavailable = errors.New("工作区能力未注入")
