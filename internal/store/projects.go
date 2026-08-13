// 本文件是 project_locations 表（项目 × 本机位置）的持久化实现。
//
// 职责：
//   - project_locations 的增（CreateProjectLocation）、查（GetProjectLocationByName）、
//     列（ListProjectLocations）、删（DeleteProjectLocation）
//   - 把 SQLite 的主键/UNIQUE 冲突翻译成 ErrProjectDuplicate 哨兵，供上层映射 409
//
// 边界：
//   - 不算 project_id：那是 internal/projectid 的纯函数，调用方算好后传进来
//   - 不判断路径是否存在、是不是 git 仓库——那是 agentd 侧 EnsureRepoUsable 的事
//   - 不做名字派生、不做名字去重——那是 agentd/projectadmin.go 的事
//   - 与 store.go 一致的叶子层纪律：方法错误 return 前不打日志，由调用方带上下文记录
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Xsxdot/handoff/internal/projectid"
	"github.com/Xsxdot/handoff/internal/proto"
)

// ErrProjectDuplicate 表示位置登记冲突，三种成因合用一个哨兵：
//   - project_id 已存在 → 这个项目在本机已经有位置了（ADR-0008 只允许一个）
//   - name 已被占用 → 引用名撞了
//   - path 已被另一个项目指向 → 两个项目声称在同一个目录
//
// 为什么合并成一个哨兵：三者在 HTTP 上都是 409，且报文由 agentd 侧按上下文
// 拼装（它知道是自动登记还是人工登记）；分成三个哨兵只会让映射层多两个分支。
var ErrProjectDuplicate = errors.New("项目位置冲突（项目、名字或路径已存在）")

// projectColumns 是 project_locations 的完整读取列清单，Get 与 List 共用同一份。
const projectColumns = `project_id, name, path, origin_url, created_at`

// scanProjectRow 把一行 project_locations 记录读成 proto.ProjectLocation。
func scanProjectRow(sc rowScanner) (proto.ProjectLocation, error) {
	var (
		loc       proto.ProjectLocation
		createdAt string
	)
	if err := sc.Scan(&loc.ProjectID, &loc.Name, &loc.Path, &loc.OriginURL, &createdAt); err != nil {
		return proto.ProjectLocation{}, err
	}
	loc.CreatedAt = parseTime(createdAt)
	return loc, nil
}

