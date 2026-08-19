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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/buildinfo"
	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/proxycfg"
	"github.com/Xsxdot/handoff/internal/release"
	"github.com/Xsxdot/handoff/internal/upgrade"
	"github.com/spf13/cobra"
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
	// FetchChecksum 只下 checksums.txt 取某平台的 sha256，供自拉模式下发。
	// 不下资产——这正是自拉的省流量点
	FetchChecksum(ctx context.Context, rel release.Release, goos, goarch string) (string, error)
}

// agentdPeer 是 upgrade 需要的 agentd 能力子集。
//
// 声明成接口而不是直接用 *client.Client：这条命令会真的推二进制并重启对端，
// 测试必须能整体替换掉，漏替一个就会在 CI 上真的去动一台机器。
type agentdPeer interface {
	Status(ctx context.Context) (*proto.StatusResp, error)
	PushUpdate(ctx context.Context, tag, sum string, tgz []byte, force bool) (*proto.UpdateResp, error)
	PullUpdate(ctx context.Context, tag, sum string, force bool) (*proto.UpdateResp, error)
	RestartAgentd(ctx context.Context, force bool) (*proto.UpdateResp, error)
	WaitVersion(ctx context.Context, want string, timeout, interval time.Duration, checkPull bool) error
}

// 七个缝，测试替换它们以避免联网、动真实二进制与真实 agentd。
var (
	newReleaseChecker = func() releaseChecker {
		// 每次调用重新 loadCLIConfig：这两个缝在测试里会被整体替换，生产路径上
		// 一条命令最多调一两次，重读一次 YAML 的代价远小于把配置提到包级变量后
		// 与 --config 标志的求值时序纠缠
		return release.NewClient(proxyTransport(loadCLIConfig()))
	}
	newReleaseFetcher = func() releaseFetcher {
		return release.NewInstaller(slog.Default(), proxyTransport(loadCLIConfig()))
	}
	activateBinary = release.Activate
	rollbackBinary = release.Rollback
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

// upgradeWaitTimeoutPull / upgradeWaitTimeoutPush / upgradeWaitInterval 是换版后
// 等新进程上线的时限与轮询间隔。
//
// 自拉模式下对端要下 20MB（慢网 + 代理下几分钟很正常），放宽到 10min，
// 且 WaitVersion 在 pull 模式会读对端 pull_state，真失败时立刻中止、不会干等满。
// 推送模式二进制已经在对端，换版本身是秒级动作——但重启不一定是。
// macOS/Linux 上管理器会很快拉回 exit 0 的进程；Windows 上只能靠计划任务每分钟
// 的重复触发模拟，最坏空窗接近 60 秒。旧值 60s 恰好压在线上，取两倍余量；一次
// 真起不来的推送换版让操作者多等 60 秒，可接受。
const (
	upgradeWaitTimeoutPull = 10 * time.Minute
	upgradeWaitTimeoutPush = 120 * time.Second
	upgradeWaitInterval    = 2 * time.Second
)

var (
	upgradeCheck    bool
	upgradeNow      bool
	upgradeRollback bool
	upgradeForce    bool
	upgradePush     bool
)

// machineState 是一台机器巡检后的状态。
//
// Bin 是本机 CLI 的版本（远端不用）；Agentd 是 agentd 版本（本机 agentd
// 未运行时为空）。Err 非空表示 status 够不着——对远端是失败，对本机只表示
// agentd 未运行（敲命令的人知道自己要不要把它起回来）。
type machineState struct {
	Ep     Endpoint
	Bin    string
	Agentd string
	// Revision 是对端上报的 VCS 提交号，仅在 Agentd（release 版本号）为空时
	// 用于渲染。非 release 构建的 agentd 没有版本号却有提交号，只显示前者会
	// 让那一行的版本列是一片空格，读表的人分不出「没探到」和「没版本号」（B147）。
	Revision string
	Platform string // 对端上报的平台；空 = 对端过旧未上报
	// Managed 是对端上报的「agentd 由进程管理器拉起」状态。
	//
	// 为什么是指针：nil 表示**对端没给这个字段**（老 agentd 不上报 Update），
	// 与「对端说 false」是两回事。用 bool 零值把前者塌成后者，就会把「我不知道」
	// 讲成「它非托管」，并据此给出一条注定白折腾的处置建议——B64 就是这么来的。
	// 与同结构里 ActiveTask.Watchers *int 是同一条纪律。
	Managed *bool
	// Pull 是对端上报的「支持自拉换版」。
	//
	// 为什么是指针：nil 表示**对端没给这个字段**（老 agentd），与「对端说 false」
	// 是两回事。老 agentd 收到 mode=pull 会当成纯重启并回 200，据此以为受理了
	// 就会干等到超时报一句误导性的「已换版但新进程未上线」。与同结构里的
	// Managed *bool 同款纪律
	Pull *bool
	Busy int
	Err  error
}

// toUpgrade 投影 CLI 侧的探测载体，供结论判据包消费。
//
// Endpoint 与 Bin 仍留在 CLI：Endpoint 是 CLI 的配置/拨号概念，Bin 是本机
// 文件路径上的版本；新包只接收一台机器的探测结果，不认识 cobra 或 Endpoint。
func (ms *machineState) toUpgrade() upgrade.Machine {
	return upgrade.Machine{
		Name: ms.Ep.Name, Local: ms.Ep.Local, Bin: ms.Bin, Agentd: ms.Agentd,
		Revision: ms.Revision, Platform: ms.Platform, Managed: ms.Managed,
		Pull: ms.Pull, Busy: ms.Busy, Err: ms.Err,
	}
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

// proxyTransport 按配置造更新链路用的 HTTP transport。
//
// 参数：
//   - cfg: 已加载的配置
//
// 返回：
//   - 配了代理的 *http.Transport；**未配代理或配置有问题时返回 nil**，
//     调用方把 nil 直接传给 release.NewClient/NewInstaller 即为标准库默认行为
//
// 注意：
//   - 坏代理走到这里只可能是绕过了 config.Load 的校验（那里已经硬拒过一道）。
//     此时降级为不用代理并打 Error，而不是 panic 或让整条命令失败——
//     升级链路本身不该因为一个附属设置而彻底不可用
func proxyTransport(cfg *config.Config) http.RoundTripper {
	if cfg == nil || cfg.Proxy == "" {
		return nil
	}
	tr, err := proxycfg.Transport(cfg.Proxy)
	if err != nil {
		slog.Default().Error("代理配置无法使用，本次出网不走代理",
			"proxy", proxycfg.Redact(cfg.Proxy), "cause", err)
		return nil
	}
	slog.Default().Info("更新链路使用代理", "proxy", proxycfg.Redact(cfg.Proxy))
	return tr
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

		// checksums.txt 对一个 release 只下一次，多台机器共用同一份内容
		//（按各自平台取自己那行）。每台机器各下一次会把自拉省下的流量
		// 还回去一部分，还平白多几次 GitHub 请求——而它有 60 次/小时/IP 的限流。
		// 缓存键是 goos/goarch：不同平台的校验和本来就不同，必须各下一次
		sumCache := map[string]string{}
		var sumMu sync.Mutex
		sumFor := func(ctx context.Context, goos, goarch string) (string, error) {
			key := goos + "/" + goarch
			sumMu.Lock()
			defer sumMu.Unlock()
			if s, ok := sumCache[key]; ok {
				return s, nil
			}
			s, err := newReleaseFetcher().FetchChecksum(ctx, rel, goos, goarch)
			if err != nil {
				return "", err
			}
			sumCache[key] = s
			return s, nil
		}

		var ok, skip, fail int
		for i := range states {
			recordOrder(states[i].Ep.Name)
			switch states[i].process(ctx, cmd, rel, sumFor) {
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
		ms.Revision = st.Version.Revision
		ms.Platform = st.Version.Platform
		if st.Update != nil {
			managed := st.Update.Managed
			ms.Managed = &managed
			ms.Pull = st.Update.Pull
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
// 行内三段：名字（定宽）、信息、结论。结论一律来自 upgrade.Classify，本函数只负责把
// Verdict 翻译成一句话——判据不在这里，改判据请改 internal/upgrade（B64）。
//
// 本机必须分别显示二进制与 agentd 两个版本——换掉磁盘上的文件后正在跑的 agentd
// 仍是旧进程，这是正常且常见的中间态，合成一个数字就必然骗人（B59 spec §4.1）。
func renderCheckRow(w io.Writer, s machineState, latest string) {
	name := s.Ep.Name
	v := upgrade.Classify(s.toUpgrade(), latest)
	if v == upgrade.VerdictUnreachable {
		fmt.Fprintf(w, "%-8s 够不着（%s）\n", name, s.Err)
		return
	}
	info := s.Agentd
	if info == "" {
		info = revisionFallback(s.Revision)
	}
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
func checkConclusion(v upgrade.Verdict) string {
	switch v {
	case upgrade.VerdictLatest:
		return "已是最新"
	case upgrade.VerdictTooOld:
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

// revisionFallback 在没有 release 版本号时渲染提交号，两者都没有才留空。
//
// 参数：rev 为对端上报的 VCS 提交号（可能为空）。
//
// 为什么截到 12 位：与 handoff status 的版本行同宽，两处对同一台机器的称呼
// 必须一致，否则对照两份输出时会以为是两台机器。
func revisionFallback(rev string) string {
	if rev == "" {
		return ""
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	return rev + "（非 release 构建）"
}

// checkPad 把结论推到固定列（信息 8 列名 + 1 空格之后，结论对齐在 ~38 列）。
func checkPad(info string) string {
	n := 38 - len([]rune(info))
	if n < 2 {
		n = 2
	}
	return strings.Repeat(" ", n)
}

// process 对一台机器执行升级，返回结果分类。
//
// 结论来自 upgrade.Classify（唯一判据），本函数只负责把结论翻译成动作与处置建议。
// busy 闸不在 Classify 里：它是「要不要现在换」的闸，只在确实需要换版时才成立
// ——否则会对一台已是最新的忙机器建议 --force，而那条命令跑完只会说「已是最新」。
func (ms *machineState) process(ctx context.Context, cmd *cobra.Command, rel release.Release, sumFor func(context.Context, string, string) (string, error)) outcome {
	out := cmd.OutOrStdout()
	name := ms.Ep.Name
	peer := newAgentdClient(ms.Ep)
	v := upgrade.Classify(ms.toUpgrade(), rel.Tag)
	slog.Default().Info("开始处理机器", "name", name, "addr", ms.Ep.Addr, "local", ms.Ep.Local,
		"verdict", v.String(), "platform", ms.Platform, "busy", ms.Busy)
	if !ms.Ep.Local {
		return ms.remoteUpgrade(ctx, out, peer, rel, sumFor)
	}

	switch v {
	case upgrade.VerdictLatest:
		slog.Default().Info("跳过：已是最新", "name", name)
		fmt.Fprintf(out, "%-8s 跳过   已是最新\n", name)
		return outcomeSkip

	case upgrade.VerdictAgentdDown:
		return ms.swapAndTell(ctx, out, rel, "agentd 未运行，请自行重启它")

	case upgrade.VerdictUnmanaged:
		return ms.swapAndTell(ctx, out, rel, "agentd 非托管启动，请自行重启它")

	case upgrade.VerdictManagedUnknown:
		return ms.swapAndTell(ctx, out, rel, "无法确认 agentd 是否托管启动，请自行重启它")
	}

	// 本机路径仍在 CLI：闸一只在本机这里施加；远端由 RemoteOne 统一施加。
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
	return ms.localUpgrade(ctx, out, peer, rel)
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

// remoteUpgrade 远端的完整升级路径。
//
// 结论、两道闸、选路、等待与失败语义由 internal/upgrade 统一负责；本函数只把
// CLI 的下载缝接上，并将结构化结果渲染成既有的两行表格输出。
type cliRemoteFetcher struct {
	releaseFetcher
	sumFor func(context.Context, string, string) (string, error)
}

func (f cliRemoteFetcher) FetchChecksum(ctx context.Context, rel release.Release, goos, goarch string) (string, error) {
	if f.sumFor != nil {
		return f.sumFor(ctx, goos, goarch)
	}
	return f.releaseFetcher.FetchChecksum(ctx, rel, goos, goarch)
}

func (ms *machineState) remoteUpgrade(ctx context.Context, out io.Writer, peer agentdPeer,
	rel release.Release, sumFor func(context.Context, string, string) (string, error)) outcome {
	name := ms.Ep.Name
	result := upgrade.RemoteOne(ctx, slog.Default(), ms.toUpgrade(), peer,
		cliRemoteFetcher{releaseFetcher: newReleaseFetcher(), sumFor: sumFor}, rel,
		upgrade.Options{
			Force: upgradeForce, PreferPush: upgradePush,
			WaitPull: upgradeWaitTimeoutPull, WaitPush: upgradeWaitTimeoutPush,
			WaitInterval: upgradeWaitInterval,
		}, nil)
	switch result.Status {
	case upgrade.StatusOK:
		fmt.Fprintf(out, "%-8s 成功   %s → %s\n", name, result.From, result.To)
		return outcomeOK
	case upgrade.StatusSkip:
		fmt.Fprintf(out, "%-8s 跳过   %s\n", name, result.Reason)
		if result.Remedy != "" {
			fmt.Fprintf(out, "         %s\n", result.Remedy)
		}
		return outcomeSkip
	default:
		fmt.Fprintf(out, "%-8s 失败   %s\n", name, result.Reason)
		if result.Remedy != "" {
			fmt.Fprintf(out, "         %s\n", result.Remedy)
		}
		return outcomeFail
	}
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
	upgradeCmd.Flags().BoolVar(&upgradePush, "push", false,
		"强制由本机下载并推送二进制（默认让执行机自己下；执行机出不了网时用它）")
	rootCmd.AddCommand(upgradeCmd)
}
