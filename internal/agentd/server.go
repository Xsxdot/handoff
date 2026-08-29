// 本文件实现 agentd 对外唯一的网络入口：HTTP API（任务列表/详情/reply/审阅命令）与 WS 事件流。
//
// 职责：
//   - 对全部 /api 与 /ws 路由做 Bearer token 鉴权
//   - 提供任务查询（attach 数据源：任务 + 待办工单 + 最近事件）
//   - 实现 reply 唤醒闭环的回程：AnswerTicket → NotifyAnswer（无等待者时经 manager
//     RelayAnswer 自愈中继）→ 无其余待办工单时状态回迁 running
//   - 提供三条审阅命令路由（diff/fetch/run）：调 workspace 包取任务仓库的
//     审阅素材（git diff、文件内容、远程跑测试/lint），run 不走审批门
//   - /ws/events 先订阅 hub 实时流，再补发 store 中 seq>n 的历史事件（重放期间实时
//     事件经排空器收集、按 seq 归并去重），窗口期事件不丢不重
//
// 边界：
//   - 不创建 ticket：ticket 由 manager（Task 8）把 adapter 事件中介成 ticket 后落库，本层只回答
//   - 不启动 executor、不执行任务；状态迁移仅限 reply 触发的 waiting_answer → running 回迁
//   - 审阅路由只读任务仓库（diff/fetch），run 的命令执行也限时回收，绝不经此写仓库
//   - 实时流不保证每条事件都送达：事件不丢不重由 store 的 seq + 客户端自存 cursor（无需 ack）承担，
//     掉线期间产生的事件由客户端携带更大 from_seq 重连补拉
package agentd

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	charterwebui "github.com/Xsxdot/charter/graph/webui"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/collab/cursor"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/proxycfg"
	"github.com/Xsxdot/handoff/internal/ptyapi"
	"github.com/Xsxdot/handoff/internal/ptyhost"
	"github.com/Xsxdot/handoff/internal/release"
	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/targetclient"
	"github.com/Xsxdot/handoff/internal/toolchain"
	"github.com/Xsxdot/handoff/internal/webui"
	"github.com/coder/websocket"
)

// recentEventsLimit 是任务详情接口返回的最近事件条数上限。
const recentEventsLimit = 100

// eventReplayLimit 是 WS 连接一次性补发历史事件的上限：
// 客户端 cursor 落后超过该值时说明断连过久，应由客户端重连分批补拉，避免单连接推流过多。
const eventReplayLimit = 10000

// liveBufferLimit 是单个 WS 连接实时事件待写缓冲的条数上限。
//
// why（必须有上限）：排空器把订阅通道的事件收进内存切片以避免 hub 的慢订阅者
// 丢弃，但对端「连着不读」（合盖的笔记本、黑洞路由）时写循环会阻塞任意久，
// 无上限的缓冲会把「实时事件被丢弃」换成「agentd 内存无限增长」。
// 越限即断开连接：所有广播事件都已落库，客户端凭 cursor 重连即可完整补拉，
// 断开是无损的。
const liveBufferLimit = 1000

// maxUpdateBytes 是换版接口单个请求体的上限，与 release 侧 maxAssetBytes 同量级。
//
// 上限本身是防线：被劫持或出错的请求不该把内存吃光——换版 body 是整包
// tar.gz 原文，失控请求直接进 io.ReadAll，无上限等于把内存消耗交给对端。
const maxUpdateBytes = 100 << 20

// Server 是 agentd 的 HTTP/WS 服务端，持有配置、存储与进程内实时路由 hub。
//
// 并发安全：**除 cfg 与升级槽位外**所有字段只读（构造后不变），hub 自身线程安全。
// cfg 是可变的——控制台增删开发机会整体换掉它（写时复制），因此一律经
// s.conf() 读取；**禁止再引入直接持有 *config.Config 的字段**，那会让
// 同样的错误从编译错误退化成静默竞态。升级槽位由 upgradeMu 保护。
type Server struct {
	cfg atomic.Pointer[config.Config]
	// cfgMu 只序列化写入方（swapConf）。读取方走 atomic 快照，不加锁。
	// 它防的是「两个写入方各自读到同一份旧配置、后写者覆盖前写者」的丢更新。
	cfgMu sync.Mutex
	// cfgPath 是配置文件路径，写配置时落盘用。由 SetConfigPath 注入
	//（与 mgr 同款：NewServer 有 50 个调用点，改签名的代价远大于收益）。
	// 未注入时 swapConf 直接报错，绝不猜一个路径写下去。
	cfgPath string
	st      *store.Store
	ledger  *ledger.Store
	// unlinked* caches the only web ledger join that may dial registered targets.
	// The cache keeps the cards endpoint bounded when a target is unavailable.
	unlinkedMu    sync.Mutex
	unlinkedAt    time.Time
	unlinkedCache map[string]any
	// roomAttachCache stores resolved remote task workdirs with per-entry TTL. Remote
	// attach lookup is non-critical for the rooms list, so refreshes run in the background;
	// failed refreshes remove the old projection instead of keeping stale executable data.
	roomAttachMu          sync.RWMutex
	roomAttachCache       map[string]roomAttachCacheEntry
	roomAttachRefreshing  bool
	roomAttachLastRefresh time.Time
	hub                   *Hub
	log                   *slog.Logger
	mgr                   *Manager // 任务状态机中枢（dispatch/continue/done 三条路由的落点），SetManager 注入
	// startedAt 是本 agentd 的启动时刻，status 用它换算 uptime。
	// 在 NewServer 里记录而非从 bootstrap 传入：NewServer 只在 bootstrap 调用
	// 一次，语义等价，且不必改动它的签名与全部测试调用点。
	startedAt time.Time
	// replayLimit / liveLimit 是 eventReplayLimit / liveBufferLimit 的实例副本，
	// 供测试注入小阈值复现「重放截断」「缓冲越限」两条边界路径（生产恒为默认值）。
	replayLimit int
	liveLimit   int
	// sessionRecheck 是 WS 连接上会话复验的周期（defaultSessionRecheck 的实例副本），
	// 供测试注入毫秒级值验证「吊销后被踢」（生产恒为默认值）。
	sessionRecheck time.Duration
	// onTruncationDiagnosed 是**测试专用**的诊断完成钩子：截断诊断跑完后带着
	// 判定结果调用一次。生产上恒为 nil。
	//
	// why 存在：诊断在事件写出之后才跑，用例拿不到「诊断完成」这个时刻，
	// 只能拿挂钟猜（曾经是 3 秒），机器越忙越容易假红（B162）。
	onTruncationDiagnosed func(verdict string)
	// upd 是换版接口的外部依赖，NewServer 填生产实现，测试整体替换
	upd UpdateDeps
	// latestFetch 查 GitHub latest release；更新提示与下载共用 selfupdate 缓存。
	latestFetch func(context.Context) (release.Release, error)
	// downloadMu 保护下载状态快照；下载 I/O 不持锁，否则 GET 进度会被阻塞。
	downloadMu       sync.Mutex
	downloadState    *proto.DownloadState
	downloadChecksum func(context.Context, string, string) (string, error)
	downloadFetch    func(context.Context, string, string) ([]byte, string, error)
	downloadOpen     func(string) error
	downloadPlatform func() (string, string)
	// pull 是自拉换版的并发锁与状态容器，NewServer 里 newPullTracker 构造
	pull *pullTracker
	// pullBaseCtx 是后台自拉的基准上下文。
	//
	// **绝不能用 r.Context()**：handler 一返回它就被取消，下载会在受理后的
	// 下一毫秒当场断掉。总时限由 Installer 的 HTTP 超时（10min）兜底。
	// NewServer 拿不到 agentd 的生命周期 ctx，留 nil 由 runPull 退到 context.Background()
	pullBaseCtx context.Context
	// restart 触发优雅关停，由 cmd/agentd.go 注入 Shutdown.Trigger。
	// nil 表示未注入（只会发生在测试或 bootstrap 顺序出错时）
	restart     func(reason string) bool
	pty         *ptyhost.Host
	ptyRootPath string
	// B156.3 自动化层：编制域与 keystone 域的服务引用，SetupAutomation 装配。
	// 生产接线只在本组装点文件内发生；实现票把 handler 接上这些服务。
	scheduling *scheduling.Service
	keystone   *keystone.Service
	autoLedger *ledgerapi.Facade
	ptyGate    *ptyapi.Host
	hostAPI    *hostapi.Host
	// B156.2 协作房间：入站门面实例与换绑端口，SetupAutomation 装配。
	rooms  *collab.Service
	rebind rebindPort
	// automationStartOnce/automationKick protect the single host automation loop.
	automationStartOnce sync.Once
	automationKick      chan struct{}
	automationMu        sync.Mutex
	automationCursor    int64
	automationSeen      map[int64]struct{}
	// automationRoundHook is a test-only observation point; production leaves it nil.
	automationRoundHook func(card string, result keystone.RoundResult)
	// desktopMu 保护薄壳状态：上报与控制台读取来自不同 HTTP 连接。
	desktopMu    sync.Mutex
	desktopState *proto.DesktopState
	desktopAt    time.Time
	// desktopNow 是 TTL 测试缝；生产为 nil，使用 time.Now。
	desktopNow func() time.Time
	// upgradeMu / machineUpgrades 保护后台执行机升级：同一台机器同时只允许一个，
	// 且记住最近一次的终态——**终态是控制台唯一的「失败出口」**（见
	// proto.MachineUpgrade 的说明），丢了它界面就会一直停在「升级中」。
	// 进程内存，agentd 重启即清空：这是诚实的，重启后本进程确实不知道。
	upgradeMu       sync.Mutex
	machineUpgrades map[string]*proto.MachineUpgrade
	// machineUpgradeInstaller 是远端升级共用的资产下载器；测试用 runner 缝整体替换。
	machineUpgradeInstaller *release.Installer
	machineUpgradeRunner    machineUpgradeRunner
	// pool 是对 target 的客户端复用池（探活/镜像/项目树/PTY/升级共用）。
	//
	// 为什么在 NewServer 里自建而不是靠注入：NewServer 有约 50 个调用点，
	// 靠注入必然漏，而漏掉的表现是运行时空指针。池的构造零成本（不发请求），
	// 自建没有代价。
	pool *targetclient.Pool
	// cardStepMu / cardStepFlight 守「同一张卡同时只允许一个环节在飞」。
	// 进程内状态：重启即清空，见 cardstep.go 的边界说明。
	cardStepMu     sync.Mutex
	cardStepFlight map[string]bool
	// runStepFn 是环节执行的落点，只为测试可替换而存在：生产恒为 s.runStep。
	// 环节要跑几十分钟且会真派 task，单测替换掉它才能验「在飞集合」这类装配逻辑。
	runStepFn func(ctx context.Context, runner *ledgerstep.StepRunner, cardID, step string)
}

// NewServer 创建 agentd 服务端。
//
// 参数：
//   - cfg: 配置，鉴权使用 cfg.Token
//   - st: 持久化存储
//   - log: 本服务日志入口
//
// 注意：
//   - hub 在内部创建，构造时捕获 slog.Default()；如需统一日志格式，调用方应先在
//     slog.SetDefault(logx.Setup(...)) 之后再调用 NewServer
func NewServer(cfg *config.Config, st *store.Store, log *slog.Logger) *Server {
	// 出网 transport 按配置里的代理造。坏值不阻断启动（config.Load 已经硬拒过
	// 一道，走到这儿只可能是绕过了它），降级为不用代理并打 Error——
	// agentd 不该因为一个附属设置而起不来
	var tr http.RoundTripper
	if cfg.Proxy != "" {
		t, err := proxycfg.Transport(cfg.Proxy)
		if err != nil {
			log.Error("代理配置无法使用，自拉换版将不走代理",
				"proxy", proxycfg.Redact(cfg.Proxy), "cause", err)
		} else {
			tr = t
			log.Info("自拉换版将使用代理", "proxy", proxycfg.Redact(cfg.Proxy))
		}
	}
	inst := release.NewInstaller(log, tr)
	releaseClient := release.NewClient(tr)
	exe, err := os.Executable()
	if err != nil {
		// 拿不到自身路径就起不了 ptyhost；PTY 是控制台的附属能力，不应拖垮 agentd。
		log.Error("无法确定自身可执行文件路径，PTY 会话将无法创建", "err", err)
	}
	s := &Server{
		st:             st,
		hub:            NewHub(),
		log:            log,
		startedAt:      time.Now(),
		replayLimit:    eventReplayLimit,
		liveLimit:      liveBufferLimit,
		pull:           newPullTracker(),
		sessionRecheck: defaultSessionRecheck,
		ptyRootPath:    filepath.Join(cfg.DataDir, "ptys"),
		latestFetch:    releaseClient.Latest,
		downloadFetch:  desktopDownloadFetcher(inst),
		downloadOpen:   openDownloadedFile,
		downloadPlatform: func() (string, string) {
			return release.CurrentPlatform()
		},
		downloadState:           &proto.DownloadState{Stage: "idle", Percent: -1},
		downloadChecksum:        desktopDownloadChecksum(inst),
		machineUpgrades:         make(map[string]*proto.MachineUpgrade),
		machineUpgradeInstaller: inst,
		cardStepFlight:          make(map[string]bool),
		roomAttachCache:         make(map[string]roomAttachCacheEntry),
		automationKick:          make(chan struct{}, 1),
		automationSeen:          make(map[int64]struct{}),
	}
	s.pty = ptyhost.New(s.ptyRootPath, exe, log)
	s.machineUpgradeRunner = s.executeMachineUpgrade
	s.runStepFn = s.runStep
	s.cfg.Store(cfg)
	s.pool = targetclient.NewPool(s.conf, log)
	s.upd = UpdateDeps{
		Getenv:     os.Getenv,
		Executable: resolvedExecutable,
		Install:    inst.InstallArchive,
		Activate:   release.Activate,
		Platform:   release.CurrentPlatform,
		FetchByTag: func(ctx context.Context, tag, goos, goarch, wantSum string) ([]byte, error) {
			return inst.FetchByTag(ctx, release.DefaultRepo, tag, goos, goarch, wantSum)
		},
	}
	// 事件落库即派生一条 event 引用帧，让帧流能表达控制面事件的时序
	s.registerEventFrameHook()
	return s
}

// Hub 返回服务内部的实时路由 hub，供上层（manager）做事件广播与 ticket 应答等待。
func (s *Server) Hub() *Hub {
	return s.hub
}

// resolvedExecutable 返回当前二进制的真实路径。
//
// 必须 EvalSymlinks：装在 ~/.local/bin 的二进制常常是个 symlink，
// 替换 symlink 本身只会把链接换成普通文件，链接目标仍是旧版。
// 与 cmd/agentd.go 的同名函数分属两包，互不冲突。
func resolvedExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

// SetRestart 注入优雅关停的触发函数（Shutdown.Trigger）。
//
// 必须在监听之前注入：换版接口返回 200 之后就靠它退出进程交接给新二进制，
// 没注入时换版会成功但永远不重启，而现场只剩一个「版本没变」的空结论。
func (s *Server) SetRestart(fn func(reason string) bool) { s.restart = fn }

