// probe.go —— 本机载体 HOME 的检测回合（B295；B464 删只读探测）。
//
// 职责：用载体（登录）HOME 检测 CLI 可用性。名单内 CLI 走 RunTurn 发固定短消息；
// 名单外只查命令是否可执行（B342：不把未实装当成检测失败的唯一结局）。只暴露
// 本机能力。B464 起不再提供只读「这个 HOME 像不像已登录」探测：登录由发现器的
// 离线取材判定，HOME 提示由 `POST /api/logins/check` 承担。
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

	"github.com/Xsxdot/handoff/internal/proxycfg"
	"github.com/Xsxdot/handoff/internal/quota"
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

// WakeOutcome 是一次唤起在本机观察到的结局，供编制域套四态表。
type WakeOutcome string

const (
	WakeReady       WakeOutcome = "ready"
	WakeNeedLogin   WakeOutcome = "need_login"
	WakeQuota       WakeOutcome = "quota" // 检测回合撞额度（真额度耗尽）；B453 起语义是「可达 + 撞额度」（Q19）
	WakeUnreachable WakeOutcome = "unreachable"
)

// WakeRequest 描述一次有时限的本机唤起。HomeDir 是载体 HOME（= 所选登录的 HOME，B464；空 =
// 主 HOME）。Timeout=0 使用 DefaultDetectTimeout。B464 起检测不再供给任何凭据（main_home_sync
// 复制删除）：HOME 里有什么凭据就用什么，没有就是需要登录。
type WakeRequest struct {
	CLI     string
	HomeDir string
	Model   string
	Timeout time.Duration
	// AccountKind 是载体所选登录的 kind：据此缩小撞额度签名集合；空 = 未选账号登录。
	AccountKind string
	// Proxy 是载体所选登录的代理（组装侧在所在机查得，不过机间 wire）；空 = 沿用今天的环境。
	Proxy string
}

// WakeReply 是唤起结局。Detail 给人看，原样交给 ApplyDetect 的 last_error。
type WakeReply struct {
	Outcome WakeOutcome
	Detail  string
}

// WakeHome 用目标机展开后的载体 HOME 跑一次检测。名单内经 RunTurn 发 DetectPrompt；名单外
// 只查命令（B342）。Timeout=0 使用 DefaultDetectTimeout。不进控制台登录 TUI。
//
// B464：凭据只读、永不复制——不再从主 HOME 拷凭据；HOME 不存在时不 mkdir 造空 HOME（那会把
// 「登录已失效」伪装成「需要登录」），直接报需要登录并点名路径。
func (h *Host) WakeHome(ctx context.Context, req WakeRequest) (WakeReply, error) {
	started := time.Now()
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultDetectTimeout
	}
	req.Timeout = timeout
	log().Info("开始检测载体 HOME", "cli", req.CLI, "home_dir", req.HomeDir,
		"account_kind", req.AccountKind, "timeout", timeout.String())
	targetHome, err := h.resolveHomePath(ctx, req.HomeDir, req.CLI)
	if err != nil {
		log().Error("检测前解析 HOME 失败", "cli", req.CLI, "path", req.HomeDir,
			"elapsed", time.Since(started).String(), "cause", err)
		return WakeReply{}, err
	}
	if _, statErr := os.Stat(targetHome); errors.Is(statErr, os.ErrNotExist) {
		detail := fmt.Sprintf("载体 HOME %s 不存在：该登录已失效或尚未在这里登录（在该机该 HOME 登录对应 CLI 后重新检测）", targetHome)
		log().Warn("检测载体 HOME：目录不存在，不创建", "cli", req.CLI, "path", targetHome)
		return WakeReply{Outcome: WakeNeedLogin, Detail: detail}, nil
	}

	reply, err := h.runWake(ctx, req, targetHome)
	if err != nil {
		log().Error("检测载体 HOME 失败", "cli", req.CLI, "path", targetHome,
			"elapsed", time.Since(started).String(), "cause", err)
		return WakeReply{}, err
	}
	log().Info("检测载体 HOME 完成", "cli", req.CLI, "path", targetHome,
		"outcome", reply.Outcome, "elapsed", time.Since(started).String())
	return reply, nil
}

// resolveHomePath 在目标 Host 进程展开载体 HOME（空 = 主 HOME），返回绝对路径。
// 只做解析与存在性/目录类型校验，不创建、不改写任何文件；目录不存在交给
// WakeHome 报「需要登录」。B464 起检测不再按凭据路径表判态：HOME 里有什么
// 凭据就用什么，没有就是需要登录。
func (h *Host) resolveHomePath(ctx context.Context, rawPath, cli string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("hostapi: 解析 HOME 被取消: %w", err)
	}
	path := strings.TrimSpace(rawPath)
	if path == "" {
		home, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("hostapi: 空 HOME 回落到主 HOME 失败: %w", err)
		}
		if home == "" {
			return "", fmt.Errorf("hostapi: 空 HOME 回落到主 HOME 失败: 主 HOME 为空")
		}
		log().Info("空 HOME 按主 HOME 检测", "cli", cli)
		path = filepath.Clean(home)
	} else {
		expanded, err := ExpandHomePath(rawPath)
		if err != nil {
			return "", err
		}
		path = expanded
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
		return "", fmt.Errorf("hostapi: stat 目标 HOME %q: %w", path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("hostapi: 目标 HOME %q 不是目录", path)
	}
	return path, nil
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
	// 账号代理覆盖检测回合的代理键（spec S4：检测回合与任务回合同走账号代理）。
	env := proxycfg.WithProxyEnv(wakeProxyEnv(cli), req.Proxy)
	if strings.TrimSpace(req.Proxy) != "" {
		log().Info("检测回合使用账号代理", "cli", cli, "proxy", proxycfg.Mask(req.Proxy))
	}
	reply, err := detectTurn(h, ctx, TurnRequest{
		CLI: req.CLI, HomeDir: targetHome, Workdir: targetHome, Model: req.Model,
		Prompt: DetectPrompt, Timeout: timeout, Env: env,
	})
	if err != nil {
		mapped := classifyTurnError(cli, req.AccountKind, err)
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

// classifyTurnError 把 RunTurn 错误收成检测结局，不返回 error。撞额度/限速的判据委托
// quota.ClassifyTurnFailure（唯一判据，plan Q10）：额度耗尽 → quota（协调侧据此标账号或写
// 最近错误，Q19）；限速 → 视为可达（ready + 说明），不再当额度；登录类分支不动；未知文本
// 保守归 unreachable，不把任何未知失败当 ready。
func classifyTurnError(cli, accountKind string, err error) WakeReply {
	detail := err.Error()
	if len(detail) > 1024 {
		detail = detail[:1024]
	}
	switch quota.ClassifyTurnFailure(cli, accountKind, detail).Class {
	case quota.TurnQuota:
		return WakeReply{Outcome: WakeQuota, Detail: detail}
	case quota.TurnRateLimited:
		return WakeReply{Outcome: WakeReady, Detail: "检测回合被限速（不算额度耗尽，载体可达）：" + detail}
	}
	lower := strings.ToLower(detail)
	switch {
	case strings.Contains(lower, "login"), strings.Contains(lower, "auth"), strings.Contains(lower, "unauthorized"), strings.Contains(lower, "not authenticated"):
		return WakeReply{Outcome: WakeNeedLogin, Detail: detail}
	default:
		return WakeReply{Outcome: WakeUnreachable, Detail: detail}
	}
}
