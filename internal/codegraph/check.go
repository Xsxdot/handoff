// 本文件实现目标图对实际图的契约对照（spec §5）。
//
// 职责：Check——归域、逐边判定、legacy 预算结算，产出 Report
// 边界：不做 I/O、不打日志——纯函数，可观测性由返回的 Report 承担；
//
//	加载与退出码语义在 cmd 层
package codegraph

import (
	"fmt"
	"slices"
	"strings"
)

// Finding 一条对照发现。Kind 取值：
// fail 侧：new-direction（无契约方向）/ off-entry 归并进 legacy 或 over-budget /
// off-interface（未声明的跨域实现）/ over-budget（legacy 超预算）
// warn 侧：legacy（预算内直调计数）/ outside-file（图外文件）/ dead-rule（规则未命中任何节点）
type Finding struct {
	Kind   string `json:"kind"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Edge   *Edge  `json:"edge,omitempty"`
	Detail string `json:"detail"`
}

// Report 是 Check 的产出。Fails 非空即闸门不过（cmd 层译成非零退出码）。
type Report struct {
	Fails      []Finding      `json:"fails"`
	Warns      []Finding      `json:"warns"`
	LegacyHits map[string]int `json:"legacyHits,omitempty"` // "from->to" → 命中数
}

// Check 把合并视图 v 套在目标图 t 上对照。算法四步见 spec §5。
// deleted 状态的节点/边不参与——它们只为渲染保留。
func Check(t *Target, v *View) *Report {
	rep := &Report{Fails: []Finding{}, Warns: []Finding{}, LegacyHits: map[string]int{}}
	assembly := make(map[string]bool, len(t.Assembly))
	for _, f := range t.Assembly {
		assembly[f] = true
	}
	contracts := make(map[string]*Contract, len(t.Contracts))
	for i := range t.Contracts {
		c := &t.Contracts[i]
		contracts[c.From+"->"+c.To] = c
	}
	// 归域 + 图外收集（每文件报一次）
	nodeDomain := make(map[string]string, len(v.Nodes))
	outside := map[string]bool{}
	fileHit := map[string]bool{} // 供死规则检测：哪些文件被规则命中过
	for id, n := range v.Nodes {
		if n.Status == "deleted" {
			continue
		}
		d := t.DomainOf(n.File)
		nodeDomain[id] = d
		if d == "" {
			outside[n.File] = true
		} else {
			fileHit[n.File] = true
		}
	}
	// call 边
	for i := range v.Edges {
		e := v.Edges[i]
		if e.Status == "deleted" {
			continue
		}
		from, to := nodeDomain[e.From], nodeDomain[e.To]
		if from == "" || to == "" || from == to {
			continue // 图外已单独 warn；域内不检查
		}
		if callerNode, ok := v.Nodes[e.From]; ok && assembly[callerNode.File] {
			continue // 组装点豁免（依赖注入的绑定边）
		}
		c := contracts[from+"->"+to]
		if c == nil {
			rep.Fails = append(rep.Fails, Finding{Kind: "new-direction", From: from, To: to,
				Edge: &Edge{e.From, e.To}, Detail: fmt.Sprintf("跨域方向 %s→%s 无契约条目", from, to)})
			continue
		}
		label := ""
		if callee, ok := v.Nodes[e.To]; ok {
			label = v.Containers[callee.Container].Label
		}
		if inList(c.Entries, label) {
			continue
		}
		rep.LegacyHits[from+"->"+to]++
	}
	// implements 边：实现(from 侧域=to 契约方) → 接口(from 契约方)
	for i := range v.Implements {
		e := v.Implements[i]
		if e.Status == "deleted" {
			continue
		}
		implDom, ifaceDom := nodeDomain[e.From], nodeDomain[e.To]
		if implDom == "" || ifaceDom == "" || implDom == ifaceDom {
			continue
		}
		c := contracts[ifaceDom+"->"+implDom]
		ifaceName := ""
		if n, ok := v.Nodes[e.To]; ok {
			ifaceName = n.Name
		}
		if c == nil || !inList(c.Interfaces, ifaceName) {
			rep.Fails = append(rep.Fails, Finding{Kind: "off-interface", From: ifaceDom, To: implDom,
				Edge:   &Edge{e.From, e.To},
				Detail: fmt.Sprintf("跨域实现未声明: %s 实现了 %s 的 %s", implDom, ifaceDom, ifaceName)})
		}
	}
	// 预算结算
	for key, hits := range rep.LegacyHits {
		c := contracts[key]
		if hits > c.LegacyBudget {
			rep.Fails = append(rep.Fails, Finding{Kind: "over-budget", From: c.From, To: c.To,
				Detail: fmt.Sprintf("%s 直调 %d 条超出预算 %d", key, hits, c.LegacyBudget)})
		} else {
			rep.Warns = append(rep.Warns, Finding{Kind: "legacy", From: c.From, To: c.To,
				Detail: fmt.Sprintf("%s 预算内直调 %d/%d（可收窄后调低预算）", key, hits, c.LegacyBudget)})
		}
	}
	// 图外文件 + 死规则
	for f := range outside {
		rep.Warns = append(rep.Warns, Finding{Kind: "outside-file", Detail: "图外文件（目标图未覆盖）: " + f})
	}
	for _, d := range t.Domains {
		for _, rule := range d.Paths {
			if !ruleHitsAny(rule, fileHit) {
				rep.Warns = append(rep.Warns, Finding{Kind: "dead-rule", From: d.ID,
					Detail: fmt.Sprintf("域 %s 的规则 %q 未命中视图中任何节点文件", d.ID, rule)})
			}
		}
	}
	sortFindings(rep) // 输出稳定排序，测试与 diff 可复现
	return rep
}

func inList(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ruleHitsAny 判断一条 paths 规则是否命中过任何已归域的节点文件。
func ruleHitsAny(rule string, fileHit map[string]bool) bool {
	prefix, isPrefix := strings.CutSuffix(rule, "/**")
	for f := range fileHit {
		if f == rule || (isPrefix && strings.HasPrefix(f, prefix+"/")) {
			return true
		}
	}
	return false
}

// sortFindings 把 Fails/Warns 按 Kind+Detail 排序——map 遍历序不定，
// 输出必须可复现，否则 CLI diff 与测试都不稳。
func sortFindings(rep *Report) {
	cmp := func(a, b Finding) int {
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		return strings.Compare(a.Detail, b.Detail)
	}
	slices.SortFunc(rep.Fails, cmp)
	slices.SortFunc(rep.Warns, cmp)
}
