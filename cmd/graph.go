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
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Xsxdot/handoff/internal/codegraph"
	"github.com/spf13/cobra"
)

var (
	graphRepo               = "."
	graphDepth              = 2
	graphView               string
	graphStale              bool
	absorbCommit            string
	absorbBranch            string
	graphResolveDoc         string
	graphContractFrom       string
	graphContractTo         string
	graphContractEntries    []string
	graphContractInterfaces []string
	graphContractBudget     int
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
	absorbCommit = ""
	absorbBranch = ""
	graphResolveDoc = ""
	graphContractFrom = ""
	graphContractTo = ""
	graphContractEntries = nil
	graphContractInterfaces = nil
	graphContractBudget = 0
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

var graphAbsorbCmd = &cobra.Command{
	Use:   "absorb <view>",
	Short: "把分支视图 diff 併入 baseline 并删除该 diff（分支合并回主线后执行）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		defer graphResetState()
		g, err := codegraph.LoadGraph(graphRepo)
		if err != nil {
			return err
		}
		d, err := codegraph.LoadDiff(graphRepo, args[0])
		if err != nil {
			return err
		}
		if issues := codegraph.ValidateDiff(g, d); len(issues) > 0 {
			return fmt.Errorf("视图 %s 引用不完整，拒绝併入: %v", args[0], issues)
		}
		merged := codegraph.Absorb(g, d)
		// 刷新来源戳。--commit/--branch 未给时从 git 取；取不到就报错，
		// 不猜——基线的 meta 是审计锚点（worktree 版本戳说谎的前科）。
		merged.Meta.Commit, merged.Meta.Branch = absorbCommit, absorbBranch
		if merged.Meta.Commit == "" {
			if merged.Meta.Commit, err = gitHead(graphRepo); err != nil {
				return fmt.Errorf("取 HEAD 失败，请显式传 --commit: %w", err)
			}
		}
		if merged.Meta.Branch == "" {
			if merged.Meta.Branch, err = gitBranch(graphRepo); err != nil {
				return fmt.Errorf("取分支失败，请显式传 --branch: %w", err)
			}
		}
		if err := codegraph.SaveGraph(graphRepo, merged); err != nil {
			return err // 写盘失败：diff 保留，重试无损
		}
		diffPath := filepath.Join(graphRepo, "codegraph", "diffs", args[0]+".json")
		if err := os.Remove(diffPath); err != nil {
			return fmt.Errorf("基线已更新但删除 diff 失败（手动删除 %s）: %w", diffPath, err)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "已併入视图 %s：+%d 节点 ~%d -%d，基线 %d 节点 @%s\n",
			args[0], len(d.NodesAdded), len(d.NodesModified), len(d.NodesDeleted),
			len(merged.Nodes), merged.Meta.Commit)
		return nil
	},
}

func gitHead(repo string) (string, error) {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	commit := strings.TrimSpace(string(out))
	if commit == "" {
		return "", fmt.Errorf("git rev-parse HEAD 返回空值")
	}
	return commit, nil
}

func gitBranch(repo string) (string, error) {
	out, err := exec.Command("git", "-C", repo, "branch", "--show-current").Output()
	if err != nil {
		return "", fmt.Errorf("git branch --show-current: %w", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return "", fmt.Errorf("当前处于 detached HEAD")
	}
	return branch, nil
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

// graphSymCmd 单点符号查询：agent 探索「X 在哪 / 什么形状」的第一跳，
// 输出行号已做查询时再锚定（图数据允许陈旧，输出必须当下可用）。
var graphSymCmd = &cobra.Command{
	Use:   "sym <符号名或节点 id>",
	Short: "单点符号查询：位置（已再锚定）、签名、字段、摘要、归属",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		defer graphResetState()
		v, _, err := graphLoadView()
		if err != nil {
			return err
		}
		r, err := codegraph.SymLookup(v, graphRepo, args[0])
		if err != nil {
			return err
		}
		return graphPrintJSON(cmd, r)
	},
}

// graphEntityCmd 查询数据实体的投影链：typed/handroll 投影点与跨语言孪生侧。
var graphEntityCmd = &cobra.Command{
	Use:   "entity <model 名或节点 id>",
	Short: "数据实体的投影链：typed/handroll 投影点 + 跨语言孪生（序列化边界四查入口）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		defer graphResetState()
		v, _, err := graphLoadView()
		if err != nil {
			return err
		}
		r, err := codegraph.EntityLookup(v, graphRepo, args[0])
		if err != nil {
			return err
		}
		return graphPrintJSON(cmd, r)
	},
}

