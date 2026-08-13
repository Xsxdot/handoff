// 本文件实现 handoff upgrade：一条命令巡检并升级本机与全部 target。
//
// 职责：
//   - 不带参数（或 --check）：巡检表——列出所有机器的版本与结论
//   - --now：升级所有落后的机器（远端全部处理完，本机最后）；--target 只升那一台
//   - --force：越过闸一（活跃任务）。**永不越过闸二（非托管）**
//   - --rollback：本机回滚（不接 --target，回滚是单机应急动作）
//
// 数据流（spec §4.2）：本机下载各机平台的资产并校验 → POST /api/update 把
// tar.gz 原文推给远端（执行机无需出网）→ agentd 复检两道闸、再校验、解包、
// 自检、原子换版 → 触发优雅关停由进程管理器拉起新版 → 本 CLI 轮询 status
// 确认新版本上线。
//
// 边界：
//   - 会通过接口触发 agentd 重启（本机最后：它会重启操作者正用着的 agentd）
//   - 部分失败不中断其余：机器之间没有事务关系，逐行报告，任一台失败退出码非零
//   - 处置建议必须对症：非托管不给 --force（它不越过闸二），够不着只报原文不编处置
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/buildinfo"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/release"
)

// releaseChecker 查最新发布。生产实现是 *release.Client。
type releaseChecker interface {
	Latest(ctx context.Context) (release.Release, error)
}

// releaseFetcher 下载并校验某个发布。
//
// Fetch 供本机路径用（下载本平台资产并落地），FetchArchive 供远端路径用
// （按远端平台下载 tar.gz 原文，由 agentd 侧解包自检）。生产实现是
// *release.Installer。
type releaseFetcher interface {
	Fetch(ctx context.Context, rel release.Release, destDir string) (string, error)
	FetchArchive(ctx context.Context, rel release.Release, goos, goarch string) ([]byte, string, error)
}

// agentdPeer 是 upgrade 需要的 agentd 能力子集。
//
// 声明成接口而不是直接用 *client.Client：这条命令会真的推二进制并重启对端，
// 测试必须能整体替换掉，漏替一个就会在 CI 上真的去动一台机器。
type agentdPeer interface {
	Status(ctx context.Context) (*proto.StatusResp, error)
	PushUpdate(ctx context.Context, tag, sum string, tgz []byte, force bool) (*proto.UpdateResp, error)
	RestartAgentd(ctx context.Context, force bool) (*proto.UpdateResp, error)
	WaitVersion(ctx context.Context, want string, timeout, interval time.Duration) error
}

// 七个缝，测试替换它们以避免联网、动真实二进制与真实 agentd。
var (
	newReleaseChecker = func() releaseChecker { return release.NewClient() }
	newReleaseFetcher = func() releaseFetcher { return release.NewInstaller(slog.Default()) }
	activateBinary    = release.Activate
	rollbackBinary    = release.Rollback
	// newAgentdClient 是「怎么跟一台 agentd 说话」这一层的缝：测试替换它
	// 就能整套替身化远端，而不必起真实 HTTP 服务
	newAgentdClient = func(ep Endpoint) agentdPeer { return client.New(ep.Addr, ep.Token) }
	// listEndpoints 是机器清单的缝：测试注入临时配置里的机器
	listEndpoints = Endpoints
	// recordOrder 在每台机器开始处理时被调用一次，生产是空实现。
	//
	// 存在的唯一理由：「本机排最后」是一条**顺序**约束，它无法从输出文本可靠
	// 断言（排版随时会改），只能观察动作序列。为一条真实约束留一个空钩子，
	// 好过让这条约束没有测试
	recordOrder = func(string) {}
	// execSkillInstall 在本机换版后于新二进制上跑一次 skill install。
	//
	// 抽成缝而不是直接 exec.CommandContext：本机换版后要同步的新 skill 在刚换
	// 上去的那个二进制里，必须 exec 新二进制（当前进程内嵌的是旧 skill）；
	// 但测试里目标路径是测试二进制，直接 exec 会递归跑一遍测试。与 agentdPeer
	// 同一条纪律：测试必须能整体替身化，漏替一处就会在 CI 上真的动到测试二进制
	execSkillInstall = func(ctx context.Context, target string) (string, error) {
		out, err := exec.CommandContext(ctx, target, "skill", "install").CombinedOutput()
		return string(out), err
	}
)

