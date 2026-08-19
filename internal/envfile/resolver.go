// resolver.go —— env 文件的定位、读盘与日志。
//
// 职责：
//   - Dir：收口 <DataDir>/env 的目录布局知识，避免各调用方自己拼路径后漂移
//   - Resolver.For：按 agent 名解析出可注入的 KEY=VALUE 切片
//   - Resolver.Preflight：agentd 启动时把坏文件暴露在启动日志里
//
// 边界：
//   - 不解析语法（交 Parse）、不注入进程（交各 adapter）、不缓存（见 For 注释）
package envfile

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Dir 返回 env 文件目录（<dataDir>/env）。
//
// 目录布局知识只此一处：manager 与 agentd 各自构造 Resolver，若各拼各的路径，
// 日后改布局必然漏改一处。
func Dir(dataDir string) string { return filepath.Join(dataDir, "env") }

// Resolver 按 agent 名把配置里的文件名换算成可注入的环境变量。
//
// 无状态：每次 For 都重新读盘，因此多个实例之间不会发散（见 For 的热更新说明）。
type Resolver struct {
	dir string            // env 文件目录
	m   map[string]string // agent 名 → 文件名（纯文件名，不含路径）
	log *slog.Logger
}

// NewResolver 构造 Resolver。
//
// 参数：
//   - dir: env 文件目录，通常取 Dir(cfg.DataDir)
//   - m: agent 名 → 文件名映射（取自 config 的 env 段）；nil 视为空映射
//   - log: 日志入口；nil 时退回 slog.Default()
func NewResolver(dir string, m map[string]string, log *slog.Logger) *Resolver {
	if log == nil {
		log = slog.Default()
	}
	if m == nil {
		m = map[string]string{}
	}
	return &Resolver{dir: dir, m: m, log: log}
}

// For 返回该 agent 启动时应注入的环境变量（KEY=VALUE 形式）。
//
// 参数：
//   - agent: executor 名（如 opencode）
//
// 返回：
//   - 该 agent 未配置 env 文件时返回 (nil, nil)——不是错误，是「没配」
//   - 文件名非法 / 打不开 / 解析失败时返回错误，错误文本带完整路径与行号
//
// 注意：
//   - 每次调用都重新读盘，不缓存。改了代理下一个任务就生效，不必重启 agentd
//     （重启会打断正在跑的任务的事件订阅，代价不小）；读一个几百字节的文件
//     相对于拉起一个 agent 的开销可以忽略
//   - 日志只打 key 名，绝不打值：环境类变量里 HTTPS_PROXY=http://user:pass@host
//     是正常写法，值里带凭据的概率不低
func (r *Resolver) For(agent string) ([]string, error) {
	name := strings.TrimSpace(r.m[agent])
	if name == "" {
		r.log.Debug("agent 未配置 env 文件，跳过注入", "agent", agent)
		return nil, nil
	}
	path, err := resolvePath(r.dir, name)
	if err != nil {
		r.log.Error("env 文件名非法", "agent", agent, "name", name, "cause", err)
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		r.log.Error("打开 env 文件失败", "agent", agent, "path", path, "cause", err)
		return nil, fmt.Errorf("打开 env 文件 %s: %w", path, err)
	}
	defer f.Close()

	kvs, dups, err := Parse(f, os.LookupEnv)
	if err != nil {
		r.log.Error("解析 env 文件失败", "agent", agent, "path", path, "cause", err)
		return nil, fmt.Errorf("解析 env 文件 %s: %w", path, err)
	}
	for _, k := range dups {
		r.log.Warn("env 文件存在重复键，后者覆盖前者", "path", path, "key", k)
	}
	out := make([]string, 0, len(kvs))
	keys := make([]string, 0, len(kvs))
	for _, kv := range kvs {
		out = append(out, kv.Key+"="+kv.Value)
		keys = append(keys, kv.Key)
	}
	r.log.Info("已加载 env 文件", "agent", agent, "path", path, "keys", keys, "count", len(keys))
	return out, nil
}

// Preflight 读一遍所有被引用的 env 文件，把问题以 WARN 暴露在启动日志里。
//
// 为什么只 WARN 不阻断启动：env 文件是数据文件不是配置键，可能在 agentd 启动后
// 才创建，为它拒绝启动太硬；但完全不检查会把问题拖到第一次派发才暴露——WARN 让它
// 在启动日志里就可见，真正的拒发发生在 Dispatch（见 spec §6）。
func (r *Resolver) Preflight() {
	for agent := range r.m {
		if _, err := r.For(agent); err != nil {
			r.log.Warn("env 文件预检失败（不阻断启动，派发时会拒发）", "agent", agent, "cause", err)
		}
	}
}
