// status.go —— 载体一等状态、默认隔离 HOME、运行命令与检测写状态入口（B293）。
//
// 职责：冻结三态词表（B453 退役「限额中」，读时归一）、用户可见默认 HOME 串、运行命令
// 格式，以及检测结果写回载体状态的入站面。PutCarrier / admitInto 的四态接线与按位准入规则分别落在
// scheduling.go，本文件集中维护状态迁移和用户可见的派生字符串。
//
// 边界：不探文件系统、不拉起 CLI——那是 hostapi.WakeHome；本文件
// 只拥有编制域数据（status / last_error）和由载体字段派生、对侧可见的字符串。
package scheduling

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
	"github.com/Xsxdot/handoff/internal/scheduling/internal/logging"
)

// CarrierStatus 是载体一等状态的英文键（wire `status`）。用户可见中文名见 Label。
type CarrierStatus string

const (
	StatusPending     CarrierStatus = "pending"     // 未上线
	StatusOnline      CarrierStatus = "online"      // 已上线
	StatusUnreachable CarrierStatus = "unreachable" // 不可达
)

// legacyStatusQuota 是 B453 退役的「限额中」wire 值（spec S4、plan Q12/F19）。本版不再
// 写它；存量行与滚动升级期间旧版 agentd 写回的 quota 一律读作已上线。
const legacyStatusQuota = "quota"

// UnmarshalJSON 是「限额中」退役的读时归一点：载体 body 无论经哪条路径解码（Carrier、
// Carriers、CarrierRows、ApplyDetect、PutCarrier 读旧行），quota 都读作 online，不做写迁移。
// 额度是账号的事，载体本身可达即已上线（spec S4）。
func (s *CarrierStatus) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == legacyStatusQuota {
		raw = string(StatusOnline)
	}
	*s = CarrierStatus(raw)
	return nil
}

// IsolatedHomeRoot 是用户可见的默认隔离 HOME 根（含 ~，目标机展开）。
// 不跟可改的 DataDir：登记串必须跨机可认，DataDir 搬迁不得改已登记默认形态。
const IsolatedHomeRoot = "~/.handoff/home"

func statusLog() *slog.Logger { return logging.StatusLog() }

// Label 返回状态的用户可见中文名（设置页药丸与 CLI 共用这一份）。
func (s CarrierStatus) Label() string {
	switch s {
	case StatusPending:
		return "未上线"
	case StatusOnline:
		return "已上线"
	case StatusUnreachable:
		return "不可达"
	default:
		return string(s)
	}
}

// DefaultHomeDir 返回隔离 HOME 的用户可见配方：`~/.handoff/home/<载体名>`。
// 名字空（含纯空白）返回空串。B334 起登记弹窗不再预填它；空 HOME = 主 HOME。
func DefaultHomeDir(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return IsolatedHomeRoot + "/" + name
}

// RunCommand 返回「运行」按钮要复制的那条命令。客户端不得再拼接。
// 有隔离 HOME 时为 `HOME=<home_dir> <cli>`；空 HOME 只输出 CLI，沿用该机主 HOME。
func RunCommand(c Carrier) string {
	if strings.TrimSpace(c.HomeDir) == "" {
		return c.CLI
	}
	return "HOME=" + c.HomeDir + " " + c.CLI
}

// DetectEvidence 是目标机检测观察到的事实。状态迁移（三态表）归 ApplyDetect，
// 本结构不含「该写成哪一态」——那是编制域的规则，不让 gateway 各写一份。
type DetectEvidence struct {
	Reachable bool // 机器/进程够得着
	NeedLogin bool // 需要登录或未见凭据
	// Quota = 检测回合撞额度（真额度耗尽，限速不算，plan Q10）。B453 起它不再改载体状态，
	// 只决定标账号无额度（绑定了账号）还是把原因写进 last_error（未绑定），spec S4。
	Quota bool
}

// ErrDetectUnwired 是 Ticket 0 骨架哨兵：ApplyDetect 尚未按状态表写状态。
// gateway 映射 503；实现票接线后正常路径不再返回它。
var ErrDetectUnwired = errors.New("scheduling: 载体检测写状态尚未接线")

