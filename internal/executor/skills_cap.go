// skills_cap.go —— skill 管理能力面（B233.2）。
//
// 职责：定义 Skills.Inspect / Install / Remove 与落点状态字面值。
//
// 边界：
//   - 原生目录留在各家包；本文件不写路径表
//   - Remove 必须明确授权，否则返回 ErrSkillRemoveUnauthorized
//   - 一家失败不得让其它已装变成全盘失败（锁现状 skill.Install 单点继续）
package executor

import (
	"context"
	"errors"
)

// 落点状态字面值，必须与 internal/skill 的 State* 逐字相同。
const (
	SkillInstalled = "installed"
	SkillSkipped   = "skipped"
	SkillInSync    = "in_sync"
	SkillStale     = "stale"
	SkillMissing   = "missing"
)

// SkillDefaultName 是落点目录名，与现状 skill.skillDirName 相同。
const SkillDefaultName = "handoff"

// ErrSkillRemoveUnauthorized 表示 Remove 未获明确授权。
var ErrSkillRemoveUnauthorized = errors.New("skills: Remove 未获明确授权")

// SkillSite 是一个落点及其状态，字段与 skill.Site 同形。
type SkillSite struct {
	Path  string
	State string
	Note  string
}

// SkillInstallReq 是一次安装/更新。Name 空则用 SkillDefaultName。
type SkillInstallReq struct {
	Home    string
	Content string
	Name    string
}

// SkillRemoveReq 是一次删除。Authorized 必须为 true。
type SkillRemoveReq struct {
	Home       string
	Name       string
	Authorized bool
}

// Skills 是 skill 管理能力面。
type Skills interface {
	Inspect(ctx context.Context, home string) ([]SkillSite, error)
	Install(ctx context.Context, req SkillInstallReq) ([]SkillSite, error)
	Remove(ctx context.Context, req SkillRemoveReq) error
}

// GuardRemove 执法 Remove 必须明确授权。实现节点在写盘之前调用。
func GuardRemove(req SkillRemoveReq) error {
	if !req.Authorized {
		return ErrSkillRemoveUnauthorized
	}
	return nil
}

// SkillDirName 返回安装目录名；空入参用缺省 "handoff"。
func SkillDirName(name string) string {
	if name == "" {
		return SkillDefaultName
	}
	return name
}
