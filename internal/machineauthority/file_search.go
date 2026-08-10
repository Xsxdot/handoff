// machineauthority Workspace 文件 literal 搜索。
//
// 职责：
//   - 在 os.Root 授权树内按文本行做 literal 搜索
//   - 跳过 .git、二进制与超大文件，并限制结果数与总扫描字节
//   - 返回 relative path/line/column/preview 与 truncated 摘要
//
// 边界：
//   - 不执行正则或 shell；不跟随目录 symlink 递归
//   - 日志不记录 query、preview 或文件内容
package machineauthority

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xushixin/handoff/internal/workspaceapi"
)

var errStopSearch = errors.New("搜索达到有界上限")

// SearchFiles 在授权根内执行有界 literal 搜索。
func (a *ResourceAuthority) SearchFiles(ctx context.Context, ws workspaceapi.WorkspaceRef, command workspaceapi.SearchFilesCommand) (workspaceapi.FileSearchResult, error) {
	started := time.Now()
	result := workspaceapi.FileSearchResult{WorkspaceID: ws.WorkspaceID, Matches: []workspaceapi.FileSearchMatch{}}
	if command.Query == "" {
		return result, commandError("搜索 query 不能为空", nil)
	}
	root, err := OpenAuthorizedRoot(ws.RootPath)
	if err != nil {
		return result, err
	}
	defer root.Close()
	start, err := root.clean(command.Path)
	if err != nil {
		return result, err
	}
	maxResults := command.MaxResults
	if maxResults <= 0 || maxResults > a.searchLimits.maxResults {
		maxResults = a.searchLimits.maxResults
	}

	err = fs.WalkDir(root.root.FS(), start, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return classifyRootError(walkErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if entry.IsDir() && path.Base(name) == ".git" {
			return fs.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		info, statErr := root.root.Stat(name)
		if statErr != nil || !info.Mode().IsRegular() {
			return nil
		}
		if info.Size() > a.searchLimits.perFileBytes {
			return nil
		}
		if result.ScannedBytes+info.Size() > a.searchLimits.totalBytes {
			result.Truncated = true
			return errStopSearch
		}
		content, readErr := root.root.ReadFile(name)
		if readErr != nil {
			return classifyRootError(readErr)
		}
		if bytes.IndexByte(content, 0) >= 0 || !utf8.Valid(content) {
			return nil
		}
		result.ScannedFiles++
		result.ScannedBytes += int64(len(content))
		for lineIndex, line := range strings.Split(string(content), "\n") {
			column := strings.Index(line, command.Query)
			if column < 0 {
				continue
			}
			preview := line
			if len(preview) > 500 {
				preview = preview[:500]
			}
			result.Matches = append(result.Matches, workspaceapi.FileSearchMatch{
				Path: name, Line: lineIndex + 1, Column: column + 1, Preview: preview,
			})
			if len(result.Matches) >= maxResults {
				result.Truncated = true
				return errStopSearch
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopSearch) {
		return result, err
	}
	a.log.Info("工作区文件搜索完成", "machine_id", ws.MachineID, "workspace_id", ws.WorkspaceID,
		"relative_path", command.Path, "match_count", len(result.Matches),
		"scanned_files", result.ScannedFiles, "scanned_bytes", result.ScannedBytes,
		"truncated", result.Truncated, "elapsed_ms", time.Since(started).Milliseconds())
	return result, nil
}
