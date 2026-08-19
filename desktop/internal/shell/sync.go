// 本文件负责「已配置的机器上要不要把 agentd/CLI 换成内嵌的那份」的判据与执行
// （spec §4.1 / §5）。
//
// 职责：
//   - PlanSync 是纯函数：吃 DecideRelease 的结论 + 活跃任务数 + 内嵌可用性，
//     吐四态之一。不碰文件系统、不发网络请求，因此四态可以穷举测试。
//   - DoSync 才动这台机器：换二进制、同步 skill、触发 agentd 重启。
//
// 边界（承重）：
//   - 本文件**不做版本比较**。判据全部来自 DecideRelease，它背后是全仓唯一的
//     selfupdate.CompareVersion。这里再写一份比较就是第四份。
//   - 本文件**不决定何时被调用**。调用时机与相对顺序是 main.go 的 openConsole
//     的责任（spec §5 的三条承重顺序），放在这里会让顺序无法被单独测试。
//   - 同步失败**绝不阻断打开控制台**（spec D8）：所有函数只返回错误，绝不
//     os.Exit、绝不 panic、绝不阻塞等待用户输入。
package shell

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SyncPlan 是同步决策的四态。
type SyncPlan int

const (
	// SyncSkip 表示不需要同步（已有的不旧，或版本判不出，或压根没有既有安装）。
	SyncSkip SyncPlan = iota
	// SyncDo 表示该换，且此刻换是安全的。
	SyncDo
	// SyncBlocked 表示该换，但有活跃任务，闸一拦下。
	SyncBlocked
	// SyncNoEmbed 表示该换但本次构建没内嵌二进制（开发构建未带 -tags embedbin）。
	SyncNoEmbed
)

// String 返回四态的可读名，供日志用。
func (p SyncPlan) String() string {
	switch p {
	case SyncSkip:
		return "skip"
	case SyncDo:
		return "do"
	case SyncBlocked:
		return "blocked"
	case SyncNoEmbed:
		return "no-embed"
	default:
		return "SyncPlan(" + strconv.Itoa(int(p)) + ")"
	}
}

// PlanSync 决定要不要把已装的 handoff 换成内嵌的那份，是纯函数。
//
// 参数：
//   - d: DecideRelease 的结论。只有 DecisionEmbeddedNewer 才可能走到换版
//   - busy: 活跃任务数（running/waiting_answer）。**负数表示调用方探测失败**
//   - embedAvailable: 本次构建有没有内嵌二进制（embedbin.Available()）
//
// 返回四态之一，语义见各常量注释。
//
// 注意：
//   - busy 为负一律按 SyncBlocked 处置。猜错的代价不对称：误判空闲会在用户
//     有活跃任务时重启 agentd，误判繁忙只是这次不升级
//   - 本函数不写日志（纯函数约定）。四态决策的日志由调用方拿到返回值后打
func PlanSync(d ReleaseDecision, busy int, embedAvailable bool) SyncPlan {
	if d != DecisionEmbeddedNewer {
		return SyncSkip
	}
	if !embedAvailable {
		return SyncNoEmbed
	}
	// busy != 0 涵盖了负数（探测失败），见 doc comment 的不对称代价说明
	if busy != 0 {
		return SyncBlocked
	}
	return SyncDo
}

// SyncDeps 是同步动作的外部依赖集合。
//
// 抽成结构体而不是直接调包级函数：这四样全都是「会真的动这台机器」的动作
// （写文件、rename 二进制、exec 子进程、重启服务），测试必须能整体替换掉。
// 漏替一个就会在 CI 上真的把测试二进制 rename 掉——这条纪律抄自
// internal/agentd 的 UpdateDeps，理由完全相同。
type SyncDeps struct {
	// OpenEmbedded 读出内嵌的 handoff 二进制。生产实现是 embedbin.Open
	OpenEmbedded func() (io.ReadCloser, error)
	// Activate 原子换版并返回旧二进制的留存路径。生产实现是 release.Activate
	Activate func(newPath, target string) (string, error)
	// SkillInstall 在指定二进制上跑 skill install，返回其输出。
	// **必须传新二进制的路径**——当前进程内嵌的是旧 skill
	SkillInstall func(ctx context.Context, bin string) ([]byte, error)
	// RestartAgentd 触发 agentd 重启。生产实现是 client.RestartAgentd
	RestartAgentd func(ctx context.Context, force bool) error
}

