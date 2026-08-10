// machineauthority Workspace 文件读取与原子写实现。
//
// 职责：
//   - 列出目录、读取 base64 内容并计算内容 SHA-256 version
//   - 校验 if_match/create_only，使用同目录临时文件 + fsync + rename 保存
//   - 把 PTY 生命周期委托给同机 ptyservice
//   - 返回只含 Workspace-relative path 的 workspaceapi 结果
//
// 边界：
//   - 每次操作都重新从 WorkspaceRef.RootPath 打开 os.Root 做 owner 端最终授权
//   - 不实现 HTTP/peer 路由；不记录 content_base64 或 terminal bytes
package machineauthority

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/xushixin/handoff/internal/ptyservice"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

type searchLimits struct {
	maxResults   int
	perFileBytes int64
	totalBytes   int64
}

var defaultSearchLimits = searchLimits{maxResults: 200, perFileBytes: 2 << 20, totalBytes: 64 << 20}

// ResourceAuthority 是本机 Workspace 文件资源的 owner authority。
type ResourceAuthority struct {
	log             *slog.Logger
	searchLimits    searchLimits
	fileStream      *FileStream
	gitStatusRunner gitStatusRunner
	terminal        *ptyservice.Service
	// beforeRename 是原子边界故障注入 seam；生产保持 nil，仅测试清理语义。
	beforeRename func() error
}

// SetTerminalService 注入 owner PTY 会话服务；nil 时明确 capability unsupported。
func (a *ResourceAuthority) SetTerminalService(service *ptyservice.Service) {
	a.terminal = service
}

// SetTerminalOutboxNotifier 注入 PTY 状态事件的本机 outbox 快速唤醒器。
func (a *ResourceAuthority) SetTerminalOutboxNotifier(notify func()) {
	if a.terminal != nil {
		a.terminal.SetOutboxNotifier(notify)
	}
}

// NewResourceAuthority 创建本机文件资源权威。
func NewResourceAuthority(log *slog.Logger) *ResourceAuthority {
	if log == nil {
		log = slog.Default()
	}
	return &ResourceAuthority{log: log, searchLimits: defaultSearchLimits, fileStream: NewFileStream(log), gitStatusRunner: runGitStatusCommand}
}