// CreateProjectLocation 写入一条项目位置。
//
// 参数：
//   - loc: 位置条目；ProjectID/Name/Path/OriginURL 必须非空，CreatedAt 由调用方给定
//
// 返回：
//   - 错误：项目/名字/路径任一已存在时返回包装 ErrProjectDuplicate 的错误；其余为写库故障
func (s *Store) CreateProjectLocation(loc *proto.ProjectLocation) error {
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO project_locations (project_id, name, path, origin_url, created_at)
VALUES (?, ?, ?, ?, ?)`,
		loc.ProjectID, loc.Name, loc.Path, loc.OriginURL, fmtTime(loc.CreatedAt))
	if err != nil {
		// modernc.org/sqlite 的约束错误只有文本可判：主键冲突报
		// "PRIMARY KEY constraint failed"，UNIQUE 冲突报 "UNIQUE constraint failed"，
		// 没有可用的错误码常量。
		if strings.Contains(err.Error(), "UNIQUE constraint failed") ||
			strings.Contains(err.Error(), "PRIMARY KEY constraint failed") {
			return fmt.Errorf("%w: project_id=%s name=%s path=%s: %v",
				ErrProjectDuplicate, loc.ProjectID, loc.Name, loc.Path, err)
		}
		return fmt.Errorf("写入项目位置 %s: %w", loc.Name, err)
	}
	return nil
}

// GetProjectLocationByName 按引用名查询单条位置。
//
// 返回：
//   - 位置条目；不存在时返回 ErrNotFound
func (s *Store) GetProjectLocationByName(name string) (proto.ProjectLocation, error) {
	row := s.db.QueryRowContext(context.Background(),
		`SELECT `+projectColumns+` FROM project_locations WHERE name = ?`, name)
	loc, err := scanProjectRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return proto.ProjectLocation{}, fmt.Errorf("项目 %s: %w", name, ErrNotFound)
	}
	if err != nil {
		return proto.ProjectLocation{}, fmt.Errorf("查询项目位置 %s: %w", name, err)
	}
	return loc, nil
}

// ListProjectLocations 返回本机全部项目位置，按名字字典序。
//
// 注意：
//   - 返回的 Status 字段恒为空——实际状态由 agentd 侧现场探测后填充
func (s *Store) ListProjectLocations() ([]proto.ProjectLocation, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT `+projectColumns+` FROM project_locations ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("查询项目位置列表: %w", err)
	}
	defer rows.Close()
	var locs []proto.ProjectLocation
	for rows.Next() {
		loc, err := scanProjectRow(rows)
		if err != nil {
			return nil, fmt.Errorf("读取项目位置行: %w", err)
		}
		locs = append(locs, loc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历项目位置: %w", err)
	}
	return locs, nil
}

// DeleteProjectLocation 删除一条位置。
//
// 返回：
//   - 错误：位置不存在时返回 ErrNotFound（而非静默成功——调用方需要知道自己删错了名字）
//
// 注意：
//   - 只删登记，**不动磁盘上的仓库**
func (s *Store) DeleteProjectLocation(name string) error {
	res, err := s.db.ExecContext(context.Background(),
		`DELETE FROM project_locations WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("删除项目位置 %s: %w", name, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("删除项目位置 %s 后取影响行数: %w", name, err)
	}
	if n == 0 {
		return fmt.Errorf("项目 %s: %w", name, ErrNotFound)
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
//     各不相同），本方法按**仓库**判定——注销一条位置会影响这个项目下的所有
//     任务，包括从它长出来的 managed worktree
//   - 原属 repos.go（B62 迁移后 repos 表删除），项目注销的占用校验仍在用它
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

// migrateReposToProjectLocations 把旧 repos 表逐行迁入 project_locations，随后 DROP 掉它。
//
// 参数：
//   - db: 已打开的数据库句柄（Open 内部调用，此时新表已建好）
//
// 返回：
//   - 错误：查/写/DROP 失败时返回；旧表不存在时直接返回 nil（新库与二次调用都走这条）
//
// 注意：
//   - **必须整体走一个事务**：中途任一失败（写库报错、DROP 失败）都要把已插入的
//     行一并回滚。非事务化时，中途失败会留下「project_locations 里已有若干行 +
//     旧 repos 表还在」的中间态，下一次 Open 重跑迁移必然在第一批行的 project_id
//     主键冲突上硬失败，库从此打不开，只能人工手术——这就是本函数要防的死局
//   - 按 created_at 升序遍历，**同一个 project_id 保留最早的一条**：ADR-0008
//     只允许一个位置，多出来的（多半是同一仓库的 worktree 各登了一条）丢弃并
//     Warn 出 name/path/origin 三项完整信息，人照着 handoff project add --path 自己补
//   - 路径做 Abs+Clean：新表的 path UNIQUE 约束要靠绝对路径才有意义
//   - 迁移不探测文件系统：目录已被移走的行照样迁入，下一次派发在 EnsureRepoUsable
//     处报「路径不存在」，处置是 project rm 后重新 add（spec §14 风险一）
//   - 幂等靠 DROP：跑完旧表就没了，第二次调用直接返回
//   - 汇总 Info（migrated/skipped）在 Commit 成功之后才打：回滚过却报「已迁入」
//     是假日志
func migrateReposToProjectLocations(db *sql.DB) error {
	ctx := context.Background()
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='repos'`).Scan(&n); err != nil {
		return fmt.Errorf("探查旧 repos 表: %w", err)
	}
	if n == 0 {
		return nil // 新库或已迁过，无操作
	}
	rows, err := db.QueryContext(ctx,
		`SELECT name, path, origin_url, created_at FROM repos ORDER BY created_at ASC`)
	if err != nil {
		return fmt.Errorf("读旧 repos 表: %w", err)
	}
	type oldRepo struct{ name, path, origin, createdAt string }
	var olds []oldRepo
	for rows.Next() {
		var r oldRepo
		if err := rows.Scan(&r.name, &r.path, &r.origin, &r.createdAt); err != nil {
			rows.Close()
			return fmt.Errorf("读旧 repos 行: %w", err)
		}
		olds = append(olds, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("遍历旧 repos 表: %w", err)
	}
	rows.Close()

	// 写侧全部走事务：INSERT 与 DROP 必须同生共死（why 见函数头注意）。
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("开启迁移事务: %w", err)
	}
	defer tx.Rollback()

	seen := make(map[string]oldRepo, len(olds))
	migrated, skipped := 0, 0
	for _, r := range olds {
		pid := projectid.FromOrigin(r.origin)
		if pid == "" {
			log().Warn("迁移跳过：origin 算不出 project_id（人工处置：handoff project add）",
				"name", r.name, "path", r.path, "origin", r.origin)
			skipped++
			continue
		}
		if prev, dup := seen[pid]; dup {
			log().Warn("迁移跳过：同一项目已有更早的登记，本条丢弃（人工处置：handoff project add --path <该路径>）",
				"name", r.name, "path", r.path, "origin", r.origin,
				"kept_name", prev.name, "kept_path", prev.path)
			skipped++
			continue
		}
		abs, err := filepath.Abs(r.path)
		if err != nil {
			log().Warn("迁移跳过：路径无法绝对化",
				"name", r.name, "path", r.path, "origin", r.origin, "cause", err)
			skipped++
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO project_locations (project_id, name, path, origin_url, created_at)
VALUES (?, ?, ?, ?, ?)`,
			pid, r.name, filepath.Clean(abs), r.origin, r.createdAt); err != nil {
			return fmt.Errorf("迁移登记 %s 到 project_locations: %w", r.name, err)
		}
		seen[pid] = r
		migrated++
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE repos`); err != nil {
		return fmt.Errorf("迁移后删除旧 repos 表: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交迁移事务: %w", err)
	}
	log().Info("旧仓库登记已迁入项目位置表", "migrated", migrated, "skipped", skipped)
	return nil
}