// SetUpdateDeps 替换换版接口的外部依赖。**仅供测试**：这些依赖会真的
// 执行文件、rename 二进制、停进程。
func (s *Server) SetUpdateDeps(d UpdateDeps) { s.upd = d }

// SetManager 把任务管理器挂到 Server 上。
//
// 注意：
//   - manager 依赖本服务内部的 hub 与外部 adapter，必须在 NewServer 之后构造并注入
//   - 注入前三条路由返回 503（manager 未就绪），agentd bootstrap 顺序保证注入先于监听
//   - 挂接时会把 Server 的活配置取值函数交给 Manager。Manager 构造时收到的是一份
//     配置**快照**指针，而 swapConf 换的是新指针，读快照永远拿不到控制台改过的值
//     （B160 §4.2）。这一步是「保存后下一个任务即生效」成立的前提。
func (s *Server) SetManager(m *Manager) {
	s.mgr = m
	if m != nil {
		m.conf = s.conf
		s.log.Info("manager 已挂接，配置读取切到活快照", "default_executor", s.conf().Executor.Default)
	}
}

// conf 返回当前配置快照。
//
// 返回的指针在调用方持有期间恒定：写入方永不原地修改 Config，只整体换新，
// 因此读者看到的始终是一份自洽的配置，而不是改到一半的状态。
func (s *Server) conf() *config.Config { return s.cfg.Load() }

// DisciplineMapping 返回当前配置里的 executor 名 → 纪律块文件名映射。
//
// B229 后它只服务 /api/discipline 端点的回显与 mapping PUT 的整段替换
// （③层机器级映射语义不动，Out of Scope）；Manager 已收文即用、不再消费该映射。
// 返回的是当前快照持有的 map，调用方只读不改（写入方永不原地修改配置，只整体换新）。
func (s *Server) DisciplineMapping() map[string]string { return s.conf().Discipline }

// EnvMapping 返回当前配置里的 agent 名 → env 文件名映射。
//
// 供 envfile.Resolver 每次派发时取活值：控制台改完映射不必重启 agentd。
// 返回的是配置快照里的 map 本体，**调用方不得修改**（写入一律走 swapConf）。
func (s *Server) EnvMapping() map[string]string { return s.conf().Env }

// SetConfigPath 注入配置文件路径，供写配置时落盘。
//
// 参数：
//   - p: 配置文件绝对路径；空串表示不允许写配置（swapConf 会报错）
//
// 注意：与 SetManager 同款的构造后注入，必须在 Handler 开始服务前调用。
func (s *Server) SetConfigPath(p string) { s.cfgPath = p }

// Pool 返回 target 客户端复用池。
//
// 用途：cmd/agentd.go 起预热循环、给 Mirror 注入同一个池——**必须是同一个**，
// 两个池等于两套隧道，relay 侧会看到重复的节点连接。
func (s *Server) Pool() *targetclient.Pool { return s.pool }

// CloseTargets 关掉池内全部客户端与 relay 隧道。
//
// 注意：只在进程退出路径调用。池关了就不再复活（relay.Dialer.Close 是终态）。
func (s *Server) CloseTargets() error { return s.pool.Close() }

// swapConf 以写时复制的方式修改配置并落盘。
//
// 参数：
//   - mutate: 在一份可安全修改的副本上施加改动；返回非 nil 则整体中止，
//     既不换快照也不落盘
//
// 返回：
//   - mutate 的错误、或落盘错误；成功时 nil
//
// 注意：
//   - 落盘成功才换快照；落盘失败时内存未曾改变——绝无「内存有、磁盘没有」的窗口
//   - 深拷贝 Targets 与 Discipline 两层——它们在 agentd 运行期可被写接口修改。
//     **新增运行期可变字段时必须在此补一层深拷**：漏了不会有测试变红，但读者
//     会看到改到一半的配置，与 conf() 承诺的「快照自洽」直接冲突
func (s *Server) swapConf(mutate func(*config.Config) error) error {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	old := s.conf()
	next := *old
	next.Targets = make(map[string]config.Target, len(old.Targets)+1)
	for k, v := range old.Targets {
		next.Targets[k] = v
	}
	next.Discipline = make(map[string]string, len(old.Discipline)+1)
	for k, v := range old.Discipline {
		next.Discipline[k] = v
	}
	// Env 与 Discipline 同为运行期可写的映射（B158 起可从控制台改），必须深拷。
	next.Env = make(map[string]string, len(old.Env)+1)
	for k, v := range old.Env {
		next.Env[k] = v
	}
	if err := mutate(&next); err != nil {
		return err
	}
	if s.cfgPath == "" {
		s.log.Error("未注入配置文件路径，拒绝写配置")
		return errors.New("agentd 未注入配置文件路径，无法写配置")
	}
	if err := config.Save(s.cfgPath, &next); err != nil {
		s.log.Error("配置落盘失败，内存快照未变更", "path", s.cfgPath, "cause", err)
		return fmt.Errorf("保存配置 %s: %w", s.cfgPath, err)
	}
	s.cfg.Store(&next)
	s.log.Info("配置已更新并落盘", "path", s.cfgPath,
		"targets", len(next.Targets), "discipline", len(next.Discipline), "env", len(next.Env))
	return nil
}

// Handler 返回带 Host 白名单 + 鉴权两层中间件的完整路由，便于 httptest 直接挂载。
//
// 路由（Go 1.22+ 方法路由）：
//   - GET  /api/status                    agentd 可用性与身份
//   - GET  /api/footprint                 全任务进程足迹体检
//   - GET  /api/reclaim                    终态任务 managed worktree 残留体检
//   - GET  /api/tasks                   任务列表
//   - POST /api/tasks                   派发新任务（dispatch）
//   - GET  /api/tasks/{id}              任务详情（attach 数据源）
//   - POST /api/tasks/{id}/reply        回答工单
//   - POST /api/tasks/{id}/continue     续发修改指令
//   - POST /api/tasks/{id}/done         归档任务
//   - POST /api/tasks/{id}/reclaim       回收单个终态任务的 managed worktree
//   - GET  /api/tasks/{id}/diff         任务分支相对基准分支的审阅素材（diff + 提交列表）
//   - GET  /api/tasks/{id}/render       任务实况（render.log）流式读取（attach 数据源）
//   - GET  /api/tasks/{id}/frames      结构化回合帧（frames.jsonl）流式读取（W4b/TUI 数据源）
//   - GET  /api/tasks/{id}/file         读任务仓库内文件（审阅上下文）
//   - GET  /api/tasks/{id}/bundle       任务分支的 git bundle（回程 pull，不经 ssh）
//   - POST /api/tasks/{id}/run          在任务仓库执行审阅命令（跑测试/lint）
//   - POST /api/projects               登记项目（必要时先克隆）
//   - GET  /api/projects               列出项目位置（含现场实际状态）
//   - GET  /api/discipline             空清单（B229 目录退役）+ executor 档位
//   - GET  /api/discipline/file        拒服务（410，纪律块已入账本）
//   - PUT  /api/discipline/file        拒服务（410，纪律块已入账本）
//   - PUT  /api/discipline/mapping     整段替换机器级 discipline 映射段
//   - GET  /api/env                    env 文件列表与 executor 档位
//   - GET  /api/env/file/keys          env 文件的变量清单（不含值）
//   - GET  /api/env/file                读 env 文件正文（仅编辑时）
//   - PUT  /api/env/file                写 env 文件（写前解析校验）
//   - PUT  /api/env/mapping             整段替换 executor→env 文件映射
//   - GET  /api/launchers               工作台自定义启动项列表
//   - PUT  /api/launchers               整段替换启动项列表
//   - GET  /api/executor/default       查询机器级缺省执行者、它的默认模型与可选名单
//   - PUT  /api/executor/default       整体替换机器级缺省执行者与它的默认模型
//   - GET  /api/workspaces/dir          列举工作树内一层目录（白名单：仅已探测到的工作树）
//   - GET  /api/workspaces/file         读工作树内单个文件（同上白名单）
//   - PUT  /api/workspaces/file         写工作树内单个文件（同上白名单，带哈希前置条件）
//   - POST /api/workspaces/entry        工作树内新建条目（同上白名单，请求体含 name/kind）
//   - POST /api/workspaces/entry/copy   复制工作树内条目（副本计数命名，目录递归）
//   - PATCH /api/workspaces/entry       改名工作树内条目（请求体含 new_name）
//   - DELETE /api/workspaces/entry      删除工作树内条目（目录连同内容一并删）
//   - GET  /api/workspaces/search       工作树内按关键词搜索命中行（含 limit/超时/跳过生成物护栏）
//   - POST /api/workspaces/reveal       在本机访达中显示工作树内条目（不支持 ?machine= 转发）
//   - DELETE /api/projects/{name}      注销项目位置（只删登记，不动磁盘）
//   - PATCH /api/projects/{name}       改项目位置的引用名与/或路径（本机或 ?machine= 指定机器）
//   - GET  /ws/events                   事件流（补发 + 实时）
//   - GET  /ws/pty                      PTY 会话双向字节通道（binary=数据，text=控制）
//   - POST /api/auth/tickets            主令牌签发一次性 ticket，返回 /console 兑换 URL
//   - GET  /api/auth/sessions           列出会话（含已吊销）
//   - DELETE /api/auth/sessions/{id}    吊销指定会话
//   - POST /api/auth/logout             吊销当前 cookie 会话并清除 cookie
//   - GET  /console                     兑换 ticket → Set-Cookie → 302 到 /（无主令牌/cookie 凭据）
//   - GET/HEAD /（含深链接）             控制台 SPA 兜底：命中文件发文件，否则回落 index.html
//     （/api、/ws 的未命中经前缀分派保持原生 404/405，不被 SPA 吞掉，见下方注册处）
func (s *Server) Handler() http.Handler {
	// api 持有全部 /api 与 /ws 路由（无兜底）。未命中的 /api、/ws 请求经下面
	// 的 mux 前缀分派回到这里，由 ServeMux 保持原样的 404 / 405，而不是被
	// SPA 兜底回落成 HTML（为什么必须这样，见 SPA 注册处的注释）。
	api := http.NewServeMux()
	api.HandleFunc("GET /api/status", s.handleStatus)
	api.HandleFunc("GET /api/footprint", s.handleFootprint)
	api.HandleFunc("GET /api/reclaim", s.handleReclaimList)
	api.HandleFunc("GET /api/tasks", s.handleListTasks)
	api.HandleFunc("POST /api/tasks", s.handleDispatch)
	// /api/tasks/{id} 系列按任务归属包一层 byTask：本机没有就查镜像索引转发
	//（W3a §5.1 透明路由，见 taskroute.go）。render 是流式也走同一条搬运。
	// 合并 B102：main 侧按原样注册、w4 侧统一用 byTask 包一层并新增 frames，
	// 这里取 w4 侧——byTask 对本地任务与原样注册行为一致（taskroute.go 第 1 条
	// 判定「本机有就交给 handler」），w4 的跨机透明路由与 frames 都要保住。
	//
	// **reclaim 也包 byTask**（08-16 合并 w4-delivery → web-console 时改）。
	// 此前 w4 侧刻意让它原样注册，理由记作「reclaim 仅存在于本机」——那条理由
	// 站不住：reclaim 回收的是 **managed worktree**，而 worktree 就落在任务实际
	// 跑过的那台机器的盘上。原样注册时，在 A 机器上回收 B 机器的任务只会撞
	// s.mgr.Reclaim 的 store.ErrNotFound → 404，什么也回收不了；包上 byTask 才
	// 会把请求转发到真正持有那个 worktree 的机器。「资源只在本机」恰恰是
	// **要转发**的论据，不是不转发的论据。
	api.HandleFunc("GET /api/tasks/{id}", s.byTask(s.handleGetTask))
	api.HandleFunc("POST /api/tasks/{id}/reply", s.byTask(s.handleReply))
	api.HandleFunc("POST /api/tasks/{id}/continue", s.byTask(s.handleContinue))
	api.HandleFunc("POST /api/tasks/{id}/done", s.byTask(s.handleDone))
	api.HandleFunc("POST /api/tasks/{id}/stop", s.byTask(s.handleStop))
	api.HandleFunc("POST /api/tasks/{id}/reclaim", s.byTask(s.handleReclaim))
	api.HandleFunc("POST /api/tasks/{id}/resume", s.byTask(s.handleResume))
	api.HandleFunc("GET /api/tasks/{id}/plan", s.byTask(s.handleTaskPlan))
	api.HandleFunc("GET /api/tasks/{id}/diff", s.byTask(s.handleTaskDiff))
	api.HandleFunc("GET /api/tasks/{id}/branches", s.byTask(s.handleTaskBranches))
	api.HandleFunc("GET /api/tasks/{id}/render", s.byTask(s.handleTaskRender))
	api.HandleFunc("GET /api/tasks/{id}/frames", s.byTask(s.handleTaskFrames))
	api.HandleFunc("GET /api/tasks/{id}/file", s.byTask(s.handleTaskFile))
	api.HandleFunc("GET /api/tasks/{id}/bundle", s.byTask(s.handleTaskBundle))
	api.HandleFunc("POST /api/tasks/{id}/run", s.byTask(s.handleTaskRun))
	api.HandleFunc("POST /api/projects", s.handleProjectAdd)
	api.HandleFunc("GET /api/projects", s.handleProjectList)
	api.HandleFunc("GET /api/projects/tree", s.handleProjectTree)
	api.HandleFunc("GET /api/machines", s.handleMachines)
	api.HandleFunc("GET /api/workbench/state", s.handleWorkbenchStateGet)
	api.HandleFunc("PUT /api/workbench/state/base", s.handleWorkbenchBasePut)
	api.HandleFunc("PUT /api/workbench/state/selected", s.handleWorkbenchSelectedPut)
	api.HandleFunc("PUT /api/workbench/state/dock", s.handleWorkbenchDockPut)
	api.HandleFunc("PUT /api/desktop/state", s.handleDesktopStatePut)
	api.HandleFunc("GET /api/desktop/state", s.handleDesktopStateGet)
	api.HandleFunc("GET /api/discipline", s.handleDisciplineGet)
	api.HandleFunc("GET /api/discipline/file", s.handleDisciplineFileRead)
	api.HandleFunc("PUT /api/discipline/file", s.handleDisciplineFileWrite)
	api.HandleFunc("PUT /api/discipline/mapping", s.handleDisciplineMapping)
	api.HandleFunc("GET /api/env", s.handleEnvGet)
	api.HandleFunc("GET /api/env/file/keys", s.handleEnvKeys)
	api.HandleFunc("GET /api/env/file", s.handleEnvFileRead)
	api.HandleFunc("PUT /api/env/file", s.handleEnvFileWrite)
	api.HandleFunc("PUT /api/env/mapping", s.handleEnvMapping)
	api.HandleFunc("GET /api/launchers", s.handleLaunchersGet)
	api.HandleFunc("PUT /api/launchers", s.handleLaunchersPut)
	api.HandleFunc("GET /api/executor/default", s.handleExecutorDefaultGet)
	api.HandleFunc("PUT /api/executor/default", s.handleExecutorDefaultPut)
	api.HandleFunc("POST /api/machines", s.handleAddMachine)
	api.HandleFunc("DELETE /api/machines/{name}", s.handleDeleteMachine)
	api.HandleFunc("POST /api/machines/{name}/upgrade", s.handleMachineUpgrade)
	api.HandleFunc("GET /api/workspaces/dir", s.handleWorkspaceDir)
	api.HandleFunc("GET /api/workspaces/file", s.handleWorkspaceFile)
	api.HandleFunc("PUT /api/workspaces/file", s.handleWorkspaceFileWrite)
	api.HandleFunc("POST /api/workspaces/entry", s.handleWorkspaceEntryCreate)
	api.HandleFunc("POST /api/workspaces/entry/copy", s.handleWorkspaceEntryCopy)
	api.HandleFunc("PATCH /api/workspaces/entry", s.handleWorkspaceEntryRename)
	api.HandleFunc("DELETE /api/workspaces/entry", s.handleWorkspaceEntryDelete)
	api.HandleFunc("GET /api/workspaces/search", s.handleWorkspaceSearch)
	// 注意：reveal 故意不接 forwardIfRequested——转发正是这个端点要拒绝的那件事
	api.HandleFunc("POST /api/workspaces/reveal", s.handleWorkspaceReveal)
	api.HandleFunc("DELETE /api/projects/{name}", s.handleProjectRemove)
	api.HandleFunc("PATCH /api/projects/{name}", s.handleProjectPatch)
	api.HandleFunc("GET /api/projects/{name}/branches", s.handleProjectBranches)
	api.HandleFunc("GET /api/projects/{name}/codegraph", s.handleProjectCodegraph)
	api.HandleFunc("GET /api/projects/{name}/codegraph/source", s.handleProjectCodegraphSource)
	api.HandleFunc("POST /api/projects/{name}/worktrees", s.handleProjectWorktreeCreate)
	api.HandleFunc("GET /api/pty/sessions", s.handleListPtySessions)
	api.HandleFunc("POST /api/pty/sessions", s.handleCreatePtySession)
	api.HandleFunc("DELETE /api/pty/sessions/{id}", s.handleDeletePtySession)
	api.HandleFunc("POST /api/update", s.handleUpdate)
	api.HandleFunc("GET /api/update/latest", s.handleUpdateLatest)
	api.HandleFunc("POST /api/update/desktop/download", s.handleDesktopDownloadStart)
	api.HandleFunc("GET /api/update/desktop/download", s.handleDesktopDownloadState)
	api.HandleFunc("GET /ws/events", s.handleEvents)
	api.HandleFunc("GET /ws/pty", s.handlePtyWS)
	api.HandleFunc("POST /api/auth/tickets", s.handleIssueTicket)
	api.HandleFunc("GET /api/auth/sessions", s.handleListSessions)
	api.HandleFunc("DELETE /api/auth/sessions/{id}", s.handleRevokeSession)
	api.HandleFunc("POST /api/auth/logout", s.handleLogout)
	s.registerLedgerRoutes(api)
	s.registerSchedulingRoutes(api)
	s.registerCoordRoutes(api)

	// 控制台静态资源兜底：一切未被更精确模式匹配的路径都到这里。
	//
	// 挂内层 mux 而不是 root：控制台页面本身要求 cookie，走 s.auth；
	// /console 是唯一免鉴权入口（ticket 本身就是它的凭据），它注册在 root 上。
	//
	// 为什么 /api、/ws 必须在这里显式分派给 api，而不是让 SPA 兜底吞掉未命中：
	// ServeMux 的 "/" 通配只会输给**已注册**的更长前缀——`GET /api/status` 这类
	// 是精确叶子，拦不住 `/api/no-such-endpoint`，后者会一路落到 "/" 被回落成
	// HTML，前端把 HTML 喂给 JSON.parse，报错与真实原因完全无关。把 /api/、/ws/
	// 前缀转给无兜底的 api 子 mux，未命中就保持 ServeMux 的原生裁决：路径不认
	// 识 → 404，路径认识但方法不对 → 405。这条边界是承重的，见 webhandler.go
	// 的文件头。
	mux := http.NewServeMux()
	mux.Handle("/api/", api)
	mux.Handle("/ws/", api)
	mux.Handle(
		"/codegraph/app/",
		http.StripPrefix("/codegraph/app", newSPAHandler(charterwebui.FS(), s.log)),
	)
	mux.Handle("/", newSPAHandler(webui.FS(), s.log))

	// charter viewer 与 handoff 自有 console 是两棵 FS；专属前缀必须先注册，
	// 且仍在 auth 内，避免深路径错误回落到另一棵 index 或绕过会话鉴权。
	s.log.Info("代码图 viewer 静态资源已挂载",
		"path", "/codegraph/app/", "source", "charter/graph/webui")

	// /console 是唯一不经主令牌/cookie 的路由——ticket 本身就是它的凭据，
	// 因此它挂在 auth 之外、hostGuard 之内。Go 1.22 的 mux 按精确度选择，
	// "GET /console" 胜过 "/"
	root := http.NewServeMux()
	root.Handle("/", s.auth(mux))
	root.HandleFunc("GET /console", s.handleConsole)

	// 这份二进制有没有前端，是「控制台打不开」时第一个要排除的可能。
	// 不打这一行的话，运维只能靠猜：是构建时漏了 -tags embedweb，
	// 还是运行时路由坏了，两者现象完全一样。
	s.log.Info("控制台前端", "embedded", webui.Embedded())
	s.log.Info("Host 白名单已生效", "hosts", sortedKeys(s.allowedHosts()))
	return s.hostGuard(root)
}