// upgradeWaitTimeout / upgradeWaitInterval 是换版后等新进程上线的时限与轮询间隔。
//
// 60s 的依据：systemd Restart=always 的默认 RestartSec 是 100ms，
// launchd KeepAlive 是立即拉起；60s 给足了慢机器加载与 SQLite 打开的余量，
// 又不至于让一台真的起不来的机器把操作者晾太久。
const (
	upgradeWaitTimeout  = 60 * time.Second
	upgradeWaitInterval = 2 * time.Second
)

var (
	upgradeCheck    bool
	upgradeNow      bool
	upgradeRollback bool
	upgradeForce    bool
)

// machineState 是一台机器巡检后的状态。
//
// Bin 是本机 CLI 的版本（远端不用）；Agentd 是 agentd 版本（本机 agentd
// 未运行时为空）。Err 非空表示 status 够不着——对远端是失败，对本机只表示
// agentd 未运行（敲命令的人知道自己要不要把它起回来）。
type machineState struct {
	Ep       Endpoint
	Bin      string
	Agentd   string
	Platform string // 对端上报的平台；空 = 对端过旧未上报
	// Managed 是对端上报的「agentd 由进程管理器拉起」状态。
	//
	// 为什么是指针：nil 表示**对端没给这个字段**（老 agentd 不上报 Update），
	// 与「对端说 false」是两回事。用 bool 零值把前者塌成后者，就会把「我不知道」
	// 讲成「它非托管」，并据此给出一条注定白折腾的处置建议——B64 就是这么来的。
	// 与同结构里 ActiveTask.Watchers *int 是同一条纪律。
	Managed *bool
	Busy    int
	Err     error
}

