// 本文件是「首次配置问什么」的字段描述表：每个字段的控件类型、标题、选项、
// 适用角色、显隐条件与随答案变化的默认值，全部表达成数据。
//
// 职责：
//   - 描述首次配置要问哪些字段、按什么顺序、在什么条件下显示
//   - 提供字段表的求值：Visible（显隐）、DefaultOf（随答案变的默认值）、
//     Apply（校验答案并写回 *config.Config）
//
// 边界：
//   - **不描述怎么问**：不碰终端、窗口或任何 UI 形态，也不调用 Prompter
//   - **不落盘**：Apply 只改内存里的 cfg，Save 由调用方决定
//   - 不改 AskAll：AskAll 仍由 initflow.go 持有，本文件只是把它的字段与分支
//     规则抽成数据，供 CLI 与桌面壳共用同一份真相
package initflow

import (
	"fmt"
	"log/slog"
	"strconv"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/toolchain"
)

// Kind 描述字段的控件类型。
type Kind string

// 三种控件类型。
const (
	KindSelect  Kind = "select"
	KindInput   Kind = "input"
	KindConfirm Kind = "confirm"
)

// Field 描述「问什么」：Key、控件类型、标题、选项、适用角色、显隐条件与随答案变的默认值。
//
// 它是一张数据表：CLI 的 AskAll 与桌面壳的前端都按这张表渲染，字段与
// 分支规则只有这一份真相。**Key 一旦发布不得更名**——桌面前端按它取值。
type Field struct {
	Key         string        `json:"key"`
	Kind        Kind          `json:"kind"`
	Title       string        `json:"title"`
	Notice      string        `json:"notice"`
	Default     string        `json:"default"`
	Options     []Option      `json:"options,omitempty"`
	Roles       []string      `json:"roles,omitempty"`
	Advanced    bool          `json:"advanced"`
	ShowWhen    *Cond         `json:"show_when,omitempty"`
	DefaultWhen []DefaultRule `json:"default_when,omitempty"`
}

// Cond 描述一条显隐或默认值的条件：命中则成立。
//
// 字段全部可选；规则是数据，因此 CLI 与桌面前端求值方式相同——
// 不会出现两边显隐不一致。
type Cond struct {
	Key      string   `json:"key"`
	Equal    string   `json:"equal,omitempty"`
	In       []string `json:"in,omitempty"`
	NonEmpty bool     `json:"non_empty,omitempty"`
}

// DefaultRule 描述一条「满足条件时默认值改用 Value」的规则。
//
// 存在的唯一理由是监听预设依赖角色答案，而字段表必须在答题前就交出去。
// 规则是数据，因此 CLI 与桌面前端求值方式相同——不会出现两边预选不一致。
type DefaultRule struct {
	Cond  Cond   `json:"cond"`
	Value string `json:"value"`
}

// roleMatches 判断某字段的适用角色是否覆盖当前角色答案。
//
// RoleBoth 同时算执行机与协调者——这是「两者」这个角色的全部含义，
// 也是 AskAll 里 isExec/isCoord 两个布尔的来源。
func roleMatches(fieldRoles []string, role string) bool {
	if len(fieldRoles) == 0 {
		return true // 与角色无关的字段恒显示
	}
	for _, r := range fieldRoles {
		if r == role || (role == RoleBoth && (r == RoleExecutor || r == RoleCoordinator)) {
			return true
		}
	}
	return false
}

// DefaultOf 返回该字段在当前答案下的默认值：命中的第一条 DefaultWhen 规则
// 优先，都不命中才用 Default。
func DefaultOf(f Field, answers map[string]string) string {
	for _, r := range f.DefaultWhen {
		if matchCond(r.Cond, answers) {
			return r.Value
		}
	}
	return f.Default
}

// matchCond 求值一条 Cond。
func matchCond(c Cond, answers map[string]string) bool {
	v := answers[c.Key]
	if c.NonEmpty {
		return v != ""
	}
	if len(c.In) > 0 {
		for _, want := range c.In {
			if v == want {
				return true
			}
		}
		return false
	}
	return v == c.Equal
}

// Visible 判断该字段在当前答案下是否要问/要显示。
//
// 先判 Roles（角色不适用则不显示），再判 ShowWhen。顺序有讲究：
// Roles 是「这个字段属于谁」，ShowWhen 是「这个字段此刻要不要出来」，
// 前者不成立时后者不必再求值。
func Visible(f Field, answers map[string]string) bool {
	if !roleMatches(f.Roles, answers["role"]) {
		return false
	}
	if f.ShowWhen != nil {
		return matchCond(*f.ShowWhen, answers)
	}
	return true
}

// Apply 校验答案并写回 cfg。
//
// **不可见字段的答案被忽略而不是报错**：前端可能在用户切角色后残留旧值，
// 报错会让一个本来可以正常走完的向导卡死。Select 答案必须落在 Options 内、
// Confirm 只接受 "true"/"false"——这两类校验失败是承重，防止非法值落盘。
func Apply(cfg *config.Config, fields []Field, answers map[string]string) error {
	for _, f := range fields {
		ans, ok := answers[f.Key]
		if !ok {
			// 前端没提交这个字段的答案（捕获时它可能还没出现，或用户在切角色时
			// 把整项丢掉了）：不校验也不写回。报错会让一个本来可以正常走完的
			// 向导卡死——与「不可见字段的残留答案被忽略」是同一个理由。
			continue
		}
		if !Visible(f, answers) {
			continue
		}
		switch f.Kind {
		case KindSelect:
			if !optionContains(f.Options, ans) {
				return fmt.Errorf("字段 %s：答案 %q 不在选项内", f.Key, ans)
			}
		case KindConfirm:
			if ans != "true" && ans != "false" {
				return fmt.Errorf("字段 %s：Confirm 只接受 true/false，收到 %q", f.Key, ans)
			}
		}
		// 写回——注意 listen 与 listen_preset 要一起处理（见 Form 的注释）
		switch f.Key {
		case "listen_preset":
			switch ans {
			case listenLoopback:
				cfg.Listen = listenLoopbackAddr
			case listenAll:
				cfg.Listen = listenAllAddr
			case listenCustom:
				// custom 不写地址，地址由 listen 字段补上
			}
		case "listen":
			cfg.Listen = ans
		case "executor_default":
			cfg.Executor.Default = ans
		case "executor_model":
			cfg.Executor.Model = ans
		case "repo_root":
			cfg.RepoRoot = ans
		case "approver_executor":
			cfg.Approver.Executor = ans
		case "approver_model":
			cfg.Approver.Model = ans
		case "sync_auto":
			cfg.Sync.Auto = ans == "true"
		}
	}
	return nil
}

