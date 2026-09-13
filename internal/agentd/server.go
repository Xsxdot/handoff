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
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	charterwebui "github.com/Xsxdot/charter/graph/webui"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/collab/cursor"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/opencode"
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

// PreviewOpener is the local desktop boundary for explicit preview open actions.
// Create/list/close never call it; Chromium is launched only after a local click.
type PreviewOpener interface {
	OpenPreview(ctx context.Context, id, machine string) (*proto.PreviewOpenResp, error)
	Stop(ctx context.Context) error
}

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
	mgr                   OrchestrationClient // 任务编排 client（B233.13：生产字段不再持有 *Manager），SetManager 注入
	providers             *executor.Registry
	ruleLoader            func(mainHome, cli string) ([]executor.ProfileFile, []executor.ProfileFile, error)
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
	// B233.14：编制域字段是使用方接口 SchedulingClient；具体 *scheduling.Service
	// 只在 cmd 组装点构造后经 SetScheduling 注入，本文件不再 new。
	scheduling SchedulingClient
	keystone   *keystone.Service
	autoLedger *ledgerapi.Facade
	ptyGate    *ptyapi.Host
	hostAPI    *hostapi.Host
	// B156.2 协作房间：入站门面实例与换绑端口，SetupAutomation 装配。
	rooms *collab.Service
	// coordLocks 串行化同一卡的 Launch→席位 CAS；每张卡独立，避免不同卡互相阻塞。
	coordLocksMu sync.Mutex
	coordLocks   map[string]*sync.Mutex
	// coordTabs 是控制台协调者 TUI tab：名额占到 tab 关掉，不是 Launch 返回就放。
	coordTabsMu sync.Mutex
	coordTabs   map[string]coordinatorLiveTab
	// openCoordTUI 打开控制台 TUI；测试可替换。nil 走生产 PTY。
	openCoordTUI func(card string, carrier scheduling.Carrier, spec keysclient.SessionSpec) (ptyID string, err error)
	// createRemoteCoordPty / closeRemoteCoordPty / lookupRemoteCoordWorkdir
	// 是远端协调者 TUI 的测试缝。nil 走 clientForTarget HTTP（B344）。
	createRemoteCoordPty     func(machine string, req proto.CreatePtySessionReq) (ptyID string, err error)
	closeRemoteCoordPty      func(machine, ptyID string) error
	lookupRemoteCoordWorkdir func(machine, card string) (string, error)
	// automationStartOnce/automationKick protect the single host automation loop.
	automationStartOnce   sync.Once
	automationKick        chan struct{}
	automationMu          sync.Mutex
	automationCursor      int64
	automationCursorStore *automationCursorStore
	automationSeen        map[int64]struct{}
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
	pool          *targetclient.Pool
	previewOwner  *PreviewOwner
	previewOpener PreviewOpener
	previewMirror *PreviewMirror
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
		coordLocks:              make(map[string]*sync.Mutex),
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