var graphResolveCmd = &cobra.Command{
	Use:   "resolve [file#Symbol]",
	Short: "校验 file#Symbol 符号锚，或批量检查文档（坏锚即非零退出）",
	RunE: func(cmd *cobra.Command, args []string) error {
		defer func() {
			graphResetState()
			cmd.Flags().Lookup("doc").Changed = false
		}()
		if len(args) > 1 {
			return fmt.Errorf("resolve 只接受一个 file#Symbol 位置参数")
		}
		if graphResolveDoc != "" && len(args) > 0 {
			return fmt.Errorf("resolve 的 --doc 与 file#Symbol 位置参数互斥")
		}
		if graphResolveDoc == "" && len(args) == 0 {
			return fmt.Errorf("resolve 必须指定 --doc 或 file#Symbol")
		}
		v, _, err := graphLoadView()
		if err != nil {
			return err
		}
		if len(args) == 1 {
			anchor, err := codegraph.ResolveAnchor(v, graphRepo, args[0])
			if err != nil {
				return err
			}
			return graphPrintJSON(cmd, anchor)
		}
		anchors, err := codegraph.CheckDocAnchors(v, graphRepo, graphResolveDoc)
		if err != nil {
			return err
		}
		if anchors == nil {
			anchors = []codegraph.AnchorResult{}
		}
		if err := graphPrintJSON(cmd, map[string]any{"anchors": anchors}); err != nil {
			return err
		}
		for _, a := range anchors {
			if a.Anchor == "vanished" || a.Anchor == "file_missing" {
				return fmt.Errorf("文档锚点检查失败: %s (%s)", a.Ref, a.Anchor)
			}
		}
		return nil
	},
}

var graphContractCmd = &cobra.Command{
	Use:   "contract",
	Short: "维护目标图中的跨领域契约",
}

var graphContractSetCmd = &cobra.Command{
	Use:   "set",
	Short: "创建或更新 From→To 契约",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		defer func() {
			graphResetState()
			for _, name := range []string{"from", "to", "entries", "interfaces", "budget"} {
				cmd.Flags().Lookup(name).Changed = false
			}
		}()
		if graphContractFrom == "" || graphContractTo == "" {
			return fmt.Errorf("contract set 必须指定 --from 与 --to")
		}
		c := codegraph.Contract{From: graphContractFrom, To: graphContractTo}
		entriesSet := cmd.Flags().Changed("entries")
		interfacesSet := cmd.Flags().Changed("interfaces")
		budgetSet := cmd.Flags().Changed("budget")
		if entriesSet {
			c.Entries = append([]string(nil), graphContractEntries...)
		}
		if interfacesSet {
			c.Interfaces = append([]string(nil), graphContractInterfaces...)
		}
		if budgetSet {
			c.LegacyBudget = graphContractBudget
		}
		before, after, err := codegraph.SetContractWithPresence(graphRepo, c, entriesSet, interfacesSet, budgetSet)
		if err != nil {
			return err
		}
		return graphPrintJSON(cmd, map[string]any{"before": before, "after": after})
	},
}

// graphSummaryCmd 输出一段图存在性摘要，供 SessionStart hook 注入会话上下文：
// 让 agent 开局就知道图存在、先查图再 grep。
var graphSummaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "图摘要（供会话开局注入：规模、领域数、查询子命令菜单）",
	RunE: func(cmd *cobra.Command, args []string) error {
		defer graphResetState()
		g, err := codegraph.LoadGraph(graphRepo)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(),
			"本仓库有代码图：%d 节点 / %d 边 / %d 领域（codegraph/）。探索已有代码先查图：handoff graph sym <符号>（定位+签名+字段，行号已再锚定）、who-calls <符号>（上游影响面）、chain <符号>（下游链）、domains（领域树）；图未命中再 grep，并把未命中符号记入产出物的「图覆盖债」小节。\n",
			len(g.Nodes), len(g.Edges), len(g.Domains))
		return nil
	},
}

func init() {
	graphCmd.PersistentFlags().StringVar(&graphRepo, "repo", ".", "目标仓库根目录")
	graphCmd.PersistentFlags().IntVar(&graphDepth, "depth", 2, "查询深度（0 = 不限）")
	graphCmd.PersistentFlags().StringVar(&graphView, "view", "", "叠加的视图名（codegraph/diffs/<名>.json）")
	graphCmd.PersistentFlags().BoolVar(&graphStale, "stale", false, "附带保鲜检测结果")
	graphAbsorbCmd.Flags().StringVar(&absorbCommit, "commit", "", "写入基线 meta 的提交号（缺省从 git HEAD 读取）")
	graphAbsorbCmd.Flags().StringVar(&absorbBranch, "branch", "", "写入基线 meta 的分支名（缺省从 git 读取）")
	graphResolveCmd.Flags().StringVar(&graphResolveDoc, "doc", "", "要检查的 Markdown 文档路径")
	graphContractSetCmd.Flags().StringVar(&graphContractFrom, "from", "", "契约来源域 id")
	graphContractSetCmd.Flags().StringVar(&graphContractTo, "to", "", "契约目标域 id")
	graphContractSetCmd.Flags().StringSliceVar(&graphContractEntries, "entries", nil, "允许进入目标域的入口清单")
	graphContractSetCmd.Flags().StringSliceVar(&graphContractInterfaces, "interfaces", nil, "允许的跨域接口清单")
	graphContractSetCmd.Flags().IntVar(&graphContractBudget, "budget", 0, "存量直调预算")
	graphContractCmd.AddCommand(graphContractSetCmd)
	graphCmd.AddCommand(graphValidateCmd, graphCheckCmd, graphAbsorbCmd, graphViewsCmd, graphChainCmd, graphWhoCallsCmd, graphDomainsCmd, graphSymCmd, graphEntityCmd, graphResolveCmd, graphContractCmd, graphSummaryCmd)
	rootCmd.AddCommand(graphCmd)
}