// ApplyDetect 按「不可达优先、需登录次之、否则上线」的三态表把一次检测结果写进载体。
// 它读取 registry 当前版本并以一次 CAS 写回；CAS、读取或解码失败都保留旧状态并返回带
// 载体名上下文的错误。（B453 的载体凭据指纹比对随 B464 删除：载体就跑在登录 HOME 里。）
//
// 撞额度（ev.Quota）不再是载体状态（spec S4）：载体按可达处理；绑定了账号的先给账号打
// 撞额度标记（标记写失败 = 本次检测失败，载体不写，重检即可），未绑定或账号已删的把
// detail 写进 last_error。online 且无额度原因时清空 LastError，其他状态原样保存 detail。
func (s *Service) ApplyDetect(name string, ev DetectEvidence, detail string) (Carrier, error) {
	statusLog().Info("开始写入载体检测", "name", name, "reachable", ev.Reachable,
		"need_login", ev.NeedLogin, "quota", ev.Quota, "detail_bytes", len(detail))
	rec, err := s.repo.Get(kindCarrier, name)
	if err != nil {
		statusLog().Error("读取载体检测目标失败", "name", name, "cause", err)
		if errors.Is(err, schedclient.ErrNotFound) {
			return Carrier{}, fmt.Errorf("载体 %s 不存在: %w", name, ErrNotFound)
		}
		return Carrier{}, fmt.Errorf("载体 %s 读取失败: %w", name, err)
	}
	var carrier Carrier
	if err := json.Unmarshal(rec.Body, &carrier); err != nil {
		statusLog().Error("解码载体检测目标失败", "name", name, "version", rec.Version, "cause", err)
		return Carrier{}, fmt.Errorf("载体 %s/%d 解码失败: %w", name, rec.Version, err)
	}
	previous := carrier.Status
	switch {
	case !ev.Reachable:
		if previous == StatusOnline || previous == StatusUnreachable {
			carrier.Status = StatusUnreachable
		} else {
			carrier.Status = StatusPending
		}
	case ev.NeedLogin && !ev.Quota:
		// 撞额度说明 CLI 已经带着凭据跑到了上游，登录类字样只是错误文本的旁证。
		carrier.Status = StatusPending
	default:
		carrier.Status = StatusOnline
	}
	if ev.Reachable && ev.NeedLogin && !ev.Quota {
		s.markDetectLoginFailed(carrier)
	}
	quotaToLastError := false
	if carrier.Status == StatusOnline && ev.Quota {
		marked, markErr := s.markDetectQuota(carrier, detail)
		if markErr != nil {
			return Carrier{}, markErr
		}
		quotaToLastError = !marked
	}
	switch {
	case quotaToLastError:
		carrier.LastError = detail
	case carrier.Status == StatusOnline:
		carrier.LastError = ""
	default:
		carrier.LastError = detail
	}
	body, err := json.Marshal(carrier)
	if err != nil {
		statusLog().Error("编码载体检测结果失败", "name", name, "version", rec.Version, "cause", err)
		return Carrier{}, fmt.Errorf("载体 %s 检测结果编码失败: %w", name, err)
	}
	version, err := s.repo.Put(kindCarrier, name, rec.Version, body, "scheduling.detect")
	if err != nil {
		statusLog().Error("写入载体检测结果失败", "name", name, "version", rec.Version,
			"next_status", carrier.Status, "cause", err)
		return Carrier{}, fmt.Errorf("载体 %s 检测写入冲突: %w", name, err)
	}
	statusLog().Info("写入载体检测结果成功", "name", name, "version", version,
		"previous_status", previous, "status", carrier.Status, "quota_to_last_error", quotaToLastError)
	return carrier, nil
}

// markDetectLoginFailed 给检测得到「需要登录」的载体所选登录记失效证据（plan Q5：登录读作
// 已过期，不挡派发；凭据变化后下一轮发现回正常）。未选登录、账号域未装配、登录已删 → 不记；
// 证据写失败只告警——载体状态与最近错误照常写，检测结论不因证据写入失败而丢。
func (s *Service) markDetectLoginFailed(c Carrier) {
	loginID := strings.TrimSpace(c.Login)
	if loginID == "" || s.accounts == nil {
		return
	}
	if _, err := s.accounts.MarkLoginAuthFailed(loginID, time.Now(), "detect:"+c.Machine, "need_login"); err != nil {
		if errors.Is(err, accounts.ErrNotFound) {
			statusLog().Warn("检测需要登录但所选登录已不存在，不记失效证据", "name", c.Name, "login", loginID)
			return
		}
		statusLog().Warn("检测需要登录：写登录失效证据失败", "name", c.Name, "login", loginID, "cause", err)
	}
}

// markDetectQuota 给检测撞额度的载体所选登录所属账号打撞额度标记（spec S4、plan Q19；B464 起
// 载体 → 登录 → 账号 id）。返回 marked=false 表示没有可标的账号（未选登录、账号域未装配、
// 登录或账号已删），调用方把原因写进 last_error；标记写入失败返回错误。
func (s *Service) markDetectQuota(c Carrier, detail string) (bool, error) {
	loginID := strings.TrimSpace(c.Login)
	if loginID == "" || s.accounts == nil {
		return false, nil
	}
	login, err := s.accounts.Login(loginID)
	if err != nil {
		if errors.Is(err, accounts.ErrNotFound) {
			statusLog().Warn("检测撞额度但所选登录已不存在，原因写入载体最近错误", "name", c.Name, "login", loginID)
			return false, nil
		}
		statusLog().Error("检测撞额度读取所选登录失败", "name", c.Name, "login", loginID, "cause", err)
		return false, fmt.Errorf("载体 %s 检测撞额度，读取登录 %s 失败: %w", c.Name, loginID, err)
	}
	err = s.accounts.MarkQuota(login.Account, accounts.QuotaMark{
		At: time.Now(), Carrier: c.Name, Machine: c.Machine,
		Category: "quota_exhausted", Message: detail,
	})
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, accounts.ErrNotFound):
		statusLog().Warn("检测撞额度但所选账号已不存在，原因写入载体最近错误", "name", c.Name, "account", login.Account)
		return false, nil
	default:
		statusLog().Error("检测撞额度标记账号失败", "name", c.Name, "account", login.Account, "cause", err)
		return false, fmt.Errorf("载体 %s 检测撞额度，标记账号 %s 失败: %w", c.Name, login.Account, err)
	}
}
