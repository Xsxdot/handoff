// probe.go —— 本机隔离 HOME 的路径探测与检测回合（B293 探测 + B295 检测）。
//
// 职责：问本机某路径相对某 CLI 是空 / 已登录 / 非空无凭据；以及用该 HOME
// 检测可用性。名单内 CLI 走 RunTurn 发固定短消息；名单外只查命令是否可执行
//（B342：不把未实装当成检测失败的唯一结局）。只暴露本机能力。
//
// 边界：不写编制域状态（那是 scheduling.ApplyDetect）；不绑卡、不经 keystone、
// 不进派发状态机；不在控制台拉登录 TUI。跨机由 gateway 经 ?machine= 转发。
package hostapi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultDetectTimeout 是 WakeRequest.Timeout 为 0 时的缺省上界。
// B295 改为 3 分钟：真发一条消息，30s 经常回不来；仍远短于 DefaultTurnTimeout。
const DefaultDetectTimeout = 3 * time.Minute

// DetectPrompt 是检测回合的固定短消息。成功看回合有没有输出，不匹配回复原文。
const DetectPrompt = "ping"

// userHomeDir 是目标 Host 进程的 HOME 解析缝；测试替换它以证明 ~ 不由协调机
// 预先展开。生产实现使用 os.UserHomeDir。
var userHomeDir = os.UserHomeDir

// detectTurn 是检测回合入口。生产走 RunTurn；测试替换以锁 Timeout=0 的默认
// 上界和 Workdir=隔离 HOME，避免假 CLI 睡眠三分钟。
var detectTurn = func(h *Host, ctx context.Context, req TurnRequest) (TurnReply, error) {
	return h.RunTurn(ctx, req)
}

// ProbeKind 是路径探测的三类结果（与设置页提示一一对应）。
type ProbeKind string

const (
	ProbeEmpty    ProbeKind = "empty"     // 不存在或目录无任何条目
	ProbeLoggedIn ProbeKind = "logged_in" // 可见该 CLI 凭据
	ProbeOccupied ProbeKind = "occupied"  // 非空且未见该 CLI 凭据
)

// ProbeRequest 描述一次只读探测。Credential 取载体词表（standalone /
// main_home_sync）；空 = standalone。值不得当凭据明文使用。
type ProbeRequest struct {
	Path       string
	CLI        string
	Credential string
}

// ProbeReply 是探测结果。Kind 是冻结三值；Detail 给人看，不参与准入。
type ProbeReply struct {
	Kind   ProbeKind
	Detail string
}

// WakeOutcome 是一次唤起在本机观察到的结局，供编制域套四态表。
type WakeOutcome string

const (
	WakeReady       WakeOutcome = "ready"
	WakeNeedLogin   WakeOutcome = "need_login"
	WakeQuota       WakeOutcome = "quota"
	WakeUnreachable WakeOutcome = "unreachable"
)

// WakeRequest 描述一次有时限的本机唤起。Credential 取载体词表（standalone /
// main_home_sync）；空 = standalone。Timeout=0 使用 DefaultDetectTimeout。
type WakeRequest struct {
	CLI        string
	HomeDir    string
	Credential string
	Model      string
	Timeout    time.Duration
}

// WakeReply 是唤起结局。Detail 给人看，原样交给 ApplyDetect 的 last_error。
type WakeReply struct {
	Outcome WakeOutcome
	Detail  string
}

// ProbeHome 只读探测目标机路径，先在目标 Host 进程展开 ~。它不创建、删除或
// 改写文件；Credential 只选择凭据路径表，不是凭据内容。返回 empty、logged_in
// 或 occupied，失败返回带目标路径上下文的错误。
func (h *Host) ProbeHome(ctx context.Context, req ProbeRequest) (ProbeReply, error) {
	started := time.Now()
	log().Info("开始探测载体 HOME", "cli", req.CLI, "path", req.Path,
		"main_home_sync", req.Credential == "main_home_sync")
	state, err := h.inspectHome(ctx, req)
	if err != nil {
		log().Error("探测载体 HOME 失败", "cli", req.CLI, "path", req.Path,
			"elapsed", time.Since(started).String(), "cause", err)
		return ProbeReply{}, err
	}
	log().Info("探测载体 HOME 完成", "cli", req.CLI, "path", state.path,
		"kind", state.reply.Kind, "elapsed", time.Since(started).String())
	return state.reply, nil
}

