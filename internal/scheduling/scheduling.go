// Package scheduling 是编制域：载体与小队的登记、两级并发准入、点火请求的
// 持久排队。规则归本域，持久化经 schedclient.Registry 端口由组装点绑定
// （账本是设施，本域是规则所有者——B156.3 spec §7.1）。
//
// 冻结语义（契约 §3/§4，实现与测试同源）：
//   - 准入判据 = 小队有位 且 载体有位；两级计数各自独立；
//   - 载体上限是物理位（跨小队全局），成员上限是政策位（按小队+载体计数）；
//   - 抢并发时协调者优先：只作用于准入排序（先清协调者队再放执行者队），
//     不抢占在跑任务；
//   - 排队顺序 = 就绪度快照优先 → 卡优先级 → 入队先后（FIFO）。
package scheduling

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/Xsxdot/handoff/internal/schedclient"
)

// 注册表里各实体的 kind 常量。新增实体先改契约再改这里。
// 默认载体 singleton 的 kind/id 在 receiver.go（KindDefaultCarrier / DefaultCarrierID）。
const (
	kindCarrier       = "carrier"
	kindSquad         = "squad"
	kindRunning       = "sched_running"
	KindLaunchQueue   = "launch_queue"   // 协调者拉起请求队列（协调者优先被清）
	KindIgnitionQueue = "ignition_queue" // 执行者节点点火请求队列
)

// QueueKinds 是空位分配的法定清队顺序：下标小者优先。协调者回合短而稀疏、
// 是消费执行产出的那一环，被数据面饿死会让整条流水线停摆（spec §3 台账⑨）。
var QueueKinds = []string{KindLaunchQueue, KindIgnitionQueue}

// maxCASAttempts 是单次计数变更的 CAS 重试上限。并发规模 N≤16 时实测该预算内
// 能收敛但偶有耗尽（plan 基线探针：N=16 打容量 4，出现「连续冲突」型失败）；
// 耗尽以 ErrRetryExhausted 外露，验收翻红先查预算再查语义。
const maxCASAttempts = 8

// CredentialSource 是载体的凭据来源两值（spec §3）。
type CredentialSource string

const (
	CredentialStandalone   CredentialSource = "standalone"     // 独立账号
	CredentialMainHomeSync CredentialSource = "main_home_sync" // 主 HOME 认证态同步
)

// Carrier 是一台机器上一个可领活的 CLI 档案（spec §3 载体实体归并后的形状）。
// Status 是唯一的运行状态真相：新建或 HOME 改变为 pending，检测可迁移到
// online/quota/unreachable；LastError 只保存最近一次非 online 结果的说明。
type Carrier struct {
	Name           string           `json:"name"`
	Machine        string           `json:"machine"`
	CLI            string           `json:"cli"`
	HomeDir        string           `json:"home_dir"`
	Model          string           `json:"model,omitempty"`
	Credential     CredentialSource `json:"credential"`
	MaxConcurrency int              `json:"max_concurrency,omitempty"` // 物理位；0 = 不设上限
	Status         CarrierStatus    `json:"status,omitempty"`
	LastError      string           `json:"last_error,omitempty"` // 最近一次检测说明；不参与准入
}

// SquadRole 区分两种不混编的小队。
type SquadRole string

const (
	RoleExecutor    SquadRole = "executor"    // 绑工作流节点
	RoleCoordinator SquadRole = "coordinator" // 绑拉起通道
)