// coordinatorLock 返回一张卡的控制面串行锁。锁只覆盖控制段，不把不同卡的
// Launch 人为串成全局队列。
func (s *Server) coordinatorLock(card string) *sync.Mutex {
	s.coordLocksMu.Lock()
	defer s.coordLocksMu.Unlock()
	if s.coordLocks == nil {
		s.coordLocks = make(map[string]*sync.Mutex)
	}
	lock := s.coordLocks[card]
	if lock == nil {
		lock = &sync.Mutex{}
		s.coordLocks[card] = lock
	}
	return lock
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

// SetManager 把编排实现挂到 Server 上。
//
// B233.13：参数是 gateway 使用方定义的 OrchestrationClient，生产字段不再持有 *Manager。
// P5：接口里的 typed-nil（(*Manager)(nil) 赋给接口）会让 `s.mgr == nil` 失效，
// 这里显式识别并落 nil，保住 handler 的 503 未就绪判据。
// P1：活配置与工作区兜底注入已移交组装点（cmd/agentd.go），本方法只赋值 + 日志。
//
// 注意：
//   - 注入前三条路由返回 503（manager 未就绪），agentd bootstrap 顺序保证注入先于监听
func (s *Server) SetManager(m OrchestrationClient) {
	if m == nil {
		s.mgr = nil
		s.log.Warn("SetManager 收到 nil 编排实现，按未就绪处理", "error_kind", "manager_nil")
		return
	}
	if rv := reflect.ValueOf(m); rv.Kind() == reflect.Ptr && rv.IsNil() {
		s.mgr = nil
		s.log.Warn("SetManager 收到 typed-nil 编排实现，按未就绪处理", "error_kind", "manager_typed_nil")
		return
	}
	s.mgr = m
	s.log.Info("manager 已挂接，凭据为编排 client 接口")
}

// SetProviders 注入能力 Registry。
func (s *Server) SetProviders(r *executor.Registry) { s.providers = r }

// conf 返回当前配置快照。
//
// 返回的指针在调用方持有期间恒定：写入方永不原地修改 Config，只整体换新，
// 因此读者看到的始终是一份自洽的配置，而不是改到一半的状态。
func (s *Server) conf() *config.Config { return s.cfg.Load() }

// Conf 返回 Server 的活配置取值函数，供组装点注入编排实现（B233.13 P1-A）。
//
// 为什么需要：SetManager 不再持有 *Manager，活配置注入（B160 §4.2）改由组装点
// 显式接线；cmd 持有具体 *orchestration.Manager，用它把 Server 的活配置快照
// 交给 Manager。handler 不消费本方法。
func (s *Server) Conf() func() *config.Config { return s.conf }

// IsSelfTarget 判断登记名是否指向本 agentd。
//
// 空串是本机的规范身份；非空登记名只有在活配置中存在、且其直连地址经
// config.IsSelfTarget 判定为本机时才算本机。relay 与未知登记名都不是本机，
// 后者交给 target client 池保留既有的未登记错误。
func (s *Server) IsSelfTarget(name string) bool {
	if name == "" {
		return true
	}
	cfg := s.conf()
	if cfg == nil {
		return false
	}
	target, ok := cfg.Targets[name]
	return ok && config.IsSelfTarget(cfg.Listen, target)
}

// CanonicalTarget 把本机身份归一为空串，保留远端与未知名称原值。
//
// 返回值会写入派发请求、任务挂账与快照；调用方不应继续使用归一前的登记名
// 作为身份。空串与配置中指向本机的登记名共用本机 client。
func (s *Server) CanonicalTarget(name string) string {
	if s.IsSelfTarget(name) || scheduling.IsLocalMachine(name) {
		return ""
	}
	return name
}

// clientForTarget 按规范目标取得 agentd client。
//
// 空串或配置中指向本机的登记名返回本机直连 client；该 client 不由调用方关闭。
// 远端和未知名称交给 target client 池，池保留既有的配置校验与 relay 生命周期。
func (s *Server) clientForTarget(target string) (*client.Client, error) {
	canonical := s.CanonicalTarget(target)
	if canonical == "" {
		cfg := s.conf()
		if cfg == nil || cfg.Token == "" {
			err := fmt.Errorf("本机客户端缺少配置或 token")
			s.log.Error("取得本机客户端失败", "target", target,
				"canonical_target", canonical, "cause", err)
			return nil, err
		}
		s.log.Info("采用本机节点客户端", "target", target,
			"canonical_target", canonical)
		return targetclient.NewLocal(config.LocalDialAddr(cfg.Listen), cfg.Token), nil
	}
	s.log.Info("采用远端节点客户端", "target", target,
		"canonical_target", canonical)
	return s.pool.For(canonical)
}

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

// SetPreviewOwner injects the owner-side preview authority before serving HTTP.
func (s *Server) SetPreviewOwner(owner *PreviewOwner) { s.previewOwner = owner }

// SetPreviewOpener injects the explicit local desktop open boundary.
func (s *Server) SetPreviewOpener(opener PreviewOpener) { s.previewOpener = opener }

// SetPreviewMirror injects the independent coordinator preview projection.
func (s *Server) SetPreviewMirror(mirror *PreviewMirror) { s.previewMirror = mirror }

// StartPreviewServices restores persisted path previews and starts owner lifecycle work.
func (s *Server) StartPreviewServices(ctx context.Context) error {
	if s.previewOwner != nil {
		if err := s.previewOwner.Restore(ctx); err != nil {
			return err
		}
	}
	if s.previewMirror != nil {
		go s.previewMirror.Run(ctx)
	}
	if s.previewOwner == nil && s.previewMirror == nil {
		s.log.Info("preview owner 未配置，跳过启动", "operation", "preview_start")
	}
	return nil
}

// StopPreviewServices stops the local opener before owner persistence closes.
func (s *Server) StopPreviewServices(ctx context.Context) error {
	var stopErr error
	if s.previewOpener != nil {
		if err := s.previewOpener.Stop(ctx); err != nil {
			stopErr = errors.Join(stopErr, err)
			s.log.Warn("preview opener 收口失败，继续关闭其余服务", "operation", "preview_stop", "cause", err)
		}
	}
	if s.previewMirror != nil {
		s.previewMirror.Stop()
	}
	if s.previewOwner != nil {
		if err := s.previewOwner.Stop(ctx); err != nil {
			stopErr = errors.Join(stopErr, err)
		}
	}
	return stopErr
}

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
//   - GET/POST /api/gc                    终态缓存与残留 managed worktree 预览/执行
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
	api.HandleFunc("GET /api/gc", s.handleGC)
	api.HandleFunc("POST /api/gc", s.handleGC)
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
	api.HandleFunc("POST /api/previews", s.handlePreviewCreate)
	api.HandleFunc("GET /api/previews", s.handlePreviewList)
	api.HandleFunc("DELETE /api/previews/{id}", s.handlePreviewClose)
	api.HandleFunc("POST /api/previews/{id}/open", s.handlePreviewOpen)
	api.HandleFunc("POST /api/update", s.handleUpdate)
	api.HandleFunc("GET /api/update/latest", s.handleUpdateLatest)
	api.HandleFunc("POST /api/update/desktop/download", s.handleDesktopDownloadStart)
	api.HandleFunc("GET /api/update/desktop/download", s.handleDesktopDownloadState)
	api.HandleFunc("GET /ws/events", s.handleEvents)
	api.HandleFunc("GET /ws/pty", s.handlePtyWS)
	api.HandleFunc("GET /ws/previews", s.handlePreviewWS)
	api.HandleFunc("GET /ws/preview-raw", s.handlePreviewRawWS)
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
	embedded := webui.Embedded()
	if embedded {
		s.log.Info("控制台前端", "embedded", true)
	} else {
		s.log.Warn("控制台前端是 stub：请使用带 -tags embedweb 的发布构建",
			"embedded", false, "consequence", "当前控制台页面只是说明页")
	}
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
// B233.14：本函数不再构造编制域具体服务；它只装配账本门面（编制域具体服务由
// cmd 组装点 `scheduling.New` 构造后 `SetScheduling` 注入，经 SchedulingRegistry()
// 取账户门面适配）。其余 rooms/keystone/hostapi 装配原样保留。
//
// 骨架期语义：服务已构造、端口已绑定，行为由实现票逐缝点亮；宿主进程照常
// 启动，不受未点亮能力影响。
func (s *Server) SetupAutomation(st *ledger.Store) {
	facade := ledgerapi.New(st)
	s.autoLedger = facade
	cursorPath := filepath.Join(s.conf().DataDir, "automation-cursor.json")
	s.automationCursorStore = newAutomationCursorStore(cursorPath)
	loadedCursor, cursorErr := s.automationCursorStore.Load()
	if cursorErr != nil {
		s.log.Error("自动化 cursor 读取失败，以 0 启动", "path", cursorPath, "cause", cursorErr)
		loadedCursor = 0
	}
	s.automationMu.Lock()
	s.automationCursor = loadedCursor
	s.automationMu.Unlock()
	s.log.Info("自动化 cursor 已装配", "path", cursorPath, "loaded_seq", loadedCursor)
	s.rooms = collab.New(facade)
	s.rooms.SetCursorStore(cursor.New(filepath.Join(s.conf().DataDir, "room-cursors.json")))
	// 凭据相对路径表仍由 toolchain 唯一维护；组装点注入给 hostapi，避免
	// hostapi 反向 import maintenance 域或复制三家 CLI 的平台规则。
	s.hostAPI = hostapi.NewWithCredentialPathFor(toolchain.CredRelPathFor)
	supplier := coordinatorHomeSupplier{
		currentConfig:  s.conf,
		userHomeDir:    os.UserHomeDir,
		expandHomeDir:  hostapi.ExpandHomePath,
		credentialPath: toolchain.CredRelPathFor,
		loadRules:      s.ruleLoader,
		profileFor: func(cli string) (executor.Profile, error) {
			base := filepath.Base(cli)
			if s.providers == nil {
				return nil, executor.UnsupportedError(base, executor.CapProfile)
			}
			prov, err := s.providers.Get(base)
			if err != nil {
				return nil, err
			}
			if profile, ok := executor.ProfileFromProvider(prov); ok {
				return profile, nil
			}
			return nil, executor.UnsupportedError(base, executor.CapProfile)
		},
	}
	coord := opencode.NewCoordinator(s.hostAPI, slog.Default())
	runner := coordinatorRunner{coord: coord, reg: s.providers, prepareHome: supplier.Prepare}
	resolver := coordinatorSessionRefResolver{server: s, expandHomeDir: hostapi.ExpandHomePath}
	ks := keystone.New(runner, roomNarrator{c: s.rooms}, facade,
		attachLocator{expandHome: hostapi.ExpandHomePath})
	ks.SetSessionRefResolver(resolver)
	s.keystone = ks
	if s.pty != nil {
		s.ptyGate = ptyapi.New(s.pty)
	}
}

// RecoverOnStartup 先对账调度占用，再执行既有任务恢复；必须在 HTTP Serve 前调用。
// 对账失败时不进入 executor 恢复，调用方应让 agentd 启动失败并保留上下文。
func (s *Server) RecoverOnStartup(probe func(string) bool, sweep func(string), log *slog.Logger) error {
	if s.autoLedger == nil {
		return errors.New("启动恢复缺少自动化账本")
	}
	if log == nil {
		log = s.log
	}
	if log == nil {
		log = slog.Default()
	}
	if err := reconcileSchedRunning(s.st, facadeAsRegistry{f: s.autoLedger}, log); err != nil {
		return err
	}
	return RecoverOnStartup(s.st, s.hub, probe, sweep, log)
}

// PtyAPI 返回终端 PTY 薄门面；PTY 宿主未装配时返回 nil。
func (s *Server) PtyAPI() *ptyapi.Host { return s.ptyGate }

// SetScheduling 注入编制域实现（使用方接口）。
//
// B233.14：参数由具体 *scheduling.Service 改为 gateway 使用方接口 SchedulingClient；
// 生产 *scheduling.Service 只在 cmd 组装点 new。接口里的 typed-nil（(*scheduling.Service)(nil)
// 赋给接口）会让 `s.scheduling == nil` 失效，这里显式识别并落 nil，保住 11 处
// 未就绪 503 守卫（withScheduling / resolveReceiver / StartAutomation / handleDispatch
// 冻结分支 / releaseTaskCarrierOccupancy / coordapi.withCoordinator / scheddispatch / scheddrain）。
func (s *Server) SetScheduling(svc SchedulingClient) {
	if svc == nil {
		s.scheduling = nil
		s.log.Warn("SetScheduling 收到 nil 编制实现，按未就绪处理", "error_kind", "scheduling_nil")
		return
	}
	if rv := reflect.ValueOf(svc); rv.Kind() == reflect.Ptr && rv.IsNil() {
		s.scheduling = nil
		s.log.Warn("SetScheduling 收到 typed-nil 编制实现，按未就绪处理", "error_kind", "scheduling_typed_nil")
		return
	}
	s.scheduling = svc
	s.log.Info("编制域已挂接，凭据为使用方接口 SchedulingClient")
}

// SchedulingRegistry 返回账本门面适配成的编制域持久化端口，供 cmd 组装点构造
// *scheduling.Service（B233.14：scheduling.New 上移 cmd）。
//
// autoLedger 未装配时返回 nil；组装点必须显式判空拒启动——scheduling.New(nil)
// 会建出 repo 为 nil 的坏服务，首次调用才空指针。
func (s *Server) SchedulingRegistry() schedclient.Registry {
	if s.autoLedger == nil {
		return nil
	}
	return facadeAsRegistry{f: s.autoLedger}
}

// SetHostAPI 注入进程承载门面（测试缝）。
func (s *Server) SetHostAPI(h *hostapi.Host) { s.hostAPI = h }

// SetKeystone 注入 keystone 域服务（测试缝：同上）。
func (s *Server) SetKeystone(svc *keystone.Service) { s.keystone = svc }

// SetRooms 注入协作房间服务（测试缝：整体替换 SetupAutomation 构造的实例）。
func (s *Server) SetRooms(svc *collab.Service) { s.rooms = svc }

// SetRuleLoader 注入载体规则与技能装载函数。
func (s *Server) SetRuleLoader(fn func(mainHome, cli string) ([]executor.ProfileFile, []executor.ProfileFile, error)) {
	s.ruleLoader = fn
}

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

// Scheduling 返回编制域能力（使用方接口）；未装配返回 nil（handler 据此降级 503）。
// B233.14：返回类型由 *scheduling.Service 改为 SchedulingClient（测试缝；生产不消费）。
func (s *Server) Scheduling() SchedulingClient { return s.scheduling }

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

// coordinatorRunner 把进程承载门面适配成 keystone 的会话承载缝。
// Launch/Resume 前通过 prepareHome 按白名单供给隔离 HOME；
// 供给失败在 child 启动前返回，绝不静默退回旧路径。
type coordinatorRunner struct {
	coord       executor.Coordinator
	reg         *executor.Registry
	prepareHome func(keysclient.SessionSpec) (string, error)
}

func (r coordinatorRunner) Launch(spec keysclient.SessionSpec, prompt string) (keysclient.TurnResult, error) {
	name := filepath.Base(spec.CLI)
	if r.reg != nil {
		if err := r.reg.Require(name, executor.CapCoordination); err != nil {
			slog.Default().Error("协调能力不支持，拒绝 Launch", "cli", spec.CLI, "cause", err)
			return keysclient.TurnResult{}, err
		}
	}
	if r.prepareHome != nil {
		prepared, err := r.prepareHome(spec)
		if err != nil {
			slog.Default().Error("协调者 Launch 准备 HOME 失败", "cli", spec.CLI,
				"home_dir", spec.HomeDir, "workdir", spec.Workdir, "cause", err)
			return keysclient.TurnResult{}, err
		}
		spec.HomeDir = prepared
	}
	if r.coord == nil {
		return keysclient.TurnResult{}, executor.UnsupportedError(name, executor.CapCoordination)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got, err := r.coord.Launch(ctx, executor.CoordSessionSpec{
		CLI: spec.CLI, HomeDir: spec.HomeDir, Model: spec.Model, Workdir: spec.Workdir, Env: spec.Env,
	}, prompt)
	if err != nil {
		return keysclient.TurnResult{}, err
	}
	return keysclient.TurnResult{SessionID: got.SessionID, Output: got.Output}, nil
}

func (r coordinatorRunner) Resume(ref keysclient.SessionRef, prompt string) (keysclient.TurnResult, error) {
	name := filepath.Base(ref.CLI)
	if r.reg != nil {
		if err := r.reg.Require(name, executor.CapCoordination); err != nil {
			slog.Default().Error("协调能力不支持，拒绝 Resume", "cli", ref.CLI, "cause", err)
			return keysclient.TurnResult{}, err
		}
	}
	if r.prepareHome != nil {
		spec := keysclient.SessionSpec{
			CLI: ref.CLI, HomeDir: ref.HomeDir, Model: ref.Model, Workdir: ref.Workdir,
		}
		prepared, err := r.prepareHome(spec)
		if err != nil {
			slog.Default().Error("协调者 Resume 准备 HOME 失败", "cli", ref.CLI,
				"home_dir", ref.HomeDir, "workdir", ref.Workdir, "cause", err)
			return keysclient.TurnResult{}, err
		}
		ref.HomeDir = prepared
	}
	if r.coord == nil {
		return keysclient.TurnResult{}, executor.UnsupportedError(name, executor.CapCoordination)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got, err := r.coord.Resume(ctx, executor.CoordSessionRef{
		CLI: ref.CLI, SessionID: ref.SessionID, HomeDir: ref.HomeDir,
		Workdir: ref.Workdir, Model: ref.Model,
	}, prompt)
	if err != nil {
		return keysclient.TurnResult{}, err
	}
	return keysclient.TurnResult{SessionID: got.SessionID, Output: got.Output}, nil
}

// resumeTurnRequest 把 SessionRef 上的续接环境映射进 RunTurn。HOME 空时
// 子进程继承 agentd 默认 HOME，隔离会话必然找不到——调用方必须带上。
func resumeTurnRequest(ref keysclient.SessionRef, prompt string) hostapi.TurnRequest {
	if ref.HomeDir == "" {
		slog.Default().Warn("协调者续接未带隔离 HOME，将继承 agentd 默认 HOME",
			"component", "agentd", "has_cli", ref.CLI != "", "has_session", ref.SessionID != "")
	}
	return hostapi.TurnRequest{
		CLI: ref.CLI, SessionID: ref.SessionID, Prompt: prompt,
		HomeDir: ref.HomeDir, Workdir: ref.Workdir, Model: ref.Model,
		Env: []string{
			"HANDOFF_SESSION_CLI=" + ref.CLI,
			"HANDOFF_SESSION_ID=" + ref.SessionID,
		},
	}
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

// attachLocator 是 attach 定位缝的实现：命令形态按 CLI 拼装，包含展开后的绝对 HOME 路径。
type attachLocator struct {
	expandHome func(string) (string, error)
}

func (l attachLocator) Locate(ref keysclient.SessionRef, workdir string) (keysclient.AttachInfo, error) {
	if ref.SessionID == "" {
		return keysclient.AttachInfo{}, errors.New("该卡没有绑定的协调者会话")
	}
	if strings.TrimSpace(ref.HomeDir) == "" {
		return keysclient.AttachInfo{}, errors.New("协调者 attach 缺少 HomeDir")
	}
	expand := l.expandHome
	if expand == nil {
		expand = hostapi.ExpandHomePath
	}
	expandedHome, err := expand(ref.HomeDir)
	if err != nil {
		return keysclient.AttachInfo{}, fmt.Errorf("展开协调者 attach HOME %q: %w", ref.HomeDir, err)
	}
	if !filepath.IsAbs(expandedHome) {
		return keysclient.AttachInfo{}, fmt.Errorf("协调者 attach HOME 不是绝对路径: %q", expandedHome)
	}
	return keysclient.AttachInfo{
		Machine: ref.Machine, Dir: workdir,
		Command: fmt.Sprintf("HOME=%s %s --session %s", shellQuote(expandedHome), shellQuote(ref.CLI), shellQuote(ref.SessionID)),
	}, nil
}