// WakeHome 用目标机展开后的隔离 HOME 跑一次检测。只在目标目录本身 empty
// 且 Credential 为 main_home_sync 时先拷表内凭据。名单内再经 RunTurn 发
// DetectPrompt；名单外只查命令（B342）。Timeout=0 使用 DefaultDetectTimeout。
// 不进控制台登录 TUI。
func (h *Host) WakeHome(ctx context.Context, req WakeRequest) (WakeReply, error) {
	started := time.Now()
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultDetectTimeout
	}
	req.Timeout = timeout
	log().Info("开始检测载体 HOME", "cli", req.CLI,
		"main_home_sync", req.Credential == "main_home_sync", "timeout", timeout.String())
	state, err := h.inspectHome(ctx, ProbeRequest{
		Path: req.HomeDir, CLI: req.CLI, Credential: req.Credential,
	})
	if err != nil {
		log().Error("检测前探测失败", "cli", req.CLI, "path", req.HomeDir,
			"elapsed", time.Since(started).String(), "cause", err)
		return WakeReply{}, err
	}
	if state.targetEmpty && req.Credential == "main_home_sync" {
		if _, err := h.copyMainCredential(ctx, state.path, req.CLI); err != nil {
			log().Error("主 HOME 凭据供给失败", "cli", req.CLI, "path", state.path,
				"elapsed", time.Since(started).String(), "cause", err)
			return WakeReply{}, err
		}
	}
	if err := os.MkdirAll(state.path, 0o700); err != nil {
		log().Error("创建载体 HOME 失败", "cli", req.CLI, "path", state.path, "cause", err)
		return WakeReply{}, fmt.Errorf("hostapi: 创建目标 HOME %q: %w", state.path, err)
	}

	reply, err := h.runWake(ctx, req, state.path)
	if err != nil {
		log().Error("检测载体 HOME 失败", "cli", req.CLI, "path", state.path,
			"elapsed", time.Since(started).String(), "cause", err)
		return WakeReply{}, err
	}
	log().Info("检测载体 HOME 完成", "cli", req.CLI, "path", state.path,
		"outcome", reply.Outcome, "elapsed", time.Since(started).String())
	return reply, nil
}

// homeProbeState 同时保留「目标目录本身是否为空」与对外三态。两者不能合并：
// main_home_sync 的空目标在主 HOME 有凭据时对外是 logged_in，但 WakeHome 仍须
// 依据目标目录为空决定是否执行一次性供给。
type homeProbeState struct {
	path        string
	targetEmpty bool
	reply       ProbeReply
}

// inspectHome 执行 ProbeHome 的共享只读判定，并返回 WakeHome 所需的目标空态。
func (h *Host) inspectHome(ctx context.Context, req ProbeRequest) (homeProbeState, error) {
	if err := ctx.Err(); err != nil {
		return homeProbeState{}, fmt.Errorf("hostapi: 探测 HOME 被取消: %w", err)
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		home, err := userHomeDir()
		if err != nil {
			return homeProbeState{}, fmt.Errorf("hostapi: 空 HOME 回落到主 HOME 失败: %w", err)
		}
		if home == "" {
			return homeProbeState{}, fmt.Errorf("hostapi: 空 HOME 回落到主 HOME 失败: 主 HOME 为空")
		}
		log().Info("空 HOME 按主 HOME 探测", "cli", req.CLI)
		path = filepath.Clean(home)
	} else {
		expanded, err := ExpandHomePath(req.Path)
		if err != nil {
			return homeProbeState{}, err
		}
		path = expanded
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			state := homeProbeState{path: path, targetEmpty: true,
				reply: ProbeReply{Kind: ProbeEmpty, Detail: "目标 HOME 不存在"}}
			return h.applyMainHomeSync(ctx, req, state)
		}
		return homeProbeState{}, fmt.Errorf("hostapi: stat 目标 HOME %q: %w", path, err)
	}
	if !info.IsDir() {
		return homeProbeState{}, fmt.Errorf("hostapi: 目标 HOME %q 不是目录", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			state := homeProbeState{path: path, targetEmpty: true,
				reply: ProbeReply{Kind: ProbeEmpty, Detail: "目标 HOME 不存在"}}
			return h.applyMainHomeSync(ctx, req, state)
		}
		return homeProbeState{}, fmt.Errorf("hostapi: 读取目标 HOME %q: %w", path, err)
	}
	if len(entries) == 0 {
		state := homeProbeState{path: path, targetEmpty: true,
			reply: ProbeReply{Kind: ProbeEmpty, Detail: "目标 HOME 为空"}}
		return h.applyMainHomeSync(ctx, req, state)
	}
	rel, ok := h.resolveCredentialPath(req.CLI)
	if !ok {
		return homeProbeState{path: path, reply: ProbeReply{
			Kind: ProbeOccupied, Detail: "目录非空，未提供该 CLI 的文件判据"}}, nil
	}
	credPath := filepath.Join(path, rel)
	if _, err := os.Stat(credPath); err == nil {
		return homeProbeState{path: path, reply: ProbeReply{
			Kind: ProbeLoggedIn, Detail: "目录中发现该 CLI 凭据"}}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return homeProbeState{}, fmt.Errorf("hostapi: stat 凭据路径 %q: %w", credPath, err)
	}
	return homeProbeState{path: path, reply: ProbeReply{
		Kind: ProbeOccupied, Detail: "目录非空，未发现该 CLI 凭据"}}, nil
}

