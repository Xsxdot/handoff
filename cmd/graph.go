// 本文件实现 handoff graph 子命令族：对仓库内代码图数据的本地只读查询。
//
// 职责：
//   - graph validate: 引用完整性 + 可选 --stale 保鲜检查，供 CI 与扫描后自检
//   - graph views:    列出可用视图（diffs 目录）
//   - graph chain:    焦点（可多个，并集）的下游调用链
//   - graph who-calls: 焦点（可多个，并集）的上游调用方——影响面查询
//
// 边界：
//   - 只读 --repo 指向的本地文件，不发任何网络请求、不依赖 agentd 存活
//     ——spec 2026-08-19-codegraph-design §2/§6 的硬约束，agent 离线可用
//   - 不产出/修改图数据（扫描配方见 docs/codegraph-scan-recipe.md）
package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/Xsxdot/handoff/internal/codegraph"
	"github.com/spf13/cobra"
)

var (
	graphRepo  = "."
	graphDepth = 2
	graphView  string
	graphStale bool
)

var graphCmd = &cobra.Command{
	Use:   "graph",
	Short: "查询仓库内的代码图（codegraph/*.json，本地只读）",
}

// graphLoadView 加载基线并按 --view 叠加 diff，返回合并视图。
func graphLoadView() (*codegraph.View, *codegraph.Graph, error) {
	g, err := codegraph.LoadGraph(graphRepo)
	if err != nil {
		return nil, nil, err
	}
	var d *codegraph.Diff
	if graphView != "" {
		if d, err = codegraph.LoadDiff(graphRepo, graphView); err != nil {
			return nil, nil, err
		}
		if issues := codegraph.ValidateDiff(g, d); len(issues) > 0 {
			return nil, nil, fmt.Errorf("视图 %s 引用不完整: %v", graphView, issues)
		}
	}
	return codegraph.Merge(g, d), g, nil
}

// graphPrintJSON 把结果编码到 stdout（缩进 JSON，agent 与人都可读）。
func graphPrintJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", " ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// graphResetState 清理进程内复用命令树时的图命令 flag 状态。
// Cobra 测试会在同一进程执行多次子命令，未提供的 flag 不能继承上一次查询。
func graphResetState() {
	graphRepo = "."
	graphDepth = 2
	graphView = ""
	graphStale = false
}

var graphValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "校验基线与全部视图的引用完整性（--stale 加保鲜检查），问题即非零退出",
	RunE: func(cmd *cobra.Command, args []string) (err error) {
		defer graphResetState()
		g, err := codegraph.LoadGraph(graphRepo)
		if err != nil {
			return err
		}
		issues := codegraph.Validate(g)
		views, err := codegraph.ListViews(graphRepo)
		if err != nil {
			return err
		}
		if views == nil {
			views = []string{}
		}
		for _, name := range views {
			d, err := codegraph.LoadDiff(graphRepo, name)
			if err != nil {
				return err
			}
			for _, is := range codegraph.ValidateDiff(g, d) {
				issues = append(issues, "["+name+"] "+is)
			}
		}
		var stale []codegraph.StaleNode
		if graphStale {
			stale = codegraph.CheckStale(graphRepo, g)
		}
		unscanned := 0
		for _, n := range g.Nodes {
			if n.Kind == "entry" && n.Unscanned {
				unscanned++
			}
		}
		out := map[string]any{
			"nodes": len(g.Nodes), "edges": len(g.Edges),
			"containers": len(g.Containers), "domains": len(g.Domains), "views": views,
			"unscannedEntries": unscanned, "issues": issues,
		}
		if graphStale {
			out["stale"] = stale
		}
		if err := graphPrintJSON(cmd, out); err != nil {
			return err
		}
		if len(issues) > 0 || len(stale) > 0 {
			return fmt.Errorf("发现 %d 个完整性问题、%d 个失鲜节点", len(issues), len(stale))
		}
		return nil
	},
}

var graphCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "目标图契约对照：实际跨域边 ⊆ target.json 声明的契约面，违规即非零退出",
	RunE: func(cmd *cobra.Command, args []string) error {
		defer graphResetState()
		t, err := codegraph.LoadTarget(graphRepo)
		if err != nil {
			// 无基准绝不静默通过——这是本机制的头号反静默约定（spec §5）
			return fmt.Errorf("目标图不可用，check 拒绝执行: %w", err)
		}
		if issues := codegraph.ValidateTarget(t); len(issues) > 0 {
			return fmt.Errorf("目标图自身不合法: %v", issues)
		}
		v, _, err := graphLoadView()
		if err != nil {
			return err
		}
		rep := codegraph.Check(t, v)
		if err := graphPrintJSON(cmd, rep); err != nil {
			return err
		}
		if len(rep.Fails) > 0 {
			return fmt.Errorf("契约对照发现 %d 处违规", len(rep.Fails))
		}
		return nil
	},
}

