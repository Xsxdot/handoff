// status.go —— 载体一等状态、默认隔离 HOME、运行命令与检测写状态入口（B293）。
//
// 职责：冻结四态词表、用户可见默认 HOME 串、运行命令格式，以及检测结果写回
// 载体状态的入站面。PutCarrier / admitInto 的旧 Healthy 翻真与按位准入仍在
// scheduling.go，实现票按契约废止并改接本文件的状态面。
//
// 边界：不探文件系统、不拉起 CLI——那是 hostapi.ProbeHome / WakeHome；本文件
// 只拥有编制域数据（status / last_error）和由载体字段派生、对侧可见的字符串。
package scheduling

import (
	"errors"
	"log/slog"
	"strings"
)

// CarrierStatus 是载体一等状态的英文键（wire `status`）。用户可见中文名见 Label。
type CarrierStatus string

const (
	StatusPending     CarrierStatus = "pending"     // 未上线
	StatusOnline      CarrierStatus = "online"      // 已上线
	StatusQuota       CarrierStatus = "quota"       // 限额中
	StatusUnreachable CarrierStatus = "unreachable" // 不可达
)

// IsolatedHomeRoot 是用户可见的默认隔离 HOME 根（含 ~，目标机展开）。
// 不跟可改的 DataDir：登记串必须跨机可认，DataDir 搬迁不得改已登记默认形态。
const IsolatedHomeRoot = "~/.handoff/home"

func statusLog() *slog.Logger { return slog.Default().With("mod", "scheduling") }

// Label 返回状态的用户可见中文名（设置页药丸与 CLI 共用这一份）。
func (s CarrierStatus) Label() string {
	switch s {
	case StatusPending:
		return "未上线"
	case StatusOnline:
		return "已上线"
	case StatusQuota:
		return "限额中"
	case StatusUnreachable:
		return "不可达"
	default:
		return string(s)
	}
}

// DefaultHomeDir 返回登记弹窗的默认 HOME 串：`~/.handoff/home/<载体名>`。
// 名字空（含纯空白）返回空串，避免拼出无主目录。
func DefaultHomeDir(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return IsolatedHomeRoot + "/" + name
}

// RunCommand 返回「运行」按钮要复制的那条命令。客户端不得再拼接。
// 格式冻结为 `HOME=<home_dir> <cli>`，home_dir 用载体已存字符串（可含 ~）。
func RunCommand(c Carrier) string {
	return "HOME=" + c.HomeDir + " " + c.CLI
}

// DetectEvidence 是目标机检测观察到的事实。状态迁移（四态表）归 ApplyDetect，
// 本结构不含「该写成哪一态」——那是编制域的规则，不让 gateway 各写一份。
type DetectEvidence struct {
	Reachable bool // 机器/进程够得着
	NeedLogin bool // 需要登录或未见凭据
	Quota     bool // 识别为额度用尽
}

// ErrDetectUnwired 是 Ticket 0 骨架哨兵：ApplyDetect 尚未按四态表写状态。
// gateway 映射 503；实现票接线后正常路径不再返回它。
var ErrDetectUnwired = errors.New("scheduling: 载体检测写状态尚未接线")

// ApplyDetect 按四态表把一次检测结果写进载体。Ticket 0 空壳：不写库，恒返回
// ErrDetectUnwired。实现票必须：读当前状态 → 按契约迁移表写入 status/last_error
// → CAS 推进 version。
func (s *Service) ApplyDetect(name string, ev DetectEvidence, detail string) (Carrier, error) {
	statusLog().Warn("ApplyDetect 尚未接线", "name", name, "reachable", ev.Reachable,
		"need_login", ev.NeedLogin, "quota", ev.Quota, "detail_bytes", len(detail))
	return Carrier{}, ErrDetectUnwired
}