// ListDirectory 列出授权目录的一层内容。
func (a *ResourceAuthority) ListDirectory(_ context.Context, ws workspaceapi.WorkspaceRef, relativePath string) ([]workspaceapi.FileEntry, error) {
	started := time.Now()
	root, err := OpenAuthorizedRoot(ws.RootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	cleanPath, err := root.clean(relativePath)
	if err != nil {
		return nil, err
	}
	dir, err := root.open(relativePath)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil {
		return nil, classifyRootError(err)
	}
	if !info.IsDir() {
		return nil, commandError("目标不是目录", nil)
	}
	children, err := dir.ReadDir(-1)
	if err != nil {
		return nil, classifyRootError(err)
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	entries := make([]workspaceapi.FileEntry, 0, len(children))
	for _, child := range children {
		childPath := child.Name()
		if cleanPath != "." {
			childPath = path.Join(cleanPath, child.Name())
		}
		childInfo, statErr := root.lstat(childPath)
		if statErr != nil {
			return nil, statErr
		}
		kind := workspaceapi.FileKindFile
		switch {
		case childInfo.Mode()&os.ModeSymlink != 0:
			kind = workspaceapi.FileKindSymlink
		case childInfo.IsDir():
			kind = workspaceapi.FileKindDirectory
		}
		entries = append(entries, workspaceapi.FileEntry{
			WorkspaceID: ws.WorkspaceID, Path: childPath, Name: child.Name(), Kind: kind,
			Size: childInfo.Size(), ModifiedAt: childInfo.ModTime().UTC(),
		})
	}
	a.log.Info("工作区目录浏览完成", "machine_id", ws.MachineID, "workspace_id", ws.WorkspaceID,
		"relative_path", relativePath, "entry_count", len(entries), "elapsed_ms", time.Since(started).Milliseconds())
	return entries, nil
}

// ReadFile 读取授权根内普通文件并返回内容版本。
func (a *ResourceAuthority) ReadFile(_ context.Context, ws workspaceapi.WorkspaceRef, relativePath string) (workspaceapi.FileDocument, error) {
	started := time.Now()
	root, err := OpenAuthorizedRoot(ws.RootPath)
	if err != nil {
		return workspaceapi.FileDocument{}, err
	}
	defer root.Close()
	doc, err := readFileDocument(root, ws.WorkspaceID, relativePath)
	if err != nil {
		return workspaceapi.FileDocument{}, err
	}
	a.log.Info("工作区文件读取完成", "machine_id", ws.MachineID, "workspace_id", ws.WorkspaceID,
		"relative_path", relativePath, "version", doc.Version, "size", doc.Size,
		"elapsed_ms", time.Since(started).Milliseconds())
	return doc, nil
}

func readFileDocument(root *AuthorizedRoot, workspaceID, relativePath string) (workspaceapi.FileDocument, error) {
	info, err := root.stat(relativePath)
	if err != nil {
		return workspaceapi.FileDocument{}, err
	}
	if !info.Mode().IsRegular() {
		return workspaceapi.FileDocument{}, commandError("目标不是普通文件", nil)
	}
	content, err := root.ReadFile(relativePath)
	if err != nil {
		return workspaceapi.FileDocument{}, err
	}
	return workspaceapi.FileDocument{
		WorkspaceID: workspaceID, Path: relativePath, Version: contentVersion(content),
		ContentBase64: base64.StdEncoding.EncodeToString(content), Size: int64(len(content)),
		ModifiedAt: info.ModTime().UTC(),
	}, nil
}

// WriteFile 以乐观锁版本执行同目录原子保存。
func (a *ResourceAuthority) WriteFile(_ context.Context, ws workspaceapi.WorkspaceRef, command workspaceapi.WriteFileCommand) (workspaceapi.FileDocument, error) {
	started := time.Now()
	root, err := OpenAuthorizedRoot(ws.RootPath)
	if err != nil {
		return workspaceapi.FileDocument{}, err
	}
	defer root.Close()
	cleanPath, err := root.clean(command.Path)
	if err != nil || cleanPath == "." {
		if err != nil {
			return workspaceapi.FileDocument{}, err
		}
		return workspaceapi.FileDocument{}, commandError("不能把工作区根目录写成文件", nil)
	}
	content, err := base64.StdEncoding.Strict().DecodeString(command.ContentBase64)
	if err != nil {
		return workspaceapi.FileDocument{}, commandError("content_base64 非法", err)
	}

	targetPath := cleanPath
	mode := fs.FileMode(0o644)
	if command.CreateOnly {
		if _, statErr := root.stat(command.Path); statErr == nil {
			return workspaceapi.FileDocument{}, commandError("目标文件已存在", nil)
		} else if !isResourceCode(statErr, workspaceapi.ErrorResourceNotFound) {
			return workspaceapi.FileDocument{}, statErr
		}
	} else {
		if command.IfMatch == "" {
			return workspaceapi.FileDocument{}, commandError("普通保存必须提供 if_match", nil)
		}
		current, readErr := readFileDocument(root, ws.WorkspaceID, command.Path)
		if readErr != nil {
			return workspaceapi.FileDocument{}, readErr
		}
		if current.Version != command.IfMatch {
			a.log.Warn("工作区文件版本冲突", "machine_id", ws.MachineID, "workspace_id", ws.WorkspaceID,
				"relative_path", command.Path, "expected_version", command.IfMatch, "actual_version", current.Version)
			return workspaceapi.FileDocument{}, &workspaceapi.Error{Code: workspaceapi.ErrorVersionConflict, Message: "文件已被外部修改，请重新加载后再保存"}
		}
		targetPath, err = root.resolveFinalSymlink(command.Path)
		if err != nil {
			return workspaceapi.FileDocument{}, err
		}
		info, statErr := root.stat(targetPath)
		if statErr != nil {
			return workspaceapi.FileDocument{}, statErr
		}
		mode = info.Mode().Perm()
	}
	if err := a.atomicWrite(root, targetPath, content, mode, command.CreateOnly); err != nil {
		return workspaceapi.FileDocument{}, err
	}
	doc, err := readFileDocument(root, ws.WorkspaceID, command.Path)
	if err != nil {
		return workspaceapi.FileDocument{}, err
	}
	a.log.Info("工作区文件原子保存完成", "machine_id", ws.MachineID, "workspace_id", ws.WorkspaceID,
		"relative_path", command.Path, "command_id", command.CommandID, "version", doc.Version,
		"size", doc.Size, "elapsed_ms", time.Since(started).Milliseconds())
	return doc, nil
}

func (a *ResourceAuthority) atomicWrite(root *AuthorizedRoot, target string, content []byte, mode fs.FileMode, createOnly bool) (err error) {
	parent := path.Dir(target)
	temp := path.Join(parent, ".handoff-save-"+uuid.NewString()+".tmp")
	file, err := root.root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return classifyRootError(err)
	}
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = root.root.Remove(temp)
		}
	}()
	if _, err = file.Write(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("写入原子临时文件: %w", err)
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("同步原子临时文件: %w", err)
	}
	if err = file.Close(); err != nil {
		return fmt.Errorf("关闭原子临时文件: %w", err)
	}
	if a.beforeRename != nil {
		if err = a.beforeRename(); err != nil {
			return fmt.Errorf("原子替换前检查: %w", err)
		}
	}
	if createOnly {
		// Link 是不覆盖创建：若目标在检查后被别的写者创建，此处会原子失败，
		// 避免 create_only 因 TOCTOU 退化成覆盖写。
		if err = root.root.Link(temp, target); err != nil {
			return commandError("目标文件已存在或无法创建", err)
		}
		if err = root.root.Remove(temp); err != nil {
			return fmt.Errorf("移除已链接临时文件: %w", err)
		}
		keepTemp = false
	} else {
		if err = root.root.Rename(temp, target); err != nil {
			return classifyRootError(err)
		}
		keepTemp = false
	}
	dir, err := root.root.Open(parent)
	if err != nil {
		return classifyRootError(err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("同步文件目录: %w", err)
	}
	return nil
}