func optionContains(opts []Option, v string) bool {
	for _, o := range opts {
		if o.Value == v {
			return true
		}
	}
	return false
}

// Form 按当前状态构造字段表。
//
// 返回的切片顺序就是提问顺序（CLI）与渲染顺序（桌面壳）。goos 参数化
// 而非直接读 runtime.GOOS 是为了可测：判据写死则 Windows 分支在别的
// 平台上永远测不到。
//
// **标题与选项标签必须与 CLI 现状逐字一致**——金样比的就是它们。
func Form(cfg *config.Config, rs []toolchain.Result, goos string, cfgExisted bool) []Field {
	roleDefault := DefaultRole(cfg, cfgExisted, rs, goos)

	var roleNotice string
	if goos == "windows" {
		// 产品输出：用户必须当场知道为什么只有一个选项，否则会以为是 bug。
		// 桌面端没有终端可看 Notice，这条日志是唯一能事后确认「这台机器为什么
		// 只给了一个角色」的地方。
		slog.Info("Windows 平台：角色选项限定为协调者", "reason", "agentd 进程承载层未实现（B37）")
		roleNotice = "注意：Windows 上 handoff 只能当协调者——agentd 的进程承载层在非 unix 平台尚未实现（backlog B37），执行机角色跑不起来。"
	}

	execDef := cfg.Executor.Default
	if execDef == "" {
		if first := toolchain.FirstReady(rs); first != "" {
			execDef = first
		} else {
			execDef = "opencode"
		}
	}

	approverOpts := append([]Option{{Value: "", Label: "不启用（权限直接找人）"}}, ExecutorOptions(rs)...)

	// 监听预设：不含 isExec 时的档位作为静态默认，
	// 「用户选了执行机才翻成所有网卡」写成一条规则，由 DefaultOf 求值。
	listenField := Field{Key: "listen_preset", Kind: KindSelect, Title: "监听地址",
		Default: ListenPreset(cfg.Listen, cfgExisted, false),
		Options: []Option{
			{Value: listenLoopback, Label: "仅本机（127.0.0.1:7777）"},
			{Value: listenAll, Label: "所有网卡（0.0.0.0:7777）"},
			{Value: listenCustom, Label: "手填（如绑单个网卡 IP，本机命令自动走辅助监听）"},
		},
		Roles: []string{RoleExecutor, RoleBoth},
	}
	if ListenPreset(cfg.Listen, cfgExisted, true) != listenField.Default {
		listenField.DefaultWhen = []DefaultRule{{
			Cond:  Cond{Key: "role", In: []string{RoleExecutor, RoleBoth}},
			Value: ListenPreset(cfg.Listen, cfgExisted, true),
		}}
	}

	return []Field{
		{Key: "role", Kind: KindSelect, Title: "这台机器的角色",
			Default: roleDefault, Options: RoleOptions(goos), Notice: roleNotice},
		listenField,
		{Key: "listen", Kind: KindInput, Title: "监听地址 listen",
			Default: cfg.Listen, Roles: []string{RoleExecutor, RoleBoth},
			ShowWhen: &Cond{Key: "listen_preset", Equal: listenCustom}},
		{Key: "executor_default", Kind: KindSelect, Title: "默认执行者",
			Default: execDef, Options: ExecutorOptions(rs), Roles: []string{RoleExecutor, RoleBoth},
			Advanced: true},
		{Key: "executor_model", Kind: KindInput, Title: "执行者模型（空=用执行者自身默认）",
			Default: cfg.Executor.Model, Roles: []string{RoleExecutor, RoleBoth}, Advanced: true},
		{Key: "repo_root", Kind: KindInput, Title: "项目落点根目录 repo_root（自动登记时 clone 到这里）",
			Default: cfg.RepoRoot, Roles: []string{RoleExecutor, RoleBoth}, Advanced: true},
		{Key: "approver_executor", Kind: KindSelect, Title: "审批链执行者",
			Default: cfg.Approver.Executor, Options: approverOpts, Roles: []string{RoleExecutor, RoleBoth},
			Advanced: true},
		{Key: "approver_model", Kind: KindInput, Title: "审批链模型（空=用执行者自身默认）",
			Default: cfg.Approver.Model, Roles: []string{RoleExecutor, RoleBoth},
			ShowWhen: &Cond{Key: "approver_executor", NonEmpty: true}, Advanced: true},
		{Key: "sync_auto", Kind: KindConfirm, Title: "任务结束自动同步远程分支到本地 sync.auto",
			Default: strconv.FormatBool(cfg.Sync.Auto), Roles: []string{RoleCoordinator, RoleBoth},
			Advanced: true},
	}
}
