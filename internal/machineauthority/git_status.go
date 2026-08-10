// machineauthority Workspace Git 基础状态读取。
//
// 职责：
//   - 有界执行 Git 2.25 兼容的 status porcelain v2 命令
//   - 解析 branch/head/upstream/ahead/behind 与文件 XY 状态
//   - 非 Git Workspace 返回 is_repository=false 的可识别空快照
//
// 边界：
//   - 只读，不提供 staging/commit/PR，不经 shell 拼接参数
//   - stdout/stderr/耗时均有上限；日志不记录 diff 或文件内容
package machineauthority

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/workspaceapi"
)

const (
	gitStatusTimeout     = 10 * time.Second
	gitStatusOutputLimit = 16 << 20
	gitStderrTailLimit   = 4096
)

var errGitOutputLimit = errors.New("git status 输出超过上限")

type gitStatusRunner func(context.Context, string, []string) ([]byte, error)

// GitStatus 返回 Workspace 的只读 Git 基础状态。
func (a *ResourceAuthority) GitStatus(ctx context.Context, ws workspaceapi.WorkspaceRef) (workspaceapi.GitStatusSnapshot, error) {
	started := time.Now()
	empty := workspaceapi.GitStatusSnapshot{WorkspaceID: ws.WorkspaceID, Entries: []workspaceapi.GitStatusEntry{}}
	root, err := OpenAuthorizedRoot(ws.RootPath)
	if err != nil {
		return empty, err
	}
	defer root.Close()
	if _, err := root.lstat(".git"); err != nil {
		if isResourceCode(err, workspaceapi.ErrorResourceNotFound) {
			a.log.Info("Workspace 非 Git 仓库", "machine_id", ws.MachineID, "workspace_id", ws.WorkspaceID,
				"elapsed_ms", time.Since(started).Milliseconds())
			return empty, nil
		}
		return empty, err
	}
	args := []string{"status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all"}
	raw, err := a.gitStatusRunner(ctx, ws.RootPath, args)
	if err != nil {
		return empty, err
	}
	status, err := parseGitStatusV2(ws.WorkspaceID, raw)
	if err != nil {
		return empty, fmt.Errorf("解析 Git porcelain v2: %w", err)
	}
	a.log.Info("Workspace Git 状态读取完成", "machine_id", ws.MachineID, "workspace_id", ws.WorkspaceID,
		"branch", status.Branch, "head_oid", status.HeadOID, "entry_count", len(status.Entries),
		"elapsed_ms", time.Since(started).Milliseconds())
	return status, nil
}

func runGitStatusCommand(parent context.Context, dir string, args []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, gitStatusTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	stdout := &boundedGitBuffer{limit: gitStatusOutputLimit}
	stderr := &tailBuffer{limit: gitStderrTailLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("git status 超时 %s: %w", gitStatusTimeout, ctx.Err())
	}
	if stdout.exceeded {
		return nil, errGitOutputLimit
	}
	if err != nil {
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		return nil, fmt.Errorf("git status exit=%d stderr_tail=%q: %w", exitCode, strings.TrimSpace(stderr.String()), err)
	}
	return stdout.Bytes(), nil
}

type boundedGitBuffer struct {
	buf      bytes.Buffer
	limit    int
	exceeded bool
}

func (b *boundedGitBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		chunk := p
		if len(chunk) > remaining {
			chunk = chunk[:remaining]
		}
		_, _ = b.buf.Write(chunk)
	}
	if len(p) > remaining {
		b.exceeded = true
		return len(p), errGitOutputLimit
	}
	return len(p), nil
}

func (b *boundedGitBuffer) Bytes() []byte { return append([]byte(nil), b.buf.Bytes()...) }

type tailBuffer struct {
	buf   []byte
	limit int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.limit {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-b.limit:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string { return string(b.buf) }

func parseGitStatusV2(workspaceID string, raw []byte) (workspaceapi.GitStatusSnapshot, error) {
	status := workspaceapi.GitStatusSnapshot{WorkspaceID: workspaceID, IsRepository: true, Entries: []workspaceapi.GitStatusEntry{}}
	records := bytes.Split(raw, []byte{0})
	for i := 0; i < len(records); i++ {
		if len(records[i]) == 0 {
			continue
		}
		recordBytes := records[i]
		// Git 在 -z 模式下用 NUL 结束 header；测试 fixture/部分旧版本也可能
		// 把多条 header 以 LF 放在同一 token。只在 token 以 "# " 开头时拆 LF，
		// 文件 path 自身的换行仍作为 record 原样保留。
		for len(recordBytes) > 0 && recordBytes[0] == '#' {
			if end := bytes.IndexByte(recordBytes, '\n'); end >= 0 {
				parseGitHeader(&status, string(recordBytes[:end]))
				recordBytes = recordBytes[end+1:]
				continue
			}
			parseGitHeader(&status, string(recordBytes))
			recordBytes = nil
		}
		if len(recordBytes) == 0 {
			continue
		}
		record := string(recordBytes)
		switch record[0] {
		case '1':
			fields := strings.SplitN(record, " ", 9)
			if len(fields) != 9 || len(fields[1]) != 2 {
				return status, fmt.Errorf("ordinary record 非法")
			}
			status.Entries = append(status.Entries, gitEntry(fields[8], "", fields[1]))
		case '2':
			fields := strings.SplitN(record, " ", 10)
			if len(fields) != 10 || len(fields[1]) != 2 || i+1 >= len(records) {
				return status, fmt.Errorf("rename record 非法")
			}
			i++
			status.Entries = append(status.Entries, gitEntry(fields[9], string(records[i]), fields[1]))
		case 'u':
			fields := strings.SplitN(record, " ", 11)
			if len(fields) != 11 || len(fields[1]) != 2 {
				return status, fmt.Errorf("unmerged record 非法")
			}
			status.Entries = append(status.Entries, gitEntry(fields[10], "", fields[1]))
		case '?':
			if len(record) < 3 {
				return status, fmt.Errorf("untracked record 非法")
			}
			status.Entries = append(status.Entries, gitEntry(record[2:], "", "??"))
		case '!':
			// ignored 不属于基础状态结果。
		default:
			return status, fmt.Errorf("未知 porcelain record kind %q", record[0])
		}
	}
	return status, nil
}

func parseGitHeader(status *workspaceapi.GitStatusSnapshot, header string) {
	key, value, ok := strings.Cut(strings.TrimPrefix(header, "# "), " ")
	if !ok {
		return
	}
	switch key {
	case "branch.oid":
		if value != "(initial)" {
			status.HeadOID = value
		}
	case "branch.head":
		if value != "(detached)" {
			status.Branch = value
		}
	case "branch.upstream":
		status.Upstream = value
	case "branch.ab":
		parts := strings.Fields(value)
		if len(parts) == 2 {
			status.Ahead, _ = strconv.Atoi(strings.TrimPrefix(parts[0], "+"))
			status.Behind, _ = strconv.Atoi(strings.TrimPrefix(parts[1], "-"))
		}
	}
}

func gitEntry(path, originalPath, xy string) workspaceapi.GitStatusEntry {
	return workspaceapi.GitStatusEntry{Path: path, OriginalPath: originalPath,
		IndexStatus: xy[:1], WorktreeStatus: xy[1:]}
}
