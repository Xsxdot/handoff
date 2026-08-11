// 本文件是项目解析的**纯逻辑**层：把「派发请求指的是哪个项目」翻译成
// 「executor 应该在本机的哪个目录工作」。
//
// 职责：
//   - resolveProject：按 project_id / project_name 在位置表里查出那一行
//   - locationLines：把位置表压成人可读的清单，供拒绝报文使用
//
// 边界：
//   - 不碰数据库：位置行由调用方查好后以切片传入
//   - 不碰 git、不碰文件系统：路径是否真的可用由 EnsureRepoUsable 另行判定
//   - 不碰 HTTP：错误只用哨兵表达，状态码映射在 server.go
//   - **不接受任何路径入参**：调用方描述「代码在这台机器的哪个目录」正是 B62
//     要根除的漏洞（spec §1.2）。路径由本机查表得出，别人不许指定
//
// 为什么单独成文件且刻意保持纯净：这段规则是 dispatch 的必经之路，一旦错了
// 就会把任务派到错误的项目上。纯函数才能表驱动穷举。
package agentd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/xushixin/handoff/internal/proto"
)

// ErrProjectNotRegistered 表示派发请求指向的项目在本机没有位置。
//
// 映射为 400（调用方先解决请求本身的问题），见 server.go 的 writeDispatchError。
// 本机 CLI 收到它会触发自动登记后重发（spec §6.2）——因此报文既要给人看，
// 也要能被 CLI 用 errors 判别，两者都靠这个哨兵。
var ErrProjectNotRegistered = errors.New("项目未登记")

// locationLines 把位置表压成「名字 → 路径」的一行串，供拒绝报文使用。
//
// 报文必须带得走「本机登记了什么」——远程派发时审核者读不到执行机的
// agentd.log，一句干巴巴的「未登记」等于让他去猜。
func locationLines(entries []proto.ProjectLocation) string {
	if len(entries) == 0 {
		return "（本机尚无任何项目）"
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, e.Name+" → "+e.Path)
	}
	return strings.Join(lines, "; ")
}

// resolveProject 把派发请求里的项目引用解析成本机的位置行。
//
// 参数：
//   - projectID: 调用方算出的 project_id（优先）
//   - projectName: 人可读引用（仅 --project <名字> 与 Web 控制台会用）
//   - entries: 本机全部位置行
//
// 返回：
//   - 命中的位置行（Path 即 executor 的工作仓库）
//   - 错误：ErrProjectNotRegistered（查不到）或 errBadDispatchRequest（两者都空），
//     均映射 400，且报文自带本机已登记清单
//
// 注意：
//   - projectID 与 projectName 同时给出时以 projectID 为准：它是身份，名字只是引用
//   - 本函数不判断路径是否真的存在，那是 EnsureRepoUsable 的职责
func resolveProject(projectID, projectName string, entries []proto.ProjectLocation) (proto.ProjectLocation, error) {
	switch {
	case projectID != "":
		for _, e := range entries {
			if e.ProjectID == projectID {
				log().Info("项目解析：project_id 命中", "project_id", projectID,
					"name", e.Name, "path", e.Path)
				return e, nil
			}
		}
		log().Warn("项目解析被拒：project_id 查不到", "project_id", projectID,
			"registered", locationLines(entries))
		return proto.ProjectLocation{}, fmt.Errorf(
			"%w: project_id=%s；本机已登记的项目：%s（用 handoff project add 落地它）",
			ErrProjectNotRegistered, projectID, locationLines(entries))
	case projectName != "":
		for _, e := range entries {
			if e.Name == projectName {
				log().Info("项目解析：名字命中", "name", projectName, "path", e.Path)
				return e, nil
			}
		}
		log().Warn("项目解析被拒：名字查不到", "name", projectName,
			"registered", locationLines(entries))
		return proto.ProjectLocation{}, fmt.Errorf(
			"%w: %q；本机已登记的项目：%s（用 handoff project ls 查看）",
			ErrProjectNotRegistered, projectName, locationLines(entries))
	default:
		log().Warn("项目解析被拒：请求未指明项目")
		return proto.ProjectLocation{}, fmt.Errorf(
			"%w: 请求未指明项目（project_id 与 project_name 至少其一）", errBadDispatchRequest)
	}
}
