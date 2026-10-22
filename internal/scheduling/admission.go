// admission.go —— 统一准入判据（B453 段 3，spec S5 / plan V10）。
//
// 职责：「载体可用 = 载体已上线 且 所选账号不在低额度/无额度」只在这里判一次；五个准入
// 入口（AdmitCarrier、SelectWhere、AdmitFrozen、AdmitSeatCarrier、admitInto）与 gateway
// 的他机点名派发预检（CheckUsable）都经 usability，不得在入口另写一份（结构断言见
// b453_admission_test.go）。不可用时给点名说明：载体、账号、状态、预计恢复（spec S5 文案）。
//
// 边界：账号状态的推导归 scheduling/accounts（Derive/RecoverAt），本文件只消费结论；
// 账号域未装配（单测或旧组装）时只按载体状态判。已在跑的任务不受影响——这里只管新准入。
package scheduling

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// SetAccounts 注入账号领域服务（组装点与 accounts API 同一实例，同一 registry 端口）。
// 准入据此读载体所选账号的状态；载体登记据此校验账号引用；检测撞额度据此打标记。
// 与 SetSelfName 一样只在组装点调用一次；nil = 不看账号（载体 account 引用登记被拒）。
func (s *Service) SetAccounts(a *accounts.Service) { s.accounts = a }

// Unusable 是一个载体在准入中视同不可用的点名说明（spec S5）。
type Unusable struct {
	Carrier   string
	Status    CarrierStatus  // 载体本身未上线时的状态
	Account   string         // 因账号被挡时为账号名；空 = 因载体状态被挡
	State     accounts.State // 账号状态（低额度/无额度）
	RecoverAt *time.Time     // 预计恢复时刻；nil = 未知
	now       time.Time
}

// String 渲染点名文案，例如「mini：账号 og-mini 无额度，预计 2h41m 后恢复」「mini：载体不可达」。
func (u Unusable) String() string { return u.Carrier + "：" + u.reason() }

// reason 是不带载体名的原因：「账号 og-mini 无额度，预计 2h41m 后恢复」「载体不可达」。
func (u Unusable) reason() string {
	if u.Account == "" {
		label := u.Status.Label()
		if label == "" {
			label = "状态未回填（按未上线）"
		}
		return "载体" + label
	}
	recover := "恢复时刻未知"
	if u.RecoverAt != nil {
		recover = "预计 " + HumanDuration(u.RecoverAt.Sub(u.now)) + " 后恢复"
	}
	return fmt.Sprintf("账号 %s %s，%s", u.Account, u.State.Label(), recover)
}

// unusableError 是统一准入拒绝的错误：errors.Is(ErrNoHealthy) 成立（HTTP 映射与调用方分流不变），
// 但文案只有点名原因本身——不带哨兵的「scheduling: 小队内没有已上线且有空的载体: 」前缀，那句话
// 对点名载体是错的（不是小队），对小队则与「小队 X 没有可用载体」重复（R5 真机反馈）。
type unusableError struct{ msg string }

func (e *unusableError) Error() string        { return e.msg }
func (e *unusableError) Is(target error) bool { return target == ErrNoHealthy }

// HumanDuration 把剩余时长写成「2h41m」「3d4h」「12m」。向上取整到分钟：说「0m 后恢复」
// 而实际还差几十秒，会让人立刻重派又被拒。
func HumanDuration(d time.Duration) string {
	minutes := int((d + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	switch {
	case minutes >= 24*60:
		return fmt.Sprintf("%dd%dh", minutes/(24*60), minutes%(24*60)/60)
	case minutes >= 60:
		return fmt.Sprintf("%dh%dm", minutes/60, minutes%60)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

// usability 是统一准入判据。返回 nil = 可用；非 nil = 不可用及点名说明；error 只表示读取
// 故障（账号定义/读数行读不出），调用方按准入失败上浮，与载体读取失败同等处理。
//
// 所选账号已被删除（删除与载体选该账号并发，plan §7 门禁绕过族）按「未选账号」放行并 Warn：
// 账号删了就不再有可挡的读数，拦住它只会让载体莫名不可用。
func (s *Service) usability(c Carrier) (*Unusable, error) {
	if c.Status != StatusOnline {
		return &Unusable{Carrier: c.Name, Status: c.Status}, nil
	}
	name := strings.TrimSpace(c.Account)
	if name == "" || s.accounts == nil {
		return nil, nil
	}
	now := time.Now()
	a, err := s.accounts.Assess(name, now)
	if err != nil {
		if errors.Is(err, accounts.ErrNotFound) {
			statusLog().Warn("载体所选账号不存在，按未选账号准入", "carrier", c.Name, "account", name)
			return nil, nil
		}
		statusLog().Error("准入读取载体账号失败", "carrier", c.Name, "account", name, "cause", err)
		return nil, fmt.Errorf("载体 %s 的账号 %s 读取失败: %w", c.Name, name, err)
	}
	if !a.State.Blocks() {
		return nil, nil
	}
	u := &Unusable{Carrier: c.Name, Status: c.Status, Account: name, State: a.State, RecoverAt: a.RecoverAt, now: now}
	statusLog().Info("准入因账号跳过载体", "carrier", c.Name, "account", name, "state", a.State,
		"recover_at", a.RecoverAt)
	return u, nil
}

// requireUsable 是单载体入口（直派、冻结、协调者冻结、gateway 预检）的统一出口：不可用 →
// 包 ErrNoHealthy 的点名错误（调用方 HTTP 映射不变）。
func (s *Service) requireUsable(c Carrier) error {
	u, err := s.usability(c)
	if err != nil {
		return err
	}
	if u != nil {
		return &unusableError{msg: "载体 " + u.Carrier + " 不可用：" + u.reason()}
	}
	return nil
}

// CheckUsable 用统一判据判一个载体当下能否接新派发（gateway 他机点名派发在转发前预检，
// spec S5「点名派发被拒，同样文案」）。可用返回 nil；不可用返回包 ErrNoHealthy 的点名错误。
func (s *Service) CheckUsable(c Carrier) error { return s.requireUsable(c) }

// noUsableMember 是小队全员被跳过时的错误：逐个点名被跳过的成员（spec S5）。
func noUsableMember(squad string, skipped []string) error {
	if len(skipped) == 0 {
		return &unusableError{msg: "小队 " + squad + " 没有在线载体"}
	}
	return &unusableError{msg: "小队 " + squad + " 没有可用载体——" + strings.Join(skipped, "；")}
}

// CarriersUsingAccount 列出所选账号为 account 的载体名（排序）。组装点把它注入账号域的
// 引用查询（accounts.SetReferrers）：删除被引用账号拒删并点名载体（spec S1）、账号读面
// carriers 列。
func (s *Service) CarriersUsingAccount(account string) ([]string, error) {
	carriers, err := s.Carriers()
	if err != nil {
		return nil, fmt.Errorf("列载体以查账号 %s 的引用: %w", account, err)
	}
	var out []string
	for _, c := range carriers {
		if c.Account == account {
			out = append(out, c.Name)
		}
	}
	sort.Strings(out)
	return out, nil
}
