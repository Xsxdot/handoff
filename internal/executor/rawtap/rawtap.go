// Package rawtap 提供上游原始字节的旁路归档。
//
// 职责：
//   - 在 adapter 解析上游消息**之前**，把原始字节按行旁写到一个文件
//   - 由环境变量 HANDOFF_RAW_TAP_DIR 门控，缺省完全关闭（Open 返回 nil）
//
// 边界：
//   - 不解析、不过滤、不判断内容：拿到什么写什么，样本的价值就在于未经加工
//   - 不参与任何回合判定：Write 的返回值是 void，adapter 无从依赖它
//   - 不做轮转与容量上限：这是诊断开关，开着跑一次探针就关，不是常驻设施
//
// 为什么需要它：grok 与 codex 的上游是进程内 WebSocket、opencode 的 SSE 端口
// 由 adapter 随机选，三者都无法从进程外 tee。没有旁路就没有原始样本，
// 而没有原始样本的现场无法从任何一个 clone 复核——B74 的原始现场（2026-08-12）
// 正是因此永久丢失。
package rawtap

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// EnvDir 是开启旁路的环境变量名。值为一个目录路径；空或未设置即完全关闭。
const EnvDir = "HANDOFF_RAW_TAP_DIR"

// Tap 是一个任务的原始字节旁路句柄。
//
// 全部方法对 nil 接收者安全：关闭状态下 Open 返回 nil，调用点因此不需要写
// 任何 if——这是「诊断开关绝不能污染主路径」的落地方式。
type Tap struct {
	mu sync.Mutex
	f  *os.File
}

// Open 按环境变量决定是否为某任务开启旁路。
//
// 参数：
//   - executor: 执行者名（opencode/claudecode/grok/codex），作文件名前缀
//   - taskID: 任务 ID，作文件名后缀
//   - log: 用于报告开启与降级
//
// 返回：开启则返回句柄；未开启或开启失败返回 nil（调用方无需区分）
//
// 注意：开启失败不是错误——诊断开关拖垮执行是本末倒置，一律降级为关闭并告警。
func Open(executor, taskID string, log *slog.Logger) *Tap {
	dir := strings.TrimSpace(os.Getenv(EnvDir))
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Warn("原始字节旁路目录不可用，本次不归档", "dir", dir, "cause", err)
		return nil
	}
	p := filepath.Join(dir, fmt.Sprintf("%s-%s.jsonl", executor, taskID))
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Warn("原始字节旁路文件打不开，本次不归档", "path", p, "cause", err)
		return nil
	}
	log.Info("原始字节旁路已开启", "executor", executor, "task", taskID, "path", p)
	return &Tap{f: f}
}

// Write 旁写一条上游原始消息，一次调用一行。
//
// 注意：
//   - 内嵌换行会被转义成 \n。上游消息（尤其是被截断的超长工具调用）内部可能
//     带裸换行，不转义的话一条消息在样本里会裂成多条，回放时与真实分帧对不上
//   - 写失败只记一次日志后静默：旁路是观测，观测失败不能影响被观测的东西
func (t *Tap) Write(b []byte) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.f == nil {
		return
	}
	esc := strings.NewReplacer("\\", `\\`, "\n", `\n`, "\r", `\r`).Replace(string(b))
	if _, err := t.f.WriteString(esc + "\n"); err != nil {
		// 只失败一次就关掉：磁盘满时逐条告警会把日志刷爆，而旁路已经废了
		t.f.Close()
		t.f = nil
	}
}

// Close 关闭旁路，幂等，nil 接收者安全。
func (t *Tap) Close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.f != nil {
		t.f.Close()
		t.f = nil
	}
}
