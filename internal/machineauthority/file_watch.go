// machineauthority Workspace 文件失效提示流。
//
// 职责：
//   - 递归监听授权根内 create/modify/remove 并合并短时间重复事件
//   - 为每个 Workspace 分配单调 seq、保留有界 journal、支持 after 重放
//   - 对慢订阅者和 unavailable Workspace 主动终止流
//
// 边界：
//   - watcher 只提示 UI 重新读取，不把事件当文件内容事实源
//   - 忽略 .git 与 handoff 原子保存临时文件，不记录文件内容
package machineauthority

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

// ErrFileStreamOverflow 表示客户端消费过慢，须携带最后 seq 重连补拉。
var ErrFileStreamOverflow = errors.New("文件事件订阅缓冲已满")

type fileSubscriber struct {
	events chan workspaceapi.FileEvent
	done   chan error
	closed bool
}

type fileWorkspaceStream struct {
	rootPath    string
	available   bool
	seq         int64
	journal     []workspaceapi.FileEvent
	subscribers map[string]*fileSubscriber
	cancelWatch context.CancelFunc
	watchID     string
}

// FileStream 管理全部 Workspace 的有界文件事件流。
type FileStream struct {
	mu               sync.Mutex
	workspaces       map[string]*fileWorkspaceStream
	journalLimit     int
	subscriberBuffer int
	coalesceWindow   time.Duration
	log              *slog.Logger
}

// NewFileStream 创建文件事件流管理器。
func NewFileStream(log *slog.Logger) *FileStream {
	if log == nil {
		log = slog.Default()
	}
	return &FileStream{
		workspaces: make(map[string]*fileWorkspaceStream), journalLimit: 1024,
		subscriberBuffer: 64, coalesceWindow: 50 * time.Millisecond, log: log,
	}
}

// SubscribeFiles 启动/复用 Workspace watcher，并返回 replay + live 订阅。
func (a *ResourceAuthority) SubscribeFiles(ctx context.Context, ws workspaceapi.WorkspaceRef, after int64) (*workspaceapi.FileSubscription, error) {
	return a.fileStream.Subscribe(ctx, ws, after)
}

// SetWorkspaceAvailable 更新 Workspace 可用性；false 会立即终止 watcher 与订阅。
func (a *ResourceAuthority) SetWorkspaceAvailable(workspaceID string, available bool) {
	a.fileStream.SetWorkspaceAvailable(workspaceID, available)
}

// InvalidateGitStatus 发布不含文件内容的显式 Git 状态失效提示。
func (a *ResourceAuthority) InvalidateGitStatus(workspaceID string) {
	a.fileStream.publish(workspaceID, workspaceapi.FileEventGitStatus, "", time.Now().UTC())
}

// Subscribe 创建单 Workspace 订阅。
func (s *FileStream) Subscribe(ctx context.Context, ws workspaceapi.WorkspaceRef, after int64) (*workspaceapi.FileSubscription, error) {
	if after < 0 {
		return nil, commandError("文件事件 cursor 不能为负数", nil)
	}
	if err := s.ensureWatch(ws); err != nil {
		return nil, err
	}

	s.mu.Lock()
	state := s.workspaces[ws.WorkspaceID]
	if !state.available {
		s.mu.Unlock()
		return nil, unavailableFileStreamError()
	}
	if after > state.seq {
		s.mu.Unlock()
		return nil, commandError("文件事件 cursor 超过当前序号", nil)
	}
	if len(state.journal) > 0 && after < state.journal[0].Seq-1 {
		s.mu.Unlock()
		return nil, &workspaceapi.Error{Code: workspaceapi.ErrorCursorExpired, Message: "文件事件游标已过期，请重新加载目录"}
	}
	replay := make([]workspaceapi.FileEvent, 0)
	for _, event := range state.journal {
		if event.Seq > after {
			replay = append(replay, event)
		}
	}
	id := uuid.NewString()
	subscriber := &fileSubscriber{
		events: make(chan workspaceapi.FileEvent, s.subscriberBuffer),
		done:   make(chan error, 1),
	}
	state.subscribers[id] = subscriber
	s.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() { s.removeSubscriber(ws.WorkspaceID, id, nil) })
	}
	if done := ctx.Done(); done != nil {
		go func() {
			<-done
			cancel()
		}()
	}
	s.log.Info("文件事件订阅已建立", "machine_id", ws.MachineID, "workspace_id", ws.WorkspaceID,
		"after_seq", after, "replay_count", len(replay))
	return workspaceapi.NewFileSubscription(replay, subscriber.events, subscriber.done, cancel), nil
}