// auth 是 Bearer token 或 cookie 会话鉴权中间件，包住全部路由。
//
// 鉴权失败（无 token / token 不匹配 / cookie 会话无效）统一返回 401，并打 Warn
// 记录来源地址——这是排查「谁在扫本地端口」与「配对端 token 未同步」的第一线索。
//
// 为什么这里做空 token 拒绝（L-2）：subtle.ConstantTimeCompare("","")==1，
// 配置 token 为空时空 token 请求会通过鉴权——今天只因 net/http 的
// textproto 行解析会掐掉 "Bearer " 后的空格才 401，属于「碰巧被别的层拦住」
// 的隐性 fail-open。config.Load 正常都会生成 token，但手写配置可能漏掉；
// 在鉴权边界 fail-closed：cfg.Token 为空 → 拒绝一切请求并打 Error，提示
// 配置问题。选在这里而非 NewServer/启动时：这是 fail-open 真正发生的边界，
// 一个位置同时覆盖 HTTP 与 WS 全路由，任何嵌入方（含测试）都逃不掉。
//
// 为什么在 Bearer 之外加 cookie 分支：浏览器里 `new WebSocket()` 只能继承
// 页面已有的 cookie，设不了 Authorization 请求头——CLI 的主令牌路径在浏览器
// 里走不通，必须允许 cookie 会话鉴权。
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.conf().Token == "" {
			s.log.Error("token 未配置，拒绝一切请求（fail-closed）：请在配置中设置 token 后重启 agentd",
				"remote_addr", r.RemoteAddr, "method", r.Method, "path", r.URL.Path)
			writeUnauthorized(w, r)
			return
		}
		// 先 Bearer：CLI 是最高频的调用方，且这条路径不碰库
		if token, ok := bearerToken(r); ok &&
			subtle.ConstantTimeCompare([]byte(token), []byte(s.conf().Token)) == 1 {
			next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), identity{})))
			return
		}
		// 后 cookie：浏览器 new WebSocket() 设不了请求头，只能走这条
		sess, reason := s.sessionFromRequest(r)
		if sess == nil {
			s.log.Warn("鉴权失败", "remote_addr", r.RemoteAddr, "method", r.Method,
				"path", r.URL.Path, "reason", reason)
			writeUnauthorized(w, r)
			return
		}
		s.refreshSession(sess)
		next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), identity{session: sess.ID})))
	})
}

