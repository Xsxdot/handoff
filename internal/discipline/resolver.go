// resolver.go —— 纪律块文件的定位、读盘与档位裁决。
//
// 职责：
//   - Dir：收口 <DataDir>/discipline 的目录布局知识，避免各调用方自己拼路径后漂移
//   - Resolver.For：按 executor 名裁出该注入哪块纪律（配置 > 显式关闭 > 内置默认）
//   - Resolver.Preflight：agentd 启动时把坏文件暴露在启动日志里
//
// 边界：
//   - 不理解纪律内容、不注入进程（交各 adapter）、不缓存（同 envfile：改完下个任务即生效）
package discipline

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// maxBlockSize 是纪律块文件大小上限，与 envfile 同为 64KiB——
// 防止误配一个二进制文件后把一堆垃圾塞进模型上下文。
const maxBlockSize = 64 << 10

// Dir 返回纪律块文件目录（<dataDir>/discipline）。
//
// 目录布局知识只此一处：manager 与 agentd 各自构造 Resolver，
// 若各拼各的路径，日后改布局必然漏改一处（envfile.Dir 同款理由）。
func Dir(dataDir string) string { return filepath.Join(dataDir, "discipline") }

// Resolver 按 executor 名裁出该次派发要注入的纪律块。
//
// 无状态：每次 For 都重新读盘，因此多个实例之间不会发散。
type Resolver struct {
	dir string            // 纪律块文件目录
	m   map[string]string // executor 名 → 文件名（纯文件名，不含路径）
	log *slog.Logger
}

// NewResolver 构造 Resolver。
//
// 参数：
//   - dir: 纪律块文件目录，通常取 Dir(cfg.DataDir)
//   - m: executor 名 → 文件名映射（取自 config 的 discipline 段）；nil 视为空映射
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

// For 返回该 executor 派发时应注入的纪律块。
//
// 参数：
//   - executor: 执行者名（如 codex）
//
// 返回：
//   - 三档语义见下；文件名非法 / 读不到 / 超限时返回错误，错误文本带完整路径
//
// 三档语义（第三档是**与 envfile 刻意的偏离**）：
//
//	配置里有非空值   → 读 <dir>/<值>
//	配置里显式给空串 → 关闭注入，返回零值 Block
//	配置里没这个 key → 用内置默认（envfile 在这一档是「不注入」）
//
// 为什么第三档不同：env 的内容是机器特有的（代理地址、私有 registry），
// 猜错不如不猜；纪律块的内容是 handoff 通用的，不给默认等于让用户退回人工
// 粘贴，而选错档位的代价已被实测（见 builtinFor）。
//
// 为什么配置指向的文件缺失是错误而不是退回内置：用户明确配了一份，
// 悄悄换成另一份比失败更危险——他会以为跑的是自己那套纪律。
func (r *Resolver) For(executor string) (Block, error) {
	raw, configured := r.m[executor]
	name := strings.TrimSpace(raw)
	if !configured {
		b := builtinFor(executor)
		r.log.Info("executor 未配置纪律块，用内置默认", "executor", executor, "source", b.Source)
		return b, nil
	}
	if name == "" {
		r.log.Info("executor 显式关闭纪律块注入", "executor", executor)
		return Block{}, nil
	}
	path, err := r.resolvePath(name)
	if err != nil {
		r.log.Error("纪律块文件名非法", "executor", executor, "name", name, "path", r.dir, "cause", err)
		return Block{}, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		r.log.Error("纪律块文件不可读", "executor", executor, "path", path, "cause", err)
		return Block{}, fmt.Errorf("读取纪律块文件 %s: %w", path, err)
	}
	if fi.Size() > maxBlockSize {
		r.log.Error("纪律块文件超限", "executor", executor, "path", path, "cause", fmt.Sprintf("%d 字节超过上限", fi.Size()))
		return Block{}, fmt.Errorf("纪律块文件 %s 超过 %d 字节上限（实际 %d）", path, maxBlockSize, fi.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		r.log.Error("读取纪律块文件失败", "executor", executor, "path", path, "cause", err)
		return Block{}, fmt.Errorf("读取纪律块文件 %s: %w", path, err)
	}
	r.log.Info("已加载纪律块", "executor", executor, "path", path, "bytes", len(data))
	return Block{Text: string(data), Source: "配置:" + name}, nil
}

// resolvePath 把配置里的文件名换算为绝对路径，并拒绝一切非「纯文件名」的写法。
//
// 为什么只收纯文件名：一杜绝路径穿越（../../etc 之类），二保证纪律块只有一个家、
// 不会散落各处——运维找配置时只需要看一个目录（envfile.resolvePath 同款理由）。
func (r *Resolver) resolvePath(name string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) || strings.ContainsRune(name, '/') {
		return "", fmt.Errorf("纪律块文件名 %q 不能含路径分隔符：只支持 %s 下的纯文件名", name, r.dir)
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("纪律块文件名 %q 非法：只支持 %s 下的纯文件名", name, r.dir)
	}
	return filepath.Join(r.dir, name), nil
}

// Preflight 读一遍所有被引用的纪律块文件，把问题以 WARN 暴露在启动日志里。
//
// 为什么只 WARN 不阻断启动：纪律块是数据文件不是配置键，可能在 agentd 启动后
// 才创建；但完全不检查会把问题拖到第一次派发才暴露——WARN 让它在启动日志里
// 就可见，真正的拒发发生在 Dispatch（envfile.Preflight 同款理由）。
func (r *Resolver) Preflight() {
	for executor := range r.m {
		if _, err := r.For(executor); err != nil {
			r.log.Warn("纪律块预检失败（不阻断启动，派发时会拒发）", "executor", executor, "cause", err)
		}
	}
}