// SetWorkspaceAvailable 标记 Workspace 可用性。
func (s *FileStream) SetWorkspaceAvailable(workspaceID string, available bool) {
	s.mu.Lock()
	state := s.workspaces[workspaceID]
	if state == nil {
		state = &fileWorkspaceStream{available: available, subscribers: make(map[string]*fileSubscriber)}
		s.workspaces[workspaceID] = state
	}
	state.available = available
	if available {
		s.mu.Unlock()
		return
	}
	cancelWatch := state.cancelWatch
	state.cancelWatch = nil
	state.watchID = ""
	for id, subscriber := range state.subscribers {
		s.closeSubscriberLocked(subscriber, unavailableFileStreamError())
		delete(state.subscribers, id)
	}
	s.mu.Unlock()
	if cancelWatch != nil {
		cancelWatch()
	}
	s.log.Info("文件事件流因工作区不可用终止", "workspace_id", workspaceID)
}

func (s *FileStream) ensureWatch(ws workspaceapi.WorkspaceRef) error {
	s.mu.Lock()
	state := s.workspaces[ws.WorkspaceID]
	if state == nil {
		state = &fileWorkspaceStream{available: true, subscribers: make(map[string]*fileSubscriber)}
		s.workspaces[ws.WorkspaceID] = state
	}
	if !state.available {
		s.mu.Unlock()
		return unavailableFileStreamError()
	}
	if state.cancelWatch != nil {
		if state.rootPath != ws.RootPath {
			s.mu.Unlock()
			return commandError("Workspace root 在活动订阅期间发生变化", nil)
		}
		s.mu.Unlock()
		return nil
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		s.mu.Unlock()
		return &workspaceapi.Error{Code: workspaceapi.ErrorUnavailable, Message: "无法启动文件监听", Retryable: true, Cause: err}
	}
	if err := addWatchTree(watcher, ws.RootPath); err != nil {
		watcher.Close()
		s.mu.Unlock()
		return &workspaceapi.Error{Code: workspaceapi.ErrorUnavailable, Message: "无法监听工作区", Retryable: true, Cause: err}
	}
	watchCtx, cancel := context.WithCancel(context.Background())
	watchID := uuid.NewString()
	state.rootPath = ws.RootPath
	state.cancelWatch = cancel
	state.watchID = watchID
	s.mu.Unlock()
	go s.watchWorkspace(watchCtx, watcher, ws, watchID)
	return nil
}

func addWatchTree(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return watcher.Add(name)
		}
		return nil
	})
}