// applyMainHomeSync 只对目标为空态追加主 HOME 的只读对照，不改变目标目录。
func (h *Host) applyMainHomeSync(ctx context.Context, req ProbeRequest, state homeProbeState) (homeProbeState, error) {
	if req.Credential != "main_home_sync" {
		return state, nil
	}
	loggedIn, err := h.mainHomeHasCredential(ctx, req.CLI)
	if err != nil {
		return homeProbeState{}, err
	}
	if loggedIn {
		state.reply = ProbeReply{Kind: ProbeLoggedIn, Detail: "主 HOME 有该 CLI 凭据"}
	}
	return state, nil
}

// ExpandHomePath 只展开当前用户的 ~、~/ 与 ~\ 前缀为目标机绝对路径；
// 其他相对路径按目标 Host 的当前工作目录解释，绝不使用协调机预先传来的 HOME。
// path 为空时返回错误；展开后通过 filepath.Clean 返回规范路径。
func ExpandHomePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("hostapi: 目标 HOME 路径不能为空")
	}
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("hostapi: 展开目标 HOME %q: %w", path, err)
		}
		path = filepath.Join(home, strings.TrimLeft(path[1:], `/\`))
	}
	return filepath.Clean(path), nil
}

// resolveCredentialPath 通过组装点注入的既有凭据表解析相对路径。
func (h *Host) resolveCredentialPath(cli string) (string, bool) {
	if h.resolveCredential == nil {
		return "", false
	}
	rel, ok := h.resolveCredential(filepath.Base(cli))
	if !ok || rel == "" || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

// mainHomeHasCredential 只在目标目录为空且请求明确 main_home_sync 时读取主 HOME。
func (h *Host) mainHomeHasCredential(ctx context.Context, cli string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("hostapi: 读取主 HOME 凭据被取消: %w", err)
	}
	rel, ok := h.resolveCredentialPath(cli)
	if !ok {
		return false, nil
	}
	home, err := userHomeDir()
	if err != nil {
		return false, fmt.Errorf("hostapi: 获取主 HOME: %w", err)
	}
	if _, err := os.Stat(filepath.Join(home, rel)); err == nil {
		return true, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else {
		return false, fmt.Errorf("hostapi: stat 主 HOME 凭据 %q: %w", filepath.Join(home, rel), err)
	}
}

// copyMainCredential 把单个表内凭据从主 HOME 复制到目标 HOME；调用者已证明目标
// 目录为空。权限沿用源文件，且只创建该文件需要的父目录，不搬运技能/规则树。
func (h *Host) copyMainCredential(ctx context.Context, targetHome, cli string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("hostapi: 供给凭据被取消: %w", err)
	}
	rel, ok := h.resolveCredentialPath(cli)
	if !ok {
		return false, nil
	}
	mainHome, err := userHomeDir()
	if err != nil {
		return false, fmt.Errorf("hostapi: 获取主 HOME: %w", err)
	}
	source := filepath.Join(mainHome, rel)
	info, err := os.Stat(source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("hostapi: stat 主 HOME 凭据 %q: %w", source, err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return false, fmt.Errorf("hostapi: 读取主 HOME 凭据 %q: %w", source, err)
	}
	destination := filepath.Join(targetHome, rel)
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return false, fmt.Errorf("hostapi: 创建凭据父目录 %q: %w", filepath.Dir(destination), err)
	}
	if err := os.WriteFile(destination, data, info.Mode().Perm()); err != nil {
		return false, fmt.Errorf("hostapi: 写入隔离凭据 %q: %w", destination, err)
	}
	if err := os.Chmod(destination, info.Mode().Perm()); err != nil {
		return false, fmt.Errorf("hostapi: 设置隔离凭据权限 %q: %w", destination, err)
	}
	log().Info("主 HOME 凭据供给完成", "cli", cli, "target", targetHome)
	return true, nil
}

// runWake 用隔离 HOME 走检测。名单内 CLI 发 DetectPrompt；名单外只查同名
// 命令在不在——找不到才 unreachable，找到即 ready，不调用 RunTurn（B342）。
// 凭据文件与 --version 不参与成败；未知错误映射为 WakeReply，好让 detect
// 编排能 ApplyDetect。
func (h *Host) runWake(ctx context.Context, req WakeRequest, targetHome string) (WakeReply, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultDetectTimeout
	}
	cli := filepath.Base(req.CLI)
	if !supportedCLIs[cli] {
		bin, err := lookPathWithFallback(req.CLI)
		if err != nil {
			log().Warn("未实装 CLI 检测：找不到命令", "cli", cli, "target", targetHome, "cause", err)
			return WakeReply{
				Outcome: WakeUnreachable,
				Detail:  fmt.Sprintf("找不到载体命令 %q: %v", cli, err),
			}, nil
		}
		log().Info("未实装 CLI 检测：命令可执行，跳过检测回合",
			"cli", cli, "bin", bin, "target", targetHome)
		return WakeReply{Outcome: WakeReady, Detail: "命令可执行（该 CLI 尚未实装检测回合）"}, nil
	}
	log().Info("开始检测回合", "cli", cli, "target", targetHome, "timeout", timeout.String())
	// Workdir 与 HOME 同指隔离目录：检测没有项目仓库，相对路径不得落到 agentd cwd。
	reply, err := detectTurn(h, ctx, TurnRequest{
		CLI: req.CLI, HomeDir: targetHome, Workdir: targetHome, Model: req.Model,
		Prompt: DetectPrompt, Timeout: timeout, Env: wakeProxyEnv(cli),
	})
	if err != nil {
		mapped := classifyTurnError(err)
		// 不把 RunTurn 的 stderr 尾部打进 slog：里面可能有凭据。结局进 outcome。
		log().Warn("检测回合失败", "cli", cli, "target", targetHome, "outcome", mapped.Outcome)
		return mapped, nil
	}
	if strings.TrimSpace(reply.Output) == "" {
		log().Warn("检测回合无输出", "cli", cli, "session_id", reply.SessionID)
		return WakeReply{Outcome: WakeUnreachable, Detail: "回合无输出"}, nil
	}
	log().Info("检测回合成功", "cli", cli, "session_id", reply.SessionID,
		"output_bytes", len(reply.Output))
	return WakeReply{Outcome: WakeReady, Detail: "回合成功"}, nil
}

// wakeProxyEnv 为检测回合注入代理环境，确保 launchd 最小 PATH 下的 agentd 仍能联网。
// 优先使用进程环境已有的代理，缺失时尝试读取 ~/.handoff/env 下的常见 env 文件。
func wakeProxyEnv(cli string) []string {
	keys := []string{"https_proxy", "http_proxy", "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY", "no_proxy"}
	var out []string
	seen := map[string]bool{}
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			out = append(out, k+"="+v)
			seen[k] = true
		}
	}
	// 若进程环境没有代理，尝试从 env 文件补齐（兼容历史配置分散）。
	if len(out) == 0 {
		candidates := []string{"opencode.env", "proxy.env"}
		home, _ := os.UserHomeDir()
		for _, name := range candidates {
			path := filepath.Join(home, ".handoff", "env", name)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
					continue
				}
				k, v, _ := strings.Cut(line, "=")
				k = strings.TrimSpace(k)
				v = strings.TrimSpace(v)
				if k == "" || v == "" || seen[k] {
					continue
				}
				// 处理 $NO_PROXY 展开
				if strings.Contains(v, "$NO_PROXY") {
					// 读取已有 NO_PROXY 或文件中的 NO_PROXY
					if np := os.Getenv("NO_PROXY"); np != "" {
						v = strings.ReplaceAll(v, "$NO_PROXY", np)
					} else {
						v = strings.ReplaceAll(v, "$NO_PROXY", "localhost,127.0.0.1,127.0.0.0/8,::1,10.0.0.0/8,100.64.0.0/10,169.254.0.0/16,172.16.0.0/12,192.168.0.0/16,fc00::/7,fe80::/10,.local")
					}
				}
				if k == "https_proxy" || k == "http_proxy" || k == "HTTPS_PROXY" || k == "HTTP_PROXY" || k == "ALL_PROXY" || k == "NO_PROXY" || k == "no_proxy" {
					out = append(out, k+"="+v)
					seen[k] = true
				}
			}
			if len(out) > 0 {
				break
			}
		}
	}
	return out
}

// classifyTurnError 把 RunTurn 错误收成四态结局，不返回 error。未知文本保守
// 归 unreachable；不把任何未知失败当 ready。
func classifyTurnError(err error) WakeReply {
	detail := err.Error()
	if len(detail) > 1024 {
		detail = detail[:1024]
	}
	lower := strings.ToLower(detail)
	switch {
	case strings.Contains(lower, "quota"), strings.Contains(lower, "rate limit"), strings.Contains(lower, "too many requests"):
		return WakeReply{Outcome: WakeQuota, Detail: detail}
	case strings.Contains(lower, "login"), strings.Contains(lower, "auth"), strings.Contains(lower, "unauthorized"), strings.Contains(lower, "not authenticated"):
		return WakeReply{Outcome: WakeNeedLogin, Detail: detail}
	default:
		return WakeReply{Outcome: WakeUnreachable, Detail: detail}
	}
}