// currentBinary 返回当前二进制的真实路径。
//
// 必须 EvalSymlinks：装在 ~/.local/bin 的二进制常常是个 symlink，
// 替换 symlink 本身只会把链接换成普通文件，链接目标仍是旧版。
func currentBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("取当前可执行文件路径: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

// outcome 是一台机器本次处理的结果分类。
type outcome int

const (
	outcomeOK   outcome = iota // 升级完成或已换文件
	outcomeSkip                // 被闸拦下 / 已是最新，跳过
	outcomeFail                // 失败
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "检查并升级本机与全部 target 的 handoff",
	Long: "不带参数等同 --check：列出所有机器的版本与结论。\n" +
		"--now 升级所有落后的机器（含本机）；--target 只升那一台；\n" +
		"--force 越过活跃任务闸（**不越过非托管闸**）；--rollback 换回本机上一版（不支持 --target）。\n" +
		"二进制由本机下载后推送，执行机无需出网。",
	RunE: func(cmd *cobra.Command, _ []string) error {
		if upgradeNow && upgradeRollback {
			return fmt.Errorf("--now 与 --rollback 不能同时使用")
		}
		out := cmd.OutOrStdout()
		ctx := cmd.Context()

		// --rollback 原样保留，不接 --target：回滚是「这台机器上出了问题」的
		// 单机应急动作，批量回滚一组机器不是任何真实场景，给它批量入口只会
		// 让误操作更省事（spec §4.1）
		if upgradeRollback {
			target, err := currentBinary()
			if err != nil {
				return err
			}
			if err := rollbackBinary(target); err != nil {
				return fmt.Errorf("回滚失败: %w", err)
			}
			fmt.Fprintf(out, "已回滚   %s\n", target)
			fmt.Fprintf(out, "注意     正在运行的 agentd 仍是回滚前的进程，需要重启才生效。\n")
			return nil
		}

		eps, err := listEndpoints(targetName)
		if err != nil {
			return err
		}
		rel, err := newReleaseChecker().Latest(ctx)
		if err != nil {
			return fmt.Errorf("检查最新版本失败: %w", err)
		}

		states := make([]machineState, 0, len(eps))
		for _, ep := range eps {
			states = append(states, probeMachine(ctx, ep))
		}

		fmt.Fprintf(out, "最新     %s\n", rel.Tag)
		if !upgradeNow {
			for _, s := range states {
				renderCheckRow(out, s, rel.Tag)
			}
			return nil
		}

		// --now：远端全部处理完，本机最后——本机换版会重启操作者正用着的
		// agentd，把干扰最大的一步放最后，前面出问题时不至于白扰一次
		sort.SliceStable(states, func(i, j int) bool {
			return !states[i].Ep.Local && states[j].Ep.Local
		})

		var ok, skip, fail int
		for i := range states {
			recordOrder(states[i].Ep.Name)
			switch states[i].process(ctx, cmd, rel) {
			case outcomeOK:
				ok++
			case outcomeSkip:
				skip++
			case outcomeFail:
				fail++
			}
		}
		slog.Default().Info("升级完成", "总数", len(states), "成功", ok, "跳过", skip, "失败", fail)
		if fail > 0 {
			return fmt.Errorf("有 %d 台机器升级失败", fail)
		}
		return nil
	},
}

// probeMachine 对一台机器做只读探测，填 machineState。
func probeMachine(ctx context.Context, ep Endpoint) machineState {
	ms := machineState{Ep: ep}
	if ep.Local {
		bi, _ := buildinfo.Read()
		ms.Bin = bi.Version
	}
	slog.Default().Info("开始探测机器", "name", ep.Name, "addr", ep.Addr, "local", ep.Local)
	st, err := newAgentdClient(ep).Status(ctx)
	switch {
	case errors.Is(err, client.ErrStatusUnsupported):
		// 对端过旧：连 /api/status 都没有，平台视为空。这是结论不是失败
		slog.Default().Warn("对端 agentd 过旧，未上报平台", "name", ep.Name)
	case err != nil:
		ms.Err = err
		slog.Default().Warn("探测机器失败", "name", ep.Name, "cause", err)
	default:
		ms.Agentd = st.Version.Version
		ms.Platform = st.Version.Platform
		if st.Update != nil {
			managed := st.Update.Managed
			ms.Managed = &managed
		}
		// 活跃口径：running + waiting_answer（waiting_review 可能挂几天，不计入）
		for _, a := range st.Active {
			if a.State == string(proto.TaskStateRunning) ||
				a.State == string(proto.TaskStateWaitingAnswer) {
				ms.Busy++
			}
		}
	}
	return ms
}

// renderCheckRow 渲染巡检表的一行（--check 默认行为）。
//
// 行内三段：名字（定宽）、信息、结论。结论一律来自 classify，本函数只负责把
// verdict 翻译成一句话——判据不在这里，改判据请改 classify（B64）。
//
// 本机必须分别显示二进制与 agentd 两个版本——换掉磁盘上的文件后正在跑的 agentd
// 仍是旧进程，这是正常且常见的中间态，合成一个数字就必然骗人（B59 spec §4.1）。
func renderCheckRow(w io.Writer, s machineState, latest string) {
	name := s.Ep.Name
	v := classify(&s, latest)
	if v == verdictUnreachable {
		fmt.Fprintf(w, "%-8s 够不着（%s）\n", name, s.Err)
		return
	}
	info := s.Agentd
	if s.Ep.Local {
		info = "二进制 " + dispVer(s.Bin)
		if s.Agentd == "" {
			info += " · agentd 未运行"
		} else {
			info += " · agentd " + s.Agentd
		}
	}
	fmt.Fprintf(w, "%-8s %s%s%s\n", name, info, checkPad(info), checkConclusion(v))
}

// checkConclusion 把结论翻译成巡检表里的一句话。
//
// 只有「已是最新」与「对端过旧」值得单独措辞：其余几格（非托管、未上报托管、
// 该升级）在只读巡检下的行动含义相同——都是「这台机器落后了」，差别体现在
// --now 的处置里，不该在巡检表上提前吓人。
func checkConclusion(v verdict) string {
	switch v {
	case verdictLatest:
		return "已是最新"
	case verdictTooOld:
		return "对端过旧（未上报平台）"
	default:
		return "需要升级"
	}
}

// dispVer 渲染版本号；空串表示不是 release 构建，如实说明而不是留空。
func dispVer(v string) string {
	if v == "" {
		return "unknown（非 release 构建）"
	}
	return v
}

// checkPad 把结论推到固定列（信息 8 列名 + 1 空格之后，结论对齐在 ~38 列）。
func checkPad(info string) string {
	n := 38 - len([]rune(info))
	if n < 2 {
		n = 2
	}
	return strings.Repeat(" ", n)
}

// isLatest 判断一台机器是否已是最新版本。
//
// 本机 agentd 未运行时只比二进制版本；运行时两者都要对齐（二进制已最新但
// agentd 还是旧进程 = 中间态，仍需要重启这一步）。
func (ms *machineState) isLatest(latest string) bool {
	if ms.Ep.Local {
		if ms.Agentd == "" {
			return ms.Bin == latest
		}
		return ms.Bin == latest && ms.Agentd == latest
	}
	return ms.Agentd == latest
}

// process 对一台机器执行升级，返回结果分类。
//
// 结论来自 classify（唯一判据），本函数只负责把结论翻译成动作与处置建议。
// busy 闸不在 classify 里：它是「要不要现在换」的闸，只在确实需要换版时才成立
// ——否则会对一台已是最新的忙机器建议 --force，而那条命令跑完只会说「已是最新」。
func (ms *machineState) process(ctx context.Context, cmd *cobra.Command, rel release.Release) outcome {
	out := cmd.OutOrStdout()
	name := ms.Ep.Name
	peer := newAgentdClient(ms.Ep)
	v := classify(ms, rel.Tag)
	slog.Default().Info("开始处理机器", "name", name, "addr", ms.Ep.Addr, "local", ms.Ep.Local,
		"verdict", v.String(), "platform", ms.Platform, "busy", ms.Busy)

	switch v {
	case verdictUnreachable:
		// 只报原始错误原文，不编处置——编一条建议就是在猜，而猜出来的建议会把人
		// 引到错误的方向
		slog.Default().Warn("机器够不着", "name", name, "cause", ms.Err)
		fmt.Fprintf(out, "%-8s 失败   %s\n", name, ms.Err)
		return outcomeFail

	case verdictLatest:
		slog.Default().Info("跳过：已是最新", "name", name)
		fmt.Fprintf(out, "%-8s 跳过   已是最新\n", name)
		return outcomeSkip

	case verdictTooOld:
		// 明确拒绝而不是猜一个默认平台——猜错就是给一台 linux 机器推 darwin 二进制
		slog.Default().Info("跳过：对端未上报平台", "name", name)
		fmt.Fprintf(out, "%-8s 跳过   对端 agentd 过旧，未上报平台，需先手工升级到 ≥v0.1.1\n", name)
		return outcomeSkip

	case verdictAgentdDown:
		return ms.swapAndTell(ctx, out, rel, "agentd 未运行，请自行重启它")

	case verdictUnmanaged:
		if ms.Ep.Local {
			return ms.swapAndTell(ctx, out, rel, "agentd 非托管启动，请自行重启它")
		}
		slog.Default().Info("跳过：agentd 非托管", "name", name)
		fmt.Fprintf(out, "%-8s 跳过   agentd 非托管启动，重启后不会被拉起\n", name)
		// 不给 --force：它不越过闸二，给了就是让用户跑一条注定失败的命令
		fmt.Fprintf(out, "         先在该机器上 handoff service install\n")
		return outcomeSkip

	case verdictManagedUnknown:
		if ms.Ep.Local {
			return ms.swapAndTell(ctx, out, rel, "无法确认 agentd 是否托管启动，请自行重启它")
		}
		// 不猜托管（猜错=换完没人拉起，这台机器就此没有 agentd 且无人知晓），
		// 也不猜非托管（猜错=把人引去装一个可能早已装好的 service，即 B64 原症状）
		slog.Default().Info("跳过：对端未上报托管状态", "name", name)
		fmt.Fprintf(out, "%-8s 跳过   对端未上报托管状态，无法确认换版后能否被拉起\n", name)
		return outcomeSkip
	}

	// verdictNeedsUpgrade：闸一（活跃任务，--force 可越过）
	if ms.Busy > 0 && !upgradeForce {
		slog.Default().Info("跳过：有活跃任务", "name", name, "busy", ms.Busy)
		fmt.Fprintf(out, "%-8s 跳过   %d 个活跃任务\n", name, ms.Busy)
		if ms.Ep.Local {
			fmt.Fprintf(out, "         handoff upgrade --now --force\n")
		} else {
			fmt.Fprintf(out, "         handoff upgrade --now --target %s --force\n", name)
		}
		return outcomeSkip
	}
	if ms.Ep.Local {
		return ms.localUpgrade(ctx, out, peer, rel)
	}
	return ms.remoteUpgrade(ctx, out, peer, rel)
}

// swapAndTell 只换本机文件、不触发重启，并如实说明「为什么要你自己重启」。
//
// 三种本机情形共用（agentd 未运行 / 非托管 / 托管状态未知）：都不能靠接口重启，
// 差别只在给操作者的那句理由，所以理由由调用方传入。
func (ms *machineState) swapAndTell(ctx context.Context, out io.Writer, rel release.Release, why string) outcome {
	oc := ms.localSwap(ctx, out, rel)
	if oc == outcomeOK {
		fmt.Fprintf(out, "%-8s 成功   已换文件；%s\n", ms.Ep.Name, why)
	}
	return oc
}

// localSwap 只换本机文件（并同步 skill），不触发重启。
//
// 供两条路径用：agentd 未运行、agentd 非托管。这两种情况都不能靠接口重启，
// 换完文件如实提示由用户自己把 agentd 起回来。
func (ms *machineState) localSwap(ctx context.Context, out io.Writer, rel release.Release) outcome {
	name := ms.Ep.Name
	target, err := currentBinary()
	if err != nil {
		fmt.Fprintf(out, "%-8s 失败   取当前二进制路径: %s\n", name, err)
		return outcomeFail
	}
	newPath, err := newReleaseFetcher().Fetch(ctx, rel, filepath.Dir(target))
	if err != nil {
		slog.Default().Error("下载本机资产失败", "name", name, "cause", err)
		fmt.Fprintf(out, "%-8s 失败   %s\n", name, err)
		return outcomeFail
	}
	if _, err := activateBinary(newPath, target); err != nil {
		slog.Default().Error("替换本机二进制失败", "name", name, "cause", err)
		fmt.Fprintf(out, "%-8s 失败   替换二进制: %s\n", name, err)
		return outcomeFail
	}
	ms.syncSkill(ctx, out, target)
	return outcomeOK
}

// localUpgrade 本机的完整升级路径：换文件 + 同步 skill + 触发 agentd 重启。
func (ms *machineState) localUpgrade(ctx context.Context, out io.Writer, peer agentdPeer, rel release.Release) outcome {
	name := ms.Ep.Name
	target, err := currentBinary()
	if err != nil {
		fmt.Fprintf(out, "%-8s 失败   取当前二进制路径: %s\n", name, err)
		return outcomeFail
	}
	newPath, err := newReleaseFetcher().Fetch(ctx, rel, filepath.Dir(target))
	if err != nil {
		slog.Default().Error("下载本机资产失败", "name", name, "cause", err)
		fmt.Fprintf(out, "%-8s 失败   %s\n", name, err)
		return outcomeFail
	}
	prev, err := activateBinary(newPath, target)
	if err != nil {
		slog.Default().Error("替换本机二进制失败", "name", name, "cause", err)
		fmt.Fprintf(out, "%-8s 失败   替换二进制: %s\n", name, err)
		return outcomeFail
	}
	ms.syncSkill(ctx, out, target)
	if _, err := peer.RestartAgentd(ctx, upgradeForce); err != nil {
		slog.Default().Error("触发本机 agentd 重启失败", "name", name, "cause", err)
		fmt.Fprintf(out, "%-8s 失败   触发重启: %s\n", name, err)
		return outcomeFail
	}
	slog.Default().Info("本机升级完成", "name", name, "target", target, "prev", prev, "version", rel.Tag)
	fmt.Fprintf(out, "%-8s 成功   已换到 %s，已触发 agentd 重启\n", name, rel.Tag)
	return outcomeOK
}

// syncSkill 在新二进制上同步 skill。
//
// 当前进程是旧二进制，它内嵌的是**旧 skill**；新 skill 在刚换上去的那个
// 文件里。所以是 exec 新二进制，不是直接调用本进程的 skill.Install。失败
// 不算升级失败：二进制已经换好了，但必须说出来——悄悄留一份旧 skill，它会
// 按已经变了的规则主动误导协调者。
func (ms *machineState) syncSkill(ctx context.Context, out io.Writer, target string) {
	if outStr, err := execSkillInstall(ctx, target); err != nil {
		fmt.Fprintf(out, "         注意 skill 同步失败，请手动跑 %s skill install：%s\n",
			target, firstLineOf(outStr))
	}
}

// remoteUpgrade 远端的完整升级路径：按远端平台下载 → 推送 → 轮询确认上线。
func (ms *machineState) remoteUpgrade(ctx context.Context, out io.Writer, peer agentdPeer, rel release.Release) outcome {
	name := ms.Ep.Name
	parts := strings.SplitN(ms.Platform, "/", 2)
	if len(parts) != 2 {
		fmt.Fprintf(out, "%-8s 失败   对端上报的平台 %q 格式非法\n", name, ms.Platform)
		return outcomeFail
	}
	slog.Default().Info("下载远端平台资产", "name", name, "platform", ms.Platform, "tag", rel.Tag)
	tgz, sum, err := newReleaseFetcher().FetchArchive(ctx, rel, parts[0], parts[1])
	if err != nil {
		slog.Default().Error("下载远端资产失败", "name", name, "cause", err)
		fmt.Fprintf(out, "%-8s 失败   %s\n", name, err)
		return outcomeFail
	}
	slog.Default().Info("下载完成，推送换版请求", "name", name, "tag", rel.Tag, "bytes", len(tgz))
	resp, err := peer.PushUpdate(ctx, rel.Tag, sum, tgz, upgradeForce)
	if err != nil {
		var rej *client.UpdateRejected
		if errors.As(err, &rej) {
			slog.Default().Warn("换版被拒", "name", name, "reason", rej.Reason, "detail", rej.Msg)
			fmt.Fprintf(out, "%-8s 跳过   %s\n", name, rej.Msg)
			// 处置建议对症：busy 能 --force 越过，unmanaged 不能
			switch rej.Reason {
			case proto.UpdateReasonBusy:
				fmt.Fprintf(out, "         handoff upgrade --now --target %s --force\n", name)
			case proto.UpdateReasonUnmanaged:
				fmt.Fprintf(out, "         先在该机器上 handoff service install\n")
			}
			return outcomeSkip
		}
		slog.Default().Error("推送换版失败", "name", name, "cause", err)
		fmt.Fprintf(out, "%-8s 失败   %s\n", name, err)
		return outcomeFail
	}
	slog.Default().Info("换版已受理，等待新版本上线", "name", name, "version", resp.Version, "prev", resp.Prev)
	if err := peer.WaitVersion(ctx, rel.Tag, upgradeWaitTimeout, upgradeWaitInterval); err != nil {
		// 轮询超时是最要紧的一条：报「已换版但新进程未上线」，绝不含糊成「升级完成」
		slog.Default().Error("等待新版本上线超时", "name", name, "prev", resp.Prev, "cause", err)
		fmt.Fprintf(out, "%-8s 失败   已换版但新进程未在 %ds 内上线\n", name, int(upgradeWaitTimeout.Seconds()))
		fmt.Fprintf(out, "         prev: %s  回滚：handoff upgrade --rollback\n", resp.Prev)
		return outcomeFail
	}
	slog.Default().Info("新版本已上线", "name", name, "version", rel.Tag)
	fmt.Fprintf(out, "%-8s 成功   %s → %s\n", name, ms.Agentd, rel.Tag)
	return outcomeOK
}

// firstLineOf 取多行输出的首行，用于把子进程的失败原因压成一行。
//
// 为什么只取首行：这行文案是缀在升级报告里的，把几十行 stderr 原样铺进去
// 会把真正的结论淹掉；完整输出用户可以自己重跑那条命令拿到。
func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeCheck, "check", false, "只检查有没有新版（默认行为）")
	upgradeCmd.Flags().BoolVar(&upgradeNow, "now", false, "升级所有落后的机器（含本机；--target 时只升那一台）")
	upgradeCmd.Flags().BoolVar(&upgradeRollback, "rollback", false, "换回本机上一版（<二进制>.prev，不支持 --target）")
	upgradeCmd.Flags().BoolVar(&upgradeForce, "force", false, "越过活跃任务闸（**不越过非托管闸**）")
	rootCmd.AddCommand(upgradeCmd)
}
