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
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/proxycfg"
	"github.com/Xsxdot/handoff/internal/release"
	"github.com/Xsxdot/handoff/internal/store"
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
// 并发安全：所有字段只读（构造后不变），hub 自身线程安全，无需额外加锁。
type Server struct {
	cfg *config.Config
	st  *store.Store
	hub *Hub
	log *slog.Logger
	mgr *Manager // 任务状态机中枢（dispatch/continue/done 三条路由的落点），SetManager 注入
	// startedAt 是本 agentd 的启动时刻，status 用它换算 uptime。
	// 在 NewServer 里记录而非从 bootstrap 传入：NewServer 只在 bootstrap 调用
	// 一次，语义等价，且不必改动它的签名与全部测试调用点。
	startedAt time.Time
	// replayLimit / liveLimit 是 eventReplayLimit / liveBufferLimit 的实例副本，
	// 供测试注入小阈值复现「重放截断」「缓冲越限」两条边界路径（生产恒为默认值）。
	replayLimit int
	liveLimit   int
	// upd 是换版接口的外部依赖，NewServer 填生产实现，测试整体替换
	upd UpdateDeps
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
	restart func(reason string) bool
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
	s := &Server{
		cfg:         cfg,
		st:          st,
		hub:         NewHub(),
		log:         log,
		startedAt:   time.Now(),
		replayLimit: eventReplayLimit,
		liveLimit:   liveBufferLimit,
		pull:        newPullTracker(),
	}
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

// SetManager 注入任务管理器，激活 dispatch/continue/done 三条路由。
//
// 注意：
//   - manager 依赖本服务内部的 hub 与外部 adapter，必须在 NewServer 之后构造并注入
//   - 注入前三条路由返回 503（manager 未就绪），agentd bootstrap 顺序保证注入先于监听
func (s *Server) SetManager(m *Manager) {
	s.mgr = m
}

// Handler 返回带 Bearer 鉴权中间件的完整路由，便于 httptest 直接挂载。
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
//   - GET  /api/tasks/{id}/file         读任务仓库内文件（审阅上下文）
//   - POST /api/tasks/{id}/run          在任务仓库执行审阅命令（跑测试/lint）
//   - POST /api/projects               登记项目（必要时先克隆）
//   - GET  /api/projects               列出项目位置（含现场实际状态）
//   - DELETE /api/projects/{name}      注销项目位置（只删登记，不动磁盘）
//   - GET  /ws/events                   事件流（补发 + 实时）
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/footprint", s.handleFootprint)
	mux.HandleFunc("GET /api/reclaim", s.handleReclaimList)
	mux.HandleFunc("GET /api/tasks", s.handleListTasks)
	mux.HandleFunc("POST /api/tasks", s.handleDispatch)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleGetTask)
	mux.HandleFunc("POST /api/tasks/{id}/reply", s.handleReply)
	mux.HandleFunc("POST /api/tasks/{id}/continue", s.handleContinue)
	mux.HandleFunc("POST /api/tasks/{id}/done", s.handleDone)
	mux.HandleFunc("POST /api/tasks/{id}/stop", s.handleStop)
	mux.HandleFunc("POST /api/tasks/{id}/reclaim", s.handleReclaim)
	mux.HandleFunc("POST /api/tasks/{id}/resume", s.handleResume)
	mux.HandleFunc("GET /api/tasks/{id}/diff", s.handleTaskDiff)
	mux.HandleFunc("GET /api/tasks/{id}/render", s.handleTaskRender)
	mux.HandleFunc("GET /api/tasks/{id}/file", s.handleTaskFile)
	mux.HandleFunc("POST /api/tasks/{id}/run", s.handleTaskRun)
	mux.HandleFunc("POST /api/projects", s.handleProjectAdd)
	mux.HandleFunc("GET /api/projects", s.handleProjectList)
	mux.HandleFunc("DELETE /api/projects/{name}", s.handleProjectRemove)
	mux.HandleFunc("POST /api/update", s.handleUpdate)
	mux.HandleFunc("GET /ws/events", s.handleEvents)
	return s.auth(mux)
}

