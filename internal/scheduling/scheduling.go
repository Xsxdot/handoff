// Package scheduling 是编制域：载体与小队的登记、两级并发准入、点火请求的
// 持久排队。规则归本域，持久化经 schedclient.Registry 端口由组装点绑定
// （账本是设施，本域是规则所有者——B156.3 spec §7.1）。
//
// 冻结语义（契约 §3/§4，实现与测试同源）：
//   - 准入判据 = 小队有位 且 载体有位；两级计数各自独立；
//   - 载体上限是物理位（跨小队全局），小队上限是政策位（按队计数）；
//   - 抢并发时协调者优先：只作用于准入排序（先清协调者队再放执行者队），
//     不抢占在跑任务；
//   - 排队顺序 = 就绪度快照优先 → 卡优先级 → 入队先后（FIFO）。
package scheduling

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Xsxdot/handoff/internal/schedclient"
)

// 注册表里各实体的 kind 常量。只有这四个前缀，新增实体先改契约再改这里。
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

// CredentialSource 是载体的凭据来源两值（spec §3）。
type CredentialSource string

const (
	CredentialStandalone   CredentialSource = "standalone"     // 独立账号
	CredentialMainHomeSync CredentialSource = "main_home_sync" // 主 HOME 认证态同步
)

// Carrier 是一台机器上一个可领活的 CLI 档案（spec §3 载体实体归并后的形状）。
// 健康位本期只留形状：限额探测落 roadmap，构造即 true。
type Carrier struct {
	Name           string           `json:"name"`
	Machine        string           `json:"machine"`
	CLI            string           `json:"cli"`
	HomeDir        string           `json:"home_dir"`
	Model          string           `json:"model,omitempty"`
	Credential     CredentialSource `json:"credential"`
	MaxConcurrency int              `json:"max_concurrency,omitempty"` // 物理位；0 = 不设上限
	Healthy        bool             `json:"healthy"`
}

// SquadRole 区分两种不混编的小队。
type SquadRole string

const (
	RoleExecutor    SquadRole = "executor"    // 绑工作流节点
	RoleCoordinator SquadRole = "coordinator" // 绑拉起通道
)

// Squad 是编制：角色 + 成员载体引用集 + 并发政策位。
type Squad struct {
	Name           string    `json:"name"`
	Role           SquadRole `json:"role"`
	Members        []string  `json:"members"`
	MaxConcurrency int       `json:"max_concurrency,omitempty"` // 政策位；0 = 不设上限
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
type Binding struct {
	Squad    string `json:"squad"`
	Carrier  string `json:"carrier"`
	Target   string `json:"target"`
	Executor string `json:"executor"`
	Model    string `json:"model,omitempty"`
}

var (
	ErrNotFound     = errors.New("scheduling: 实体不存在")
	ErrNoSlot       = errors.New("scheduling: 小队或载体并发已满")
	ErrRoleMismatch = errors.New("scheduling: 小队角色不符")
	ErrNoHealthy    = errors.New("scheduling: 小队内没有健康且有空的载体")
)

// Service 是编制域的规则引擎本体。全部状态经 Registry 持久（agentd 重启
// 不丢），内存里不留任何权威计数。
type Service struct {
	repo schedclient.Registry
}

// New 用组装点绑定的注册表端口构造服务。
func New(repo schedclient.Registry) *Service { return &Service{repo: repo} }

// PutCarrier 以 CAS 写载体定义（expect=0 新建）。Healthy 为零值时视为 true：
// 本期没有探测手段，「未探测」不得表现为「不健康」把载体饿死。
func (s *Service) PutCarrier(c Carrier, expect int) error {
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Machine) == "" || strings.TrimSpace(c.CLI) == "" {
		return fmt.Errorf("载体登记不完整：name/machine/cli 必填")
	}
	if c.Credential != CredentialStandalone && c.Credential != CredentialMainHomeSync {
		return fmt.Errorf("载体 %s 的凭据来源只能是 standalone 或 main_home_sync", c.Name)
	}
	if !c.Healthy {
		c.Healthy = true
	}
	return s.putEntity(kindCarrier, c.Name, c, expect)
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
		return fmt.Errorf("小队名不能为空")
	}
	if q.Role != RoleExecutor && q.Role != RoleCoordinator {
		return fmt.Errorf("小队 %s 角色只能是 executor 或 coordinator", q.Name)
	}
	for _, m := range q.Members {
		if _, err := s.Carrier(m); err != nil {
			return fmt.Errorf("小队 %s 成员 %s: %w", q.Name, m, err)
		}
	}
	return s.putEntity(kindSquad, q.Name, q, expect)
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
	q, err := s.Squad(req.Squad)
	if err != nil {
		return Binding{}, err
	}
	if q.Role != RoleExecutor {
		return Binding{}, fmt.Errorf("%w: %s 是协调者小队", ErrRoleMismatch, q.Name)
	}
	binding, carrier, err := s.resolve(q, req)
	if err != nil {
		return Binding{}, err
	}
	if err := s.acquire(q, carrier); err != nil {
		return Binding{}, err
	}
	return binding, nil
}

