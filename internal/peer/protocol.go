// Package peer 定义 agentd 之间的机器同步协议（peer v1）。
//
// 职责：
//   - Hello：协商 protocol version 与 capability map
//   - MachineSnapshot：带 through_machine_seq 的全量快照
//   - Negotiate：capability 协商（未知 capability 忽略，缺核心项标 incompatible）
//
// 边界：
//   - 纯类型与协商语义，不发起网络
//   - 依赖 capability 的行为只有双方确认后才启用（spec §9）
package peer

import (
	"encoding/json"
	"time"
)

// ProtocolVersion 是 peer v1 协议版本。
const ProtocolVersion = 1

// Workspace 资源 capability keys。缺少某项时 catalog 连接仍可成立，但对应
// resource gateway 必须返回 CAPABILITY_UNSUPPORTED，不能猜测旧 peer 支持。
const (
	CapabilityFiles   = "files"
	CapabilityGit     = "git"
	CapabilityPty     = "pty"
	CapabilityPreview = "preview"
)

// requiredCapabilities 是 peer 连接成立的核心 capability。
// 缺任一即标记 incompatible（升级 agentd），不走猜测。
var requiredCapabilities = []string{"catalog", "machine_events"}

// supportedCapabilities 是本版本理解的完整 capability 白名单。协商结果只包含
// 白名单项，避免未来或拼写错误的 capability 被本端误认为已经实现。
var supportedCapabilities = []string{
	"catalog",
	"machine_events",
	CapabilityFiles,
	CapabilityGit,
	CapabilityPty,
	CapabilityPreview,
}

// Hello 是 /v1/peer/hello 的响应体。
type Hello struct {
	ProtocolVersion int            `json:"protocol_version"`
	Capabilities    map[string]int `json:"capabilities"`
}

// Negotiate 返回协商后的 capability 交集。
//
// 语义：
//   - 未知 capability 被忽略（双方各自理解各自认识的）
//   - 缺失核心 capability（catalog/machine_events）→ incompatible=true
//
// 返回：
//   - negotiated：双方共同的 capability
//   - incompatible：是否缺少核心能力（需要升级）
func Negotiate(peerCaps map[string]int) (map[string]int, bool) {
	negotiated := make(map[string]int)
	for _, core := range requiredCapabilities {
		if v, ok := peerCaps[core]; !ok || v < 1 {
			return nil, true
		}
	}
	for _, capability := range supportedCapabilities {
		if v := peerCaps[capability]; v >= 1 {
			negotiated[capability] = v
		}
	}
	return negotiated, false
}

// MachineSnapshot 是 /v1/machine/snapshot 的响应体（catch-up 前的全量校准）。
type MachineSnapshot struct {
	// ThroughMachineSeq 是快照覆盖到的 machine_seq 上界。
	ThroughMachineSeq int64 `json:"through_machine_seq"`
	WorkspaceCount    int   `json:"workspace_count"`
	GitRefCount       int   `json:"git_ref_count"`
	TaskCount         int   `json:"task_count"`
}

// MachineEvent 是 peer 同步的 machine event 线格式（与 controlplane 一致）。
type MachineEvent struct {
	MachineID  string          `json:"machine_id"`
	MachineSeq int64           `json:"machine_seq"`
	EventID    string          `json:"event_id"`
	Kind       string          `json:"kind"`
	ResourceID string          `json:"resource_id"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}
