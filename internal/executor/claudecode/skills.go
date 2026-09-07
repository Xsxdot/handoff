// skills.go —— claudecode 的 Skills 能力实现。
//
// 职责：
//   - Inspect：检查指定 home 下的 skill 落点状态
//   - Install：按白名单将 skill 内容写入 <home>/<SkillsRelDir>/<name>/SKILL.md
//   - Remove：在授权后安全删除指定 skill 目录
//
// 边界：
//   - agent 的 home 目录不存在就跳过（StateSkipped），绝不代为创建，
//     避免为未安装的 agent 制造半截目录结构；
//   - Remove 必须经 GuardRemove 校验授权，未授权拒绝执行；
//   - 全平台写入实体副本，不建目录软链。
package claudecode

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/executor"
)

var _ executor.Skills = (*Adapter)(nil)

func (a *Adapter) Inspect(ctx context.Context, home string) ([]executor.SkillSite, error) {
	name := executor.SkillDirName("")
	site := filepath.Join(home, SkillsRelDir, name)
	target := filepath.Join(site, "SKILL.md")
	parent := filepath.Dir(filepath.Join(home, SkillsRelDir))
	if _, err := os.Stat(parent); errors.Is(err, os.ErrNotExist) {
		return []executor.SkillSite{{
			Path:  target,
			State: executor.SkillMissing,
			Note:  parent + " 不存在（该 agent 未安装）",
		}}, nil
	}
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		return []executor.SkillSite{{
			Path:  target,
			State: executor.SkillMissing,
		}}, nil
	} else if err != nil {
		return []executor.SkillSite{{
			Path:  target,
			State: executor.SkillMissing,
			Note:  "读取失败: " + err.Error(),
		}}, nil
	}
	return []executor.SkillSite{{
		Path:  target,
		State: executor.SkillInstalled,
	}}, nil
}

func (a *Adapter) Install(ctx context.Context, req executor.SkillInstallReq) ([]executor.SkillSite, error) {
	name := executor.SkillDirName(req.Name)
	dir := filepath.Join(req.Home, SkillsRelDir)
	parent := filepath.Dir(dir)
	if _, err := os.Stat(parent); err != nil {
		return []executor.SkillSite{{
			Path:  filepath.Join(dir, name),
			State: executor.SkillSkipped,
			Note:  parent + " 不存在（该 agent 未安装）",
		}}, nil
	}
	site := filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return []executor.SkillSite{{
			Path:  site,
			State: executor.SkillSkipped,
			Note:  "创建目录失败: " + err.Error(),
		}}, nil
	}
	if err := os.RemoveAll(site); err != nil {
		return []executor.SkillSite{{
			Path:  site,
			State: executor.SkillSkipped,
			Note:  "清理旧落点失败: " + err.Error(),
		}}, nil
	}
	if err := os.MkdirAll(site, 0o755); err != nil {
		return []executor.SkillSite{{
			Path:  site,
			State: executor.SkillSkipped,
			Note:  "创建落点目录失败: " + err.Error(),
		}}, nil
	}
	if err := os.WriteFile(filepath.Join(site, "SKILL.md"), []byte(req.Content), 0o644); err != nil {
		return []executor.SkillSite{{
			Path:  site,
			State: executor.SkillSkipped,
			Note:  "写落点副本失败: " + err.Error(),
		}}, nil
	}
	return []executor.SkillSite{{
		Path:  site,
		State: executor.SkillInstalled,
	}}, nil
}

func (a *Adapter) Remove(ctx context.Context, req executor.SkillRemoveReq) error {
	if err := executor.GuardRemove(req); err != nil {
		return err
	}
	name := executor.SkillDirName(req.Name)
	site := filepath.Join(req.Home, SkillsRelDir, name)
	return os.RemoveAll(site)
}