// LaunchAdmit 对一次协调者拉起做两级准入（协调者小队的成员载体必须在协调机上，
// 该约束由配置审核保证，本域只管计数）。
func (s *Service) LaunchAdmit(squadName string) (Binding, error) {
	q, err := s.Squad(squadName)
	if err != nil {
		return Binding{}, err
	}
	if q.Role != RoleCoordinator {
		return Binding{}, fmt.Errorf("%w: %s 是执行者小队", ErrRoleMismatch, q.Name)
	}
	req := IgnitionRequest{Squad: q.Name}
	binding, carrier, err := s.resolve(q, req)
	if err != nil {
		return Binding{}, err
	}
	if err := s.acquire(q, carrier); err != nil {
		return Binding{}, err
	}
	return binding, nil
}

// Release 归还一个已结束回合占用的两级名额。幂等：计数到 0 后不再下探。
func (s *Service) Release(squadName, carrierName string) error {
	if err := s.stepRunning(kindSquad+"/"+squadName, -1); err != nil {
		return err
	}
	return s.stepRunning(kindCarrier+"/"+carrierName, -1)
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

// resolve 按小队成员顺序挑第一个健康且有空的载体，产出有效三元组：
// 一次性覆盖 > 载体缺省（spec §3：--target/--executor/--model 降级为载体字段覆盖）。
// 全部成员都满员返回 ErrNoSlot（调用方转排队），没有健康载体才报 ErrNoHealthy
// ——两种失败处置不同：前者是常态排队，后者是配置或限额问题。
func (s *Service) resolve(q Squad, req IgnitionRequest) (Binding, Carrier, error) {
	capacityBlocked := false
	for _, name := range q.Members {
		carrier, err := s.Carrier(name)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return Binding{}, Carrier{}, err
		}
		if !carrier.Healthy {
			continue
		}
		free, err := s.hasSlot(q, carrier)
		if err != nil {
			return Binding{}, Carrier{}, err
		}
		if !free {
			capacityBlocked = true
			continue
		}
		target := req.Target
		if target == "" {
			target = carrier.Machine
		}
		executor := req.Executor
		if executor == "" {
			executor = carrier.CLI
		}
		model := req.Model
		if model == "" {
			model = carrier.Model
		}
		return Binding{Squad: q.Name, Carrier: carrier.Name, Target: target, Executor: executor, Model: model}, carrier, nil
	}
	if capacityBlocked {
		return Binding{}, Carrier{}, ErrNoSlot
	}
	return Binding{}, Carrier{}, ErrNoHealthy
}

// hasSlot 报告两级并发是否都有位：小队有位 且 载体有位。
func (s *Service) hasSlot(q Squad, c Carrier) (bool, error) {
	squadRunning, err := s.running(kindSquad + "/" + q.Name)
	if err != nil {
		return false, err
	}
	if q.MaxConcurrency > 0 && squadRunning >= q.MaxConcurrency {
		return false, nil
	}
	carrierRunning, err := s.running(kindCarrier + "/" + c.Name)
	if err != nil {
		return false, err
	}
	if c.MaxConcurrency > 0 && carrierRunning >= c.MaxConcurrency {
		return false, nil
	}
	return true, nil
}

// acquire 两级计数各 +1。计数缺失视为 0。
func (s *Service) acquire(q Squad, c Carrier) error {
	if err := s.stepRunning(kindSquad+"/"+q.Name, +1); err != nil {
		return err
	}
	return s.stepRunning(kindCarrier+"/"+c.Name, +1)
}

// stepRunning 对一条运行计数记录做带符号增量，CAS 重试读改写。
func (s *Service) stepRunning(id string, delta int) error {
	for attempt := 0; attempt < 8; attempt++ {
		rec, err := s.repo.Get(kindRunning, id)
		count := 0
		expect := 0
		if err == nil {
			var body struct {
				Count int `json:"count"`
			}
			if json.Unmarshal(rec.Body, &body) != nil {
				return fmt.Errorf("运行计数 %s 解码失败", id)
			}
			count = body.Count
			expect = rec.Version
		} else if !errors.Is(err, schedclient.ErrNotFound) {
			return err
		}
		next := count + delta
		if next < 0 {
			next = 0
		}
		bodyBytes, _ := json.Marshal(map[string]int{"count": next})
		if _, err := s.repo.Put(kindRunning, id, expect, bodyBytes, "scheduling"); err != nil {
			if errors.Is(err, schedclient.ErrCASConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("运行计数 %s 连续冲突", id)
}

// running 读一条运行计数；缺失 = 0（agentd 重启后计数随用随建，权威状态
// 在账本不在内存）。
func (s *Service) running(id string) (int, error) {
	rec, err := s.repo.Get(kindRunning, id)
	if err != nil {
		if errors.Is(err, schedclient.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	var body struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body, &body); err != nil {
		return 0, fmt.Errorf("运行计数 %s 解码失败: %w", id, err)
	}
	return body.Count, nil
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