// auth 是 Bearer token 鉴权中间件，包住全部路由。
//
// 鉴权失败（无 token / token 不匹配）统一返回 401，并打 Warn 记录来源地址——
// 这是排查「谁在扫本地端口」与「配对端 token 未同步」的第一线索。
//
// 为什么这里做空 token 拒绝（L-2）：subtle.ConstantTimeCompare("","")==1，
// 配置 token 为空时空 token 请求会通过鉴权——今天只因 net/http 的
// textproto 行解析会掐掉 "Bearer " 后的空格才 401，属于「碰巧被别的层拦住」
// 的隐性 fail-open。config.Load 正常都会生成 token，但手写配置可能漏掉；
// 在鉴权边界 fail-closed：cfg.Token 为空 → 拒绝一切请求并打 Error，提示
// 配置问题。选在这里而非 NewServer/启动时：这是 fail-open 真正发生的边界，
// 一个位置同时覆盖 HTTP 与 WS 全路由，任何嵌入方（含测试）都逃不掉。
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token == "" {
			s.log.Error("token 未配置，拒绝一切请求（fail-closed）：请在配置中设置 token 后重启 agentd",
				"remote_addr", r.RemoteAddr, "method", r.Method, "path", r.URL.Path)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未授权"})
			return
		}
		token, ok := bearerToken(r)
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Token)) != 1 {
			s.log.Warn("鉴权失败", "remote_addr", r.RemoteAddr, "method", r.Method, "path", r.URL.Path)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未授权"})
			return
		}
		next.ServeHTTP(w, r)
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
	views := make([]proto.TaskView, 0, len(tasks))
	unattended := 0
	for _, t := range tasks {
		w := s.hub.Watchers(t.ID)
		if w == 0 && !isTerminalState(t.State) && t.State != proto.TaskStateWaitingReview {
			unattended++
		}
		views = append(views, proto.TaskView{Task: t, Watchers: w})
	}
	s.log.Info("任务列表完成", "tasks", len(views), "unattended", unattended)
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
	Executor    string `json:"executor"`
	Model       string `json:"model"`
	Branch      string `json:"branch"`
	// NewBranch/NewWorktree 用 snake_case 新键，与 CLI flag 语义一一对应。
	NewBranch   string `json:"new_branch"`
	Base        string `json:"base"`
	Worktree    string `json:"worktree"`
	NewWorktree bool   `json:"new_worktree"`
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
		Prompt: req.Prompt, Name: req.Name, Executor: req.Executor, Model: req.Model,
		Branch: req.Branch, NewBranch: req.NewBranch, Base: req.Base,
		Worktree: req.Worktree, NewWorktree: req.NewWorktree, BaseCommit: req.BaseCommit,
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
	default:
		s.log.Error("派发任务失败", "project", projectRef, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "派发任务失败"})
	}
}

// projectAddRequest 是 POST /api/projects 的请求体。
//
// 两种形态由 path 是否为空决定：给了 path 就是「这台机器上已经有一份，用它」
// （agentd 会现读它的 origin 校验一致）；没给就是「你自己 clone 到 repo_root/<name>」。
type projectAddRequest struct {
	OriginURL string `json:"origin_url"`
	Name      string `json:"name"`
	Path      string `json:"path"`
}

