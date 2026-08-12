// Package cmd 提供 handoff 的 cobra 命令行入口。
//
// 职责：
//   - 定义根命令与全局 flag（--agentd / --target / --config）
//   - 提供 TargetEndpoint 辅助函数，供各子命令换算实际 agentd 端点
//
// 边界：
//   - 不包含具体业务逻辑（dispatch/gate 等子命令由后续任务补充）
//   - 不在此处初始化日志，由各子命令按需调用 logx.Setup
package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/buildinfo"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/release"
	"github.com/xushixin/handoff/internal/selfupdate"
)

var (
	agentdURL  string
	targetName string
	configPath string
)

var rootCmd = &cobra.Command{
	Use:   "handoff",
	Short: "handoff：把任务派发到本机/远程 agentd 执行并续接的 CLI",
	// 运行时错误只打印错误本身，不打印整页 flag 帮助（L-5）：
	// 运行期失败（配置加载失败、远端 401、任务不存在等）的根因是错误信息
	// 而非用法，usage 段只会淹没 stderr 里真正的问题。注意 SilenceErrors
	// 保持 false——cobra 默认把 RunE 返回的错误打到 stderr 且 Execute 原样
	// 返回（main 据此按 ExitCode 退出），错误绝不被静默吞掉；只消掉噪音 usage。
	//
	// 但**参数/flag 错误例外**：那类失败的根因恰恰就是用法，此时 usage 是唯一
	// 有用的信息（否则 `handoff wait` 缺参只得到一句 "accepts 1 arg(s)"，
	// 不给任何语法提示）。cobra 的 Args 校验发生在 PreRun 之前，所以把消音
	// 推迟到 PersistentPreRun：参数校验已通过 = 之后的失败都是运行期问题。
	PersistentPreRun: func(cmd *cobra.Command, _ []string) {
		cmd.SilenceUsage = true
	},
	PersistentPostRun: func(cmd *cobra.Command, _ []string) { maybeNotifyUpdate(cmd) },
}

func init() {
	rootCmd.PersistentFlags().StringVar(&agentdURL, "agentd", "http://127.0.0.1:7777", "agentd 服务地址")
	rootCmd.PersistentFlags().StringVar(&targetName, "target", "", "目标主机名（从配置 Targets 中换算 addr/token）")
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "配置文件路径（默认 ~/.handoff/config.yaml）")
	rootCmd.AddCommand(updateCheckCmd)
}

// Execute 运行根命令，错误返回给 main。
func Execute() error {
	resetPerRunState(rootCmd)
	return rootCmd.Execute()
}

// ExecuteContext 是带 ctx 的 Execute（同样先清理单次执行的残留状态）。
//
// 参数：
//   - ctx: 传给命令 RunE 的上下文（取消即中断长驻命令，如 wait）
func ExecuteContext(ctx context.Context) error {
	resetPerRunState(rootCmd)
	return rootCmd.ExecuteContext(ctx)
}

// resetPerRunState 清掉命令树上「属于上一次执行」的残留状态。
//
// 为什么必须清（进程内复用命令树时才暴露，如测试与未来的交互式复用）：
//   - ctx：cobra 只在子命令 ctx 为 nil 时才把根的 ctx 传下去
//     （ExecuteC 里的 `if cmd.ctx == nil`）。第二次执行时子命令还挂着上一次
//     那个**已取消**的 ctx，新 ctx 传不进去——命令一进 RunE 就拿到
//     context canceled，表现为「刚启动就被取消」的假故障
//   - SilenceUsage：PersistentPreRun 会把它置为 true（见 rootCmd 的 why），
//     不清零则第二次执行时连参数错误也不再打 usage
func resetPerRunState(c *cobra.Command) {
	// 这里必须置 nil（而不是 context.TODO 之类的占位）：cobra 正是靠
	// `cmd.ctx == nil` 判断「这个子命令还没有自己的 ctx，把根的传下去」，
	// 塞任何非 nil 值都会让新 ctx 传不进子命令
	c.SetContext(nil)
	c.SilenceUsage = false
	for _, sub := range c.Commands() {
		resetPerRunState(sub)
	}
}

