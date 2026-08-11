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
	"strings"

	"github.com/xushixin/handoff/internal/proto"
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