var graphViewsCmd = &cobra.Command{
	Use:   "views",
	Short: "列出可用视图（codegraph/diffs/ 下的文件名）",
	RunE: func(cmd *cobra.Command, args []string) error {
		defer graphResetState()
		views, err := codegraph.ListViews(graphRepo)
		if err != nil {
			return err
		}
		if views == nil {
			views = []string{}
		}
		return graphPrintJSON(cmd, map[string]any{"views": views})
	},
}

// graphQueryOutput 保持 Result 字段在 JSON 顶层，附加 CLI 层的深度与可选 stale 数据。
type graphQueryOutput struct {
	*codegraph.Result
	Depth int                   `json:"depth"`
	Stale []codegraph.StaleNode `json:"stale,omitempty"`
}

// graphQueryRunE 是 chain 与 who-calls 的共用主体：解析焦点 → 邻域查询 → 输出。
func graphQueryRunE(down, up bool) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		defer graphResetState()
		v, g, err := graphLoadView()
		if err != nil {
			return err
		}
		foci := make([]string, 0, len(args))
		for _, a := range args {
			id, err := codegraph.Resolve(v, a)
			if err != nil {
				return err
			}
			foci = append(foci, id)
		}
		limit := graphDepth
		if limit == 0 {
			limit = -1 // CLI 语义：0 = 不限 → 核心语义 -1
		}
		dn, upn := 0, 0
		if down {
			dn = limit
		}
		if up {
			upn = limit
		}
		r, err := codegraph.Neighborhood(v, foci, dn, upn)
		if err != nil {
			return err
		}
		out := graphQueryOutput{Result: r, Depth: graphDepth}
		if graphStale {
			out.Stale = codegraph.CheckStale(graphRepo, g)
		}
		return graphPrintJSON(cmd, out)
	}
}

var graphChainCmd = &cobra.Command{
	Use:   "chain <节点 id 或名字>...",
	Short: "焦点的下游调用链（多个焦点取并集）",
	Args:  cobra.MinimumNArgs(1),
	RunE:  graphQueryRunE(true, false),
}

var graphWhoCallsCmd = &cobra.Command{
	Use:   "who-calls <节点 id 或名字>...",
	Short: "谁调用了焦点——上游影响面（多个焦点取并集）",
	Args:  cobra.MinimumNArgs(1),
	RunE:  graphQueryRunE(false, true),
}

// graphDomainsCmd 列领域树：agent 定位「该从哪个领域下手」的第一跳。
var graphDomainsCmd = &cobra.Command{
	Use:   "domains",
	Short: "列出领域树（职责、成员统计、对外接口）",
	RunE: func(cmd *cobra.Command, args []string) error {
		defer graphResetState()
		v, _, err := graphLoadView()
		if err != nil {
			return err
		}
		doms := codegraph.DomainTree(v)
		out := map[string]any{"view": v.Name, "domains": doms}
		if doms == nil {
			// 明确区分「没有领域」与「查不出领域」：前者是旧数据，给可行动的提示
			out["domains"] = []codegraph.DomainStat{}
			out["warning"] = "该图未包含领域划分（扫描版本较旧）：重扫可获得领域信息"
		}
		return graphPrintJSON(cmd, out)
	},
}

func init() {
	graphCmd.PersistentFlags().StringVar(&graphRepo, "repo", ".", "目标仓库根目录")
	graphCmd.PersistentFlags().IntVar(&graphDepth, "depth", 2, "查询深度（0 = 不限）")
	graphCmd.PersistentFlags().StringVar(&graphView, "view", "", "叠加的视图名（codegraph/diffs/<名>.json）")
	graphCmd.PersistentFlags().BoolVar(&graphStale, "stale", false, "附带保鲜检测结果")
	graphCmd.AddCommand(graphValidateCmd, graphCheckCmd, graphViewsCmd, graphChainCmd, graphWhoCallsCmd, graphDomainsCmd)
	rootCmd.AddCommand(graphCmd)
}