// Endpoint 是一台可被升级的机器。
//
// Local 为 true 时 Name 恒为「本机」：它的二进制由 CLI 直接换（文件就在本地），
// 与远端走的是两条不同的路径（spec §4.2）。
type Endpoint struct {
	Name  string
	Addr  string
	Token string
	Local bool
}

// Endpoints 返回要处理的机器清单。
//
// 参数：
//   - only: 为空时返回 [本机, 全部 target（按名字排序）]；非空时只返回该 target
//
// 返回：
//   - 机器清单；only 指定的 target 不存在时返回错误
//
// 为什么本机也在清单里：版本一致本身就是要解决的问题，把本机排除在外，
// 「本机新远端旧」就会成为常态。而操作者不必记住配置里有哪些 target——
// 这正是「一条命令看清所有机器」的前提（spec D2）。
func Endpoints(only string) ([]Endpoint, error) {
	p := configPath
	if p == "" {
		p = config.DefaultPath()
	}
	cfg, err := config.Load(p)
	if err != nil {
		return nil, fmt.Errorf("加载配置 %s: %w", p, err)
	}
	if only != "" {
		t, ok := cfg.Targets[only]
		if !ok {
			return nil, fmt.Errorf("target %q 未在配置 %s 中定义", only, p)
		}
		return []Endpoint{{Name: only, Addr: "http://" + t.Addr, Token: t.Token}}, nil
	}
	local := cfg.Listen
	if !strings.Contains(local, "://") {
		local = "http://" + local
	}
	eps := []Endpoint{{Name: "本机", Addr: local, Token: cfg.Token, Local: true}}
	names := make([]string, 0, len(cfg.Targets))
	for n := range cfg.Targets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		eps = append(eps, Endpoint{Name: n, Addr: "http://" + cfg.Targets[n].Addr, Token: cfg.Targets[n].Token})
	}
	return eps, nil
}

// TargetEndpoint 根据 --target / --agentd / --config 换算实际请求的 agentd 端点与令牌。
//
// 参数（读取全局 flag）：
//   - --target 为空（本机模式）：token 一律取本地配置 cfg.Token（服务端无条件要求
//     Bearer，无 token 的本机调用必然 401）；地址取 --agentd（显式指定时优先）或
//     cfg.Listen（与 agentd 实际监听一致），cfg.Token 为空时返回错误
//   - --target 非空：从配置 Targets 中查出 addr/token（远程配对）
//
// 返回：
//   - addr: agentd 完整地址（含 http:// 前缀）
//   - token: 访问令牌
//   - err: 配置加载失败、target 未定义或本机 token 为空时返回
func TargetEndpoint() (addr, token string, err error) {
	p := configPath
	if p == "" {
		p = config.DefaultPath()
	}
	cfg, err := config.Load(p)
	if err != nil {
		return "", "", fmt.Errorf("加载配置 %s: %w", p, err)
	}
	if targetName == "" {
		// 本机模式：token 必须来自本地配置——agentd 的 Bearer 鉴权无条件生效，
		// 且 config.Load 保证配置文件存在时 token 一定已生成（首次运行即写盘）
		if cfg.Token == "" {
			return "", "", fmt.Errorf("配置 %s 未设置 token，无法认证本机 agentd", p)
		}
		// 地址优先级：显式 --agentd 优先（用户指明了别的端点）；未显式指定时
		// 用配置 Listen，与 agentd 实际监听保持一致，避免默认值与配置漂移
		if rootCmd.PersistentFlags().Changed("agentd") {
			addr = agentdURL
		} else {
			addr = cfg.Listen
			if !strings.Contains(addr, "://") {
				addr = "http://" + addr
			}
		}
		return addr, cfg.Token, nil
	}
	t, ok := cfg.Targets[targetName]
	if !ok {
		return "", "", fmt.Errorf("target %q 未在配置 %s 中定义", targetName, p)
	}
	return "http://" + t.Addr, t.Token, nil
}