func (s *FileStream) watchWorkspace(ctx context.Context, watcher *fsnotify.Watcher, ws workspaceapi.WorkspaceRef, watchID string) {
	defer func() {
		watcher.Close()
		s.mu.Lock()
		if state := s.workspaces[ws.WorkspaceID]; state != nil && state.watchID == watchID {
			state.cancelWatch = nil
			state.watchID = ""
		}
		s.mu.Unlock()
	}()
	pending := make(map[string]workspaceapi.FileEventKind)
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	var timerC <-chan time.Time
	flush := func() {
		paths := make([]string, 0, len(pending))
		for name := range pending {
			paths = append(paths, name)
		}
		sort.Strings(paths)
		for _, name := range paths {
			s.publish(ws.WorkspaceID, pending[name], name, time.Now().UTC())
		}
		clear(pending)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-timerC:
			flush()
			timerC = nil
		case err, ok := <-watcher.Errors:
			if ok {
				s.log.Warn("文件 watcher 错误", "machine_id", ws.MachineID, "workspace_id", ws.WorkspaceID, "cause", err)
			}
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			relative, err := filepath.Rel(ws.RootPath, event.Name)
			if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue
			}
			relative = filepath.ToSlash(relative)
			if ignoredWatchPath(relative) {
				continue
			}
			if event.Op&fsnotify.Create != 0 {
				if info, statErr := os.Stat(event.Name); statErr == nil && info.IsDir() {
					_ = addWatchTree(watcher, event.Name)
				}
			}
			kind := watchEventKind(event.Op)
			if kind == "" {
				continue
			}
			pending[relative] = mergeWatchKind(pending[relative], kind)
			if timer == nil {
				timer = time.NewTimer(s.coalesceWindow)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(s.coalesceWindow)
			}
			timerC = timer.C
		}
	}
}

func watchEventKind(op fsnotify.Op) workspaceapi.FileEventKind {
	switch {
	case op&(fsnotify.Remove|fsnotify.Rename) != 0:
		return workspaceapi.FileEventRemove
	case op&fsnotify.Create != 0:
		return workspaceapi.FileEventCreate
	case op&(fsnotify.Write|fsnotify.Chmod) != 0:
		return workspaceapi.FileEventModify
	default:
		return ""
	}
}

func mergeWatchKind(previous, next workspaceapi.FileEventKind) workspaceapi.FileEventKind {
	if previous == workspaceapi.FileEventCreate && next == workspaceapi.FileEventModify {
		return previous
	}
	if next == workspaceapi.FileEventRemove {
		return next
	}
	return next
}

func ignoredWatchPath(relative string) bool {
	for _, segment := range strings.Split(relative, "/") {
		if segment == ".git" || strings.HasPrefix(segment, ".handoff-save-") {
			return true
		}
	}
	return false
}

func (s *FileStream) publish(workspaceID string, kind workspaceapi.FileEventKind, relativePath string, observedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.workspaces[workspaceID]
	if state == nil {
		state = &fileWorkspaceStream{available: true, subscribers: make(map[string]*fileSubscriber)}
		s.workspaces[workspaceID] = state
	}
	if !state.available {
		return
	}
	state.seq++
	event := workspaceapi.FileEvent{WorkspaceID: workspaceID, Seq: state.seq, Kind: kind, Path: relativePath, ObservedAt: observedAt.UTC()}
	state.journal = append(state.journal, event)
	if len(state.journal) > s.journalLimit {
		state.journal = append([]workspaceapi.FileEvent(nil), state.journal[len(state.journal)-s.journalLimit:]...)
	}
	for id, subscriber := range state.subscribers {
		select {
		case subscriber.events <- event:
		default:
			s.closeSubscriberLocked(subscriber, ErrFileStreamOverflow)
			delete(state.subscribers, id)
			s.log.Warn("文件事件慢订阅者已断开", "workspace_id", workspaceID, "through_seq", state.seq)
		}
	}
}

func (s *FileStream) removeSubscriber(workspaceID, id string, reason error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.workspaces[workspaceID]
	if state == nil {
		return
	}
	if subscriber, ok := state.subscribers[id]; ok {
		s.closeSubscriberLocked(subscriber, reason)
		delete(state.subscribers, id)
		s.log.Info("文件事件订阅已释放", "workspace_id", workspaceID)
	}
}

func (s *FileStream) closeSubscriberLocked(subscriber *fileSubscriber, reason error) {
	if subscriber.closed {
		return
	}
	subscriber.closed = true
	if reason != nil {
		subscriber.done <- reason
	}
	close(subscriber.events)
	close(subscriber.done)
}

func unavailableFileStreamError() error {
	return &workspaceapi.Error{Code: workspaceapi.ErrorUnavailable, Message: "工作区当前不可用", Retryable: true}
}
