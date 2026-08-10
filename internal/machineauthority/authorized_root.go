// machineauthority AuthorizedRoot 封装 Go os.Root 的 Workspace 文件边界。
//
// 职责：
//   - 在进入 os.Root 前拒绝非规范 relative slash path
//   - 把越界、缺失等底层错误映射成 workspaceapi typed error
//   - 为文件实现提供不会跟随越界 symlink 的根内操作
//
// 边界：
//   - 不做文件版本、搜索、写入或业务校验
//   - 不暴露 owner absolute root 给 wire DTO
package machineauthority

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/xushixin/handoff/internal/workspaceapi"
)

// AuthorizedRoot 是一个已打开的 Workspace 根目录句柄。
type AuthorizedRoot struct {
	root *os.Root
}

// OpenAuthorizedRoot 打开 Workspace 根目录。
func OpenAuthorizedRoot(rootPath string) (*AuthorizedRoot, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, &workspaceapi.Error{Code: workspaceapi.ErrorUnavailable, Message: "工作区根目录不可用", Retryable: true, Cause: err}
	}
	return &AuthorizedRoot{root: root}, nil
}

// Close 关闭底层根目录句柄。
func (r *AuthorizedRoot) Close() error { return r.root.Close() }

// clean 校验 wire relative path，并转换 os.Root 使用的根路径“.”。
func (r *AuthorizedRoot) clean(relativePath string) (string, error) {
	if relativePath == "" {
		return ".", nil
	}
	// fs.ValidPath 同时拒绝 absolute、空 segment、.、..；显式拒绝反斜杠，
	// 保证 Windows 与 Unix 都只接受 wire 约定的 slash path。
	if strings.ContainsRune(relativePath, 0) || strings.Contains(relativePath, `\`) || !fs.ValidPath(relativePath) {
		return "", pathError("路径必须是规范的 Workspace-relative slash path", nil)
	}
	return relativePath, nil
}

// ReadFile 读取授权根内文件。
func (r *AuthorizedRoot) ReadFile(relativePath string) ([]byte, error) {
	name, err := r.clean(relativePath)
	if err != nil {
		return nil, err
	}
	data, err := r.root.ReadFile(name)
	if err != nil {
		return nil, classifyRootError(err)
	}
	return data, nil
}

func (r *AuthorizedRoot) stat(relativePath string) (fs.FileInfo, error) {
	name, err := r.clean(relativePath)
	if err != nil {
		return nil, err
	}
	info, err := r.root.Stat(name)
	if err != nil {
		return nil, classifyRootError(err)
	}
	return info, nil
}

func (r *AuthorizedRoot) lstat(relativePath string) (fs.FileInfo, error) {
	name, err := r.clean(relativePath)
	if err != nil {
		return nil, err
	}
	info, err := r.root.Lstat(name)
	if err != nil {
		return nil, classifyRootError(err)
	}
	return info, nil
}

func (r *AuthorizedRoot) open(relativePath string) (*os.File, error) {
	name, err := r.clean(relativePath)
	if err != nil {
		return nil, err
	}
	file, err := r.root.Open(name)
	if err != nil {
		return nil, classifyRootError(err)
	}
	return file, nil
}

func (r *AuthorizedRoot) resolveFinalSymlink(relativePath string) (string, error) {
	name, err := r.clean(relativePath)
	if err != nil {
		return "", err
	}
	for range 32 {
		info, statErr := r.root.Lstat(name)
		if statErr != nil {
			return "", classifyRootError(statErr)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return name, nil
		}
		target, readErr := r.root.Readlink(name)
		if readErr != nil {
			return "", classifyRootError(readErr)
		}
		if path.IsAbs(target) {
			return "", pathError("符号链接越出工作区", nil)
		}
		name = path.Clean(path.Join(path.Dir(name), target))
		if name == ".." || strings.HasPrefix(name, "../") || !fs.ValidPath(name) {
			return "", pathError("符号链接越出工作区", nil)
		}
	}
	return "", pathError("符号链接层级过深", nil)
}

func classifyRootError(err error) error {
	var resourceErr *workspaceapi.Error
	if errors.As(err, &resourceErr) {
		return err
	}
	if errors.Is(err, fs.ErrNotExist) {
		return &workspaceapi.Error{Code: workspaceapi.ErrorResourceNotFound, Message: "文件资源不存在", Cause: err}
	}
	// os.Root 的关键职责是把 symlink/rename traversal 拒绝在内核边界；这类错误
	// 不向外暴露宿主路径或底层文本，统一成 PATH_OUTSIDE_WORKSPACE。
	return pathError("路径不在工作区授权范围内", err)
}

func pathError(message string, cause error) error {
	return &workspaceapi.Error{Code: workspaceapi.ErrorPathOutsideWorkspace, Message: message, Cause: cause}
}
