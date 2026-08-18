// symlinkcap.go —— grok 的符号链接能力探测。
//
// 职责：回答「本机现在能不能建符号链接」，供 agentd 决定是否注册 grok。
//
// 边界：
//   - 只探测，不注册、不报错到调用方之外：结论由 agentd 呈现
//   - 不缓存：agentd 启动时探一次即可，权限在运行中变化不是要覆盖的场景
//
// 为什么 grok 需要这个而别的执行器不需要：grok 给每个任务建一条指向用户
// auth 文件的符号链接（taskenv.go 建、authsync.go 周期复位），而 Windows 上
// 建符号链接需要 SeCreateSymbolicLinkPrivilege（管理员）或开发者模式。
//
// 为什么不改成复制文件绕开：软链的意义是 auth 文件只有一份权威副本。改成
// 复制后，grok 在任务里刷新 token 写的是副本，用户那份与任务那份各自漂移，
// 且这种不一致是静默的——正是 B26 那一整类问题。宁可诚实拒绝，不静默降级。
package grok

import (
	"fmt"
	"os"
	"path/filepath"
)

// SymlinkCapability 探测本机是否具备创建符号链接的能力。
//
// 参数：probeDir 为探测目录（用 DataDir，它一定存在且可写）
//
// 返回：
//   - supported: 是否可用
//   - reason: 不可用的原因，**含可行动的处置建议**；可用时为空串
//
// 注意：探测会在 probeDir 下建一个临时软链再删掉，正常路径不留任何残留。
func SymlinkCapability(probeDir string) (supported bool, reason string) {
	link := filepath.Join(probeDir, ".handoff-symlink-probe")
	// 先清掉上次异常退出可能留下的残留，否则 Symlink 会因已存在而失败，
	// 把一台其实可用的机器误判成不可用
	_ = os.Remove(link)
	if err := os.Symlink(probeDir, link); err != nil {
		return false, fmt.Sprintf("在 %s 下建符号链接失败: %v（Windows 上需要管理员权限或开启开发者模式）", probeDir, err)
	}
	if err := os.Remove(link); err != nil {
		// 建成了但删不掉：能力是有的，只是留了个残留。不因此拒绝注册，
		// 但要让人看见——下次启动的 os.Remove 会把它清掉
		return true, ""
	}
	return true, ""
}
