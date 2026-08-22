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
// 无状态：每次 For 都重新取映射并重新读盘，因此配置改动与文件改动有同一种
// 时效——都在下一个任务生效，都不需要重启 agentd。
type Resolver struct {
	dir     string                   // env 文件目录
	mapping func() map[string]string // 取当前 agent 名 → 文件名映射
	log     *slog.Logger
}

// NewResolver 构造 Resolver。
//
// 参数：
//   - dir: env 文件目录，通常取 Dir(cfg.DataDir)
//   - mapping: 取当前映射的函数（生产上指向 agentd 的活配置）；nil 视为空映射，
//     此时所有 agent 都不注入
//   - log: 日志入口；nil 时退回 slog.Default()
//
// 注意：mapping 会在每次 For 时被调用，实现方必须是廉价且并发安全的
// （Server.EnvMapping 读的是 atomic 快照，满足这两条）。
func NewResolver(dir string, mapping func() map[string]string, log *slog.Logger) *Resolver {
	if log == nil {
		log = slog.Default()
	}
	if mapping == nil {
		log.Warn("env 映射取值函数为空，所有 agent 都不会注入环境变量", "dir", dir)
		mapping = func() map[string]string { return nil }
	}
	return &Resolver{dir: dir, mapping: mapping, log: log}
}

// Static 把一份固定映射包成取值函数，供测试与不需要热更新的调用方使用。
func Static(m map[string]string) func() map[string]string {
	return func() map[string]string { return m }
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
	name := strings.TrimSpace(r.mapping()[agent])
	if name == "" {
		r.log.Debug("agent 未配置 env 文件，跳过注入", "agent", agent)
		return nil, nil
	}
	return LoadFile(r.dir, name, r.log)
}

// LoadFile 按**文件名**加载一份 env 文件，返回可注入的 KEY=VALUE 列表。
//
// 参数：
//   - dir: env 文件目录（通常取 Dir(cfg.DataDir)）
//   - name: 纯文件名，含路径分隔符即拒
//   - log: 日志入口；nil 时退回 slog.Default()
//
// 返回：
//   - 变量列表；文件名非法 / 打不开 / 解析失败时返回错误，错误文本带完整路径
//   - **不把「文件不存在」特殊化成 (nil,nil)**：调用方明确点名了一份文件，
//     它不在就是一个要说出来的失败。「没配」那一档由调用方判断（见 For）
//
// 注意：
//   - 每次调用都重新读盘，不缓存（理由见 For）
//   - 展开用 os.LookupEnv：同一份文件在「作为 executor env」与「作为终端 env」
//     时必须展开出相同的值，所以查的是 agentd 自身环境，不查调用方拼好的环境
//   - **日志只打 key 名，绝不打值**：环境类变量里 HTTPS_PROXY=http://user:pass@host
//     是正常写法，值里带凭据的概率不低。这条纪律是本函数存在的主要理由——
//     调用方各写各的三行加载，第二个实现漏掉它时不报错、不变红，只是日志里
//     多了一行带 token 的字符串
func LoadFile(dir, name string, log *slog.Logger) ([]string, error) {
	if log == nil {
		log = slog.Default()
	}
	path, err := resolvePath(dir, name)
	if err != nil {
		log.Error("env 文件名非法", "name", name, "cause", err)
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		log.Error("打开 env 文件失败", "path", path, "cause", err)
		return nil, fmt.Errorf("打开 env 文件 %s: %w", path, err)
	}
	defer f.Close()

	kvs, dups, err := Parse(f, os.LookupEnv)
	if err != nil {
		log.Error("解析 env 文件失败", "path", path, "cause", err)
		return nil, fmt.Errorf("解析 env 文件 %s: %w", path, err)
	}
	for _, k := range dups {
		log.Warn("env 文件存在重复键，后者覆盖前者", "path", path, "key", k)
	}
	out := make([]string, 0, len(kvs))
	keys := make([]string, 0, len(kvs))
	for _, kv := range kvs {
		out = append(out, kv.Key+"="+kv.Value)
		keys = append(keys, kv.Key)
	}
	log.Info("已加载 env 文件", "path", path, "keys", keys, "count", len(keys))
	return out, nil
}

// Preflight 读一遍所有被引用的 env 文件，把问题以 WARN 暴露在启动日志里。
//
// 为什么只 WARN 不阻断启动：env 文件是数据文件不是配置键，可能在 agentd 启动后
// 才创建，为它拒绝启动太硬；但完全不检查会把问题拖到第一次派发才暴露——WARN 让它
// 在启动日志里就可见，真正的拒发发生在 Dispatch（见 spec §6）。
func (r *Resolver) Preflight() {
	for agent := range r.mapping() {
		if _, err := r.For(agent); err != nil {
			r.log.Warn("env 文件预检失败（不阻断启动，派发时会拒发）", "agent", agent, "cause", err)
		}
	}
}