func contentVersion(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func commandError(message string, cause error) error {
	return &workspaceapi.Error{Code: workspaceapi.ErrorCommandConflict, Message: message, Cause: cause}
}

func isResourceCode(err error, code workspaceapi.ErrorCode) bool {
	var resourceErr *workspaceapi.Error
	return errors.As(err, &resourceErr) && resourceErr.Code == code
}

// CreateTerminal 在 owner Workspace 下创建幂等登录 shell。
func (a *ResourceAuthority) CreateTerminal(ctx context.Context, ws workspaceapi.WorkspaceRef,
	command workspaceapi.CreateTerminalCommand) (workspaceapi.PtySession, error) {
	if a.terminal == nil {
		return workspaceapi.PtySession{}, &workspaceapi.Error{Code: workspaceapi.ErrorCapabilityUnsupported, Message: "PTY 资源能力尚未接入"}
	}
	return a.terminal.Create(ctx, ws, command)
}

// GetTerminal 读取 owner PTY session 元数据，不创建新 shell。
func (a *ResourceAuthority) GetTerminal(ctx context.Context, sessionID string) (workspaceapi.PtySession, error) {
	if a.terminal == nil {
		return workspaceapi.PtySession{}, &workspaceapi.Error{Code: workspaceapi.ErrorCapabilityUnsupported, Message: "PTY 资源能力尚未接入"}
	}
	return a.terminal.Get(ctx, sessionID)
}

// ConnectTerminal 连接 owner PTY 的 replay + live 双向流。
func (a *ResourceAuthority) ConnectTerminal(ctx context.Context, sessionID, incarnation string,
	after int64) (*workspaceapi.PtySubscription, error) {
	if a.terminal == nil {
		return nil, &workspaceapi.Error{Code: workspaceapi.ErrorCapabilityUnsupported, Message: "PTY 资源能力尚未接入"}
	}
	return a.terminal.Connect(ctx, sessionID, incarnation, after)
}

// CloseTerminal 显式终止 owner PTY，保留 metadata。
func (a *ResourceAuthority) CloseTerminal(ctx context.Context, sessionID, incarnation string) (workspaceapi.PtySession, error) {
	if a.terminal == nil {
		return workspaceapi.PtySession{}, &workspaceapi.Error{Code: workspaceapi.ErrorCapabilityUnsupported, Message: "PTY 资源能力尚未接入"}
	}
	return a.terminal.CloseTerminal(ctx, sessionID, incarnation)
}

// CreatePreview 在 Task 5 接入。
func (a *ResourceAuthority) CreatePreview(context.Context, workspaceapi.WorkspaceRef, workspaceapi.CreatePreviewCommand) (workspaceapi.PreviewSession, error) {
	return workspaceapi.PreviewSession{}, &workspaceapi.Error{Code: workspaceapi.ErrorCapabilityUnsupported, Message: "Preview 资源能力尚未接入"}
}
