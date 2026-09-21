// plugin.go —— OpenCode monitor 插件落点（B373）。
//
// 职责：把内嵌的 handoff-monitor.ts 写到本机 OpenCode 自动加载目录。
// 边界：不改 opencode.json；OpenCode 配置目录不存在就跳过，不代为创建。
package skill

import (
	"crypto/sha256"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
)

// PluginRelFile 是相对 $HOME 的插件落点。OpenCode 启动时自动加载该目录下的 .ts/.js。
const PluginRelFile = ".config/opencode/plugins/handoff-monitor.ts"

// pluginParentRel 是「OpenCode 已安装」的判据目录，与 skills 落点同一层。
const pluginParentRel = ".config/opencode"

// InstallPlugin 把 content 写到本机 OpenCode 插件目录。
//
// 空内容、配置目录不存在、写失败都返回 skipped + Note，不返回 error——
// 与 skill.Install 一样：一家失败不能吃掉其它落点。
func InstallPlugin(content, home string) Site {
	target := filepath.Join(home, PluginRelFile)
	if content == "" {
		slog.Default().Warn("内嵌 OpenCode 插件为空，跳过", "path", target)
		return Site{Path: target, State: StateSkipped, Note: "内嵌插件为空，跳过"}
	}
	parent := filepath.Join(home, pluginParentRel)
	if _, err := os.Stat(parent); err != nil {
		note := parent + " 不存在（该 agent 未安装）"
		if !errors.Is(err, os.ErrNotExist) {
			note = "读取 OpenCode 配置目录失败: " + err.Error()
		}
		slog.Default().Warn("OpenCode 插件落点跳过", "path", target, "reason", note)
		return Site{Path: target, State: StateSkipped, Note: note}
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Default().Error("创建 OpenCode 插件目录失败", "path", dir, "cause", err)
		return Site{Path: target, State: StateSkipped, Note: "创建插件目录失败: " + err.Error()}
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		slog.Default().Error("写 OpenCode 插件失败", "path", target, "cause", err)
		return Site{Path: target, State: StateSkipped, Note: "写插件失败: " + err.Error()}
	}
	slog.Default().Info("OpenCode 插件已写入", "path", target)
	return Site{Path: target, State: StateInstalled}
}

// PluginStatus 用插件内嵌内容做哈希比对，不能拿 SKILL.md 去比。
func PluginStatus(content, home string) Site {
	target := filepath.Join(home, PluginRelFile)
	parent := filepath.Join(home, pluginParentRel)
	if _, err := os.Stat(parent); err != nil {
		note := parent + " 不存在（该 agent 未安装）"
		if !errors.Is(err, os.ErrNotExist) {
			note = "读取 OpenCode 配置目录失败: " + err.Error()
		}
		return Site{Path: target, State: StateMissing, Note: note}
	}
	if content == "" {
		return Site{Path: target, State: StateSkipped, Note: "内嵌插件为空，跳过比对"}
	}
	b, err := os.ReadFile(target)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return Site{Path: target, State: StateMissing}
	case err != nil:
		return Site{Path: target, State: StateMissing, Note: "读取失败: " + err.Error()}
	}
	want := sha256.Sum256([]byte(content))
	got := sha256.Sum256(b)
	if got == want {
		return Site{Path: target, State: StateInSync}
	}
	return Site{Path: target, State: StateStale}
}