// handleProjectAdd 登记一个项目（必要时先克隆）。
func (s *Server) handleProjectAdd(w http.ResponseWriter, r *http.Request) {
	s.log.Info("project add 请求", "method", r.Method, "path", r.URL.Path)
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

// taskRepoOrErr 读取路径中的任务并返回其工作区目录；任务不存在（404）或没有
// 工作区（400）时已写响应并返回 ok=false。
//
// 返回的是 task.Workdir() 而非 task.RepoPath（为什么 diff/fetch/run 必须在
// Workdir 而非主仓库：worktree 任务的 executor cwd 与分支 HEAD 都在 Workdir，
// 主仓库的 HEAD 停在派发前的位置——diff 相对基准、fetch 看工作区文件、run 跑
// 测试都必须落在 executor 真正干活的目录，否则审阅的是错误的代码状态）。
//
// 供 diff/fetch/run 三条审阅路由共用——它们只关心任务指向的仓库，不依赖状态机。
func (s *Server) taskRepoOrErr(w http.ResponseWriter, taskID string) (repo string, ok bool) {
	task, err := s.st.GetTask(taskID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("任务不存在", "task", taskID)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "任务不存在"})
		} else {
			s.log.Error("读取任务失败", "task", taskID, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		}
		return "", false
	}
	workdir := task.Workdir()
	if workdir == "" {
		s.log.Warn("任务缺少工作区路径", "task", taskID)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "任务没有工作区路径"})
		return "", false
	}
	return workdir, true
}

// handleTaskDiff 返回任务分支相对基准分支的审阅素材（git diff + 提交列表）。
//
// 参数：
//   - base: 查询参数，基准分支名；缺省时按仓库默认分支推导（resolveBaseBranch）
//
// 注意：
//   - diff 是协调者主动发起的只读审阅，不做状态门禁——running 中即可看实时进度
func (s *Server) handleTaskDiff(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	s.log.Info("diff 请求", "method", r.Method, "path", r.URL.Path, "task", taskID)
	repo, ok := s.taskRepoOrErr(w, taskID)
	if !ok {
		return
	}
	base := r.URL.Query().Get("base")
	if base == "" {
		base = resolveBaseBranch(repo)
	}
	if base == "" {
		s.log.Warn("无法确定基准分支", "task", taskID, "repo", repo)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无法确定基准分支，请用 base 参数指定"})
		return
	}
	diff, err := Diff(repo, base)
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
	writeJSON(w, http.StatusOK, map[string]string{"diff": diff})
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
	content, err := ReadFile(repo, rel)
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
		if errors.Is(err, ErrNoProcHeadroom) {
			s.log.Warn("run 被拒：进程余量不足", "task", taskID, "cmd", truncateRunes(req.Cmd, 200), "cause", err)
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
	if _, err := s.st.GetTask(taskID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("WS 订阅任务不存在", "task", taskID, "remote_addr", r.RemoteAddr)
			if cerr := conn.Close(websocket.StatusPolicyViolation, "task not found"); cerr != nil {
				// 连接已断时 Close 失败不影响结论——客户端侧按断线走退避重连，
				// 若再次拨号仍会走到本分支被关闭
				s.log.Warn("WS 关闭任务不存在连接失败", "task", taskID, "err", cerr)
			}
			return
		}
		s.log.Error("WS 校验任务失败", "task", taskID, "cause", err)
		return
	}

	sent := 0
	defer func() {
		s.log.Info("WS 连接断开", "task", taskID, "from_seq", fromSeq, "sent", sent)
	}()

	// 连接关闭（含对端断开）时该 ctx 取消，作为写循环退出信号
	ctx := conn.CloseRead(r.Context())

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
	replays, err := s.st.EventsFromAsc(taskID, fromSeq, s.replayLimit)
	if err != nil {
		s.log.Error("WS 补发历史事件失败", "task", taskID, "from_seq", fromSeq, "cause", err)
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
		case gapTotal > deliveredInGap:
			s.log.Warn("WS 补发窗口截断且缺口未由实时流补齐", "task", taskID, "from_seq", fromSeq,
				"replayed", len(replays), "gap_total", gapTotal, "gap_delivered", deliveredInGap,
				"store_max", storeMax)
		default:
			s.log.Debug("WS 补发窗口截断但缺口已由实时流补齐", "task", taskID,
				"replayed", len(replays), "gap_total", gapTotal, "store_max", storeMax)
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
