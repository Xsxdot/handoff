// 本文件是 repos 表（执行机 × 仓库登记）的持久化实现。
//
// 职责：
//   - repos 表的增（CreateRepo）、查（GetRepoByName / ListRepos）、删（DeleteRepo）
//   - 把 SQLite 的 UNIQUE 冲突翻译成 ErrRepoDuplicate 哨兵，供上层映射 409
//   - ActiveTasksByRepoPath：按仓库路径查活跃任务，供注销登记前的占用校验
//
// 边界：
//   - 不判断路径是否真的存在、是不是 git 仓库——那是 agentd 侧 EnsureRepoUsable 的事
//   - 不做名字派生、不做 URL 归一化——那是 agentd/reporegistry.go 的事
//   - 与 store.go 一致的叶子层纪律：方法错误 return 前不打日志，由调用方带上下文记录
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/xushixin/handoff/internal/proto"
)

// ErrRepoDuplicate 表示登记冲突：名字已被占用，或该路径已被另一条登记指向。
//
// 为什么路径也算冲突：两个名字指向同一路径会让 origin 自动匹配产生假歧义，
// 也会让「注销前检查占用」漏掉另一个名字下的活跃任务。
var ErrRepoDuplicate = errors.New("仓库登记冲突（名字或路径已存在）")

// repoColumns 是 repos 表的完整读取列清单，Get 与 List 共用同一份。
const repoColumns = `name, path, origin_url, created_at`

// scanRepoRow 把一行 repos 记录读成 proto.Repo。
func scanRepoRow(sc rowScanner) (proto.Repo, error) {
	var (
		r         proto.Repo
		createdAt string
	)
	if err := sc.Scan(&r.Name, &r.Path, &r.OriginURL, &createdAt); err != nil {
		return proto.Repo{}, err
	}
	r.CreatedAt = parseTime(createdAt)
	return r, nil
}

// CreateRepo 写入一条仓库登记。
//
// 参数：
//   - r: 登记条目；Name/Path/OriginURL 必须非空，CreatedAt 由调用方给定
//
// 返回：
//   - 错误：名字或路径已存在时返回包装了 ErrRepoDuplicate 的错误；其余为写库故障
func (s *Store) CreateRepo(r *proto.Repo) error {
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO repos (name, path, origin_url, created_at) VALUES (?, ?, ?, ?)`,
		r.Name, r.Path, r.OriginURL, fmtTime(r.CreatedAt))
	if err != nil {
		// modernc.org/sqlite 的唯一约束错误文本形如
		// "constraint failed: UNIQUE constraint failed: repos.name (2067)"，
		// 没有可用的错误码常量，只能按文本判定。
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("%w: name=%s path=%s: %v", ErrRepoDuplicate, r.Name, r.Path, err)
		}
		return fmt.Errorf("写入仓库登记 %s: %w", r.Name, err)
	}
	return nil
}

// GetRepoByName 按登记名查询单条登记。
//
// 返回：
//   - 登记条目；不存在时返回 ErrNotFound
func (s *Store) GetRepoByName(name string) (proto.Repo, error) {
	row := s.db.QueryRowContext(context.Background(),
		`SELECT `+repoColumns+` FROM repos WHERE name = ?`, name)
	r, err := scanRepoRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return proto.Repo{}, fmt.Errorf("仓库登记 %s: %w", name, ErrNotFound)
	}
	if err != nil {
		return proto.Repo{}, fmt.Errorf("查询仓库登记 %s: %w", name, err)
	}
	return r, nil
}

// ListRepos 返回全部登记，按名字字典序。
//
// 注意：
//   - 返回的 Status 字段恒为空——实际状态由 agentd 侧现场探测后填充
func (s *Store) ListRepos() ([]proto.Repo, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT `+repoColumns+` FROM repos ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("查询仓库登记列表: %w", err)
	}
	defer rows.Close()
	var repos []proto.Repo
	for rows.Next() {
		r, err := scanRepoRow(rows)
		if err != nil {
			return nil, fmt.Errorf("读取仓库登记行: %w", err)
		}
		repos = append(repos, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历仓库登记: %w", err)
	}
	return repos, nil
}

// DeleteRepo 删除一条登记。
//
// 返回：
//   - 错误：登记不存在时返回 ErrNotFound（而非静默成功——调用方需要知道自己删错了名字）
//
// 注意：
//   - 只删登记，**不动磁盘上的仓库**
func (s *Store) DeleteRepo(name string) error {
	res, err := s.db.ExecContext(context.Background(), `DELETE FROM repos WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("删除仓库登记 %s: %w", name, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("删除仓库登记 %s 后取影响行数: %w", name, err)
	}
	if n == 0 {
		return fmt.Errorf("仓库登记 %s: %w", name, ErrNotFound)
	}
	return nil
}

// ActiveTasksByRepoPath 返回仓库路径为 repoPath 的全部非终态任务。
//
// 参数：
//   - repoPath: 仓库绝对路径；空串返回空切片
//
// 注意：
//   - 与 ActiveTasksByWorkDir 的区别：那个按**工作目录**判定（managed worktree
//     各不相同），本方法按**仓库**判定——注销一条登记会影响这个仓库下的所有
//     任务，包括从它长出来的 managed worktree
func (s *Store) ActiveTasksByRepoPath(repoPath string) ([]proto.Task, error) {
	if repoPath == "" {
		return nil, nil
	}
	placeholders := make([]string, len(proto.TerminalStates))
	args := []any{repoPath}
	for i, st := range proto.TerminalStates {
		placeholders[i] = "?"
		args = append(args, string(st))
	}
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT `+taskColumns+` FROM tasks
WHERE repo_path = ? AND state NOT IN (`+strings.Join(placeholders, ", ")+`)
ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("查询仓库 %s 的活跃任务: %w", repoPath, err)
	}
	defer rows.Close()
	var tasks []proto.Task
	for rows.Next() {
		task, err := scanTaskRow(rows)
		if err != nil {
			return nil, fmt.Errorf("读取活跃任务行: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历活跃任务: %w", err)
	}
	return tasks, nil
}
