// machineauthority git_watch：文件系统变化只作「尽快扫描」的提示。
//
// 职责：
//   - 监视 .git 目录元数据变化，去抖后触发一次完整 Reconcile
//
// 边界：
//   - watcher 不直接把文件系统事件当事实：它只是提示 Reconcile 该跑了，
//     事实永远是 git 命令扫描结果（spec §8.2）
//   - 不使用 2.36+ 的新特性，文件系统事件本身与 git 版本无关
package machineauthority

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watchDebounce 是 .git 变化事件去抖窗口。
//
// 为什么去抖：一次 `git worktree add` 会产生多起文件系统事件（refs、HEAD、
// 索引等），不去抖会让一次仓库操作触发十几次 Reconcile；窗口内合并为一次。
const watchDebounce = 300 * time.Millisecond

// GitWatcher 监视仓库 .git 目录并去抖触发 Reconcile。
type GitWatcher struct {
	root     string
	gitDir   string
	onChange func()
	watcher  *fsnotify.Watcher
	closeCh  chan struct{}
	mu       sync.Mutex
	timer    *time.Timer
	log      *slog.Logger
}

// NewGitWatcher 创建 .git 监视器。
//
// 参数：
//   - root: 仓库根目录（日志用）
//   - gitDir: 要监视的 .git 目录路径
//   - onChange: 去抖后触发的 Reconcile 回调
func NewGitWatcher(root, gitDir string, onChange func()) *GitWatcher {
	return &GitWatcher{
		root: root, gitDir: gitDir, onChange: onChange,
		closeCh: make(chan struct{}),
		log:     slog.Default(),
	}
}

// Start 启动监视。返回错误表示无法创建 watcher（通常 .git 不存在或不可读）。
func (w *GitWatcher) Start() error {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	roots, err := resolveGitWatchRoots(w.gitDir)
	if err != nil {
		fw.Close()
		return err
	}
	for _, root := range roots {
		if err := addGitWatchTree(fw, root); err != nil {
			fw.Close()
			return fmt.Errorf("监听 Git 元数据目录 %s: %w", root, err)
		}
	}
	w.watcher = fw
	go w.loop()
	w.log.Info("git watcher 已启动", "root", w.root, "git_dir", w.gitDir, "watch_roots", len(roots))
	return nil
}

// resolveGitWatchRoots 解析普通仓库与 linked worktree 的实际 Git 元数据根。
// linked worktree 的 .git 是指针文件，分支 refs 则位于 commondir；两者都要监听。
func resolveGitWatchRoots(dotGit string) ([]string, error) {
	info, err := os.Stat(dotGit)
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", dotGit, err)
	}
	gitDir := dotGit
	if !info.IsDir() {
		value, err := readGitPathFile(dotGit, "gitdir:")
		if err != nil {
			return nil, err
		}
		gitDir = resolveGitMetadataPath(filepath.Dir(dotGit), value)
	}
	roots := map[string]struct{}{filepath.Clean(gitDir): {}}
	commonFile := filepath.Join(gitDir, "commondir")
	if _, err := os.Stat(commonFile); err == nil {
		value, readErr := readGitPathFile(commonFile, "")
		if readErr != nil {
			return nil, readErr
		}
		roots[resolveGitMetadataPath(gitDir, value)] = struct{}{}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取 Git commondir %s: %w", commonFile, err)
	}
	out := make([]string, 0, len(roots))
	for path := range roots {
		out = append(out, path)
	}
	sort.Strings(out)
	return out, nil
}

func readGitPathFile(path, prefix string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("打开 Git 元数据指针 %s: %w", path, err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return "", fmt.Errorf("读取 Git 元数据指针 %s: %w", path, err)
	}
	if len(raw) > 4096 {
		return "", fmt.Errorf("Git 元数据指针 %s 超过 4096 字节", path)
	}
	value := strings.TrimSpace(string(raw))
	if prefix != "" {
		var ok bool
		value, ok = strings.CutPrefix(value, prefix)
		if !ok {
			return "", fmt.Errorf("Git 元数据指针 %s 缺少 %s", path, prefix)
		}
		value = strings.TrimSpace(value)
	}
	if value == "" {
		return "", fmt.Errorf("Git 元数据指针 %s 为空", path)
	}
	return value, nil
}

func resolveGitMetadataPath(base, value string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(base, value))
}

func addGitWatchTree(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// object 文件的写入不会改变工作区/分支清单；忽略它可避免大型仓库
		// 为成千上万个 fan-out 目录占用 watcher 描述符。
		if entry.IsDir() && entry.Name() == "objects" && path != root {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return watcher.Add(path)
		}
		return nil
	})
}

// Close 停止监视并释放资源。
func (w *GitWatcher) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.closeCh:
		return // 已关闭
	default:
		close(w.closeCh)
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	if w.watcher != nil {
		w.watcher.Close()
	}
	w.log.Info("git watcher 已停止", "root", w.root)
}

// loop 消费 fsnotify 事件，去抖后触发一次 Reconcile。
func (w *GitWatcher) loop() {
	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					if err := addGitWatchTree(w.watcher, event.Name); err != nil {
						w.log.Warn("git watcher 添加新目录失败", "root", w.root, "cause", err)
					}
				}
			}
			w.scheduleReconcile()
		case _, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			// 文件系统监视错误仅记录：不能让它阻断 watcher
			w.log.Warn("git watcher 收到错误事件", "root", w.root)
		case <-w.closeCh:
			return
		}
	}
}

// scheduleReconcile 去抖：窗口内合并所有事件为一次 Reconcile。
func (w *GitWatcher) scheduleReconcile() {
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.closeCh:
		return
	default:
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(watchDebounce, func() {
		w.log.Info("git watcher 触发 Reconcile", "root", w.root)
		w.onChange()
	})
}