// DoSync 把已装的 handoff 换成内嵌的那份，并触发 agentd 重启。
//
// 参数：
//   - target: 要被替换的二进制路径（ResolveBinPath 的结果，即 agentd 实际
//     在跑的那一份）
//   - force: 越过闸一（活跃任务）。**不越过闸二**（非托管），那是 agentd 侧
//     的硬拒绝，这里传什么都没用
//   - progress: 阶段回调，供 UI 显示。传 nil 安全
//
// 返回：
//   - 换版失败或重启触发失败时返回错误。**skill 同步失败不算失败**（理由见
//     函数内注释），但会记 Error 日志
//
// 注意：
//   - **本函数不等 agentd 回来**。那是 WaitAgentdBack 的职责，分开是因为
//     两者的失败语义不同：这里失败意味着没换成，那里失败意味着换了但没起来
//   - 四步的相对顺序是承重的，见 TestDoSyncCallOrderIsLoadBearing
func DoSync(ctx context.Context, target string, force bool, d SyncDeps, progress func(stage string)) error {
	if progress == nil {
		progress = func(string) {}
	}
	logger.Info("开始同步 handoff 二进制", "target", target, "force", force)

	progress("正在取出内嵌的 handoff")
	rc, err := d.OpenEmbedded()
	if err != nil {
		logger.Error("打开内嵌二进制失败", "target", target, "cause", err)
		return fmt.Errorf("打开内嵌二进制: %w", err)
	}
	defer rc.Close()

	// 先落到 target 同目录的临时文件：Activate 是 rename，跨设备 rename 会
	// 失败，所以临时文件必须与 target 同一个文件系统。
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".handoff-sync-*")
	if err != nil {
		logger.Error("创建临时文件失败", "dir", dir, "cause", err)
		return fmt.Errorf("创建临时文件: %w", err)
	}
	tmpName := tmp.Name()
	// 成功 rename 后置空，防止 defer 把刚落位的文件删掉。失败路径上这个
	// defer 是唯一的清理者——半截文件若以 target 那个名字留下，launchd 会
	// 把它当可执行拉起来，症状是「装好了但 agentd 起不来」。
	defer func() {
		if tmpName != "" {
			os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, rc); err != nil {
		tmp.Close()
		logger.Error("写入内嵌二进制失败", "tmp", tmpName, "cause", err)
		return fmt.Errorf("写入内嵌二进制: %w", err)
	}
	if err := tmp.Close(); err != nil {
		logger.Error("关闭临时文件失败", "tmp", tmpName, "cause", err)
		return fmt.Errorf("关闭临时文件: %w", err)
	}
	// 在 rename 之前就给执行位：target 一出现就是可执行的，不存在
	// 「已可见但还没权限」的窗口
	if err := os.Chmod(tmpName, 0o755); err != nil {
		logger.Error("设置执行权限失败", "tmp", tmpName, "cause", err)
		return fmt.Errorf("设置执行权限: %w", err)
	}

	progress("正在换版")
	prev, err := d.Activate(tmpName, target)
	if err != nil {
		logger.Error("换版失败，磁盘上仍是旧二进制", "target", target, "cause", err)
		return fmt.Errorf("换版: %w", err)
	}
	tmpName = ""
	logger.Info("换版完成", "target", target, "prev", prev)

	// skill 随二进制分发（B59）。必须 exec **新**二进制来装——当前进程内嵌
	// 的是旧 skill。失败不算同步失败：二进制已经换好了，报错回去会让调用方
	// 以为换版没成功。但绝不能静默：留一份旧 skill 会按已经变了的状态机
	// 主动误导协调者。
	progress("正在同步 skill")
	if out, err := d.SkillInstall(ctx, target); err != nil {
		logger.Error("skill 同步失败，二进制已换但 skill 是旧的",
			"target", target, "cause", err, "output", firstLine(out))
	} else {
		logger.Info("skill 同步完成", "target", target)
	}

	progress("正在重启 agentd")
	if err := d.RestartAgentd(ctx, force); err != nil {
		logger.Error("触发 agentd 重启失败，磁盘已是新版但跑着的仍是旧进程",
			"target", target, "force", force, "cause", err)
		return fmt.Errorf("触发 agentd 重启: %w", err)
	}
	logger.Info("同步完成，已触发 agentd 重启", "target", target, "force", force)
	return nil
}

// firstLine 取输出的第一行，供日志用。多行输出灌进日志会把一条 Error 撑成
// 一屏，反而看不见旁边的行。
func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
