// dispatch.go —— B229 缝 1：派发期纪律正文的唯一裁决点。
//
// 职责：取正文（经注入的 lookup 读账本）→ 三层组装（复用本包 Compose 与平台
// 常量）→ 判目标机能力 → 返回正文或拒发理由。
//
// 归属订正（B229 契约 §4.5）：spec 初稿把本入口放在派发编排包，落骨架时被
// 仓库契约闸推翻——ledgerstep（d_ledger）调用 Compose 会造出 d_ledger→d_policy
// 全新方向，而基线扫描在分支合并重扫前看不到这条边，TestRepoContractGate 必红。
// 平台组装的本体（Compose + 平台常量）本来就在本包，缝 1 回归本包后：
// 组装点仍是唯一一处，三个调用方家族全部走既有已声明边，零新增方向。
//
// 边界：
//   - 不 import 账本包：账本依赖以 DisciplineLookup 函数注入，适配由调用方闭包完成
//   - 不发网络：目标机能力位由调用方探好，以 *bool 原值传入
//   - 执行机侧不导出本文件的接线——执行机收文即用，不再解析
package discipline

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupportedTarget 是能力位三态判为不支持时的拒发错误。
// 它必须显式可辨：调用方与测试都靠它断言「拒发而不是降级」。
var ErrUnsupportedTarget = errors.New("目标机 agentd 不支持接收下发的纪律正文（能力位缺席或不支持）：请先把目标机升级到同批版本再派发")

// DisciplineLookup 是缝 1 对账本的窄依赖视图：按名字取最新版正文。
//
// 以函数注入而不是接口或类型引用——本包不 import 账本包，d_policy 对 d_ledger
// 零依赖；适配由调用方三行闭包完成（st.GetDiscipline(name, 0) 拆包）。
// 未知名必须原样上抛错误，任何「查不到就兜底」的实现会让 charter-must-override
// 哨兵连同缺陷三一起复活（B229 契约 §3.2）。
type DisciplineLookup func(name string) (version int, body string, err error)

// DisciplineRef 点名一次纪律来源。
//
// Name 与 RawText 二选一：Name 经 lookup 取账本最新版（产物带版本号）；RawText
// 直接作为角色层正文下发，不落库、不影响其他派发（spec 用户故事 3 的「临时捏
// 一份」），版本号记 0。两者都空 = 未点名，只注入平台层（B229 实现决定 1）；
// 两者都非空是调用方的参数错误。
type DisciplineRef struct {
	Name    string
	RawText string
}

// ResolvedDiscipline 是随派发请求下发的纪律正文及其出处。
type ResolvedDiscipline struct {
	Text    string // 平台层+角色层组装后的完整正文；空=不注入
	Source  string // 人可读来源标注（沿用 Block.Source 形态）
	Name    string // 点名的角色名；未点名为空
	Version int    // 账本版本号；未点名或临时正文为 0
}

// ResolveDispatch 是一次派发的纪律正文全链路收口（B229 契约 §2.2）。
//
// 参数：
//   - lookup: 账本读取视图。ref 未点名时不调用，可为 nil
//   - ref: 纪律来源（Name / RawText 二选一 / 都空）
//   - platformEnabled: 协调者侧 PlatformInvariantsEnabled() 的原值
//   - targetCap: 目标机 StatusResp.DisciplinesSupported 原值。三态解释：
//     nil（对端没上报 = 版本太老）与 false 都按不支持处置 → 拒发；
//     true 放行。方向与 PtySupported 刻意相反、与 LaunchersSupported 同向，
//     理由见 B229 契约 §2.4：这里放行的代价是静默降级。
func ResolveDispatch(lookup DisciplineLookup, ref DisciplineRef, platformEnabled bool, targetCap *bool) (ResolvedDiscipline, error) {
	if targetCap == nil || !*targetCap {
		return ResolvedDiscipline{}, ErrUnsupportedTarget
	}
	if ref.Name != "" && ref.RawText != "" {
		return ResolvedDiscipline{}, fmt.Errorf("discipline name 与 raw text 只能二选一")
	}
	switch {
	case strings.TrimSpace(ref.Name) != "":
		if ref.Name != strings.TrimSpace(ref.Name) {
			return ResolvedDiscipline{}, fmt.Errorf("纪律块名字 %q 带首尾空白", ref.Name)
		}
		if lookup == nil {
			return ResolvedDiscipline{}, fmt.Errorf("点名纪律块 %s 需要账本，但账本读取视图未注入", ref.Name)
		}
		version, body, err := lookup(ref.Name)
		if err != nil {
			return ResolvedDiscipline{}, fmt.Errorf("未知纪律块名字 %q: %w", ref.Name, err)
		}
		base := Block{Text: body, Source: "账本:" + ref.Name}
		assembled := Compose(base, platformEnabled)
		return ResolvedDiscipline{
			Text: assembled.Text, Source: assembled.Source,
			Name: ref.Name, Version: version,
		}, nil
	case ref.RawText != "":
		base := Block{Text: ref.RawText, Source: "临时正文"}
		assembled := Compose(base, platformEnabled)
		return ResolvedDiscipline{Text: assembled.Text, Source: assembled.Source}, nil
	default:
		assembled := Compose(Block{}, platformEnabled)
		return ResolvedDiscipline{Text: assembled.Text, Source: assembled.Source}, nil
	}
}
