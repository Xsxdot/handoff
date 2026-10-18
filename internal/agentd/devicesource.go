// devicesource.go —— 设备表快照源（B439 WU2 / F5）。
//
// 职责：
//   - 把账本里的设备表投影成 target 客户端池的选路数据源（Names/Target）
//   - 读表失败时回上一份成功快照 + WARN（不静默、不撕订阅）
//
// 边界：
//   - 只读账本设备表，不做网络、不构造 client（构造归池）
//   - 不缓存「当前有哪些机器」之外的任何状态；每次读都反映账本现状
//   - 有 DSN 的机器才注入本源；无 DSN 机器池回落活配置 targets:（S5）
package agentd

import (
	"log/slog"
	"sort"
	"sync"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
)

// DeviceTableSource 是设备表快照源，满足 targetclient.TargetSource。
type DeviceTableSource struct {
	st  *ledger.Store
	log *slog.Logger

	mu      sync.Mutex
	names   []string
	targets map[string]config.Target
	loaded  bool
}

// NewDeviceTableSource 构造设备表快照源。st 必须非 nil（有 DSN 才注入本源）。
func NewDeviceTableSource(st *ledger.Store, log *slog.Logger) *DeviceTableSource {
	if log == nil {
		log = slog.Default()
	}
	return &DeviceTableSource{st: st, log: log, targets: map[string]config.Target{}}
}

// Names 返回设备表当前机器名（已排序）。
//
// 读表失败时返回上一份成功快照（首次失败时为空集），并打一次 WARN——
// 瞬态读库失败不等于配置已变更，按 B163 ② 的保守语义持旧凭据。
func (s *DeviceTableSource) Names() []string {
	s.refresh()
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.names...)
}

// Target 取一台机器的连接方式（含凭据）；未登记返回 ok=false。
func (s *DeviceTableSource) Target(name string) (config.Target, bool) {
	s.refresh()
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.targets[name]
	return t, ok
}

// refresh 现读设备表并替换快照；失败保留 last-good。
func (s *DeviceTableSource) refresh() {
	rows, err := s.st.DeviceTargets()
	if err != nil {
		s.log.Warn("设备表读失败，回上一份快照（订阅不撕）", "cause", err)
		return
	}
	names := make([]string, 0, len(rows))
	targets := make(map[string]config.Target, len(rows))
	for _, d := range rows {
		names = append(names, d.Name)
		targets[d.Name] = deviceTargetToConfig(d.Target)
	}
	sort.Strings(names)
	s.mu.Lock()
	s.names = names
	s.targets = targets
	s.loaded = true
	s.mu.Unlock()
}

// deviceTargetToConfig 把设备表连接块投影成池消费的 config.Target。
//
// 指针 nil 与指向空串都折算为空串：config.Target 是全 string 值类型，
// 池的实例判等按整体比较（relay 加字段时不漏），两种「无值」对选路等价。
func deviceTargetToConfig(t ledger.DeviceTarget) config.Target {
	return config.Target{
		Addr:       derefStr(t.Addr),
		Token:      derefStr(t.Token),
		User:       derefStr(t.User),
		Relay:      derefStr(t.Relay),
		Credential: derefStr(t.Credential),
		Node:       derefStr(t.Node),
	}
}

// configToDeviceTarget 把 config.Target 投影成设备表连接块（非空字段落指针）。
func configToDeviceTarget(t config.Target) ledger.DeviceTarget {
	dt := ledger.DeviceTarget{}
	if t.Addr != "" {
		dt.Addr = strp(t.Addr)
	}
	if t.Token != "" {
		dt.Token = strp(t.Token)
	}
	if t.User != "" {
		dt.User = strp(t.User)
	}
	if t.Relay != "" {
		dt.Relay = strp(t.Relay)
	}
	if t.Credential != "" {
		dt.Credential = strp(t.Credential)
	}
	if t.Node != "" {
		dt.Node = strp(t.Node)
	}
	return dt
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func strp(s string) *string { return &s }

// 编译期锚：设备表快照源必须始终满足池的数据源接口。
var _ interface {
	Names() []string
	Target(name string) (config.Target, bool)
} = (*DeviceTableSource)(nil)
