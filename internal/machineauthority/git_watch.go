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
	"log/slog"
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
	if err := fw.Add(w.gitDir); err != nil {
		fw.Close()
		return err
	}
	w.watcher = fw
	go w.loop()
	w.log.Info("git watcher 已启动", "root", w.root, "git_dir", w.gitDir)
	return nil
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
		case _, ok := <-w.watcher.Events:
			if !ok {
				return
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