// SquadMember 是小队对一个具体载体的政策位。MaxConcurrency=0 表示不限。
type SquadMember struct {
	Carrier        string `json:"carrier"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
}

// Squad 是编制：角色 + 成员载体引用集；政策位落在每个成员上。
type Squad struct {
	Name    string        `json:"name"`
	Role    SquadRole     `json:"role"`
	Members []SquadMember `json:"members"`
}

// UnmarshalJSON 兼容 B156.3 的存量成员字符串形状。旧队级 max_concurrency 刻意不
// 出现在目标形状中，因此由 encoding/json 忽略；旧成员逐个规范化为不限政策位。
func (q *Squad) UnmarshalJSON(data []byte) error {
	type squadWire struct {
		Name    string          `json:"name"`
		Role    SquadRole       `json:"role"`
		Members json.RawMessage `json:"members"`
	}
	var raw squadWire
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	q.Name, q.Role = raw.Name, raw.Role
	if len(raw.Members) == 0 || string(raw.Members) == "null" {
		q.Members = nil
		return nil
	}
	var members []SquadMember
	if err := json.Unmarshal(raw.Members, &members); err == nil {
		q.Members = members
		return nil
	}
	var legacy []string
	if err := json.Unmarshal(raw.Members, &legacy); err != nil {
		return json.Unmarshal(raw.Members, &members)
	}
	q.Members = make([]SquadMember, len(legacy))
	for i, carrier := range legacy {
		q.Members[i] = SquadMember{Carrier: carrier}
	}
	return nil
}

// IgnitionRequest 是一次点火/拉起请求。Priority 与 Ready 是入队时刻的卡状态
// 快照，出队不再重读卡——新鲜性由出队后唤醒协调者确认基线兜底（spec §3，
// 契约拍板记录②）。
type IgnitionRequest struct {
	Card     string `json:"card"`
	Squad    string `json:"squad"`
	Node     string `json:"node,omitempty"`
	Target   string `json:"target,omitempty"`
	Executor string `json:"executor,omitempty"`
	Model    string `json:"model,omitempty"`
	Priority string `json:"priority,omitempty"`
	Ready    bool   `json:"ready"`
	Actor    string `json:"actor"`
}

// Binding 是准入成功后的落点：哪个小队的哪个载体、有效三元组是什么。
// HomeDir 是绑定当时的载体 HOME 快照（B233.5）；空=沿用该机主 HOME。
type Binding struct {
	Squad    string `json:"squad"`
	Carrier  string `json:"carrier"`
	Target   string `json:"target"`
	Executor string `json:"executor"`
	Model    string `json:"model,omitempty"`
	HomeDir  string `json:"home_dir,omitempty"`
}

var (
	ErrNotFound     = errors.New("scheduling: 实体不存在")
	ErrNoSlot       = errors.New("scheduling: 小队或载体并发已满")
	ErrRoleMismatch = errors.New("scheduling: 小队角色不符")
	ErrNoHealthy    = errors.New("scheduling: 小队内没有已上线且有空的载体")

	// ErrRetryExhausted 表示运行计数的 CAS 连续冲突超出重试预算。它与 ErrNoSlot
	// 的分流是并发验收的排查序：先预算后语义（岔口三附加约束，plan §D5.3）。
	ErrRetryExhausted = errors.New("scheduling: 运行计数连续冲突")

	// ErrInvalid 表示登记输入未过校验（名字缺失、凭据来源词表外、角色词表外、
	// 成员引用不存在）。gateway 据此把用户输入错（400）与 registry 故障（500）
	// 分流；哨兵住 scheduling 包——校验规则与词表常量都在本包（B156.3 K3）。
	// PutCarrier/PutSquad 的四处校验 return 均以 %w 包它（B156.3.3 修复轮
	// Major-3 + 协调者白名单扩容：plan §D4 原文本就如此）。
	ErrInvalid = errors.New("scheduling: 登记校验未过")
)

// Service 是编制域的规则引擎本体。全部状态经 Registry 持久（agentd 重启
// 不丢），内存里不留任何权威计数。
type Service struct {
	repo          schedclient.Registry
	knownMachines func(string) bool // nil：不拦远程名（编制单测）；生产注入 targets 键
	// selfName 是本机自名（B439 S6 门控）。空 = 未配自名，判据回退今天的本机
	// 别名词表（S5）；非空 = 只有省略或 == 自名是本机，local/本机/hostname 拒收。
	selfName string
	// legacyAlias 报告一个已存机器值是否是「本机」旧别名（B439 S6）。组装点注入
	// config.IsLegacyLocalAlias——判据唯一定义点在 config；nil = 未注入，派发
	// 准入不做旧别名拒收（S5）。只在 selfName 非空（本机已配自名）时生效。
	legacyAlias func(string) bool
}

// New 用组装点绑定的注册表端口构造服务。
func New(repo schedclient.Registry) *Service { return &Service{repo: repo} }

// SetSelfName 注入本机自名（组装点从 config.Name 取）。它把「本机」判据从
// 别名词表收敛到唯一名字（B439 §4.1）：配了自名的机器上，local/本机/hostname
// 不再是本机，登记时以 ErrInvalid 拒收并点名改成什么（S6）；未注入（无自名）
// 保持今天词表（S5）。与 SetKnownMachines 一样只在组装点调用一次。
func (s *Service) SetSelfName(name string) { s.selfName = name }

// SetLegacyAlias 注入「一个已存机器值是否本机旧别名」的判据（B439 S6）。组装点
// 从 config.IsLegacyLocalAlias 取——旧别名的词法（local/本机/hostname）只有
// config 一处定义，本域不另立一份。与 SetSelfName 一样只在组装点调用一次；
// nil（未注入）时派发准入不做旧别名拒收。
func (s *Service) SetLegacyAlias(fn func(string) bool) { s.legacyAlias = fn }

// isLocalMachine 是本机判据的编制域入口（B439 §4.1）：
//   - 有自名：省略或 == 自名才是本机；local/本机/hostname 都否（S6 由
//     checkKnownMachine 给可行动错误，不再静默当本机）。
//   - 无自名：回退别名词表（空串/local/本机/hostname），与今天一致（S5）。
//
// 物理身份核验（samePhysicalMachine）刻意不用它：那是比对两条已存值的形态
// 差异，不是「一次输入是不是本机」，两者门控不同（B398）。
func (s *Service) isLocalMachine(machine string) bool {
	if s.selfName != "" {
		name := strings.TrimSpace(machine)
		return name == "" || name == s.selfName
	}
	return IsLocalMachine(machine)
}

// SetKnownMachines 注入「这台机器现在能不能派到」的查询。生产绑
// conf().Targets 的键；本机别名不走这张表。fn 为 nil 时只放行本机别名之外的
// 任意名——单测不持 agentd 配置时用，生产组装点必须注入。
func (s *Service) SetKnownMachines(fn func(string) bool) { s.knownMachines = fn }

// IsLocalMachine 报告 carrier.machine 是否为本机别名。空串、local、本机、
// 以及当前进程 hostname 都算本机；其余名字必须出现在 targets 里才能登记。
func IsLocalMachine(machine string) bool {
	name := strings.TrimSpace(machine)
	if name == "" || name == "local" || name == "本机" {
		return true
	}
	hostname, err := os.Hostname()
	return err == nil && name == hostname
}

// PutCarrier 以 CAS 写载体定义（expect=0 新建）。新建时忽略输入状态并落
// pending；更新时 HOME 变化会清掉旧错误并回到 pending，HOME 未变则保留旧
// 状态。这避免登记请求伪造上线，也避免编辑无关字段抹掉检测结果。
func (s *Service) PutCarrier(c Carrier, expect int) error {
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Machine) == "" || strings.TrimSpace(c.CLI) == "" {
		statusLog().Error("载体登记校验失败", "name", c.Name, "expect", expect, "cause", "name/machine/cli 必填")
		return fmt.Errorf("%w: 载体登记不完整：name/machine/cli 必填", ErrInvalid)
	}
	if strings.TrimSpace(string(c.Credential)) == "" {
		c.Credential = CredentialStandalone
	}
	if c.Credential != CredentialStandalone && c.Credential != CredentialMainHomeSync {
		statusLog().Error("载体登记校验失败", "name", c.Name, "expect", expect, "cause", "credential 来源非法")
		return fmt.Errorf("%w: 载体 %s 的凭据来源只能是 standalone 或 main_home_sync", ErrInvalid, c.Name)
	}
	if err := s.checkKnownMachine(c.Name, c.Machine, expect); err != nil {
		return err
	}
	if _, squadErr := s.Squad(c.Name); squadErr == nil {
		if err := CrossKindConflict(true); err != nil {
			statusLog().Error("载体登记跨 kind 冲突", "name", c.Name, "expect", expect, "cause", err)
			return fmt.Errorf("%w: %s", err, c.Name)
		}
	} else if !errors.Is(squadErr, ErrNotFound) {
		statusLog().Error("检查载体同名小队失败", "name", c.Name, "expect", expect, "cause", squadErr)
		return squadErr
	}
	homeChanged := false
	if expect == 0 {
		c.Status = StatusPending
		c.LastError = ""
	} else {
		rec, err := s.repo.Get(kindCarrier, c.Name)
		if err != nil {
			statusLog().Error("读取载体旧记录失败", "name", c.Name, "expect", expect, "cause", err)
			if errors.Is(err, schedclient.ErrNotFound) {
				return fmt.Errorf("载体 %s 旧记录不存在: %w", c.Name, ErrNotFound)
			}
			return fmt.Errorf("载体 %s 读取旧记录失败: %w", c.Name, err)
		}
		var previous Carrier
		if err := json.Unmarshal(rec.Body, &previous); err != nil {
			statusLog().Error("解码载体旧记录失败", "name", c.Name, "expect", expect, "version", rec.Version, "cause", err)
			return fmt.Errorf("载体 %s/%d 解码失败: %w", c.Name, rec.Version, err)
		}
		homeChanged = previous.HomeDir != c.HomeDir
		if homeChanged {
			c.Status = StatusPending
			c.LastError = ""
		} else {
			// HOME 未变时保留旧检测结果；否则仅编辑 CLI 就会把可见状态重置，
			// 而登记请求本身没有足够证据重新判定运行状态。
			c.Status = previous.Status
			c.LastError = previous.LastError
		}
	}
	statusLog().Info("写入载体", "name", c.Name, "expect", expect, "home_changed", homeChanged,
		"status", c.Status, "last_error_bytes", len(c.LastError))
	body, err := json.Marshal(c)
	if err != nil {
		statusLog().Error("编码载体失败", "name", c.Name, "expect", expect, "cause", err)
		return err
	}
	version, err := s.repo.Put(kindCarrier, c.Name, expect, body, "scheduling")
	if err != nil {
		statusLog().Error("写入载体失败", "name", c.Name, "expect", expect, "version", version, "cause", err)
		return err
	}
	statusLog().Info("写入载体成功", "name", c.Name, "expect", expect, "version", version,
		"home_changed", homeChanged, "status", c.Status, "machine", c.Machine,
		"credential", string(c.Credential), "home_empty", strings.TrimSpace(c.HomeDir) == "")
	return nil
}

// DeleteCarrier 以 CAS 语义删除未入队载体。
// 若载体名为空或仍在任一小队成员中，拒绝删除返回 ErrInvalid；
// 若载体不存在返回 ErrNotFound；若版本不匹配返回 ErrCASConflict。
func (s *Service) DeleteCarrier(name string, expect int) error {
	statusLog().Info("删除载体请求", "name", name, "expect", expect)
	if strings.TrimSpace(name) == "" {
		statusLog().Warn("删除载体校验失败", "name", name, "expect", expect, "cause", "name 必填")
		return fmt.Errorf("%w: 载体名不能为空", ErrInvalid)
	}

	// 遍历所有小队：若仍在任一小队的成员清单中，拒绝删除以防小队引用悬空。
	squadRecs, err := s.repo.List(kindSquad)
	if err != nil {
		statusLog().Error("读取小队列表失败", "name", name, "expect", expect, "cause", err)
		return fmt.Errorf("读取小队列表失败: %w", err)
	}
	for _, rec := range squadRecs {
		var q Squad
		if err := json.Unmarshal(rec.Body, &q); err != nil {
			statusLog().Error("解码小队记录失败", "id", rec.ID, "cause", err)
			return fmt.Errorf("squad/%s 解码失败: %w", rec.ID, err)
		}
		for _, m := range q.Members {
			if m.Carrier == name {
				statusLog().Warn("删除载体被拒：仍在小队中", "name", name, "expect", expect, "squad", q.Name)
				return fmt.Errorf("%w: 载体 %s 仍在小队 %s 中，请先从小队移除", ErrInvalid, name, q.Name)
			}
		}
	}

	// 检查载体是否存在，区分 NotFound 与 CAS 版本冲突。
	if _, err := s.repo.Get(kindCarrier, name); err != nil {
		if errors.Is(err, schedclient.ErrNotFound) {
			statusLog().Warn("删除载体未找到", "name", name, "expect", expect)
			return fmt.Errorf("载体 %s 旧记录不存在: %w", name, ErrNotFound)
		}
		statusLog().Error("读取载体记录失败", "name", name, "expect", expect, "cause", err)
		return fmt.Errorf("读取载体 %s 失败: %w", name, err)
	}

	if err := s.repo.Delete(kindCarrier, name, expect, "scheduling"); err != nil {
		statusLog().Error("删除载体失败", "name", name, "expect", expect, "cause", err)
		return err
	}
	statusLog().Info("删除载体成功", "name", name, "expect", expect)
	return nil
}

func (s *Service) checkKnownMachine(name, machine string, expect int) error {
	if s.isLocalMachine(machine) {
		return nil
	}
	// 有自名时，local/本机/hostname 是「本机」旧别名：拒收并点名改成自名（S6）。
	// 不落到下面的 targets 未命中错误——那会把人指向错误的方向。
	if s.selfName != "" && IsLocalMachine(machine) {
		statusLog().Error("载体登记校验失败", "name", name, "machine", machine, "expect", expect,
			"cause", "machine 是本机旧别名")
		return fmt.Errorf("%w: 载体 %s 的 carrier.machine=%q 是本机旧别名（local/本机/hostname），不再按本机解释；请改成 config.yaml 的 name: 值 %q",
			ErrInvalid, name, machine, s.selfName)
	}
	if s.knownMachines == nil {
		return nil
	}
	key := strings.TrimSpace(machine)
	if s.knownMachines(key) {
		return nil
	}
	statusLog().Error("载体登记校验失败", "name", name, "machine", key, "expect", expect, "cause", "machine 不在 targets")
	return fmt.Errorf("%w: 载体机器 %q 未在 targets 中定义", ErrInvalid, key)
}

// Carrier 读一个载体。
func (s *Service) Carrier(name string) (Carrier, error) {
	var c Carrier
	if err := s.getEntity(kindCarrier, name, &c); err != nil {
		return Carrier{}, err
	}
	return c, nil
}

// Carriers 列出全部载体（写入序稳定）。
func (s *Service) Carriers() ([]Carrier, error) {
	var out []Carrier
	return out, s.listEntities(kindCarrier, func(raw []byte) error {
		var c Carrier
		if err := json.Unmarshal(raw, &c); err != nil {
			return err
		}
		out = append(out, c)
		return nil
	})
}

// PutSquad 以 CAS 写小队定义（expect=0 新建）。成员引用必须指向已存在的载体。
func (s *Service) PutSquad(q Squad, expect int) error {
	if strings.TrimSpace(q.Name) == "" {
		return fmt.Errorf("%w: 小队名不能为空", ErrInvalid)
	}
	if q.Role != RoleExecutor && q.Role != RoleCoordinator {
		return fmt.Errorf("%w: 小队 %s 角色只能是 executor 或 coordinator", ErrInvalid, q.Name)
	}
	if _, carrierErr := s.Carrier(q.Name); carrierErr == nil {
		if err := CrossKindConflict(true); err != nil {
			statusLog().Error("小队登记跨 kind 冲突", "name", q.Name, "expect", expect, "cause", err)
			return fmt.Errorf("%w: %s", err, q.Name)
		}
	} else if !errors.Is(carrierErr, ErrNotFound) {
		statusLog().Error("检查小队同名载体失败", "name", q.Name, "expect", expect, "cause", carrierErr)
		return carrierErr
	}
	for _, m := range q.Members {
		if _, err := s.Carrier(m.Carrier); err != nil {
			return fmt.Errorf("小队 %s 成员 %s: %w", q.Name, m.Carrier, err)
		}
	}
	return s.putEntity(kindSquad, q.Name, q, expect)
}

// AdmitCarrier 对一次无小队的载体直派只占载体物理位。
// Binding.Squad 为空；HomeDir 是绑定当时的 IdentityOf 快照。
// 与 Admit 的小队两级准入不同，直派不创建小队成员计数键。
func (s *Service) AdmitCarrier(name string) (Binding, error) {
	name = strings.TrimSpace(name)
	statusLog().Info("scheduling.admit_carrier.start", "carrier", name, "error_kind", "start")
	c, err := s.Carrier(name)
	if err != nil {
		statusLog().Error("scheduling.admit_carrier.error", "carrier", name, "error_kind", "lookup", "cause", err)
		return Binding{}, err
	}
	if c.Status != StatusOnline {
		err := fmt.Errorf("%w: 载体 %s 状态为 %s", ErrNoHealthy, c.Name, c.Status)
		statusLog().Warn("scheduling.admit_carrier.error", "carrier", c.Name, "status", c.Status, "error_kind", "no_healthy", "cause", err)
		return Binding{}, err
	}
	_, carrierKey := OccupancyKeys("", c.Name)
	for attempt := 0; attempt < maxCASAttempts; attempt++ {
		cur, expect, err := s.readRunning(carrierKey)
		if err != nil {
			statusLog().Error("scheduling.admit_carrier.error", "carrier", c.Name, "carrier_key", carrierKey, "attempt", attempt+1, "error_kind", "counter_read", "cause", err)
			return Binding{}, err
		}
		running := cur.Count
		if c.MaxConcurrency > 0 && running >= c.MaxConcurrency {
			err := fmt.Errorf("%w: 载体 %s", ErrNoSlot, c.Name)
			statusLog().Warn("scheduling.admit_carrier.full", "carrier", c.Name, "carrier_key", carrierKey, "running", running, "carrier_cap", c.MaxConcurrency, "error_kind", "no_slot", "cause", err)
			return Binding{}, err
		}
		if err := s.casCount(carrierKey, expect, cur); err != nil {
			if errors.Is(err, schedclient.ErrCASConflict) {
				continue
			}
			statusLog().Error("scheduling.admit_carrier.error", "carrier", c.Name, "carrier_key", carrierKey, "attempt", attempt+1, "error_kind", "counter_write", "cause", err)
			return Binding{}, err
		}
		id := IdentityOf(c)
		b := Binding{Squad: "", Carrier: c.Name, Target: id.Machine, Executor: id.CLI, Model: c.Model, HomeDir: id.HomeDir}
		statusLog().Info("scheduling.admit_carrier.success", "carrier", c.Name, "carrier_key", carrierKey, "target", b.Target, "executor", b.Executor, "home_dir", b.HomeDir, "error_kind", "success")
		return b, nil
	}
	err = fmt.Errorf("%w: %s", ErrRetryExhausted, carrierKey)
	statusLog().Error("scheduling.admit_carrier.error", "carrier", c.Name, "carrier_key", carrierKey, "error_kind", "retry_exhausted", "cause", err)
	return Binding{}, err
}

// DefaultCarrier 读取已确认的默认载体名。没有记录或记录指向未登记载体时返回 ErrNoDefault。
func (s *Service) DefaultCarrier() (string, error) {
	statusLog().Info("scheduling.default_carrier.read.start", "kind", KindDefaultCarrier, "id", DefaultCarrierID)
	rec, err := s.repo.Get(KindDefaultCarrier, DefaultCarrierID)
	if err != nil {
		if errors.Is(err, schedclient.ErrNotFound) {
			err = ErrNoDefault
		}
		statusLog().Error("scheduling.default_carrier.read.error", "kind", KindDefaultCarrier, "id", DefaultCarrierID, "error_kind", "registry", "cause", err)
		return "", err
	}
	name, err := DecodeDefaultCarrier(rec.Body)
	if err != nil {
		statusLog().Error("scheduling.default_carrier.decode.error", "kind", KindDefaultCarrier, "id", DefaultCarrierID, "error_kind", "decode", "cause", err)
		return "", err
	}
	if _, err := s.Carrier(name); err != nil {
		if errors.Is(err, ErrNotFound) {
			err = fmt.Errorf("%w: 默认载体 %s 未登记", ErrNoDefault, name)
		}
		statusLog().Error("scheduling.default_carrier.read.error", "name", name, "error_kind", "carrier_lookup", "cause", err)
		return "", err
	}
	statusLog().Info("scheduling.default_carrier.read.success", "name", name, "kind", KindDefaultCarrier, "id", DefaultCarrierID, "error_kind", "success")
	return name, nil
}

// SetDefaultCarrier 写入默认载体 singleton；kind 为 default_carrier、id 为 current。
// 空名由 EncodeDefaultCarrier 拒绝，非载体名不写入记录。
func (s *Service) SetDefaultCarrier(name string) error {
	name = strings.TrimSpace(name)
	body, err := EncodeDefaultCarrier(name)
	if err != nil {
		statusLog().Error("scheduling.default_carrier.write.error", "name", name, "error_kind", "invalid_name", "cause", err)
		return err
	}
	if _, err := s.Carrier(name); err != nil {
		if errors.Is(err, ErrNotFound) {
			err = fmt.Errorf("%w: 默认载体 %s 未登记为载体", ErrNoDefault, name)
		}
		statusLog().Error("scheduling.default_carrier.write.error", "name", name, "error_kind", "carrier_lookup", "cause", err)
		return err
	}
	expect := 0
	if rec, getErr := s.repo.Get(KindDefaultCarrier, DefaultCarrierID); getErr == nil {
		expect = rec.Version
	} else if !errors.Is(getErr, schedclient.ErrNotFound) {
		statusLog().Error("读取默认载体版本失败", "name", name, "expect", expect, "cause", getErr)
		return getErr
	}
	version, err := s.repo.Put(KindDefaultCarrier, DefaultCarrierID, expect, body, "scheduling")
	if err != nil {
		statusLog().Error("写入默认载体失败", "name", name, "expect", expect, "version", version, "cause", err)
		return err
	}
	statusLog().Info("写入默认载体成功", "name", name, "expect", expect, "version", version, "kind", KindDefaultCarrier, "id", DefaultCarrierID, "error_kind", "success")
	return nil
}

// Squad 读一个小队。
func (s *Service) Squad(name string) (Squad, error) {
	var q Squad
	if err := s.getEntity(kindSquad, name, &q); err != nil {
		return Squad{}, err
	}
	return q, nil
}

// Admit 对一次执行者点火做解析加两级准入。满员返回 ErrNoSlot，调用方转 Enqueue。
func (s *Service) Admit(req IgnitionRequest) (Binding, error) {
	capacity := s.capacityContext(req.Squad, "")
	slog.Default().Info("scheduling.admit.start", "squad", req.Squad, "card", req.Card,
		"node", req.Node, "target", req.Target, "executor", req.Executor, "model", req.Model,
		"carrier", capacity.carrier, "member_policy", capacity.memberPolicy,
		"carrier_cap", capacity.carrierCap, "error_kind", "start")
	q, err := s.Squad(req.Squad)
	if err != nil {
		slog.Default().Error("scheduling.admit.error", "squad", req.Squad,
			"carrier", capacity.carrier, "member_policy", capacity.memberPolicy,
			"carrier_cap", capacity.carrierCap, "error_kind", "squad_lookup", "cause", err)
		return Binding{}, err
	}
	if q.Role != RoleExecutor {
		err := fmt.Errorf("%w: %s 是协调者小队", ErrRoleMismatch, q.Name)
		slog.Default().Error("scheduling.admit.error", "squad", q.Name,
			"carrier", capacity.carrier, "member_policy", capacity.memberPolicy,
			"carrier_cap", capacity.carrierCap, "error_kind", "role_mismatch", "cause", err)
		return Binding{}, err
	}
	binding, err := s.admitInto(q, req)
	if err != nil {
		slog.Default().Error("scheduling.admit.error", "squad", q.Name,
			"carrier", capacity.carrier, "member_policy", capacity.memberPolicy,
			"carrier_cap", capacity.carrierCap, "error_kind", admissionErrorKind(err), "cause", err)
		return binding, err
	}
	capacity = s.capacityContext(q.Name, binding.Carrier)
	slog.Default().Info("scheduling.admit.success", "squad", q.Name,
		"carrier", capacity.carrier, "member_policy", capacity.memberPolicy,
		"carrier_cap", capacity.carrierCap, "error_kind", "success", "card", req.Card)
	return binding, err
}

// Select 从 req.Squad 按登记顺序选择一个在线且两级计数均有空位的载体，并返回
// 当时的物理身份快照。它只读 Registry，不为后续执行预占任何运行计数；调用方
// 必须把返回的 Binding 原样交给执行侧 AdmitFrozen，容量竞态由后者的 CAS 裁决。
func (s *Service) Select(req IgnitionRequest) (Binding, error) {
	return s.SelectWhere(req, nil)
}

// SelectWhere 同 Select，但只在 accept 放行的载体里选（B452 机器亲和）。accept 为 nil 等于全放行。
//
// 谓词是**过滤**不是排序：被拒的成员视同不存在——放行集合里没有在线成员返回 ErrNoHealthy，
// 放行的在线成员全满返回 ErrNoSlot；绝不退到被拒的成员上（「不静默换机」）。
// 谓词不进 IgnitionRequest：那个结构会随排队行持久化，给它加过滤语义会改变存量排队行的行为。
func (s *Service) SelectWhere(req IgnitionRequest, accept func(Carrier) bool) (Binding, error) {
	logger := statusLog().With("squad", req.Squad, "card", req.Card, "node", req.Node,
		"target", req.Target, "executor", req.Executor, "model", req.Model)
	logger.Info("冻结身份选择开始", "error_kind", "select_start")
	if strings.TrimSpace(req.Squad) == "" {
		err := fmt.Errorf("%w: 选择小队不能为空", ErrInvalid)
		logger.Error("冻结身份选择拒绝", "error_kind", "squad_missing", "cause", err)
		return Binding{}, err
	}
	q, err := s.Squad(req.Squad)
	if err != nil {
		logger.Error("冻结身份选择读取小队失败", "error_kind", "squad_read", "cause", err)
		return Binding{}, err
	}
	if q.Role != RoleExecutor {
		err := fmt.Errorf("%w: %s 是协调者小队", ErrRoleMismatch, q.Name)
		logger.Error("冻结身份选择拒绝协调者小队", "error_kind", "role_mismatch", "cause", err)
		return Binding{}, err
	}

	anyOnline := false
	for _, member := range q.Members {
		carrier, carrierErr := s.Carrier(member.Carrier)
		if carrierErr != nil {
			if errors.Is(carrierErr, ErrNotFound) {
				logger.Warn("冻结身份选择跳过未登记载体", "carrier", member.Carrier,
					"member_key", OccupancyMemberKey(q.Name, member.Carrier),
					"carrier_key", OccupancyCarrierKey(member.Carrier), "error_kind", "carrier_missing")
				continue
			}
			logger.Error("冻结身份选择读取载体失败", "carrier", member.Carrier,
				"member_key", OccupancyMemberKey(q.Name, member.Carrier),
				"carrier_key", OccupancyCarrierKey(member.Carrier), "error_kind", "carrier_read", "cause", carrierErr)
			return Binding{}, carrierErr
		}
		if accept != nil && !accept(carrier) {
			logger.Info("冻结身份选择按谓词过滤掉载体", "carrier", carrier.Name,
				"machine", carrier.Machine, "error_kind", "carrier_filtered")
			continue
		}
		if carrier.Status != StatusOnline {
			logger.Info("冻结身份选择跳过离线载体", "carrier", carrier.Name,
				"member_key", OccupancyMemberKey(q.Name, carrier.Name),
				"carrier_key", OccupancyCarrierKey(carrier.Name), "status", carrier.Status,
				"error_kind", "carrier_offline")
			continue
		}
		anyOnline = true
		memberKey, carrierKey := OccupancyKeys(q.Name, carrier.Name)
		memberRunning, _, memberErr := s.readCount(memberKey)
		if memberErr != nil {
			logger.Error("冻结身份选择读取成员计数失败", "carrier", carrier.Name,
				"member_key", memberKey, "carrier_key", carrierKey,
				"error_kind", "member_counter_read", "cause", memberErr)
			return Binding{}, memberErr
		}
		carrierRunning, _, carrierCountErr := s.readCount(carrierKey)
		if carrierCountErr != nil {
			logger.Error("冻结身份选择读取载体计数失败", "carrier", carrier.Name,
				"member_key", memberKey, "carrier_key", carrierKey,
				"error_kind", "carrier_counter_read", "cause", carrierCountErr)
			return Binding{}, carrierCountErr
		}
		if member.MaxConcurrency > 0 && memberRunning >= member.MaxConcurrency {
			logger.Info("冻结身份选择跳过成员满员载体", "carrier", carrier.Name,
				"member_key", memberKey, "carrier_key", carrierKey,
				"running", memberRunning, "member_cap", member.MaxConcurrency,
				"error_kind", "member_full")
			continue
		}
		if carrier.MaxConcurrency > 0 && carrierRunning >= carrier.MaxConcurrency {
			logger.Info("冻结身份选择跳过载体满员载体", "carrier", carrier.Name,
				"member_key", memberKey, "carrier_key", carrierKey,
				"running", carrierRunning, "carrier_cap", carrier.MaxConcurrency,
				"error_kind", "carrier_full")
			continue
		}
		binding := bindingFor(q, carrier, req)
		logger.Info("冻结身份选择成功", "carrier", binding.Carrier,
			"member_key", memberKey, "carrier_key", carrierKey,
			"target", binding.Target, "executor", binding.Executor, "model", binding.Model,
			"home_dir", binding.HomeDir, "error_kind", "select_success")
		return binding, nil
	}
	if !anyOnline {
		err := fmt.Errorf("%w: 小队 %s 没有在线载体", ErrNoHealthy, q.Name)
		logger.Warn("冻结身份选择无在线载体", "error_kind", "no_healthy", "cause", err)
		return Binding{}, err
	}
	err = fmt.Errorf("%w: 小队 %s 所有在线载体均无空位", ErrNoSlot, q.Name)
	logger.Info("冻结身份选择无空位", "error_kind", "no_slot", "cause", err)
	return Binding{}, err
}

// rejectLegacyCarrierAlias 报告一次冻结派发准入是否撞上存量旧别名（B439 S6）。
// 本机已配自名（selfName 非空）时，载体记录里的 machine 或冻结的 Binding.Target
// 是 local/本机/hostname 即拒发，给可行动错误（点名 carrier、当前值与改成什么）。
// 无自名或未注入判据时返回 nil（S5，沿今天行为）。
//
// 为什么必须在占用写入之前：旧别名经 samePhysicalMachine 的别名语义会被静默
// 当本机放行（两值都是本机别名时判等为真），那正是本卡要杀的缺陷族；拒发若晚于
// acquire，就已写下一份占用又回滚，留下状态迁移与日志噪声。
func (s *Service) rejectLegacyCarrierAlias(binding Binding, registeredMachine string) error {
	if s.selfName == "" || s.legacyAlias == nil {
		return nil
	}
	if s.legacyAlias(registeredMachine) {
		return fmt.Errorf("%w: 载体 %s 的 machine=%q 是本机旧别名（local/本机/hostname），不再按本机解释；请把 carrier %s 的 machine 从 %q 改成 %q",
			ErrInvalid, binding.Carrier, registeredMachine, binding.Carrier, registeredMachine, s.selfName)
	}
	if s.legacyAlias(binding.Target) {
		return fmt.Errorf("%w: 冻结绑定的 target=%q 是本机旧别名（local/本机/hostname），不再按本机解释；请把冻结绑定 target 从 %q 改成 %q（或把 carrier %s 的 machine 改成 %q）",
			ErrInvalid, binding.Target, binding.Target, s.selfName, binding.Carrier, s.selfName)
	}
	return nil
}

// AdmitFrozen 按 Binding.Carrier 对冻结载体执行一次两级 CAS 准入，并核验绑定时
// 的机器、CLI、HOME 与载体登记仍一致。它绝不按 Binding.Squad 重新选择成员；
// Squad 为空时只占载体物理键，不产生成员键。
func (s *Service) AdmitFrozen(binding Binding) (Binding, error) {
	memberKey, carrierKey := OccupancyKeys(binding.Squad, binding.Carrier)
	logger := statusLog().With("squad", binding.Squad, "carrier", binding.Carrier,
		"target", binding.Target, "executor", binding.Executor, "model", binding.Model,
		"home_dir", binding.HomeDir, "member_key", memberKey, "carrier_key", carrierKey)
	logger.Info("冻结载体准入开始", "error_kind", "admit_frozen_start")
	if strings.TrimSpace(binding.Carrier) == "" {
		err := fmt.Errorf("%w: 冻结载体不能为空", ErrInvalid)
		logger.Error("冻结载体准入拒绝", "error_kind", "carrier_missing", "cause", err)
		return Binding{}, err
	}
	carrier, err := s.Carrier(binding.Carrier)
	if err != nil {
		logger.Error("冻结载体读取失败", "error_kind", "carrier_read", "cause", err)
		return Binding{}, err
	}
	if carrier.Status != StatusOnline {
		err := fmt.Errorf("%w: 载体 %s 当前状态为 %s", ErrNoHealthy, binding.Carrier, carrier.Status)
		logger.Warn("冻结载体不在线", "error_kind", "carrier_offline", "cause", err)
		return Binding{}, err
	}

	q := Squad{}
	member := SquadMember{Carrier: binding.Carrier}
	if strings.TrimSpace(binding.Squad) != "" {
		q, err = s.Squad(binding.Squad)
		if err != nil {
			logger.Error("冻结小队读取失败", "error_kind", "squad_read", "cause", err)
			return Binding{}, err
		}
		if q.Role != RoleExecutor {
			err := fmt.Errorf("%w: %s 是协调者小队", ErrRoleMismatch, q.Name)
			logger.Error("冻结载体准入拒绝协调者小队", "error_kind", "role_mismatch", "cause", err)
			return Binding{}, err
		}
		found := false
		for _, candidate := range q.Members {
			if candidate.Carrier == binding.Carrier {
				member = candidate
				found = true
				break
			}
		}
		if !found {
			err := fmt.Errorf("%w: 载体 %s 不在小队 %s", ErrRoleMismatch, binding.Carrier, binding.Squad)
			logger.Warn("冻结载体不是小队成员", "error_kind", "member_mismatch", "cause", err)
			return Binding{}, err
		}
	}
	identity := IdentityOf(carrier)
	// B439 S6：有自名时，存量 carrier 的 machine（或冻结 Binding.Target）是本机
	// 旧别名即拒发——必须在同一物理核验与 acquire 之前，避免经 samePhysicalMachine
	// 的别名语义被静默当本机放行、并防止先占后滚留下状态迁移。
	if aerr := s.rejectLegacyCarrierAlias(binding, identity.Machine); aerr != nil {
		logger.Warn("冻结载体准入拒绝本机旧别名", "registered_target", identity.Machine,
			"error_kind", "legacy_alias", "cause", aerr)
		return Binding{}, aerr
	}
	if !samePhysicalMachine(binding.Target, identity.Machine) || binding.Executor != identity.CLI || binding.HomeDir != identity.HomeDir {
		err := fmt.Errorf("%w: 冻结物理身份与载体登记不一致", ErrRoleMismatch)
		logger.Warn("冻结物理身份不匹配", "registered_target", identity.Machine,
			"registered_executor", identity.CLI, "registered_home_dir", identity.HomeDir,
			"error_kind", "physical_mismatch", "cause", err)
		return Binding{}, err
	}

	admitted, err := s.acquire(q, member, carrier, IgnitionRequest{
		Squad: binding.Squad, Model: binding.Model, Actor: "admit_frozen",
	})
	if errors.Is(err, errMemberFull) {
		err = ErrNoSlot
	}
	if err != nil {
		memberKey, carrierKey := OccupancyKeys(binding.Squad, binding.Carrier)
		logger.Error("冻结载体准入失败", "member_key", memberKey, "carrier_key", carrierKey,
			"error_kind", admissionErrorKind(err), "cause", err)
		return Binding{}, err
	}
	// acquire 共用普通准入的绑定投影；冻结准入的模型也必须是当时快照，
	// 即使载体登记在 Select 与本次 CAS 之间已被更新，也不能重新取当前模型。
	admitted.Model = binding.Model
	logger.Info("冻结载体准入成功", "member_key", OccupancyMemberKey(admitted.Squad, admitted.Carrier),
		"carrier_key", OccupancyCarrierKey(admitted.Carrier), "target", admitted.Target,
		"executor", admitted.Executor, "model", admitted.Model, "home_dir", admitted.HomeDir,
		"error_kind", "admit_frozen_success")
	return admitted, nil
}

// samePhysicalMachine 把空串、local、本机和当前 hostname 统一看作本 agentd。
// 冻结目标的原始登记名仍保留在 Binding/Task 中；这里只在物理核验时消除本机
// 别名差异，避免 client 直连 canonical 化后把同一台机器误判成两台。
func samePhysicalMachine(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if IsLocalMachine(left) && IsLocalMachine(right) {
		return true
	}
	return left == right
}

// AdmitSeatCarrier 按冻结载体对协调者小队做一次两级 CAS 准入：载体由调用方
// 指定（来自承载记录），不做候选遍历与打分。与 LaunchAdmit 的唯一差别是
// 「载体由调用方冻结指定」；与 AdmitFrozen 的差别是协调者小队合法（后者明确
// 拒绝协调者小队）。carrier 必须是该小队成员，否则 ErrRoleMismatch。
func (s *Service) AdmitSeatCarrier(squad, carrier string) (Binding, error) {
	logger := statusLog().With("squad", squad, "carrier", carrier)
	logger.Info("冻结载体协调者准入开始", "error_kind", "admit_seat_start")
	if strings.TrimSpace(squad) == "" || strings.TrimSpace(carrier) == "" {
		err := fmt.Errorf("%w: 冻结载体协调者准入需要小队与载体", ErrInvalid)
		logger.Error("冻结载体协调者准入参数缺失", "error_kind", "params_missing", "cause", err)
		return Binding{}, err
	}
	q, err := s.Squad(squad)
	if err != nil {
		logger.Error("冻结载体协调者小队读取失败", "error_kind", "squad_read", "cause", err)
		return Binding{}, err
	}
	if q.Role != RoleCoordinator {
		err := fmt.Errorf("%w: %s 是执行者小队", ErrRoleMismatch, q.Name)
		logger.Error("冻结载体协调者准入角色不符", "error_kind", "role_mismatch", "cause", err)
		return Binding{}, err
	}
	member := SquadMember{Carrier: carrier}
	found := false
	for _, candidate := range q.Members {
		if candidate.Carrier == carrier {
			member, found = candidate, true
			break
		}
	}
	if !found {
		err := fmt.Errorf("%w: 载体 %s 不在小队 %s", ErrRoleMismatch, carrier, q.Name)
		logger.Warn("冻结载体不是协调者小队成员", "error_kind", "member_mismatch", "cause", err)
		return Binding{}, err
	}
	c, err := s.Carrier(carrier)
	if err != nil {
		logger.Error("冻结载体读取失败", "error_kind", "carrier_read", "cause", err)
		return Binding{}, err
	}
	if c.Status != StatusOnline {
		err := fmt.Errorf("%w: 载体 %s 当前状态为 %s", ErrNoHealthy, carrier, c.Status)
		logger.Warn("冻结载体不在线", "error_kind", "carrier_offline", "cause", err)
		return Binding{}, err
	}
	binding, err := s.acquire(q, member, c, IgnitionRequest{Squad: q.Name, Actor: "admit_seat_carrier"})
	if errors.Is(err, errMemberFull) {
		err = ErrNoSlot
	}
	if err != nil {
		logger.Error("冻结载体协调者准入失败", "error_kind", admissionErrorKind(err), "cause", err)
		return Binding{}, err
	}
	logger.Info("冻结载体协调者准入成功", "member_key", OccupancyMemberKey(binding.Squad, binding.Carrier),
		"carrier_key", OccupancyCarrierKey(binding.Carrier),
		"target", binding.Target, "executor", binding.Executor, "error_kind", "admit_seat_success")
	return binding, nil
}

// LaunchAdmit 对一次协调者拉起做两级准入（协调者小队的成员载体必须在协调机上，
// 该约束由配置审核保证，本域只管计数）。
func (s *Service) LaunchAdmit(squadName string) (Binding, error) {
	capacity := s.capacityContext(squadName, "")
	slog.Default().Info("scheduling.launch_admit.start", "squad", squadName,
		"carrier", capacity.carrier, "member_policy", capacity.memberPolicy,
		"carrier_cap", capacity.carrierCap, "error_kind", "start")
	q, err := s.Squad(squadName)
	if err != nil {
		slog.Default().Error("scheduling.launch_admit.error", "squad", squadName,
			"carrier", capacity.carrier, "member_policy", capacity.memberPolicy,
			"carrier_cap", capacity.carrierCap, "error_kind", "squad_lookup", "cause", err)
		return Binding{}, err
	}
	if q.Role != RoleCoordinator {
		err := fmt.Errorf("%w: %s 是执行者小队", ErrRoleMismatch, q.Name)
		slog.Default().Error("scheduling.launch_admit.error", "squad", q.Name,
			"carrier", capacity.carrier, "member_policy", capacity.memberPolicy,
			"carrier_cap", capacity.carrierCap, "error_kind", "role_mismatch", "cause", err)
		return Binding{}, err
	}
	binding, err := s.admitInto(q, IgnitionRequest{Squad: q.Name})
	if err != nil {
		slog.Default().Error("scheduling.launch_admit.error", "squad", q.Name,
			"carrier", capacity.carrier, "member_policy", capacity.memberPolicy,
			"carrier_cap", capacity.carrierCap, "error_kind", admissionErrorKind(err), "cause", err)
		return binding, err
	}
	capacity = s.capacityContext(q.Name, binding.Carrier)
	slog.Default().Info("scheduling.launch_admit.success", "squad", q.Name,
		"carrier", capacity.carrier, "member_policy", capacity.memberPolicy,
		"carrier_cap", capacity.carrierCap, "error_kind", "success")
	return binding, err
}

// Release 归还一个已结束回合占用的两级名额。幂等：计数到 0 后不再下探。
// 空小队是载体直派，必须跳过成员键，避免释放一个从未存在的 squad//carrier 计数。
func (s *Service) Release(squadName, carrierName string) error {
	capacity := s.capacityContext(squadName, carrierName)
	slog.Default().Info("scheduling.release.start", "squad", squadName, "carrier", carrierName,
		"member_policy", capacity.memberPolicy, "carrier_cap", capacity.carrierCap,
		"error_kind", "start")
	memberKey, carrierKey := OccupancyKeys(squadName, carrierName)
	if memberKey != "" {
		if err := s.stepRunning(memberKey, -1); err != nil {
			slog.Default().Error("scheduling.release.error", "squad", squadName,
				"carrier", carrierName, "member_policy", capacity.memberPolicy,
				"carrier_cap", capacity.carrierCap, "error_kind", "squad_counter", "cause", err)
			return err
		}
	}
	if carrierKey != "" {
		if err := s.stepRunning(carrierKey, -1); err != nil {
			slog.Default().Error("scheduling.release.error", "squad", squadName,
				"carrier", carrierName, "member_policy", capacity.memberPolicy,
				"carrier_cap", capacity.carrierCap, "error_kind", "carrier_counter", "cause", err)
			return err
		}
	}
	slog.Default().Info("scheduling.release.success", "squad", squadName, "carrier", carrierName,
		"member_policy", capacity.memberPolicy, "carrier_cap", capacity.carrierCap,
		"member_key", memberKey, "carrier_key", carrierKey, "error_kind", "success")
	return nil
}

// Enqueue 把一次满员的点火请求持久入队。同一（卡，节点）重复排队按更新处理
// 并保留原入队序（body.Seq 不变），位置按当前队序重算返回。
func (s *Service) Enqueue(req IgnitionRequest, kind string) (position int, err error) {
	if kind != KindIgnitionQueue && kind != KindLaunchQueue {
		return 0, fmt.Errorf("未知的排队种类 %q", kind)
	}
	id := queueID(req)
	existing, getErr := s.repo.Get(kind, id)
	seq := int64(0)
	expect := 0
	switch {
	case getErr == nil:
		var old queuedEntry
		if json.Unmarshal(existing.Body, &old) == nil {
			seq = old.Seq
		}
		expect = existing.Version
	case errors.Is(getErr, schedclient.ErrCASConflict), errors.Is(getErr, schedclient.ErrNotFound):
		// 不存在或并发下刚被取走：走新建分支，CAS 冲突交给 Put 兜底
	default:
		return 0, getErr
	}
	body, err := json.Marshal(queuedEntry{Req: req, Seq: seq})
	if err != nil {
		return 0, err
	}
	if _, err := s.repo.Put(kind, id, expect, body, req.Actor); err != nil {
		return 0, err
	}
	return s.position(kind, id), nil
}

// PopReady 取走队列头部的下一个请求（就绪度 → 卡优先级 → FIFO）。
// 队列为空返回 (zero, false, nil)。取走即删除（CAS 版本防并发双取）。
func (s *Service) PopReady(kind string) (IgnitionRequest, bool, error) {
	var zero IgnitionRequest
	entries, err := s.repo.List(kind)
	if err != nil {
		return zero, false, err
	}
	type ranked struct {
		rec  schedclient.Record
		req  IgnitionRequest
		seq  int64
		rank int
	}
	items := make([]ranked, 0, len(entries))
	for _, rec := range entries {
		var e queuedEntry
		if err := json.Unmarshal(rec.Body, &e); err != nil {
			return zero, false, fmt.Errorf("queue %s/%s 解码失败: %w", kind, rec.ID, err)
		}
		items = append(items, ranked{rec: rec, req: e.Req, seq: e.Seq, rank: e.Req.orderRank()})
	}
	if len(items) == 0 {
		return zero, false, nil
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].req.Ready != items[j].req.Ready {
			return items[i].req.Ready
		}
		if items[i].rank != items[j].rank {
			return items[i].rank < items[j].rank
		}
		if items[i].seq != items[j].seq {
			return items[i].seq < items[j].seq
		}
		return items[i].rec.Seq < items[j].rec.Seq
	})
	head := items[0]
	if err := s.repo.Delete(kind, head.rec.ID, head.rec.Version, head.req.Actor); err != nil {
		return zero, false, err
	}
	return head.req, true, nil
}

// queuedEntry 是队列行的信封：Seq 定格首次入队序（拍板记录②的落点之一）。
type queuedEntry struct {
	Req IgnitionRequest `json:"req"`
	Seq int64           `json:"seq"`
}

// queueID 是队列行的主键：卡号，节点级排队加节点名。
func queueID(req IgnitionRequest) string {
	if req.Node == "" {
		return req.Card
	}
	return req.Card + "|" + req.Node
}

// orderRank 返回优先级排名：高=0 中=1 低=2，未知词一律垫底（卡优先级是自由
// 文本，cards.go 默认「中」，这里只认出厂词表）。
func (r IgnitionRequest) orderRank() int {
	switch r.Priority {
	case "高":
		return 0
	case "", "中":
		return 1
	case "低":
		return 2
	default:
		return 3
	}
}

// errMemberFull 是 admitInto 的内部分流哨兵：单个成员满员不是终局，调用方
// 换下一成员继续；它绝不外露，外露的满员语义只有 ErrNoSlot。
var errMemberFull = errors.New("scheduling: 该成员并发已满")

type capacityLogContext struct {
	carrier      string
	memberPolicy int
	carrierCap   int
}

// capacityContext 为入口日志尽力补齐实际成员政策和载体物理上限；日志读取失败不
// 改变准入/释放结果，避免可观测性反过来成为调度路径的第二个错误出口。
func (s *Service) capacityContext(squadName, carrierName string) capacityLogContext {
	context := capacityLogContext{carrier: carrierName}
	q, err := s.Squad(squadName)
	if err == nil {
		for _, member := range q.Members {
			if carrierName != "" && member.Carrier != carrierName {
				continue
			}
			context.carrier = member.Carrier
			context.memberPolicy = member.MaxConcurrency
			break
		}
	}
	if context.carrier != "" {
		if carrier, err := s.Carrier(context.carrier); err == nil {
			context.carrierCap = carrier.MaxConcurrency
		}
	}
	return context
}

// admitInto 按成员声明顺序对每个健康载体尝试原子准入：某成员满员不终局，
// 换下一个继续；健康成员全部满员才 ErrNoSlot，一个健康成员都没有才
// ErrNoHealthy（两种失败处置不同：前者是常态排队，后者是配置或限额问题）。
//
// 为什么按成员循环而不是「先挑一个成员、再对它 +1」：两步之间隔着若干次共享
// 读，高争用下准入者拿着过时的成员选择撞上终局 ErrNoSlot——另一成员明明有位
// 也报满员，违反冻结准入判据「小队有位 且 载体有位」（本卡台账实测：字面实现
// N=16 打容量 4 只进 2~3 个）。权威裁决只在计数 CAS 里做，成员选择随之在同一
// 趟里完成。
func (s *Service) admitInto(q Squad, req IgnitionRequest) (Binding, error) {
	anyOnline := false
	for _, member := range q.Members {
		carrier, err := s.Carrier(member.Carrier)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			statusLog().Error("读取准入载体失败", "squad", q.Name, "carrier", member.Carrier, "cause", err)
			return Binding{}, err
		}
		if carrier.Status != StatusOnline {
			continue
		}
		anyOnline = true
		binding, err := s.acquire(q, member, carrier, req)
		switch {
		case err == nil:
			return binding, nil
		case errors.Is(err, errMemberFull):
			continue
		default:
			statusLog().Error("准入载体失败", "squad", q.Name, "carrier", member.Carrier, "cause", err)
			return Binding{}, err
		}
	}
	if !anyOnline {
		statusLog().Warn("准入无已上线载体", "squad", q.Name, "members", len(q.Members))
		return Binding{}, ErrNoHealthy
	}
	statusLog().Info("准入无空槽", "squad", q.Name)
	return Binding{}, ErrNoSlot
}

// acquire 对「小队 q + 成员 m + 载体 c」做一次原子准入尝试（B156.3 契约岔口三·方案A）：
// 每次先读两级计数并各自对上限复核，任一级满员返回 errMemberFull，再以 CAS
// 递增；冲突则带着新读数重试。两级上界判断只此一份——它是防超发的唯一执法
// 闸，去掉它并发准入必超发（变异复验的靶子）。
//
// 计数键仍只有 squad/<队>/<载体> 与 carrier/<载体> 两类，按（角色,名字）二元组计数不按卡
// （B292 修订契约 §3），不得引入第三类。MaxConcurrency<=0 视为不设上限：两个
// 上界分支恒不走时，本函数等价于无上界的 CAS 直增。
//
// 半程回滚：小队侧 +1 成功而载体侧失败时，用带符号减法回滚小队侧而不是按版本
// 回写——期间可能有第三方又动了小队计数，按版本回写会把别人的增量踩掉。回滚
// 失败只吞掉：计数偏高是保守方向（多拒不超发），偏低才会超发。
func (s *Service) acquire(q Squad, member SquadMember, c Carrier, req IgnitionRequest) (Binding, error) {
	squadKey, carrierKey := OccupancyKeys(q.Name, member.Carrier)
	for attempt := 0; attempt < maxCASAttempts; attempt++ {
		squadBody, squadExpect := runningBody{}, 0
		if squadKey != "" {
			var err error
			squadBody, squadExpect, err = s.readRunning(squadKey)
			if err != nil {
				slog.Default().Error("scheduling.acquire.error", "squad", q.Name,
					"carrier", c.Name, "member_policy", member.MaxConcurrency,
					"carrier_cap", c.MaxConcurrency, "member_key", squadKey,
					"error_kind", "member_counter_read", "cause", err)
				return Binding{}, err
			}
		}
		carrierBody, carrierExpect, err := s.readRunning(carrierKey)
		if err != nil {
			slog.Default().Error("scheduling.acquire.error", "squad", q.Name,
				"carrier", c.Name, "member_policy", member.MaxConcurrency,
				"carrier_cap", c.MaxConcurrency, "error_kind", "carrier_counter_read", "cause", err)
			return Binding{}, err
		}
		squadRunning, carrierRunning := squadBody.Count, carrierBody.Count
		if squadKey != "" && member.MaxConcurrency > 0 && squadRunning >= member.MaxConcurrency {
			slog.Default().Warn("scheduling.acquire.member_full", "squad", q.Name,
				"carrier", c.Name, "member_policy", member.MaxConcurrency,
				"carrier_cap", c.MaxConcurrency, "error_kind", "member_full")
			return Binding{}, errMemberFull
		}
		if c.MaxConcurrency > 0 && carrierRunning >= c.MaxConcurrency {
			slog.Default().Warn("scheduling.acquire.carrier_full", "squad", q.Name,
				"carrier", c.Name, "member_policy", member.MaxConcurrency,
				"carrier_cap", c.MaxConcurrency, "error_kind", "carrier_full")
			return Binding{}, errMemberFull
		}
		if squadKey != "" {
			if err := s.casCount(squadKey, squadExpect, squadBody); err != nil {
				if errors.Is(err, schedclient.ErrCASConflict) {
					continue
				}
				slog.Default().Error("scheduling.acquire.error", "squad", q.Name,
					"carrier", c.Name, "member_policy", member.MaxConcurrency,
					"carrier_cap", c.MaxConcurrency, "member_key", squadKey,
					"error_kind", "member_counter_write", "cause", err)
				return Binding{}, err
			}
		}
		if err := s.casCount(carrierKey, carrierExpect, carrierBody); err != nil {
			if squadKey != "" {
				if rollbackErr := s.stepRunning(squadKey, -1); rollbackErr != nil {
					slog.Default().Error("scheduling.acquire.rollback_error", "squad", q.Name,
						"carrier", c.Name, "member_key", squadKey, "carrier_key", carrierKey,
						"error_kind", "member_rollback", "cause", rollbackErr)
				}
			}
			if errors.Is(err, schedclient.ErrCASConflict) {
				continue
			}
			slog.Default().Error("scheduling.acquire.error", "squad", q.Name,
				"carrier", c.Name, "member_policy", member.MaxConcurrency,
				"carrier_cap", c.MaxConcurrency, "error_kind", "carrier_counter_write", "cause", err)
			return Binding{}, err
		}
		binding := bindingFor(q, c, req)
		slog.Default().Info("scheduling.acquire.success", "squad", q.Name,
			"carrier", c.Name, "member_policy", member.MaxConcurrency,
			"carrier_cap", c.MaxConcurrency, "target", binding.Target,
			"executor", binding.Executor, "model", binding.Model)
		return binding, nil
	}
	err := fmt.Errorf("%w: %s 与 %s", ErrRetryExhausted, squadKey, carrierKey)
	slog.Default().Error("scheduling.acquire.error", "squad", q.Name,
		"carrier", c.Name, "member_policy", member.MaxConcurrency,
		"carrier_cap", c.MaxConcurrency, "error_kind", "retry_exhausted", "cause", err)
	return Binding{}, err
}

func admissionErrorKind(err error) string {
	switch {
	case errors.Is(err, ErrNoSlot):
		return "no_slot"
	case errors.Is(err, ErrNoHealthy):
		return "no_healthy"
	case errors.Is(err, ErrRetryExhausted):
		return "retry_exhausted"
	default:
		return "admission"
	}
}

// bindingFor 产出绑定快照：物理身份来自载体，只有模型接受请求覆盖。
// 只在两级计数都成功递增后调用；请求侧物理字段由网关在此之前校验。
func bindingFor(q Squad, c Carrier, req IgnitionRequest) Binding {
	id := IdentityOf(c)
	return Binding{
		Squad:    q.Name,
		Carrier:  c.Name,
		Target:   id.Machine,
		Executor: id.CLI,
		Model:    EffectiveModel(c.Model, req.Model),
		HomeDir:  id.HomeDir,
	}
}

// runningBody 是 sched_running/<key> 行的 body（B448 r1 冻结 F38–F44）。
//
// Count 是准入判据的唯一读数（F40）；Holders 是「哪个 agentd 自名名下占着几个」的
// 持有分布，只服务启动对账（ReclaimLocalOccupancy），准入与满员判断不读它。
// Holders 缺失 / {} / null 同义（F39），写出时空分布省略字段，于是无持有的 body
// 与旧二进制写出的 {"count":N} 逐字节相同。旧读者只解 count、忽略未知字段（F41）；
// 旧写者每写一次会丢掉整个 Holders——那部分占用退化为「无主保留」，只少扣不多扣。
type runningBody struct {
	Count   int            `json:"count"`
	Holders map[string]int `json:"holders,omitempty"`
}

// decodeRunning 解一条 sched_running 记录；失败带 key 与 version（F49 的排障上下文）。
func decodeRunning(rec schedclient.Record) (runningBody, error) {
	var b runningBody
	if err := json.Unmarshal(rec.Body, &b); err != nil {
		return runningBody{}, fmt.Errorf("运行计数 %s（version=%d）解码失败: %w", rec.ID, rec.Version, err)
	}
	return b, nil
}

// stepped 返回 count 与 holders[holder] 同步加 delta 后的新 body（F44），不改原值。
// count 下限 0；holder 条目减到 0 即删键。其它持有者条目原值照抄（F43）——读改写
// 必须基于本次读到的 body，不能拿旧读数回写，否则会抹掉并发写入的他机条目。
// holder 为空（自名与 hostname 都取不到）时只动 count，退化为无主占用。
func (b runningBody) stepped(holder string, delta int) runningBody {
	next := runningBody{Count: b.Count + delta}
	if next.Count < 0 {
		next.Count = 0
	}
	next.Holders = make(map[string]int, len(b.Holders)+1)
	for k, v := range b.Holders {
		next.Holders[k] = v
	}
	if holder != "" {
		if n := next.Holders[holder] + delta; n > 0 {
			next.Holders[holder] = n
		} else {
			delete(next.Holders, holder)
		}
	}
	if len(next.Holders) == 0 {
		next.Holders = nil
	}
	return next
}

// reclaimed 按 F47 计算本机遗留归还：d = min(max(0, h−owned), count)，
// holders[self] 写成 min(h, owned)。changed=false 表示无需写（version 不变）。
// owned 是本机仍存活任务落在该键上的数目——这些名额日后会经 Release 正常归还，
// 对账再扣一次就是重复扣（计数偏低 → 超发），所以必须留着（Q-r1-2 甲）。
func (b runningBody) reclaimed(self string, owned int) (next runningBody, deducted int, changed bool) {
	h := b.Holders[self]
	if owned < 0 {
		owned = 0
	}
	deducted = h - owned
	if deducted < 0 {
		deducted = 0
	}
	if deducted > b.Count {
		deducted = b.Count
	}
	keep := h
	if owned < keep {
		keep = owned
	}
	if deducted == 0 && keep == h {
		return b, 0, false
	}
	next = runningBody{Count: b.Count - deducted, Holders: make(map[string]int, len(b.Holders))}
	for k, v := range b.Holders {
		next.Holders[k] = v
	}
	if keep > 0 {
		next.Holders[self] = keep
	} else {
		delete(next.Holders, self)
	}
	if len(next.Holders) == 0 {
		next.Holders = nil
	}
	return next, deducted, true
}

// holderName 是本进程写入持有分布时用的名字（F45 / Q-r1-3 甲）：自名（cfg.Name）
// 优先，空则 os.Hostname()——与账本镜像 holder 的回退同口径。两者都取不到返回空，
// 调用方据此只动 count 不记持有。
func (s *Service) holderName() string {
	if s.selfName != "" {
		return s.selfName
	}
	host, err := os.Hostname()
	if err != nil {
		slog.Default().Warn("scheduling.holder_name.hostname_error", "error_kind", "hostname", "cause", err)
		return ""
	}
	return host
}

// readRunning 读一条运行计数的 body 与 CAS 期望版本；缺失视为空 body、期望版本 0
// （agentd 重启后计数随用随建，权威状态在账本不在内存）。
func (s *Service) readRunning(id string) (runningBody, int, error) {
	rec, err := s.repo.Get(kindRunning, id)
	if err != nil {
		if errors.Is(err, schedclient.ErrNotFound) {
			return runningBody{}, 0, nil
		}
		return runningBody{}, 0, err
	}
	rec.ID = id
	body, err := decodeRunning(rec)
	if err != nil {
		return runningBody{}, 0, err
	}
	return body, rec.Version, nil
}

// readCount 只取运行计数的 count 与期望版本（只读选择路径用；准入判据只看 count，F40）。
func (s *Service) readCount(id string) (count, expect int, err error) {
	body, expect, err := s.readRunning(id)
	return body.Count, expect, err
}

// casCount 以 CAS 把一条运行计数写成 cur 加一个本机名额（count+1、holders[self]+1，
// 同一次写，F44）；冲突原样透传 schedclient.ErrCASConflict 供调用方带新读数重试。
func (s *Service) casCount(id string, expect int, cur runningBody) error {
	bodyBytes, _ := json.Marshal(cur.stepped(s.holderName(), 1))
	_, err := s.repo.Put(kindRunning, id, expect, bodyBytes, "scheduling")
	return err
}

// stepRunning 对一条运行计数记录做带符号增量（count 与 holders[self] 同步），CAS 重试读改写。
func (s *Service) stepRunning(id string, delta int) error {
	holder := s.holderName()
	for attempt := 0; attempt < maxCASAttempts; attempt++ {
		body, expect, err := s.readRunning(id)
		if err != nil {
			return err
		}
		bodyBytes, _ := json.Marshal(body.stepped(holder, delta))
		if _, err := s.repo.Put(kindRunning, id, expect, bodyBytes, "scheduling"); err != nil {
			if errors.Is(err, schedclient.ErrCASConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("%w: %s", ErrRetryExhausted, id)
}

// ReclaimedKey 是一把被对账改写的计数键的明细。
type ReclaimedKey struct {
	Key      string
	Version  int // 写入前版本
	Deducted int // 本次归还的名额数
	Count    int // 写后保留的 count
	Owned    int // 本机仍存活任务在该键上的数目
}

// ReclaimReport 是一次本机遗留归还的结果。
type ReclaimReport struct {
	Self      string         // 本次对账认定的本机持有者名
	Rows      int            // 扫描的 sched_running 行数
	Reclaimed []ReclaimedKey // 实际改写的键
	Deducted  int            // 归还名额总数
	Skipped   []string       // 写失败（CAS 耗尽 / Put 报错 / 重读失败）而跳过的键
}

// ReclaimLocalOccupancy 把上一进程遗留在本机名下的占用归还（B448 S7，F46/F47）。
//
// 参数 owned：键 → 本机任务表里非终态、带载体的任务落在该键上的数目（由调用方按
// OccupancyKeys(task.Squad, task.Carrier) 计数，与 Release 归还时同源）。
//
// 规则只动 count 与 holders[self]：他机条目、无主部分（旧格式 / 旧写者丢掉的持有）
// 一个不动。调用时序由调用方保证：必须先于本进程的任何准入（F48），否则会把本进程
// 刚准入、尚未记进 owned 的活名额当遗留扣掉。
//
// 返回 error 仅在列表读失败或 body 解码失败（F49，带 key/version）；单键写失败按
// Q-r1-4 甲记 Error 跳过——写不进去的后果就是今天的现状（遗留保留到下次重启）。
func (s *Service) ReclaimLocalOccupancy(owned map[string]int) (ReclaimReport, error) {
	self := s.holderName()
	report := ReclaimReport{Self: self}
	log := slog.Default().With("self", self)
	log.Info("scheduling.reclaim.start", "owned_keys", len(owned), "error_kind", "start")
	if self == "" {
		log.Warn("scheduling.reclaim.skip", "error_kind", "no_holder_name",
			"cause", "自名与 hostname 都为空，本机名下无可认领的持有")
		return report, nil
	}
	rows, err := s.repo.List(kindRunning)
	if err != nil {
		log.Error("scheduling.reclaim.error", "error_kind", "running_list", "cause", err)
		return report, fmt.Errorf("对账读取 sched_running: %w", err)
	}
	report.Rows = len(rows)
	for _, row := range rows {
		rec := row
		body, err := decodeRunning(rec)
		if err != nil {
			log.Error("scheduling.reclaim.error", "key", rec.ID, "version", rec.Version,
				"error_kind", "decode", "cause", err)
			return report, err
		}
		for attempt := 1; ; attempt++ {
			next, deducted, changed := body.reclaimed(self, owned[rec.ID])
			if !changed {
				log.Info("scheduling.reclaim.keep", "key", rec.ID, "version", rec.Version,
					"count", body.Count, "held", body.Holders[self], "owned", owned[rec.ID],
					"error_kind", "nothing_to_reclaim")
				break
			}
			if attempt > maxCASAttempts {
				log.Error("scheduling.reclaim.error", "key", rec.ID, "version", rec.Version,
					"deduct", deducted, "error_kind", "retry_exhausted",
					"cause", fmt.Errorf("%w: %s", ErrRetryExhausted, rec.ID))
				report.Skipped = append(report.Skipped, rec.ID)
				break
			}
			bodyBytes, _ := json.Marshal(next)
			_, err := s.repo.Put(kindRunning, rec.ID, rec.Version, bodyBytes, "scheduling")
			if err == nil {
				log.Warn("scheduling.reclaim.reclaimed", "key", rec.ID, "version", rec.Version,
					"deducted", deducted, "kept", next.Count, "held_before", body.Holders[self],
					"owned", owned[rec.ID], "error_kind", "reclaimed")
				report.Reclaimed = append(report.Reclaimed, ReclaimedKey{Key: rec.ID, Version: rec.Version,
					Deducted: deducted, Count: next.Count, Owned: owned[rec.ID]})
				report.Deducted += deducted
				break
			}
			if !errors.Is(err, schedclient.ErrCASConflict) {
				log.Error("scheduling.reclaim.error", "key", rec.ID, "version", rec.Version,
					"deduct", deducted, "error_kind", "write", "cause", err)
				report.Skipped = append(report.Skipped, rec.ID)
				break
			}
			// CAS 冲突：他机在读后写前动了这一行。重读重算，绝不拿旧读数回写。
			fresh, getErr := s.repo.Get(kindRunning, rec.ID)
			if getErr != nil {
				if errors.Is(getErr, schedclient.ErrNotFound) {
					log.Info("scheduling.reclaim.keep", "key", rec.ID, "error_kind", "row_gone")
					break
				}
				log.Error("scheduling.reclaim.error", "key", rec.ID, "version", rec.Version,
					"deduct", deducted, "error_kind", "reread", "cause", getErr)
				report.Skipped = append(report.Skipped, rec.ID)
				break
			}
			fresh.ID = rec.ID
			rec = fresh
			if body, err = decodeRunning(rec); err != nil {
				log.Error("scheduling.reclaim.error", "key", rec.ID, "version", rec.Version,
					"error_kind", "decode", "cause", err)
				return report, err
			}
		}
	}
	done := log.Info
	if report.Deducted > 0 || len(report.Skipped) > 0 {
		done = log.Warn // 默认 warn 级别下有归还/跳过必须可见（B390 R4 先例）
	}
	done("scheduling.reclaim.done", "rows", report.Rows, "reclaimed_keys", len(report.Reclaimed),
		"deducted", report.Deducted, "skipped", report.Skipped, "error_kind", "success")
	return report, nil
}

// position 计算某请求在当前队序中的 1 基位置。
func (s *Service) position(kind, id string) int {
	entries, err := s.repo.List(kind)
	if err != nil {
		return 0
	}
	type ranked struct {
		eid  string
		req  IgnitionRequest
		rec  schedclient.Record
		seq  int64
		rank int
	}
	items := make([]ranked, 0, len(entries))
	for _, rec := range entries {
		var e queuedEntry
		if json.Unmarshal(rec.Body, &e) != nil {
			continue
		}
		items = append(items, ranked{eid: rec.ID, req: e.Req, rec: rec, seq: e.Seq, rank: e.Req.orderRank()})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].req.Ready != items[j].req.Ready {
			return items[i].req.Ready
		}
		if items[i].rank != items[j].rank {
			return items[i].rank < items[j].rank
		}
		if items[i].seq != items[j].seq {
			return items[i].seq < items[j].seq
		}
		return items[i].rec.Seq < items[j].rec.Seq
	})
	for i, item := range items {
		if item.eid == id {
			return i + 1
		}
	}
	return 0
}

func (s *Service) putEntity(kind, id string, entity any, expect int) error {
	body, err := json.Marshal(entity)
	if err != nil {
		return err
	}
	_, err = s.repo.Put(kind, id, expect, body, "scheduling")
	return err
}

func (s *Service) getEntity(kind, id string, into any) error {
	rec, err := s.repo.Get(kind, id)
	if err != nil {
		if errors.Is(err, schedclient.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	if err := json.Unmarshal(rec.Body, into); err != nil {
		return fmt.Errorf("%s/%s 解码失败: %w", kind, id, err)
	}
	return nil
}

func (s *Service) listEntities(kind string, visit func([]byte) error) error {
	rows, err := s.repo.List(kind)
	if err != nil {
		return err
	}
	for _, rec := range rows {
		if err := visit(rec.Body); err != nil {
			return err
		}
	}
	return nil
}
