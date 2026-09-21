// 移动端配对载体（pairing bundle）的 wire 定义与编解码（B369）。
//
// 本文件是配对载体的跨语言 wire 唯一定义处：Go 侧编码（CLI `console --qr`、
// 桌面壳），Kotlin / Swift 侧解码（移动 App 扫码）。信封带版本字段；未知版本
// 或畸形载荷一律拒收。改形状先回 contract 节点。
//
// 边界（spec 契约语义）：relay 不参与配对、也不签发任何凭据——凭据只在用户
// 自有设备间经 QR 带外拷贝。一份 bundle = relay 端点 + 管道 credential +
// 各已知执行机的登记 + 每台 agentd 各一张一次性 ticket；单机配对是其退化形态。
// （与 internal/proto/seat.go 同例：本包承载 wire 形状与形状自洽校验，不碰 I/O、
// 不触网络；真正拨号归 internal/mobilecore。）
package proto

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PairVersion 是配对载体的信封版本。解码方见到不认识的版本必须拒收——
// 这是跨语言（Go 编码 / Kotlin·Swift 解码）演进的唯一锚点。
const PairVersion = 1

// PairMachineBudgetBytes 是单台机器登记在 QR 容量预算里的上界（spec：每机约 200B）。
// 超界退路 = 粘贴串 / 分段码，归 contract 后续硬化。
const PairMachineBudgetBytes = 200

// 配对拒收哨兵。解码方按 errors.Is 判定，不解析文案。
var (
	// ErrPairVersion 表示信封版本不受支持（含缺省 v=0）。
	ErrPairVersion = errors.New("proto: 配对载体版本不受支持")
	// ErrPairMalformed 表示载荷畸形：JSON 不可解、无机器登记、单机登记自相矛盾等。
	ErrPairMalformed = errors.New("proto: 配对载体畸形")
)

// PairBundle 是配对载体的语义：relay 端点 + 管道 credential + 各已知执行机
// 登记 + 每台 agentd 各一张一次性 ticket。单机配对是其退化形态（Machines 一条）。
type PairBundle struct {
	Version   int           `json:"v"`
	Relay     *PairRelay    `json:"relay,omitempty"`
	Machines  []PairMachine `json:"machines"`
	IssuedAt  time.Time     `json:"issued_at"`
	ExpiresAt time.Time     `json:"expires_at"`
}

// PairRelay 是管道级 relay 端点与 coordinator CONNECT 凭证（管道 credential）。
// relay 形态的机器共用它；bundle 无任何 relay 形态机器时省键。
type PairRelay struct {
	URL        string `json:"url"`
	Credential string `json:"credential"`
}

// PairMachine 是一台执行机的登记：relay 形态与直连形态 0..1、按可达性选择。
// relay 形态（Node 非空）与直连形态（Addr 非空）由 Validate 保证互斥——这是
// bundle 自有 wire 语义，不改 config.Target 本身的 relay/addr 互斥。
type PairMachine struct {
	Name       string      `json:"name"`
	Token      string      `json:"token"`
	Credential string      `json:"credential,omitempty"` // 管道凭证覆盖；通常空，沿用 bundle.Relay
	Node       string      `json:"node,omitempty"`       // relay 形态：relay 上的 executor 节点名
	Addr       string      `json:"addr,omitempty"`       // 直连形态：agentd 地址
	Ticket     *PairTicket `json:"ticket,omitempty"`     // 离线机可缺
}

// PairTicket 是一台 agentd 的一次性 ticket（CLI 经各自通路代领）。
// 寿命 60s（internal/agentd/auth.go#ticketLifetime），N 张同时起算。
type PairTicket struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Validate 检查机器的自洽：名字与 token 非空，且恰为 relay 或直连形之一。
func (m PairMachine) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("机器名不能为空")
	}
	if strings.TrimSpace(m.Token) == "" {
		return errors.New("token 不能为空")
	}
	relayForm := m.Node != ""
	directForm := m.Addr != ""
	if relayForm == directForm {
		return errors.New("机器登记必须恰为 relay 形态或直连形态之一")
	}
	return nil
}

// Validate 检查整份 bundle 的自洽。
func (b PairBundle) Validate() error {
	if b.Version != PairVersion {
		return fmt.Errorf("%w: %d", ErrPairVersion, b.Version)
	}
	if len(b.Machines) == 0 {
		return fmt.Errorf("%w: 无机器登记", ErrPairMalformed)
	}
	for i, m := range b.Machines {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("%w: machines[%d]: %v", ErrPairMalformed, i, err)
		}
		if m.Node != "" && (b.Relay == nil || b.Relay.URL == "") {
			return fmt.Errorf("%w: machines[%d] 为 relay 形态但缺管道端点", ErrPairMalformed, i)
		}
	}
	return nil
}

// EncodePairBundle 把 bundle 编成 QR 载荷文本（紧凑 JSON，无缩进）。
// 先 Validate；非法 bundle 不产出荷载。
func EncodePairBundle(b PairBundle) (string, error) {
	if err := b.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return "", fmt.Errorf("编码配对载体: %w", err)
	}
	return string(raw), nil
}

// DecodePairBundle 解析 QR 载荷文本。
//
// 拒收语义（跨语言边界法定锁）：
//   - 不可解 JSON / 语义不自洽 → ErrPairMalformed
//   - 未知或缺省版本 → ErrPairVersion
func DecodePairBundle(raw string) (PairBundle, error) {
	var b PairBundle
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return PairBundle{}, fmt.Errorf("%w: %v", ErrPairMalformed, err)
	}
	if b.Version != PairVersion {
		return PairBundle{}, fmt.Errorf("%w: %d", ErrPairVersion, b.Version)
	}
	if err := b.Validate(); err != nil {
		return PairBundle{}, err
	}
	return b, nil
}
