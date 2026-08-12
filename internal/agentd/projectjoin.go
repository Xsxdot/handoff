// 本文件实现任务的项目归属 join：task.repo_path → project_locations.path →
// 该行的 project_id。
//
// 职责：
//   - 把位置表压成一张 path → project_id 的等值索引
//   - 给任务盖上 ProjectID 注解（线字段，不入库）
//
// 边界：
//   - 不加库列：tasks 表**不加** project_id 列。历史任务或已注销项目的任务
//     应当诚实显示「未归属」，而不是一列陈旧数据说谎；加列的代价（回填 +
//     注销后列失真）只换来微不足道的查询加速
//   - 不做模糊匹配：B62 的 MainWorktreeRoot 归并让 repo_path 与
//     project_locations.path 同源同形态，一次 filepath.Clean 后等值比即可。
//     早前设想的「先比 location 再逐个比 workspace」两段式已整个去掉
package agentd

import (
	"path/filepath"

	"github.com/xushixin/handoff/internal/proto"
)

// projectIndex 是 归一化路径 → project_id 的等值索引。
type projectIndex map[string]string

// newProjectIndex 由位置表构建索引。
func newProjectIndex(locs []proto.ProjectLocation) projectIndex {
	idx := make(projectIndex, len(locs))
	for _, l := range locs {
		if l.Path == "" {
			continue
		}
		idx[filepath.Clean(l.Path)] = l.ProjectID
	}
	return idx
}

// projectIDOf 返回该仓库路径所属的 project_id；未登记/已注销返回空串。
//
// 注意：空串是**正常结果**而非错误——「未归属」是项目树与看板要如实展示的一种状态。
func (idx projectIndex) projectIDOf(repoPath string) string {
	if repoPath == "" {
		return ""
	}
	return idx[filepath.Clean(repoPath)]
}

// projectIndex 读一次位置表并构建索引；查表失败时返回空索引（全部未归属）。
//
// 为什么失败不向上抛：归属只是任务列表上的一个注解，位置表读不到时列表本身
// 仍然有效。为一个注解让 /api/tasks 500，是把附加信息变成了单点故障。
func (s *Server) projectIndex() projectIndex {
	locs, err := s.st.ListProjectLocations()
	if err != nil {
		s.log.Warn("读取位置表失败，任务归属本次全部显示未归属", "cause", err)
		return projectIndex{}
	}
	return newProjectIndex(locs)
}