// LocalEndpoint 返回**本机** agentd 的地址与令牌，忽略 --target。
//
// 返回：
//   - addr: 本机 agentd 完整地址（含 http:// 前缀）
//   - token: 本机令牌
//   - err: 配置加载失败或本机 token 为空时返回
//
// 为什么需要它而不是复用 TargetEndpoint：登记是**两跳**（本机 + 目标机，
// spec §6.1），而 TargetEndpoint 读的是包级 targetName，指定了 --target 时
// 拿不到本机端点。两跳都要发，就必须有一个不受 --target 影响的取端点入口。
func LocalEndpoint() (addr, token string, err error) {
	saved := targetName
	targetName = ""
	defer func() { targetName = saved }()
	return TargetEndpoint()
}

// loadCLIConfig 加载 CLI 侧配置（wait/dispatch/pull 等子命令读同步/终端偏好）。
// 配置加载失败时返回空配置：偏好项（Sync.Auto / Terminal.Auto）取默认值即可，
// 真正的配置错误由 TargetEndpoint 在更早处暴露。
func loadCLIConfig() *config.Config {
	p := configPath
	if p == "" {
		p = config.DefaultPath()
	}
	cfg, err := config.Load(p)
	if err != nil {
		return &config.Config{}
	}
	return cfg
}

// updateCheckCmd 是被 CLI 自己以后台进程方式拉起的隐藏子命令：查一次版本、
// 写缓存、退出。
//
// 为什么要一个独立进程而不是在当前命令里起 goroutine：CLI 进程通常在几十
// 毫秒内就退出了，goroutine 会被一起带走，那个"异步检查"永远跑不完。
// 独立子进程在父进程退出后被 init 收养，能安安静静地把检查做完。
var updateCheckCmd = &cobra.Command{
	Use:    "update-check",
	Short:  "内部命令：后台检查最新版本并写缓存",
	Hidden: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := config.Load(effectiveConfigPath())
		if err != nil {
			return err
		}
		rel, err := release.NewClient().Latest(cmd.Context())
		if err != nil {
			return err
		}
		return selfupdate.SaveCLICheck(cfg.DataDir, &selfupdate.CLICheck{
			CheckedAt: time.Now().UTC(), Latest: rel.Tag,
		})
	},
}

// maybeNotifyUpdate 在每条命令跑完后打一行更新提示，并在缓存过期时拉起一次
// 后台检查。
//
// 注意：
//   - 提示打在 **stderr**：stdout 是各命令的机器可读输出（dispatch 的 JSON、
//     tasks 的每行 JSON），往里掺一行人话会让 jq 直接失败
//   - 任何一步失败都静默跳过。这条路径挂在每一条命令上，它自己绝不能成为
//     故障源——少提示一次更新，比让所有命令都报错好得多
//   - 隐藏子命令自己不触发（否则每次后台检查又拉起一个后台检查）
func maybeNotifyUpdate(cmd *cobra.Command) {
	if cmd.Name() == updateCheckCmd.Name() || cmd.Name() == upgradeCmd.Name() {
		return
	}
	cfg, err := config.Load(effectiveConfigPath())
	if err != nil {
		return
	}
	c := selfupdate.LoadCLICheck(cfg.DataDir)
	bi, _ := buildinfo.Read()
	if line := selfupdate.NotifyLine(c, bi.Version); line != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), line)
	}
	if !selfupdate.CLICheckStale(c, time.Now().UTC()) {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	// 不等它：Start 之后本进程照常退出，子进程被 init 收养后自己跑完
	bg := exec.Command(exe, "update-check", "--config", effectiveConfigPath())
	bg.Stdout, bg.Stderr, bg.Stdin = nil, nil, nil
	if err := bg.Start(); err != nil {
		return
	}
	// 必须 Release：不回收也不等待的子进程会变成僵尸，虽然本进程马上就退了，
	// 但在 `handoff wait` 这种长命命令里会实实在在留一个
	_ = bg.Process.Release()
}
