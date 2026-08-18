// resolver.go resolves configured discipline files and built-in defaults.
//
// Responsibilities:
//   - keep the <DataDir>/discipline layout in one place
//   - choose configured, explicitly disabled, or built-in discipline blocks
//   - expose bad files during agentd startup without blocking startup
//
// It does not interpret discipline content, inject processes, or cache file contents.
package discipline

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// maxBlockSize is the maximum configured block size, matching envfile's 64 KiB limit.
const maxBlockSize = 64 << 10

// Dir returns the discipline file directory (<dataDir>/discipline).
func Dir(dataDir string) string { return filepath.Join(dataDir, "discipline") }

// Resolver chooses the discipline block injected for an executor.
//
// It is stateless: every For call reads the file again, so changes take effect on
// the next task without stale resolver instances.
type Resolver struct {
	dir string
	m   map[string]string
	log *slog.Logger
}

// NewResolver constructs a Resolver.
//
// dir is normally Dir(cfg.DataDir), m maps executor names to pure filenames, and
// a nil logger uses slog.Default(). A nil map means no configured overrides.
func NewResolver(dir string, m map[string]string, log *slog.Logger) *Resolver {
	if log == nil {
		log = slog.Default()
	}
	if m == nil {
		m = map[string]string{}
	}
	return &Resolver{dir: dir, m: m, log: log}
}

// For returns the discipline block for executor.
//
// A non-empty configured value reads <dir>/<value>; an explicitly empty value
// disables injection; an absent key uses the built-in default. Invalid filenames,
// unreadable files, and oversized files return errors.
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

// resolvePath turns a configured filename into a path and rejects anything other
// than a pure filename. This prevents traversal and keeps all blocks in one directory.
func (r *Resolver) resolvePath(name string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) || strings.ContainsRune(name, '/') {
		return "", fmt.Errorf("纪律块文件名 %q 不能含路径分隔符：只支持 %s 下的纯文件名", name, r.dir)
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("纪律块文件名 %q 非法：只支持 %s 下的纯文件名", name, r.dir)
	}
	return filepath.Join(r.dir, name), nil
}

// Preflight checks all configured discipline files and logs failures as warnings.
// Startup remains non-blocking; dispatch performs the actual rejecting resolution.
func (r *Resolver) Preflight() {
	for executor := range r.m {
		if _, err := r.For(executor); err != nil {
			r.log.Warn("纪律块预检失败（不阻断启动，派发时会拒发）", "executor", executor, "cause", err)
		}
	}
}