// bearerToken 从 Authorization 头解析 "Bearer <token>" 形式的令牌。
//
// 返回：
//   - token: 解析出的令牌
//   - ok: 头部存在且前缀为 "Bearer "（token 本身可为空，由调用方与配置比较）
func bearerToken(r *http.Request) (string, bool) {
	return strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// unauthorizedPage 是浏览器直接访问 agentd 而没有会话 cookie 时看到的页面。
//
// 为什么不直接返回裸 JSON 401：浏览器会把它当成一段纯文本显示，用户看到的是
// 一个孤零零的 {"error":"未授权"}，无从判断是自己没登录、还是服务坏了。
// 说明页把「怎么拿入口」直接写出来，是这里唯一不会把人引向错误排查方向的做法。
const unauthorizedPage = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<title>handoff：需要登录</title></head>
<body style="font-family:system-ui;max-width:40rem;margin:4rem auto;line-height:1.7">
<h1>需要登录</h1>
<p>agentd 工作正常，但这个浏览器还没有有效会话。控制台入口需要一张一次性 ticket。</p>
<h2>怎么拿入口</h2>
<ul>
<li>命令行：<code>handoff console</code>，它会签一张 ticket 并给出可直接打开的链接。</li>
<li>桌面端：直接打开 handoff 桌面应用，它会自动完成这一步。</li>
</ul>
</body></html>
`

// wantsHTML 报告请求方是否更希望拿到 HTML。
//
// 判据刻意从严：只有 Accept 里**显式**出现 text/html 才算。浏览器地址栏发起的
// 导航一定带它；而 fetch/XHR、CLI、`*/*` 都不带，会走原有 JSON 分支——
// 那些调用方的错误处理都按 JSON 写的，给它们 HTML 会让整条错误链失效。
func wantsHTML(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html")
}

// writeUnauthorized 按调用方偏好输出 401。
//
// 注意：无论走哪个分支，**状态码恒为 401**。不要因为返回了 HTML 就改成 200，
// 那会让监控与前端的鉴权拦截器同时失效。
func writeUnauthorized(w http.ResponseWriter, r *http.Request) {
	if wantsHTML(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, unauthorizedPage)
		return
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未授权"})
}

// handleStatus 返回本 agentd 的可用性与身份信息（handoff status 的数据源）。
//
// 注意：
//   - manager 未就绪时返回 503：任务计数与探活都要经 manager，此时没有能给的
//     真实答案，宁可明确报「未就绪」也不返回一个半真的响应
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.log.Info("状态查询请求", "method", r.Method, "path", r.URL.Path)
	if s.mgr == nil {
		s.log.Error("manager 未就绪，无法回答状态查询")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	resp, err := s.mgr.Status()
	if err != nil {
		s.log.Error("聚合状态失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	resp.StartedAt = s.startedAt
	// pull 能力位与自拉状态由 Server 装配而不是 Manager：它们的持有者是
	// Server（换版 handler 在这里），Manager 不该为了填两个字段而反向依赖它
	if resp.Update != nil {
		yes := true
		resp.Update.Pull = &yes
		resp.Update.PullState = s.pull.snapshot()
	}
	ptyOK := s.pty.Supported()
	resp.PtySupported = &ptyOK
	launchersOK := true
	resp.LaunchersSupported = &launchersOK
	// B229 能力位与实现同生同死：契约 §2.4 四件事核对单（T1 提交 5585ecc2 逐条核销）——
	//   1. 收文即用：Dispatch 将 DisciplineText 逐字节注入，本地纪律解析已退役（manager.go:757）
	//   2. 正文落盘：先写任务目录 discipline.md 再启动 executor，落盘失败拒派（manager.go:923）
	//   3. continue 消费首派落盘正文：Cold 缺失拒绝续接、热重连 Error 不阻断（manager.go:1300）
	//   4. resume 启动恢复消费同一份落盘正文，不另起第二处解析入口（manager.go:3410）
	// 四件齐才许上报 true；先报 true 后补实现 = 协调者信了能力位、正文发到一台不会用的
	// 机器上（缺陷三的镜像事故）。
	disciplinesOK := true
	resp.DisciplinesSupported = &disciplinesOK
	revealOK := revealSupportedOS
	resp.RevealSupported = &revealOK
	resp.ScratchRoot = s.scratchRoot()
	// 会话数是读一个内存 map 的长度，不枚举进程——status 必须保持快
	if s.pty != nil {
		n := len(s.pty.List())
		resp.PtySessions = &n
	}
	s.log.Info("状态查询完成", "active", len(resp.Active), "executors", len(resp.Executors))
	writeJSON(w, http.StatusOK, resp)
}

// handleFootprint 返回全部任务（含已归档）的进程足迹体检。
//
// 注意：这是慢接口——它遍历全部历史任务目录逐个枚举进程。与 /api/status
// 分开正是为了不把那条「必须快」的诊断路径拖下水。
func (s *Server) handleFootprint(w http.ResponseWriter, r *http.Request) {
	s.log.Info("足迹体检请求", "method", r.Method, "path", r.URL.Path)
	if s.mgr == nil {
		s.log.Error("manager 未就绪，无法体检足迹")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	resp, err := s.mgr.FootprintAll()
	if err != nil {
		s.log.Error("足迹体检失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	s.log.Info("足迹体检请求完成", "rows", len(resp.Rows))
	resp.Pty = s.ptyFootprint()
	writeJSON(w, http.StatusOK, resp)
}

// handleReclaimList 返回全部终态任务的 managed worktree 残留体检结果。
func (s *Server) handleReclaimList(w http.ResponseWriter, r *http.Request) {
	s.log.Info("残留体检请求", "method", r.Method, "path", r.URL.Path)
	if s.mgr == nil {
		s.log.Error("manager 未就绪，无法体检残留")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	resp, err := s.mgr.ReclaimList()
	if err != nil {
		s.log.Error("残留体检失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	s.log.Info("残留体检请求完成", "rows", len(resp.Rows), "scanned", resp.Scanned)
	writeJSON(w, http.StatusOK, resp)
}

// handleReclaim 回收单个终态任务的 managed worktree。
//
// 状态码：404 任务不存在；409 四种拒绝（reason 区分）；200 成功。
//
// 注意：四种 409 共用状态码，响应体必须带机器码 reason——CLI 靠它分派渲染，
// 解析中文文案是不行的（文案会改，机器码不改）
func (s *Server) handleReclaim(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	s.log.Info("回收请求", "method", r.Method, "path", r.URL.Path, "task", taskID)
	if s.mgr == nil {
		s.log.Warn("回收请求到达但 manager 未注入", "remote_addr", r.RemoteAddr)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	var body struct {
		Force bool `json:"force"`
	}
	// 解码失败按 force=false 处理：强删是破坏性动作，看不懂的输入必须走保守的那边
	_ = json.NewDecoder(r.Body).Decode(&body)

	resp, err := s.mgr.Reclaim(r.Context(), taskID, body.Force)
	if err != nil {
		s.writeReclaimError(w, taskID, err)
		return
	}
	s.log.Info("回收请求完成", "task", taskID, "action", resp.Action, "removed", resp.Removed)
	writeJSON(w, http.StatusOK, resp)
}

// writeReclaimError 把 Reclaim 的错误翻成 HTTP 应答。
//
// 注意：4xx 一律 Warn 不 Error（B11 已定的纪律）——被拒不是 agentd 出故障
func (s *Server) writeReclaimError(w http.ResponseWriter, taskID string, err error) {
	var de *DirtyWorktreeError
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.log.Warn("回收被拒：任务不存在", "task", taskID)
		writeJSON(w, http.StatusNotFound, proto.ReclaimError{Error: err.Error()})
	case errors.As(err, &de):
		s.log.Warn("回收被拒：工作树脏", "task", taskID, "dirty", len(de.Files))
		writeJSON(w, http.StatusConflict, proto.ReclaimError{
			Error: err.Error(), Reason: proto.ReasonDirty, Dirty: de.Files})
	case errors.Is(err, ErrReclaimNotTerminal):
		s.log.Warn("回收被拒：任务非终态", "task", taskID)
		writeJSON(w, http.StatusConflict, proto.ReclaimError{
			Error: err.Error(), Reason: proto.ReasonNotTerminal})
	case errors.Is(err, ErrReclaimNotManaged):
		s.log.Warn("回收被拒：非 managed 工作区", "task", taskID)
		writeJSON(w, http.StatusConflict, proto.ReclaimError{
			Error: err.Error(), Reason: proto.ReasonNotManaged})
	case errors.Is(err, ErrReclaimRepoUnreachable):
		s.log.Warn("回收被拒：仓库不可达", "task", taskID, "cause", err)
		writeJSON(w, http.StatusConflict, proto.ReclaimError{
			Error: err.Error(), Reason: proto.ReasonRepoUnreachable})
	default:
		s.log.Error("回收失败", "task", taskID, "cause", err)
		writeJSON(w, http.StatusInternalServerError, proto.ReclaimError{Error: err.Error()})
	}
}

// handleListTasks 返回全部任务（created_at 降序）及其实时订阅数，供 tasks 命令展示。
//
// 注意：watchers 取自 hub 的瞬时状态、不落库；它只回答「此刻有几个连接在听」，
// 不回答「该不该有人听」——那条判据在 status 侧（unattended）。
func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	s.log.Info("任务列表请求", "method", r.Method, "path", r.URL.Path)
	if r.URL.Query().Get("scope") == "all" && !isForwarded(r) {
		// 跨机汇总信封（镜像快照，不现场扇出）；带转发头时降级为本机
		writeJSON(w, http.StatusOK, s.tasksAll(r.Context()))
		return
	}
	tasks, err := s.st.ListTasks()
	if err != nil {
		s.log.Error("查询任务列表失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	if tasks == nil {
		// 空列表序列化为 [] 而非 null，保证客户端解码出的始终是数组
		tasks = []proto.Task{}
	}
	// 拼装 API 视图：附上「有几个人在听」这条只有 hub 知道的运行态
	idx := s.projectIndex()
	views := make([]proto.TaskView, 0, len(tasks))
	unattended := 0
	owned := 0
	for _, t := range tasks {
		t.ProjectID = idx.projectIDOf(t.RepoPath) // 读时 join，不落库
		if t.ProjectID != "" {
			owned++
		}
		w := s.hub.Watchers(t.ID)
		if w == 0 && !isTerminalState(t.State) && t.State != proto.TaskStateWaitingReview {
			unattended++
		}
		views = append(views, proto.TaskView{Task: t, Watchers: w})
	}
	// ?project= 过滤：在盖注解之后、写响应之前做；过滤后可能为空，
	// 空数组是正确答案，不是 404
	pid := r.URL.Query().Get("project")
	if pid != "" {
		filtered := views[:0]
		for _, v := range views {
			if v.ProjectID == pid {
				filtered = append(filtered, v)
			}
		}
		views = filtered
		if views == nil {
			views = []proto.TaskView{}
		}
	}
	s.log.Info("任务列表完成", "tasks", len(views), "unattended", unattended,
		"owned", owned, "project", pid, "filtered", len(views))
	writeJSON(w, http.StatusOK, views)
}

// handleGetTask 返回任务详情（任务 + 待办工单 + 最近事件），是 attach 命令的数据源。
func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	s.log.Info("任务详情请求", "method", r.Method, "path", r.URL.Path, "task", taskID)

	task, err := s.st.GetTask(taskID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("任务不存在", "task", taskID)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "任务不存在"})
			return
		}
		s.log.Error("读取任务失败", "task", taskID, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	pending, err := s.st.PendingTickets(taskID)
	if err != nil {
		s.log.Error("读取待办工单失败", "task", taskID, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	events, err := s.st.EventsFrom(taskID, 0, recentEventsLimit)
	if err != nil {
		s.log.Error("读取最近事件失败", "task", taskID, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	// 空值归一化为 [] 而非 null（L-7）：PendingTickets/RecentEvents 是数组契约，
	// 客户端（attach）按数组解码迭代；store 返回 nil 时 Go 会把 nil slice 序列化
	// 成 null，列表接口（如 /api/tasks）已归一化而此处遗漏。marshal 前显式
	// 非 nil，保证字段始终存在且为数组
	if pending == nil {
		pending = []proto.Ticket{}
	}
	if events == nil {
		events = []proto.Event{}
	}
	watchers := s.hub.Watchers(taskID)
	task.ProjectID = s.projectIndex().projectIDOf(task.RepoPath) // 读时 join，不落库
	s.log.Info("任务详情完成", "task", taskID, "state", task.State,
		"pending", len(pending), "watchers", watchers)
	writeJSON(w, http.StatusOK, taskDetail{
		Task:           proto.TaskView{Task: *task, Watchers: watchers},
		PendingTickets: pending,
		RecentEvents:   events,
	})
}

// taskDetail 是 GET /api/tasks/{id} 的响应体（attach 数据源）。
type taskDetail struct {
	// Task 用 TaskView 而非 Task：多带一个 watchers，且因字段提升线格式不变
	Task           proto.TaskView `json:"task"`
	PendingTickets []proto.Ticket `json:"pending_tickets"`
	RecentEvents   []proto.Event  `json:"recent_events"`
}

// replyRequest 是 POST /api/tasks/{id}/reply 的请求体。
type replyRequest struct {
	TicketID string `json:"ticket_id"`
	Answer   string `json:"answer"`
}

// replyResult 是 reply 接口的响应体。
//
// Relayed=false 表示「回答已落库但 executor 侧递送失败」，此时 HTTP 状态码为
// 502（语义与任务保持 waiting_answer 的原因见 handleReply 函数头）；OK 恒为
// true——回答本身被接受且已持久化，失败只发生在 executor 侧递送环节。
type replyResult struct {
	OK      bool   `json:"ok"`
	Relayed bool   `json:"relayed"`
	Reason  string `json:"reason,omitempty"`
}

// handleReply 回答一个工单，完成唤醒闭环的回程。
//
// 流程：
//  1. 校验 ticket 存在且属于路径中的任务（跨任务一律按不存在处理，不泄露信息）
//  2. store.AnswerTicket 持久化应答（answer IS NULL 条件保证不可重复回答）
//  3. hub.NotifyAnswer 唤醒阻塞在 WaitAnswer 上的 executor 侧
//  4. 若任务处于 waiting_answer 且无其余未答工单，状态回迁 running（resumeIfIdle）
//
// 响应：正常（NotifyAnswer 命中等待者或 RelayAnswer 成功）返回 200
// `{"ok":true,"relayed":true}`；回答已落库但 executor 侧递送失败（无等待者且
// 中继失败）返回 502 `{"ok":true,"relayed":false,"reason":...}`。
//
// 为什么中继失败返回 502 而非回滚工单：
//   - 502 的语义是「回答已被接受，但上游（executor）递送失败」，与 502 Bad
//     Gateway 一致——agentd 是协调者与 executor 之间的网关。不用 409：
//     409 表达「当前状态不允许该操作」（状态机语义），而这里回答本身已被接受
//   - 不回滚工单：应答已落库是「协调者裁决过」的持久审计事实，回滚会让已答
//     工单重新出现在 pending 而裁决记录消失；且中继失败的典型场景（executor
//     不在运行）下回滚只是把问题推迟到下一次 reply，协调者拿到 502 + reason
//     即可凭看门狗 stalled / 下次 agentd 重启的恢复路径处置
//
// 为什么中继失败时任务保持 waiting_answer 不回迁 running：executor 并未收到
// 应答、没有恢复执行，标 running 是虚假状态；保持 waiting_answer 让下次
// agentd 重启时 RecoverOnStartup 的探活恢复路径（waiting_answer 在探测范围）
// 仍然生效。
func (s *Server) handleReply(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	s.log.Info("reply 请求", "method", r.Method, "path", r.URL.Path, "task", taskID)

	var req replyRequest
	// LimitReader 限制请求体大小，防止恶意大 body 占满内存
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.log.Warn("reply 请求体解析失败", "task", taskID, "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体必须是 JSON {ticket_id, answer}"})
		return
	}
	if req.TicketID == "" || req.Answer == "" {
		s.log.Warn("reply 缺少 ticket_id 或 answer", "task", taskID)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ticket_id 与 answer 不能为空"})
		return
	}

	tk, err := s.st.GetTicket(req.TicketID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("reply 目标工单不存在", "task", taskID, "ticket", req.TicketID)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "工单不存在"})
			return
		}
		s.log.Error("读取工单失败", "task", taskID, "ticket", req.TicketID, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	if tk.TaskID != taskID {
		s.log.Warn("reply 工单不属于该任务", "task", taskID, "ticket", req.TicketID, "ticket_task", tk.TaskID)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "工单不存在"})
		return
	}

	if err := s.st.AnswerTicket(req.TicketID, req.Answer); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// 并发场景：另一请求已抢先回答（answer IS NULL 条件失效），按不存在处理
			s.log.Warn("reply 工单已被回答", "task", taskID, "ticket", req.TicketID)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "工单不存在"})
			return
		}
		s.log.Error("回答工单失败", "task", taskID, "ticket", req.TicketID, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	if _, err := s.st.AppendEvent(taskID, proto.EventTypeTicketAnswered,
		ticketAnsweredPayload{TicketID: req.TicketID, Answer: req.Answer}); err != nil {
		s.log.Warn("追加工单答复事件失败", "task", taskID, "ticket", req.TicketID, "cause", err)
	}

	// 唤醒阻塞在该 ticket 上的 WaitAnswer 调用者（executor 侧继续执行）；
	// 无人等待（典型为 agentd 重启后等待 goroutine 已随进程消亡）时走
	// RelayAnswer 自愈中继，把应答直接回传 executor——否则回答已落库但
	// executor 永远阻塞（工单已答、二次 reply 404、done 409，不可恢复）。
	// relayed=false 即「回答已落库但 executor 侧递送失败」，交给协调者的是
	// 502 + reason 而非只有一行 agentd.log（P0-5）
	relayed := true
	reason := ""
	if !s.hub.NotifyAnswer(req.TicketID, req.Answer) {
		if s.mgr == nil {
			relayed = false
			reason = "manager 未注入，应答未回传 executor"
			s.log.Error("reply 无等待者且 manager 未注入，应答未回传 executor",
				"task", taskID, "ticket", req.TicketID)
		} else if err := s.mgr.RelayAnswer(taskID, req.TicketID, req.Answer); err != nil {
			relayed = false
			// 响应里的 reason 截断展示，完整错误留在日志
			reason = truncateRunes(err.Error(), 200)
			s.log.Error("reply 自愈中继失败", "task", taskID, "ticket", req.TicketID, "cause", err)
		}
	}
	s.log.Info("reply 完成", "task", taskID, "ticket", req.TicketID,
		"answer", truncateRunes(req.Answer, 80), "relayed", relayed)

	if !relayed {
		// 中继失败：回答已落库但 executor 未收到（why 与状态处理见函数头）。
		// 非 2xx 让 CLI 非零退出并展示 reason，协调者立即知道 executor 没拿到，
		// 而不是只能去远端 agentd.log 里翻一行日志。
		// 同时落一条 delivery_failed 事件：502 只回给「当前这次 reply」的调用方，
		// 而事件是持久的——换个会话接管、或此刻根本没人盯着终端时，仍能从
		// attach/wait 看到「有裁决卡在半路，该执行 handoff resume」
		if s.mgr != nil {
			s.mgr.NoteDeliveryFailed(taskID, req.TicketID, errors.New(reason))
		}
		writeJSON(w, http.StatusBadGateway, replyResult{OK: true, Relayed: false, Reason: reason})
		return
	}

	// 回答已落库与唤醒完成，此时任务若无其余未答工单即可回迁 running；
	// 回迁失败（如任务已被并发迁移）不影响 reply 本身的成功
	s.resumeIfIdle(taskID)

	writeJSON(w, http.StatusOK, replyResult{OK: true, Relayed: true})
}

// resumeIfIdle 当任务处于 waiting_answer 且已无未答工单时，把状态回迁 running。
//
// 为什么用有界重试：两个工单被并发回答时，先回答的请求可能读到「仍有未答工单」而跳过，
// 后回答的请求负责回迁；也可能两个请求都读到「已无工单」，此时先执行者成功，
// 后执行者因 CAS（WHERE state = 旧值）收到 ErrBadTransit。重试让意图在最新快照上重新
// 评估，而不是把并发迁移当作错误直接吞掉。
func (s *Server) resumeIfIdle(taskID string) {
	for attempt := 0; attempt < 3; attempt++ {
		task, err := s.st.GetTask(taskID)
		if err != nil {
			s.log.Error("reply 后读取任务失败", "task", taskID, "cause", err)
			return
		}
		if task.State != proto.TaskStateWaitingAnswer {
			return // 任务已不在等待应答，无需回迁
		}
		pending, err := s.st.PendingTickets(taskID)
		if err != nil {
			s.log.Error("reply 后查询待办工单失败", "task", taskID, "cause", err)
			return
		}
		if len(pending) > 0 {
			return // 仍有未答工单，任务保持 waiting_answer
		}
		if err := s.st.UpdateTaskState(taskID, proto.TaskStateRunning); err == nil {
			return
		} else if !errors.Is(err, store.ErrBadTransit) {
			s.log.Error("reply 后恢复任务运行失败", "task", taskID, "cause", err)
			return
		}
		// ErrBadTransit：状态被并发变更（如另一 reply 已回迁），重读最新快照重试
	}
	s.log.Warn("reply 后恢复任务运行重试耗尽", "task", taskID)
}

// dispatchRequest 是 POST /api/tasks 的请求体（plan 内容 base64 编码上传，
// prompt-only 派发时 prompt 非空、plan_b64 为空）。
type dispatchRequest struct {
	// project_id 与 project_name 二选一。**请求体里没有任何路径字段**：
	// 「代码在这台机器的哪个目录」是执行机自己的私事，调用方不该描述它（B62）。
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	PlanB64     string `json:"plan_b64"`
	PlanName    string `json:"plan_name"`
	Target      string `json:"target"`
	Prompt      string `json:"prompt"`
	Name        string `json:"name"`
	// HomeDir 是小队派发载体 HOME 的可空字段；缺席与显式空串必须可区分。
	HomeDir  *string `json:"home_dir,omitempty"`
	Executor string  `json:"executor"`
	// Discipline 是派发点名的纪律块角色名；空=未点名（只注入平台层）。
	// B229：正文由协调者侧组装后经 DisciplineText 下发，本机收文即用不再解析；
	// DisciplineVersion 是命中的账本版本，随任务落盘供回放。
	Discipline        string `json:"discipline"`
	DisciplineText    string `json:"discipline_text,omitempty"`
	DisciplineVersion int    `json:"discipline_version,omitempty"`
	Model             string `json:"model"`
	Branch            string `json:"branch"`
	// NewBranch/NewWorktree 用 snake_case 新键，与 CLI flag 语义一一对应。
	NewBranch string `json:"new_branch"`
	Base      string `json:"base"`
	// ResolveDefaultBase 仅由 card dispatch 传入；普通 CLI 派发保持 Base
	// 为空时退回任务仓库 HEAD 的既有语义。
	ResolveDefaultBase bool `json:"resolve_default_base"`
	// LocalBaseBranch 表示 Base 是目标机本地工作分支；与 ResolveDefaultBase 互斥。
	LocalBaseBranch bool   `json:"local_base_branch"`
	Worktree        string `json:"worktree"`
	NewWorktree     bool   `json:"new_worktree"`
	// BaseCommit 是协调者本地 HEAD 的提交号，用于校验任务仓库不落后于本地（空=不校验）。
	BaseCommit string `json:"base_commit"`
}

// handleDispatch 派发一个新任务，返回创建后的任务（state=running）。
//
// 流程：解析请求体 → manager.Dispatch（建任务/写 plan/启动 executor/进 running）。
func (s *Server) handleDispatch(w http.ResponseWriter, r *http.Request) {
	s.log.Info("dispatch 请求", "method", r.Method, "path", r.URL.Path)
	if s.mgr == nil {
		s.log.Warn("dispatch 请求到达但 manager 未注入", "remote_addr", r.RemoteAddr)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	var req dispatchRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		s.log.Warn("dispatch 请求体解析失败", "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体必须是 JSON {project_id, plan_b64, ...}"})
		return
	}
	task, err := s.mgr.Dispatch(r.Context(), DispatchReq{
		ProjectID: req.ProjectID, ProjectName: req.ProjectName,
		PlanB64: req.PlanB64, PlanName: req.PlanName, Target: req.Target,
		Prompt: req.Prompt, Name: req.Name, Executor: req.Executor, Discipline: req.Discipline, Model: req.Model,
		HomeDir:           req.HomeDir,
		DisciplineText:    req.DisciplineText,
		DisciplineVersion: req.DisciplineVersion,
		Branch:            req.Branch, NewBranch: req.NewBranch, Base: req.Base,
		ResolveDefaultBase: req.ResolveDefaultBase,
		LocalBaseBranch:    req.LocalBaseBranch,
		Worktree:           req.Worktree, NewWorktree: req.NewWorktree, BaseCommit: req.BaseCommit,
	})
	if err != nil {
		s.writeDispatchError(w, req.ProjectID, err)
		return
	}
	s.log.Info("dispatch 完成", "task", task.ID, "state", task.State)
	writeJSON(w, http.StatusOK, task)
}

// writeDispatchError 把 dispatch 失败映射为 HTTP 状态码与可读原因（P1-14）。
//
// 映射规则：
//   - ErrDirtyWorktree → 409：工作区状态与服务端要求冲突，这是最常见的拒绝原因，
//     协调者一条 git 命令即可修复——必须带可读 reason（err.Error() 含脏文件第一行），
//     而非扁平化的「派发任务失败」
//   - ErrWorkdirBusy → 409：目标工作目录已被一个非终态任务占用（含 waiting_review），
//     与 ErrDirtyWorktree 同为状态冲突而非请求错误——报文点名占用任务并给出
//     两条出路（done/stop 它，或改用 --new-worktree）
//   - ErrBaseCommitMissing → 400：任务仓库落后于协调者本地基线，拒发并带 git push
//     动作提示；与参数类错误同层级——调用方先解决远程仓库再重派
//   - ErrRepoUnusable / errBadDispatchRequest / ErrBadWorkspaceReq → 400：调用方先
//     解决请求本身的问题（仓库路径不对、参数缺失/互斥/分支不存在、plan 编码错误）
//   - ErrProjectNotRegistered → 400：project_id / project_name 在本机位置表里查不到，
//     报文自带本机已登记清单——协调者拿到即可行动（换名字，或先 handoff project add）；
//     本机 CLI 收到这条会自动补登记后重发（B62）
//   - errExecutorStartFailed → 500 + 可读真因：executor 启动失败（执行者二进制不在 PATH、
//     opencode 未安装等）是环境问题而非 agentd 内部故障——响应体直接带
//     err.Error()（含真因如 exec: "opencode": executable file not found），协调者拿到
//     即可行动（装依赖），不必去 agentd.log 翻一行 exec 错误
//   - errEnvResolveFailed → 500 + 可读真因：env 文件缺失/语法错是执行机上的配置
//     问题，响应体带完整路径与行号，派发者改完文件重派即可
//   - 其余（任务目录/落库等 agentd 侧故障）→ 500
func (s *Server) writeDispatchError(w http.ResponseWriter, projectRef string, err error) {
	switch {
	case errors.Is(err, ErrDirtyWorktree):
		s.log.Warn("dispatch 被拒：工作区不干净", "project", projectRef, "cause", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrWorkdirBusy):
		s.log.Warn("dispatch 被拒：目标工作目录被占用", "project", projectRef, "cause", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrBaseCommitMissing):
		s.log.Warn("dispatch 被拒：任务仓库落后于本地基线", "project", projectRef, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrRepoUnusable):
		s.log.Warn("dispatch 被拒：仓库不可用", "project", projectRef, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrProjectNotRegistered):
		s.log.Warn("dispatch 被拒：项目未登记", "project", projectRef, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, errBadDispatchRequest):
		s.log.Warn("dispatch 被拒：请求参数非法", "project", projectRef, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrBadWorkspaceReq):
		s.log.Warn("dispatch 被拒：工作区参数非法", "project", projectRef, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrNoProcHeadroom):
		s.log.Warn("dispatch 被拒：进程余量不足", "project", projectRef, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, errExecutorStartFailed):
		s.log.Error("dispatch 启动 executor 失败（环境问题，真因回显）", "project", projectRef, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	case errors.Is(err, errEnvResolveFailed):
		s.log.Error("dispatch 被拒：env 文件解析失败（配置问题，真因回显）", "project", projectRef, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	case errors.Is(err, errDisciplineResolveFailed):
		s.log.Error("dispatch 被拒：纪律块解析失败（配置问题，真因回显）", "project", projectRef, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	default:
		s.log.Error("派发任务失败", "project", projectRef, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "派发任务失败"})
	}
}

// projectAddRequest 是 POST /api/projects 的请求体。
//
// 形态由 path / 路径是否存在 / origin_url 是否非空共同决定：
//   - path 空 + origin 有 → clone 到 repo_root/<name>
//   - path 有且目录存在 → 登记已有仓（origin 可省，省则现读）
//   - path 有且目录不存在 + origin 有 → clone 到该 path
//   - 其余非法组合 → 400
type projectAddRequest struct {
	OriginURL string `json:"origin_url"`
	Name      string `json:"name"`
	Path      string `json:"path"`
}

// handleProjectAdd 登记一个项目（必要时先克隆）。
func (s *Server) handleProjectAdd(w http.ResponseWriter, r *http.Request) {
	s.log.Info("project add 请求", "method", r.Method, "path", r.URL.Path)
	if s.forwardIfRequested(w, r) {
		return // 显式指名了别的机器：本机只做搬运（W3a §5.1.1）
	}
	if s.mgr == nil {
		s.log.Warn("project add 请求到达但 manager 未注入", "remote_addr", r.RemoteAddr)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	var req projectAddRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.log.Warn("project add 请求体解析失败", "err", err)
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "请求体必须是 JSON {origin_url, name, path}"})
		return
	}
	loc, err := s.mgr.RegisterProject(r.Context(), RegisterProjectReq{
		OriginURL: req.OriginURL, Name: req.Name, Path: req.Path})
	if err != nil {
		s.writeProjectError(w, req.Name, err)
		return
	}
	s.log.Info("project add 完成", "project_id", loc.ProjectID, "name", loc.Name, "path", loc.Path)
	writeJSON(w, http.StatusOK, loc)
}

// handleProjectList 列出全部项目位置（含现场探得的实际状态）。
func (s *Server) handleProjectList(w http.ResponseWriter, r *http.Request) {
	s.log.Info("project list 请求", "method", r.Method, "path", r.URL.Path)
	if s.mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	locs, err := s.mgr.ListProjects(r.Context())
	if err != nil {
		s.writeProjectError(w, "", err)
		return
	}
	if locs == nil {
		locs = []proto.ProjectLocation{} // 空列表要序列化成 []，不是 null
	}
	s.log.Info("project list 完成", "count", len(locs))
	writeJSON(w, http.StatusOK, locs)
}

// handleProjectRemove 注销一条项目位置（只删登记，不动磁盘）。
func (s *Server) handleProjectRemove(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.log.Info("project remove 请求", "name", name)
	if s.forwardIfRequested(w, r) {
		return // 显式指名了别的机器：本机只做搬运（W3a §5.1.1）
	}
	if s.mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	if err := s.mgr.UnregisterProject(r.Context(), name); err != nil {
		s.writeProjectError(w, name, err)
		return
	}
	s.log.Info("project remove 完成", "name", name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// writeProjectError 把项目登记操作的失败映射为 HTTP 状态码与可读原因。
//
// 映射规则（与 writeDispatchError 同一套哲学：调用方拿到就能行动）：
//   - store.ErrNotFound → 404：登记名不存在
//   - ErrProjectAlreadyExists → 409：项目/名字/路径已被占用，或克隆落点已存在——
//     与 ErrDirtyWorktree/ErrWorkdirBusy 同为状态冲突
//   - store.ErrProjectDuplicate → 409：改名/改路径撞上已被占用的名字或路径
//     （handleProjectPatch 直接透传 store 的冲突哨兵，映射集中在这一处）
//   - ErrWorkdirBusy → 409：注销时项目仓库仍被活跃任务占用
//   - ErrProjectOriginMismatch → 400：路径上是另一个项目——报文同时给出两边
//     的 origin，人一眼就能看出「你说的是 A，那儿实际是 B」
//   - ErrRepoUnusable / errBadDispatchRequest → 400：请求本身的问题
//     （路径不是仓库、没有 origin、clone 失败、参数缺失）
//   - 其余 → 500
func (s *Server) writeProjectError(w http.ResponseWriter, name string, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.log.Warn("项目登记操作被拒：登记不存在", "name", name, "cause", err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrProjectAlreadyExists):
		s.log.Warn("项目登记操作被拒：已存在", "name", name, "cause", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrProjectDuplicate):
		s.log.Warn("项目登记操作被拒：名字或路径已被占用", "name", name, "cause", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrWorkdirBusy):
		s.log.Warn("项目登记操作被拒：被活跃任务占用", "name", name, "cause", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrProjectOriginMismatch):
		s.log.Warn("项目登记被拒：路径上是另一个项目", "name", name, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrRepoUnusable), errors.Is(err, errBadDispatchRequest):
		s.log.Warn("项目登记操作被拒：请求非法", "name", name, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		s.log.Error("项目登记操作失败", "name", name, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "项目登记操作失败"})
	}
}

// continueRequest 是 POST /api/tasks/{id}/continue 的请求体。
type continueRequest struct {
	Instructions string `json:"instructions"`
}

// handleContinue 向任务续发修改指令（要求任务处于 waiting_review）。
//
// 错误映射：任务不存在 404；状态不允许续接 409（manager 返回 store.ErrBadTransit）；
// executor 运行态已丢失 409（executor.ErrTaskNotRunning，agentd 可能重启过，提示
// 重新派发）。
func (s *Server) handleContinue(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	s.log.Info("continue 请求", "method", r.Method, "path", r.URL.Path, "task", taskID)
	if s.mgr == nil {
		s.log.Warn("continue 请求到达但 manager 未注入", "task", taskID)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	var req continueRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.log.Warn("continue 请求体解析失败", "task", taskID, "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体必须是 JSON {instructions}"})
		return
	}
	if req.Instructions == "" {
		s.log.Warn("continue 指令为空", "task", taskID)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "instructions 不能为空"})
		return
	}
	if err := s.mgr.Continue(r.Context(), taskID, req.Instructions); err != nil {
		s.writeManagerError(w, taskID, "续发指令", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// doneRequest 是归档请求的可选请求体。
//
// 为什么整个 body 可选、解析失败也不报错：旧版 CLI 根本不发 body，把「解析不出
// 说明」升级成 400 会让新 agentd 拒收所有未升级的客户端——而归档本身与说明无关。
type doneRequest struct {
	Note string `json:"note"`
}

// doneResult 是归档响应。
//
// NoteSaved 恒等于「本次请求带了非空说明且已落库」。旧版 agentd 不返回该字段，
// 客户端按 false 处理并告警——这与 stop 的 worktree_removed 是同一个模式，
// 保证「说明丢了」不会变成哑失败（B30 的教训）。
type doneResult struct {
	OK        bool `json:"ok"`
	NoteSaved bool `json:"note_saved"`
}

// handleDone 归档任务（要求任务处于 waiting_review）：置 completed 并回收 executor。
func (s *Server) handleDone(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	s.log.Info("done 请求", "method", r.Method, "path", r.URL.Path, "task", taskID)
	if s.mgr == nil {
		s.log.Warn("done 请求到达但 manager 未注入", "task", taskID)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	// body 缺失或非法一律按「无说明」处理，不报错（见 doneRequest 注释）
	var req doneRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		s.log.Debug("done 请求体解析失败，按无说明处理", "task", taskID, "cause", err)
		req.Note = ""
	}
	if len(req.Note) > proto.MaxDoneNoteBytes {
		s.log.Warn("归档说明超长被拒", "task", taskID,
			"note_bytes", len(req.Note), "limit", proto.MaxDoneNoteBytes)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("归档说明超长（%d 字节，上限 %d）", len(req.Note), proto.MaxDoneNoteBytes)})
		return
	}
	// done 幂等：agentd 被压垮时（B93 事故），第一次 done 的请求已落库但响应
	// 读超时，审核者的自然反应是重发，而重发拿到的 409 看起来像「状态不对」。
	// 客户端分不清「超时 = 请求没到」和「超时 = 请求到了但响应没回来」——
	// 这是只有服务端才有的信息，只能在这里解决。
	//
	// **判据要严**：只有 completed 转 200。其余非 waiting_review 的状态仍然
	// 409——那些是真的状态不对，一并放行等于让 done 变成万能收口，审核者会
	// 失去「我操作错了」这个信号。
	if cur, err := s.st.GetTask(taskID); err == nil && cur.State == proto.TaskStateCompleted {
		writeJSON(w, http.StatusOK, doneResult{OK: true, NoteSaved: req.Note != ""})
		return
	}
	if err := s.mgr.Done(r.Context(), taskID, req.Note); err != nil {
		s.writeManagerError(w, taskID, "归档任务", err)
		return
	}
	// 消息文字必须与 manager.Done 的「done 完成」区分开：两处同名会让一次归档
	// 捞出两行日志，其中一行没有 note_saved，排障时分不清看的是哪一层
	s.log.Info("done 请求完成", "task", taskID, "note_saved", req.Note != "")
	writeJSON(w, http.StatusOK, doneResult{OK: true, NoteSaved: req.Note != ""})
}

// handleStop 主动中止任务（停 executor、落 failed；作废由终态迁移收口完成）。
//
// 响应体：status=stopped；worktree_removed 如实反映本次是否删除了 managed
// worktree（true=agentd 建的 worktree 已删，false=用户自带 worktree / 原地模式，
// 或 managed 清理失败）。CLI 据此打印提示文案，不猜。
//
// 错误映射：任务不存在 404；已是终态 409（manager 返回 store.ErrBadTransit）。
func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	s.log.Info("stop 请求", "method", r.Method, "path", r.URL.Path, "task", taskID)
	if s.mgr == nil {
		s.log.Warn("stop 请求到达但 manager 未注入", "remote_addr", r.RemoteAddr)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	removed, err := s.mgr.Stop(r.Context(), taskID)
	if err != nil {
		s.writeManagerError(w, taskID, "stop", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "stopped", "worktree_removed": removed})
}

// parseForce 解析 resume 的 force 查询参数。
//
// 为什么自己解析而不用 strconv.ParseBool 的错误：非法值（如 force=yes）一律
// 按 false 处理——强制收口是绕过正常流程的动作，看不懂的输入必须走保守的那边。
func parseForce(r *http.Request) bool {
	switch r.URL.Query().Get("force") {
	case "true", "1":
		return true
	}
	return false
}

// handleResume 显式恢复卡死的任务：重投「已落库但未送达 executor」的应答，
// 以及（B38）断连窗口内丢失的回合终态对账补发。
//
// 这是 reply 返回 502 之后协调者唯一的自助出口——在它之前，工单已被消耗、
// 任务停在 waiting_answer，reply 得 404、continue/done 得 409，CLI 上无路可走
// （详见 Manager.RecoverStuck 的 why）。
//
// 查询参数：
//   - force=true：对账判不出（executor 不支持对账 / 回合确实还在忙 / 查询失败）
//     时仍把任务强制收口到 waiting_review，使 continue/done 可用；收口会留下
//     写明「人工强制、未经 executor 确认」的事件。**保住 executor 会话**——
//     这是它与 stop 的根本区别（stop 会杀掉会话并把任务落成 failed）。
//
// 响应：
//   - 200 + RecoverReport：包含重投条数、对账结果、executor 是否已不在、收尾状态与结论
//   - 502 + RecoverReport：executor 仍在但这次没打通，可稍后重试（报告一并回传，
//     让协调者看到已经重投成功了几条）
//   - 404 任务不存在；409 任务已终结
func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	force := parseForce(r)
	s.log.Info("resume 请求", "method", r.Method, "path", r.URL.Path,
		"task", taskID, "force", force)
	if s.mgr == nil {
		s.log.Warn("resume 请求到达但 manager 未注入", "task", taskID)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	rep, err := s.mgr.RecoverStuck(taskID, force)
	if err != nil {
		if rep != nil {
			// 重投中途失败：报告仍有价值（已成功几条、任务停在哪），带 502 回传
			s.log.Warn("resume 重投未完成", "task", taskID, "redelivered", rep.Redelivered, "cause", err)
			writeJSON(w, http.StatusBadGateway, rep)
			return
		}
		s.writeManagerError(w, taskID, "恢复任务", err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// taskOrErr 读取路径中的任务并做门禁：任务不存在写 404、缺工作区路径写 400，
// 两种情况都返回 ok=false（调用方直接 return 即可）。
//
// 为什么独立于 taskRepoOrErr：diff / branches 两个端点除了工作区路径还要读
// 任务的 BaseCommit（B65），而另外三个调用点只关心路径。拆开后既不动它们的
// 签名，也不必让它们承担一个用不到的返回值。
//
// 返回的是 task.Workdir() 而非 task.RepoPath（为什么 diff/fetch/run 必须在
// Workdir 而非主仓库：worktree 任务的 executor cwd 与分支 HEAD 都在 Workdir，
// 主仓库的 HEAD 停在派发前的位置——diff 相对基准、fetch 看工作区文件、run 跑
// 测试都必须落在 executor 真正干活的目录，否则审阅的是错误的代码状态）。
//
// 供 diff/fetch/run 三条审阅路由共用——它们只关心任务指向的仓库，不依赖状态机。
func (s *Server) taskOrErr(w http.ResponseWriter, taskID string) (*proto.Task, bool) {
	task, err := s.st.GetTask(taskID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("任务不存在", "task", taskID)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "任务不存在"})
		} else {
			s.log.Error("读取任务失败", "task", taskID, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		}
		return nil, false
	}
	if task.Workdir() == "" {
		s.log.Warn("任务缺少工作区路径", "task", taskID)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "任务没有工作区路径"})
		return nil, false
	}
	return task, true
}

// taskRepoOrErr 读取路径中的任务并返回其工作区目录；任务不存在（404）或没有
// 工作区路径（400）时已写好响应并返回 ok=false。
func (s *Server) taskRepoOrErr(w http.ResponseWriter, taskID string) (repo string, ok bool) {
	task, ok := s.taskOrErr(w, taskID)
	if !ok {
		return "", false
	}
	return task.Workdir(), true
}

// handleTaskDiff 返回任务分支相对基准分支的审阅素材（git diff + 提交列表）。
//
// 参数：
//   - base: 查询参数，基准；缺省时优先用任务 BaseCommit，没有才按仓库推导
//
// 注意：
//   - diff 是协调者主动发起的只读审阅，不做状态门禁——running 中即可看实时进度
func (s *Server) handleTaskDiff(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	s.log.Info("diff 请求", "method", r.Method, "path", r.URL.Path, "task", taskID)
	task, ok := s.taskOrErr(w, taskID)
	if !ok {
		return
	}
	repo, headRev := taskDiffTarget(task)
	base := r.URL.Query().Get("base")
	if base == "" {
		base = diffBaseFor(task, repo)
	}
	s.log.Info("diff 基准已确定", "task", taskID, "base", base,
		"from_task_base", r.URL.Query().Get("base") == "" && task.BaseCommit != "")
	if base == "" {
		s.log.Warn("无法确定基准分支", "task", taskID, "repo", repo)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无法确定基准分支，请用 base 参数指定"})
		return
	}
	// 回退到主仓库时，任务分支可能也已经被删了（任务做完、分支合并后删掉是常态）。
	// 那种情况下素材是真的没有了，要说清楚——原来它表现为 git 的 exit status 128，
	// 读的人无从判断是「分支没了」还是「git 坏了」。
	if repo != task.Workdir() && !manualBranchExists(r.Context(), repo, headRev) {
		s.log.Warn("任务分支已不存在，无可比对素材", "task", taskID, "repo", repo, "branch", headRev)
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("任务分支 %s 已不存在，且任务 worktree 已回收——没有可比对的素材了", headRev)})
		return
	}
	diff, err := DiffRange(repo, base, headRev)
	if err != nil {
		if errors.Is(err, ErrBadBaseBranch) {
			// base 是协调者可控的查询参数：非法 base（"-" 前缀）是请求问题而非
			// 服务故障，400 明确告知（与 ErrPathEscape 同款映射）
			s.log.Warn("diff 基准分支非法被拒绝", "task", taskID, "base", base)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": truncateRunes(err.Error(), 200)})
			return
		}
		s.log.Error("取 diff 失败", "task", taskID, "repo", repo, "base", base, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	s.log.Info("diff 完成", "task", taskID, "base", base, "bytes", len(diff))
	writeJSON(w, http.StatusOK, map[string]string{"diff": diff})
}

// handleTaskBundle 吐任务分支的 git bundle，供协调者 pull 时不经 ssh 取回改动。
//
// 请求：GET /api/tasks/{id}/bundle?have=<sha>
//   - have 给了：生成 <have>..<branch> 的薄包（常态，通常几百字节）
//   - have 空：  生成全量包（协调者手上没有基线时的罕见退路）
//
// 响应：
//   - 200 application/octet-stream，带 Content-Length
//   - **没有 204**：区间为空时 BundleRange 自动放宽区间（§5.2），照样产出一个
//     带 ref 的包。客户端的本地分支引用是 fetch 的副产品，短路掉 fetch 就会让
//     「已是最新」在协调者手上什么都没有的情况下打出来
//   - 400 任务无分支 / have 在任务仓库中不存在 / 参数以 - 开头
//   - 404 任务不存在（byTask 已处理）
//   - 500 git 失败
//
// 注意：
//   - 用 task.RepoPath（主仓库）而不是 Workdir()：worktree 是主仓的从属工作树，
//     分支对象在主仓库里。这与 handleTaskDiff 不同，那个要的是工作树状态
//   - 先把包落临时文件再整体发出，**不**把 git 的输出直接流进 ResponseWriter：
//     直接流的话 git 中途失败时响应头早已发出，客户端收到的是一个截断的 200——
//     一次服务端故障被伪装成内容不完整的成功
func (s *Server) handleTaskBundle(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	have := r.URL.Query().Get("have")
	s.log.Info("bundle 请求", "method", r.Method, "path", r.URL.Path, "task", taskID, "have", have)
	task, ok := s.taskOrErr(w, taskID)
	if !ok {
		return
	}
	if task.Branch == "" {
		s.log.Warn("任务尚无分支，无可打包", "task", taskID)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "任务尚无分支，无可同步"})
		return
	}
	start := time.Now()
	path, err := BundleRange(r.Context(), task.RepoPath, have, task.Branch)
	switch {
	case errors.Is(err, ErrHaveMissing), errors.Is(err, ErrBadBaseBranch):
		// have 与 branch 都由请求侧决定，属请求问题不是服务故障（与 diff 的
		// ErrBadBaseBranch 同款映射）
		s.log.Warn("bundle 请求参数被拒", "task", taskID, "have", have, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	case err != nil:
		s.log.Error("生成 bundle 失败", "task", taskID, "repo", task.RepoPath,
			"branch", task.Branch, "have", have, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	defer os.Remove(path)

	f, err := os.Open(path)
	if err != nil {
		s.log.Error("打开生成的 bundle 失败", "task", taskID, "path", path, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		s.log.Error("读取 bundle 大小失败", "task", taskID, "path", path, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	n, err := io.Copy(w, f)
	if err != nil {
		// 头已经发出去了，改不了状态码——只能把事实记下来。客户端会因为
		// 收到的字节数与 Content-Length 不符而失败，这是它该失败的方式
		s.log.Error("发送 bundle 中断", "task", taskID, "sent", n, "total", fi.Size(), "cause", err)
		return
	}
	s.log.Info("bundle 发送完成", "task", taskID, "branch", task.Branch, "have", have,
		"bytes", n, "elapsed_ms", time.Since(start).Milliseconds())
}

// handleTaskBranches 返回任务仓库的本地分支名列表与推导出的默认基准分支。
//
// 供前端审阅栏的基准下拉用（spec 2026-08-17 §6.2）。只读，不做状态门禁。
// default 为空表示推导不出（前端下拉退化为仅「自动推导」项）。
func (s *Server) handleTaskBranches(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	s.log.Info("branches 请求", "method", r.Method, "path", r.URL.Path, "task", taskID)
	task, ok := s.taskOrErr(w, taskID)
	if !ok {
		return
	}
	repo := task.Workdir()
	branches, err := Branches(repo)
	if err != nil {
		s.log.Error("列分支失败", "task", taskID, "repo", repo, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	s.log.Info("branches 完成", "task", taskID, "count", len(branches))
	writeJSON(w, http.StatusOK, map[string]any{
		"branches":  branches,
		"default":   resolveBaseBranch(repo),
		"task_base": task.BaseCommit,
	})
}

// handleTaskFile 返回任务仓库内指定文件的内容（协调者取上下文用）。
//
// 参数：
//   - path: 查询参数，相对仓库根的路径（必须）；逃逸出仓库的路径返回 400
func (s *Server) handleTaskFile(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	rel := r.URL.Query().Get("path")
	s.log.Info("file 请求", "method", r.Method, "path", r.URL.Path, "task", taskID, "file", rel)
	repo, ok := s.taskRepoOrErr(w, taskID)
	if !ok {
		return
	}
	if rel == "" {
		s.log.Warn("file 请求缺 path 参数", "task", taskID)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 path 参数"})
		return
	}
	res, err := ReadFile(repo, rel)
	if err != nil {
		switch {
		case errors.Is(err, ErrPathEscape):
			s.log.Warn("file 路径逃逸被拒绝", "task", taskID, "path", rel)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径不合法（不允许逃出任务仓库）"})
		case errors.Is(err, fs.ErrNotExist):
			s.log.Warn("file 目标不存在", "task", taskID, "path", rel)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "文件不存在"})
		case errors.Is(err, ErrPathIsDir):
			// 目录是可确定的状态（不同于「读取失败」的环境性问题），400 明确告知
			s.log.Warn("file 目标是目录", "task", taskID, "path", rel)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径是目录，不是文件"})
		case errors.Is(err, ErrNotRegularFile):
			s.log.Warn("file 目标不是普通文件", "task", taskID, "path", rel)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径不是普通文件"})
		default:
			s.log.Error("读取文件失败", "task", taskID, "path", rel, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取文件失败"})
		}
		return
	}
	// 截断提示留在 CLI 这条线上：handoff fetch 的用途就是看文件开头，提示是给
	// 审核者看的（没有它，审核者会把第 1 MiB 处当成文件末尾去推理）。搬到这里
	// 之后 ReadFile 的返回才是保真的，在线编辑那条线才敢把内容存回磁盘。
	// 本端点的响应体因此逐字节不变，handoff fetch 行为零变更
	content := res.Content
	if res.Truncated {
		content += truncatedNotice(res.Size)
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": content})
}

// runRequest 是 POST /api/tasks/{id}/run 的请求体。
type runRequest struct {
	Cmd string `json:"cmd"`
}

// runResponse 是 run 接口的响应体（合并输出 + 退出码）。
type runResponse struct {
	Stdout   string `json:"stdout"`
	ExitCode int    `json:"exit_code"`
}

// handleTaskRun 在任务仓库执行一条审阅命令（sh -c），返回合并输出与退出码。
//
// 注意：这是协调者主动发起的只读审阅动作（跑测试/lint），**不走审批门**——
// 命令由协调者指定并经 sh 执行，agentd 只负责执行、限时（10min 超时被杀，退出码
// 124）与回收。命令非零退出同样返回 200，退出码在响应体中表达。
func (s *Server) handleTaskRun(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	s.log.Info("run 请求", "method", r.Method, "path", r.URL.Path, "task", taskID)
	repo, ok := s.taskRepoOrErr(w, taskID)
	if !ok {
		return
	}
	var req runRequest
	// LimitReader 限制请求体大小，防止恶意大 body 占满内存
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.log.Warn("run 请求体解析失败", "task", taskID, "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体必须是 JSON {cmd}"})
		return
	}
	if strings.TrimSpace(req.Cmd) == "" {
		s.log.Warn("run 命令为空", "task", taskID)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cmd 不能为空"})
		return
	}
	stdout, exitCode, err := RunCmd(r.Context(), repo, req.Cmd)
	if err != nil {
		if errors.Is(err, ErrNoProcHeadroom) || errors.Is(err, ErrWorkdirGone) {
			s.log.Warn("run 被拒", "task", taskID, "repo", repo,
				"cmd", truncateRunes(req.Cmd, 200), "cause", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": truncateRunes(err.Error(), 200)})
			return
		}
		s.log.Error("run 执行失败", "task", taskID, "cmd", truncateRunes(req.Cmd, 200), "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	writeJSON(w, http.StatusOK, runResponse{Stdout: stdout, ExitCode: exitCode})
}

// writeManagerError 把 manager 返回的错误映射为 HTTP 状态码与提示。
//
// 映射规则：store.ErrNotFound → 404；store.ErrBadTransit → 409（状态不允许）；
// executor.ErrTaskNotRunning → 409（executor 运行态已丢失，agentd 可能重启过，
// 需重新派发——可行动提示而非扁平 500）；其余 → 500。
func (s *Server) writeManagerError(w http.ResponseWriter, taskID, op string, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.log.Warn("manager 操作目标任务不存在", "task", taskID, "op", op)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "任务不存在"})
	case errors.Is(err, store.ErrBadTransit):
		s.log.Warn("manager 操作状态不允许", "task", taskID, "op", op, "cause", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "任务当前状态不允许该操作"})
	case errors.Is(err, executor.ErrTaskNotRunning):
		// 为什么映射 409 而非 500：executor 运行态随 agentd 重启（或进程死亡）丢失，
		// 是「可预期、可行动」的状态而非内部故障——协调者需要的是「重新派发」的
		// 明确指引，而不是被扁平 500 挡在门外（resume 的 executor_gone=false 已表明
		// 会话上下文还在，缺的只是恢复路径）
		s.log.Warn("manager 操作遇执行器运行态已丢失", "task", taskID, "op", op, "cause", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "任务执行器运行态已丢失（agentd 可能重启过），请重新派发"})
	default:
		s.log.Error("manager 操作失败", "task", taskID, "op", op, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
	}
}

// handleEvents 处理 /ws/events：先订阅 hub 实时流，再补发 store 中 seq>from_seq 的
// 历史事件；排空器 goroutine 在整个连接生命周期内持续消费订阅通道，主循环按批次
// 归并（按 seq 升序、去重）后写出。
//
// 参数：
//   - task: 任务 ID（必填）
//   - from_seq: 起始 seq（不含，默认 0）；客户端自存 cursor，重连时带最后收到的 seq 即可补齐断线事件
//
// 注意：
//   - 客户端只读不写：CloseRead 接管读侧（响应 ping/pong/close 帧），其返回的 ctx 在
//     连接关闭时取消，作为写循环的退出信号，避免空闲断连连接泄漏订阅
//   - **为什么先订阅后补发**：重放写循环可能因 TCP 背压阻塞任意久，若先重放后订阅，
//     窗口期内 Publish 的事件订阅者为零、被 hub 直接丢弃。丢的是 question/
//     permission_request 这类一次性唤醒事件：任务随即进入 waiting_answer 不再产出
//     事件，客户端连接健康不会重连（WaitEvent 只在连接出错时重连），协调者永远
//     不被唤醒，executor 阻塞到看门狗兜底。先订阅 + 排空器全程消费 + seq 归并去重后，
//     窗口期事件既不丢也不重。
//   - **为什么排空器覆盖整个连接生命周期**：任何一次事件写出都可能因背压阻塞任意久，
//     阻塞期间若订阅通道无人消费，16 缓冲被 Publish 写满后 hub 即按慢订阅者契约丢
//     弃——排空器与所有写出并发运行，订阅通道从握手完成到连接关闭永不写满。
//   - 重放用 EventsFromAsc（截断尾部、缺口可凭更大 cursor 续拉），而非 EventsFrom
//     （截最旧、cursor 越过缺口永不补齐，见 store 包两方法的语义说明）
//   - **镜像任务同形**：本机 tasks 表没有、但 mirror_tasks 有的任务，从
//     mirror_events 重放历史，活事件由镜像订阅经同一个 Hub 送来。对浏览器
//     协议完全同形（帧就是带 seq 的 Event），ws.ts 无感——这正是「浏览器
//     永远只连本机一条 WS」的兑现处
//   - 任务归档（done）时 hub 会关闭本连接的订阅，此处以 StatusNormalClosure +
//     "task archived" 收尾。客户端据这个关闭码区分「归档」与「断线」——断线要
//     重连，归档要退出，两者搞混就是无限重连一个已经结束的任务
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task")
	if taskID == "" {
		s.log.Warn("WS 连接缺少 task 参数", "remote_addr", r.RemoteAddr)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 task 参数"})
		return
	}
	fromSeq := int64(0)
	if q := r.URL.Query().Get("from_seq"); q != "" {
		var err error
		fromSeq, err = strconv.ParseInt(q, 10, 64)
		if err != nil || fromSeq < 0 {
			s.log.Warn("WS from_seq 参数非法", "task", taskID, "from_seq", q, "remote_addr", r.RemoteAddr)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from_seq 必须是大于等于 0 的整数"})
			return
		}
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		// Accept 失败时已自行写回响应（如非升级请求的 400），此处仅记录
		s.log.Warn("WS 握手失败", "task", taskID, "remote_addr", r.RemoteAddr, "err", err)
		return
	}
	defer conn.CloseNow()

	// 任务存在性校验：打错 task-id 对订阅无意义——hub 按任务路由，不存在任务的事件
	// 永远不会来，旧实现会让 wait 无限阻塞（与「还没有事件」无法区分，P0-2 根因）。
	// 以 PolicyViolation（1008）close 码关闭连接：语义是「你的请求本身非法」而非
	// 网络断连，客户端据此判定永久失败立即报错，而不是把它当瞬时故障无限退避重连
	mirrored := false
	if _, err := s.st.GetTask(taskID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// 本机没有：可能是镜像任务（远端的活，本机订着它的事件）。命中则本
			// 连接从 mirror_events 重放历史，活事件由镜像订阅经同一个 Hub 送来
			if _, ok, mErr := s.st.MirrorTaskTarget(taskID); mErr == nil && ok {
				mirrored = true
				s.log.Info("WS 订阅镜像任务", "task", taskID, "from_seq", fromSeq)
			} else {
				s.log.Warn("WS 订阅任务不存在", "task", taskID, "remote_addr", r.RemoteAddr)
				if cerr := conn.Close(websocket.StatusPolicyViolation, "task not found"); cerr != nil {
					// 连接已断时 Close 失败不影响结论——客户端侧按断线走退避重连，
					// 若再次拨号仍会走到本分支被关闭
					s.log.Warn("WS 关闭任务不存在连接失败", "task", taskID, "err", cerr)
				}
				return
			}
		} else {
			s.log.Error("WS 校验任务失败", "task", taskID, "cause", err)
			return
		}
	}

	sent := 0
	defer func() {
		s.log.Info("WS 连接断开", "task", taskID, "from_seq", fromSeq, "sent", sent)
	}()

	// 连接关闭（含对端断开）时该 ctx 取消，作为写循环退出信号
	ctx := conn.CloseRead(r.Context())

	// 会话身份的连接必须周期性复验：Hub 只按 taskID 路由、不持有会话身份，
	// 吊销一个会话不会自动断开它已经建立的 WS，而手机丢失场景下
	// 「吊销了但还连着」不可接受。Bearer（CLI）连接不受影响
	if id := identityFrom(r.Context()); id.session != "" {
		go s.watchSession(ctx, conn, id.session, taskID)
	}

	// 先订阅再补发：见函数头的「为什么先订阅后补发」——重放期间的事件必须被捕获
	ch, cancel := s.hub.Subscribe(taskID)
	defer cancel()

	// 实时事件排空器：整个连接生命周期内的唯一消费者 goroutine，持续阻塞消费订阅
	// 通道并收集进 live 切片（互斥锁保护），每收集一条向 drainNotify 发送一次唤醒
	// （缓冲 1；主循环总是整批快照 live，单个待处理唤醒足够，default 分支防堆积）。
	//
	// 为什么必须覆盖「整个连接生命周期」而非只覆盖重放阶段：任何一次事件写出
	// （重放写、归并写、实时写）都可能因 TCP 背压阻塞任意久，阻塞期间若无人消费
	// 订阅通道，16 缓冲被 Publish 写满后 hub 的 select-default 慢订阅者丢弃逻辑
	// 开始丢事件——排空器与所有写出并发运行，订阅通道从握手完成到连接关闭永不
	// 写满，Publish 的事件 100% 进入 live 等待写出，不存在「停止排空后再写归并」
	// 的丢事件窗口。
	//
	// 缓冲有上限（liveLimit）：对端「连着不读」时写循环会阻塞任意久，无上限的
	// 收集会把 hub 的有界丢弃换成 agentd 的无界内存增长。越限即置 overflow 并
	// 唤醒主循环断开连接——所有事件都已落库，客户端凭 cursor 重连可完整补拉。
	var (
		liveMu      sync.Mutex
		live        []proto.Event
		overflow    bool
		archived    bool // 订阅被 hub 关闭（任务归档），与「本连接自己结束」区分
		drainNotify = make(chan struct{}, 1)
	)
	notifyDrain := func() {
		select {
		case drainNotify <- struct{}{}:
		default: // 已有待消费唤醒；主循环整批快照时会一并取走全部 live
		}
	}
	go func() {
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					// 通道被关闭有两种可能：连接结束时 defer cancel 关的（此时主循环
					// 已在退出路上，下面这个标记没人读，无害），或 hub.CloseTask 关的
					//（任务归档）。后者必须让主循环知道，好以正常关闭码收尾
					liveMu.Lock()
					archived = true
					liveMu.Unlock()
					notifyDrain()
					return
				}
				liveMu.Lock()
				if len(live) >= s.liveLimit {
					overflow = true
					liveMu.Unlock()
					notifyDrain()
					return // 排空器退出：主循环随即断开连接
				}
				live = append(live, ev)
				liveMu.Unlock()
				notifyDrain()
			case <-ctx.Done():
				return // 连接断开，排空器退出（防御；defer cancel 关闭通道同样触发退出）
			}
		}
	}()

	// 阶段一：补发历史事件（from_seq 之后按 seq 升序的最旧 limit 条，截断在尾部）
	// 镜像任务（本机 tasks 表没有、mirror_tasks 有）从 mirror_events 重放，
	// 语义与本机 EventsFromAsc 一致（开区间、截尾部、可凭更大 cursor 续拉）。
	var replays []proto.Event
	if mirrored {
		replays, err = s.st.MirrorEventsFrom(taskID, fromSeq, s.replayLimit)
	} else {
		replays, err = s.st.EventsFromAsc(taskID, fromSeq, s.replayLimit)
	}
	if err != nil {
		s.log.Error("WS 补发历史事件失败", "task", taskID, "from_seq", fromSeq, "mirrored", mirrored, "cause", err)
		return
	}
	// 重放覆盖 (fromSeq, maxReplayed]；maxReplayed 同时是「实时去重分界线」：
	// 任务内 seq 随落库单调递增，任何 seq <= maxReplayed 的事件都已先于重放快照
	// 落库，必然在重放结果中（已写出），订阅通道里同序号的拷贝可直接跳过
	maxReplayed := int64(0)
	if len(replays) > 0 {
		maxReplayed = replays[len(replays)-1].Seq
	}
	// 截断基线：记录重放快照时刻 store 的最新 seq。若 > maxReplayed 说明补发窗口
	// 被 eventReplayLimit 截断，缺口在尾部 (maxReplayed, storeMax]；缺口是否真的
	// 丢失（真截断）在归并后核对（见阶段二后的截断诊断）
	storeMax := int64(0)
	if latest, lerr := s.st.LatestEvent(taskID); lerr == nil {
		storeMax = latest.Seq
	}
	s.log.Debug("WS 重放开始", "task", taskID, "from_seq", fromSeq, "replays", len(replays), "store_max", storeMax)
	for _, ev := range replays {
		if err := writeEvent(ctx, conn, ev); err != nil {
			s.log.Warn("WS 补发写入失败", "task", taskID, "seq", ev.Seq, "err", err)
			return
		}
		sent++
	}
	s.log.Info("WS 连接建立", "task", taskID, "from_seq", fromSeq, "replayed", len(replays))

	// 已写出的最大 seq：重放写完全部 (fromSeq, maxReplayed]。此后实时事件按 seq
	// 单调推进，seq <= lastWrittenSeq 的事件必已写出，作为去重/乱序判据
	lastWrittenSeq := maxReplayed

	// deliveredInGap 统计「落在截断缺口区间 (maxReplayed, storeMax] 内且确实由
	// 实时流补出」的条数，供截断诊断核对。
	//
	// why 数条数而非看 seq 是否逐格衔接：seq 由 AUTOINCREMENT **全局**分配，
	// 跨任务交错，单任务的 seq 本来就不连续，衔接判定会在多任务下恒定误报。
	// 也不能像修复前那样比最大值（storeMax > lastWrittenSeq）——任何一条 seq
	// 更大的实时事件都会把 lastWrittenSeq 顶过缺口，正是「告警恒不触发」的成因。
	deliveredInGap := 0

	// writeLiveBatch 写出一个归并批次：按 seq 升序排序（排空收集顺序是「Publish
	// 到达通道的顺序」，与重放写交错后可能与 seq 序不一致——并发 Publish 时尤其
	// 如此——而单连接 SSE 要求全局保序，按 seq 升序写出保证客户端按 seq 连续推进
	// cursor），跳过重放已覆盖的重复，推进最后写出 seq。
	// 返回 false 表示本连接应立即结束（写失败或收到乱序迟到事件）。
	writeLiveBatch := func(pending []proto.Event) bool {
		sort.Slice(pending, func(i, j int) bool { return pending[i].Seq < pending[j].Seq })
		for _, ev := range pending {
			if ev.Seq <= maxReplayed {
				continue // 真重复：订阅后、重放快照前落库的事件已随重放写出
			}
			if ev.Seq <= lastWrittenSeq {
				// 乱序迟到：seq 大于重放覆盖面却小于已写出的最大 seq，说明它从未
				// 被写出（每条事件只广播一次，重复只可能来自重放交集）。
				// 落库与广播是两步，watchdog 与 mediate 并发发布时可交错成这个次序。
				// 直接跳过就是永久丢事件（客户端 cursor 已越过它），而单连接又无法
				// 回头补写——断开连接让客户端凭 cursor 重连，重放会按 seq 序完整补齐
				s.log.Warn("WS 收到乱序迟到事件，断开连接由客户端凭 cursor 重连补齐",
					"task", taskID, "seq", ev.Seq, "last_written", lastWrittenSeq)
				return false
			}
			if err := writeEvent(ctx, conn, ev); err != nil {
				s.log.Warn("WS 实时写入失败", "task", taskID, "seq", ev.Seq, "err", err)
				return false
			}
			lastWrittenSeq = ev.Seq
			if ev.Seq <= storeMax {
				deliveredInGap++
			}
			sent++
		}
		return true
	}

	// 阶段二：归并写出排空器在重放期间收集的实时事件。
	//
	// 去重依据：订阅后、重放快照前落库的事件会同时出现在重放结果与 live 中；由
	// 上面 maxReplayed 的分界论证，live 中 seq <= maxReplayed 的事件必已被重放
	// 写出，跳过；seq > maxReplayed 的是订阅后新产生的事件，必须补出，否则就是
	// P0-1 的窗口期丢失。本阶段写循环可能因背压阻塞任意久，但排空器仍在运行，
	// 阻塞期间新到事件继续进 live，由阶段三无缝接管——无丢事件窗口。
	liveMu.Lock()
	pending := live
	live = nil
	liveMu.Unlock()
	if !writeLiveBatch(pending) {
		return
	}
	s.log.Debug("WS 重放归并完成", "task", taskID, "live_merged", len(pending))

	// 截断诊断（真实可触发）：重放快照时刻 store 的最新 seq 大于最后写出的 seq，
	// 说明 (lastWrittenSeq, storeMax] 区间的事件既未随重放（被 eventReplayLimit
	// 截断）也未进入实时流（订阅前已落库并发布、被 hub 无订阅者丢弃）——真缺口，
	// 客户端需凭更大 cursor 重连续拉补齐；若归并已追平 store 最新，尾部缺口由
	// 实时流补齐，属预期场景，仅 Debug
	if len(replays) == s.replayLimit && storeMax > maxReplayed {
		gapTotal, cerr := s.st.CountEvents(taskID, maxReplayed, storeMax)
		switch {
		case cerr != nil:
			s.log.Error("WS 截断缺口核对失败", "task", taskID, "cause", cerr)
			s.noteTruncationDiagnosed("error")
		case gapTotal > deliveredInGap:
			s.log.Warn("WS 补发窗口截断且缺口未由实时流补齐", "task", taskID, "from_seq", fromSeq,
				"replayed", len(replays), "gap_total", gapTotal, "gap_delivered", deliveredInGap,
				"store_max", storeMax)
			s.noteTruncationDiagnosed("warned")
		default:
			s.log.Debug("WS 补发窗口截断但缺口已由实时流补齐", "task", taskID,
				"replayed", len(replays), "gap_total", gapTotal, "store_max", storeMax)
			s.noteTruncationDiagnosed("covered")
		}
	}

	// 阶段三：实时写循环。排空器始终是订阅通道的唯一消费者，主循环等唤醒后整批
	// 快照 → 排序 → 写出；写循环阻塞期间新到事件继续被排空器收集进 live，
	// 不产生「无人消费订阅通道」的丢事件窗口
	for {
		select {
		case <-drainNotify:
			liveMu.Lock()
			pending := live
			live = nil
			over := overflow
			arch := archived
			liveMu.Unlock()
			if !writeLiveBatch(pending) {
				return
			}
			if arch {
				// 顺序硬约束：先写完归档前排队的事件，再关连接——反过来会在
				// 归档瞬间吞掉最后一批事件
				s.log.Info("任务已归档，以正常关闭码结束事件流", "task", taskID,
					"sent", sent, "last_written", lastWrittenSeq)
				if cerr := conn.Close(websocket.StatusNormalClosure, "task archived"); cerr != nil {
					// 对端可能已经走了；关闭码送不到不改变结论，如实记一笔即可
					s.log.Debug("WS 归档关闭失败", "task", taskID, "err", cerr)
				}
				return
			}
			if over {
				s.log.Warn("WS 待写缓冲越限，断开连接（对端长时间不读）：客户端凭 cursor 重连补拉",
					"task", taskID, "limit", s.liveLimit, "last_written", lastWrittenSeq)
				return
			}
		case <-ctx.Done():
			return // 客户端已断开，退出并释放订阅（defer cancel 关闭通道，排空器随之退出）
		}
	}
}

// noteTruncationDiagnosed 通知测试截断诊断已完成；生产路径不设置钩子。
func (s *Server) noteTruncationDiagnosed(verdict string) {
	if s.onTruncationDiagnosed != nil {
		s.onTruncationDiagnosed(verdict)
	}
}

// writeEvent 将事件序列化为 JSON 文本帧写入 WS 连接。
func writeEvent(ctx context.Context, conn *websocket.Conn, ev proto.Event) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("序列化事件 %d: %w", ev.Seq, err)
	}
	return conn.Write(ctx, websocket.MessageText, b)
}

// writeJSON 以指定状态码写出 JSON 响应。
//
// 编码失败（响应已开始后无法回退）仅打日志，不改变已写出的状态码。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Default().Warn("JSON 编码失败", "err", err)
	}
}

// truncateRunes 将字符串截断为最多 n 个字符（按 rune 截断，避免切断多字节 UTF-8 字符）。
//
// 用途：日志里记录用户应答时限制长度，防止超长自由文本刷爆日志。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// ===== B156.3 自动化层组装点（架构法第四条第 3 档）=====

// SetupAutomation 装配三期自动化层：账本门面、编制域服务、keystone 域服务，
// 并把各域的出站端口绑定到具体实现上。与 SetLedger 同形：cmd 侧持账本库，
// 组装点内完成全部跨域绑定；绑定代码只允许出现在本文件（target.json assembly
// 登记点），组装点之外不得 new 他方具体类型。
//
// 骨架期语义：服务已构造、端口已绑定，行为由实现票逐缝点亮；宿主进程照常
// 启动，不受未点亮能力影响。
func (s *Server) SetupAutomation(st *ledger.Store) {
	facade := ledgerapi.New(st)
	s.autoLedger = facade
	s.scheduling = scheduling.New(facadeAsRegistry{f: facade})
	s.rooms = collab.New(facade)
	s.rooms.SetCursorStore(cursor.New(filepath.Join(s.conf().DataDir, "room-cursors.json")))
	s.rebind = facadeBindAdapter{f: facade}
	// 凭据相对路径表仍由 toolchain 唯一维护；组装点注入给 hostapi，避免
	// hostapi 反向 import maintenance 域或复制三家 CLI 的平台规则。
	s.hostAPI = hostapi.NewWithCredentialPathFor(toolchain.CredRelPathFor)
	runner := coordinatorRunner{h: s.hostAPI}
	s.keystone = keystone.New(runner, roomNarrator{c: s.rooms}, facade, attachLocator{})
	if s.pty != nil {
		s.ptyGate = ptyapi.New(s.pty)
	}
}

// PtyAPI 返回终端 PTY 薄门面；PTY 宿主未装配时返回 nil。
func (s *Server) PtyAPI() *ptyapi.Host { return s.ptyGate }

// SetScheduling 注入编制域服务（测试缝：整体替换单测构造的实例）。
func (s *Server) SetScheduling(svc *scheduling.Service) { s.scheduling = svc }

// SetHostAPI 注入进程承载门面（测试缝）。
func (s *Server) SetHostAPI(h *hostapi.Host) { s.hostAPI = h }

// SetKeystone 注入 keystone 域服务（测试缝：同上）。
func (s *Server) SetKeystone(svc *keystone.Service) { s.keystone = svc }

// SetRooms 注入协作房间服务（测试缝：整体替换 SetupAutomation 构造的实例）。
func (s *Server) SetRooms(svc *collab.Service) { s.rooms = svc }

// SetRebind 注入换绑端口（测试缝：同 SetRooms）。
func (s *Server) SetRebind(p rebindPort) { s.rebind = p }

// withRooms 守卫房间面端点：账本与会话服务未装配时 503（与 withLedger 同款降级）。
func (s *Server) withRooms(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.ledger == nil || s.rooms == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "账本/会话服务未装配",
			})
			return
		}
		h(w, r)
	}
}

// facadeBindAdapter 把账本薄门面的换绑能力适配成 gateway 的 rebindPort（岔口二
// 条件 4 机械判据：单一 Facade 字段、方法体只做转调、由组装点注入消费端口）。
// 定义在组装点文件（server.go = target.json assembly 登记点），它 →Facade 的
// 调用边走组装点豁免（graph check），不构成 gateway 自身引用门面。
type facadeBindAdapter struct {
	f *ledgerapi.Facade
}

func (a facadeBindAdapter) Rebind(id, toSession, carrier, expect string) error {
	return a.f.BindDriver(id, toSession, carrier, expect)
}

// Scheduling 返回编制域服务；未装配返回 nil（handler 据此降级 503，同 withLedger）。
func (s *Server) Scheduling() *scheduling.Service { return s.scheduling }

// Keystone 返回 keystone 域服务。
func (s *Server) Keystone() *keystone.Service { return s.keystone }

// facadeAsRegistry 把账本门面适配成编制域的持久化端口。错误哨兵按
// schedclient 的契约翻译；放在组装点是双向门面的法定形态——实现类只在
// 这里认识两边的具体类型。
type facadeAsRegistry struct {
	f *ledgerapi.Facade
}

func (a facadeAsRegistry) Put(kind, id string, expectVersion int, body []byte, actor string) (int, error) {
	v, err := a.f.Put(kind, id, expectVersion, body, actor)
	return v, translateRegistryErr(err)
}

func (a facadeAsRegistry) Get(kind, id string) (schedclient.Record, error) {
	e, err := a.f.Get(kind, id)
	if err != nil {
		return schedclient.Record{}, translateRegistryErr(err)
	}
	return schedclient.Record{ID: e.ID, Version: e.Version, Seq: e.Seq, Body: e.Body}, nil
}

func (a facadeAsRegistry) List(kind string) ([]schedclient.Record, error) {
	rows, err := a.f.List(kind)
	if err != nil {
		return nil, err
	}
	out := make([]schedclient.Record, 0, len(rows))
	for _, e := range rows {
		out = append(out, schedclient.Record{ID: e.ID, Version: e.Version, Seq: e.Seq, Body: e.Body})
	}
	return out, nil
}

func (a facadeAsRegistry) Delete(kind, id string, expectVersion int, actor string) error {
	return translateRegistryErr(a.f.Delete(kind, id, expectVersion, actor))
}

// translateRegistryErr 把账本门面的错误翻译成 schedclient 契约哨兵（NotFound/
// CASConflict）。代价声明（拍板记录，2026-08-26）：哨兵替换会丢底层报文，
// 诊断信息由调用方日志补。
func translateRegistryErr(err error) error {
	switch {
	case errors.Is(err, ledger.ErrNotFound):
		return schedclient.ErrNotFound
	case errors.Is(err, ledger.ErrCASConflict):
		// 计数与队列的 CAS 重试靠这个哨兵分流（schedclient 契约：适配器负责
		// 翻译底层同义错误）；漏翻译会让重试路径整体失效，冲突变成硬失败。
		return schedclient.ErrCASConflict
	default:
		return err
	}
}

// coordinatorRunner 把进程承载门面适配成 keystone 的会话承载缝。骨架期
// RunTurn 直通镜像转发，宿主实现落地后自动生效。
type coordinatorRunner struct {
	h *hostapi.Host
}

func (r coordinatorRunner) Launch(spec keysclient.SessionSpec, prompt string) (keysclient.TurnResult, error) {
	reply, err := r.h.RunTurn(context.Background(), hostapi.TurnRequest{
		CLI: spec.CLI, HomeDir: spec.HomeDir, Workdir: spec.Workdir,
		Model: spec.Model, Prompt: prompt, Env: spec.Env,
	})
	return keysclient.TurnResult{SessionID: reply.SessionID, Output: reply.Output}, err
}

func (r coordinatorRunner) Resume(ref keysclient.SessionRef, prompt string) (keysclient.TurnResult, error) {
	reply, err := r.h.RunTurn(context.Background(), hostapi.TurnRequest{
		CLI: ref.CLI, SessionID: ref.SessionID, Prompt: prompt,
	})
	return keysclient.TurnResult{SessionID: reply.SessionID, Output: reply.Output}, err
}

// roomNarrator 是叙事落点的房间实现：B156.2 房间制已落地，按 keysclient.Narrator
// 预告的换绑路径把协调者叙事从卡 note 迁到卡房间——薄里程碑指针行（仅系统组件
// 可书）。本路经 d_collab 入站门面的指针专用入口 Service.Pointer：kind=pointer
// 与 BySystem=true 由 Pointer 自己置，房间解析与只读判定也归 collab 执法；
// keystone 不感知差异。凡承重必须落账，通道不再是兜底通道。
//
// 当前实况（协调者复核，2026-08-26 更新）：Pointer 已由 C4 子卡填肉并入功能线
// （归属一度写作 C7，后改判给 C4），上一段描述的即是它今天的真实行为——房间解析
// 走 room.Resolve、只读/终态房返回 ErrReadOnly、kind 与 BySystem 由 Pointer 自置。
// 本路因此是 Service.Pointer 在仓内的**第一个上游消费方**。
//
// 连带一条给下游子卡的判据：Pointer 落账的 actor 是 collab 包内常量
// "system:pointer"，proto.RoomMessage 也没有字段记「哪个系统组件写的」，而签名
// 已冻结且不含 actor 参数。所以本路的指针行与 C7 的派发指针行在账本里只能靠正文
// 区分——针对指针行的断言一律写成存在式，不要写成计数式（「恰好一条」「行数 +1」），
// 否则两条上游都活着的时候会互相把对方变成偶发红。
type roomNarrator struct {
	c *collab.Service
}

func (n roomNarrator) Say(cardID, text string) error {
	_, err := n.c.Pointer(cardID, proto.RoomMessage{Body: text})
	return err
}

// attachLocator 是 attach 定位缝的骨架实现：命令形态按 CLI 拼装，终端 tab
// 本体仍由 PTY 域承载。实现票接 ptyapi 后在此补充存在性校验。
type attachLocator struct{}

func (attachLocator) Locate(ref keysclient.SessionRef, workdir string) (keysclient.AttachInfo, error) {
	if ref.SessionID == "" {
		return keysclient.AttachInfo{}, errors.New("该卡没有绑定的协调者会话")
	}
	return keysclient.AttachInfo{
		Machine: ref.Machine, Dir: workdir,
		Command: strings.TrimSpace(ref.CLI + " --session " + ref.SessionID),
	}, nil
}
