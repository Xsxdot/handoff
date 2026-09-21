// receiver.go —— 统一接收者名称解析与物理身份绑定（B233.5）。
//
// 职责：把「交给谁」收成编制域纯函数——空名走已确认默认载体、显式名在
// 载体/小队同一登记范围唯一分类、绑定后禁止覆盖机器/引擎/HOME。
//
// 边界：无 I/O、不改 registry、不启动任务。Service.Admit / PutCarrier /
// Manager.Dispatch 接线归实现节点。本文件不 import 账本或卡列。
package scheduling

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ReceiverKind 是统一名称解析的结果类型。与登记 kind 字面值对齐，但不含
// 默认载体 singleton（那是另一条 registry 记录，不是可派发接收者）。
type ReceiverKind string

const (
	ReceiverCarrier ReceiverKind = "carrier"
	ReceiverSquad   ReceiverKind = "squad"
)

// KindDefaultCarrier 是已确认默认载体的 registry kind。id 恒为 DefaultCarrierID。
// 不进载体/小队名称空间——「current」不是可派发接收者名。
const (
	KindDefaultCarrier = "default_carrier"
	DefaultCarrierID   = "current"
)

// DefaultCarrierRecord 是默认载体 singleton 的 body。
type DefaultCarrierRecord struct {
	Name string `json:"name"`
}

var (
	// ErrNoDefault 表示未给出接收者且没有有效默认载体。不得回退
	// config.executor.default 或裸环境。
	ErrNoDefault = errors.New("scheduling: 没有有效的默认载体")

	// ErrNameConflict 表示同一名字同时登记为载体和小队。拒绝，不猜类型。
	ErrNameConflict = errors.New("scheduling: 名称同时登记为载体和小队")

	// ErrPhysicalOverride 表示请求试图覆盖已绑定载体的机器、引擎或 HOME。
	ErrPhysicalOverride = errors.New("scheduling: 禁止覆盖已绑定载体的机器、引擎或 HOME")
)

// ResolvedReceiver 是一次名称解析的结果。
type ResolvedReceiver struct {
	Kind       ReceiverKind
	Name       string
	ViaDefault bool
}

// PhysicalIdentity 是绑定后不可覆盖的物理身份。Model 不是物理身份。
type PhysicalIdentity struct {
	Machine string
	CLI     string
	HomeDir string
}

// PhysicalOverlay 是请求侧想施加的物理字段。空串 = 未指定。
// HomeDir 用指针：nil=缺席，非 nil=显式值（含空串）。
type PhysicalOverlay struct {
	Machine string
	CLI     string
	HomeDir *string
}

// EffectiveReceiverName 决定本次要用的接收者名。requested 非空则用它；
// 否则用 defaultName；两者都空则 ErrNoDefault。
func EffectiveReceiverName(requested, defaultName string) (name string, viaDefault bool, err error) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		return requested, false, nil
	}
	defaultName = strings.TrimSpace(defaultName)
	if defaultName == "" {
		return "", true, ErrNoDefault
	}
	return defaultName, true, nil
}

// ClassifyRegistered 在已知「该名是否存在于载体表/小队表」后分类。
// 两边都有 → ErrNameConflict；两边都无 → ErrNotFound；只在一边 → 对应 Kind。
func ClassifyRegistered(name string, hasCarrier, hasSquad bool) (ReceiverKind, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("%w: 接收者名不能为空", ErrNotFound)
	}
	if hasCarrier && hasSquad {
		return "", fmt.Errorf("%w: %s", ErrNameConflict, name)
	}
	if hasCarrier {
		return ReceiverCarrier, nil
	}
	if hasSquad {
		return ReceiverSquad, nil
	}
	return "", fmt.Errorf("%w: %s", ErrNotFound, name)
}

// CheckDefaultKind 要求默认解析结果必须是载体。默认名命中小队或冲突都不算有效默认。
func CheckDefaultKind(kind ReceiverKind) error {
	if kind != ReceiverCarrier {
		return ErrNoDefault
	}
	return nil
}

// ResolveLookup 组合空名默认与登记分类。lookup 报告该名在两张表里是否存在。
// viaDefault 且分类失败（不存在/是小队）时统一成 ErrNoDefault；显式名的冲突仍是 ErrNameConflict。
func ResolveLookup(requested, defaultName string, lookup func(string) (hasCarrier, hasSquad bool)) (ResolvedReceiver, error) {
	name, viaDefault, err := EffectiveReceiverName(requested, defaultName)
	if err != nil {
		return ResolvedReceiver{}, err
	}
	hasCarrier, hasSquad := lookup(name)
	kind, err := ClassifyRegistered(name, hasCarrier, hasSquad)
	if err != nil {
		if viaDefault && !errors.Is(err, ErrNameConflict) {
			return ResolvedReceiver{}, fmt.Errorf("%w: %s", ErrNoDefault, name)
		}
		if viaDefault && errors.Is(err, ErrNameConflict) {
			return ResolvedReceiver{}, fmt.Errorf("%w: %s", ErrNameConflict, name)
		}
		return ResolvedReceiver{}, err
	}
	if viaDefault {
		if err := CheckDefaultKind(kind); err != nil {
			return ResolvedReceiver{}, err
		}
	}
	return ResolvedReceiver{Kind: kind, Name: name, ViaDefault: viaDefault}, nil
}

// CrossKindConflict 在写入一侧时，对侧同名已存在则冲突。
func CrossKindConflict(otherExists bool) error {
	if otherExists {
		return ErrNameConflict
	}
	return nil
}

// IdentityOf 从载体取出物理身份。空 HOME 表示沿用该机主 HOME，仍是身份的一部分。
func IdentityOf(c Carrier) PhysicalIdentity {
	return PhysicalIdentity{Machine: c.Machine, CLI: c.CLI, HomeDir: c.HomeDir}
}

// BindPhysical 把请求覆盖套到已绑定身份上。未指定字段保持载体值；指定但与载体
// 不同则 ErrPhysicalOverride。相同值重述合法。
func BindPhysical(id PhysicalIdentity, overlay PhysicalOverlay) (PhysicalIdentity, error) {
	if overlay.Machine != "" && overlay.Machine != id.Machine {
		return PhysicalIdentity{}, ErrPhysicalOverride
	}
	if overlay.CLI != "" && overlay.CLI != id.CLI {
		return PhysicalIdentity{}, ErrPhysicalOverride
	}
	if overlay.HomeDir != nil && *overlay.HomeDir != id.HomeDir {
		return PhysicalIdentity{}, ErrPhysicalOverride
	}
	return id, nil
}

// EffectiveModel 是允许的逐任务运行参数：请求非空则用请求，否则用载体模型。
// 它不改变物理身份，也不扩大权限。
func EffectiveModel(carrierModel, requestModel string) string {
	if trimmed := strings.TrimSpace(requestModel); trimmed != "" {
		return trimmed
	}
	return carrierModel
}

// EncodeDefaultCarrier 编码默认载体 singleton body。空名拒绝。
func EncodeDefaultCarrier(name string) ([]byte, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: 默认载体名不能为空", ErrNoDefault)
	}
	return json.Marshal(DefaultCarrierRecord{Name: name})
}

// DecodeDefaultCarrier 解码默认载体 singleton body。空 name 视为没有有效默认。
func DecodeDefaultCarrier(body []byte) (string, error) {
	var rec DefaultCarrierRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		return "", err
	}
	name := strings.TrimSpace(rec.Name)
	if name == "" {
		return "", ErrNoDefault
	}
	return name, nil
}
