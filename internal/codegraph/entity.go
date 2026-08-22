// 本文件实现 graph entity：以 model 为中心拼出 typed/handroll 投影链与跨语言孪生侧。
//
// 职责：复用 symResolve 决议数据实体、复用 symMatchFor 生成再锚定卡片，并合并 twin 两侧的投影点。
// 边界：只读视图与仓库源码，不扫描或修改 codegraph 数据；投影关系由扫描产出。
package codegraph

import (
	"fmt"
	"sort"
	"strings"
)

// EntityResult 是一次数据实体投影链查询的完整结果。
type EntityResult struct {
	View        string     `json:"view"`
	Query       string     `json:"query"`
	Model       SymMatch   `json:"model"`
	Twins       []SymMatch `json:"twins,omitempty"`
	Typed       []ProjSite `json:"typed,omitempty"`
	Handroll    []ProjSite `json:"handroll,omitempty"`
	ProjScanned bool       `json:"projScanned"`
	Warning     string     `json:"warning,omitempty"`
}

// ProjSite 是投影点卡片。Via 为 direct，或标明来自哪一个 twin model。
type ProjSite struct {
	SymMatch
	Via string `json:"via,omitempty"`
}

// EntityLookup 查询一个数据实体及其序列化投影链。
func EntityLookup(v *View, repoRoot, arg string) (*EntityResult, error) {
	ids := symResolve(v, arg)
	if len(ids) == 0 {
		return nil, fmt.Errorf(
			"符号 %q 不在图中（图未覆盖或名字有误）；近似候选: [%s]。确认图未覆盖时回落 grep，并把该符号记入本节点产出物的「图覆盖债」小节",
			arg, strings.Join(symFuzzy(v, arg), ", "))
	}
	for _, id := range ids {
		if v.Nodes[id].Kind != "model" {
			return nil, fmt.Errorf("%s 不是 model 节点（kind=%s），entity 只查数据实体", arg, v.Nodes[id].Kind)
		}
	}
	primaryID, err := entityPrimary(v, arg, ids)
	if err != nil {
		return nil, err
	}

	primary := v.Nodes[primaryID]
	r := &EntityResult{
		View:        v.Name,
		Query:       arg,
		Model:       symMatchFor(v, repoRoot, primaryID),
		ProjScanned: primary.ProjScanned,
	}
	if !r.ProjScanned {
		r.Warning = "该实体未做投影盘点——链可能不完整，勿当序列化边界清单用；盘点走扫描派发"
	}

	twinIDs := map[string]bool{}
	for _, id := range ids {
		if id != primaryID {
			twinIDs[id] = true
		}
	}
	for _, p := range v.Projections {
		if p.Status == "deleted" {
			continue
		}
		remote, ok := projectionTwinOther(p, primaryID)
		if !ok || !entityModelLive(v, remote) {
			continue
		}
		twinIDs[remote] = true
	}
	for id := range twinIDs {
		if id == primaryID || !entityModelLive(v, id) {
			delete(twinIDs, id)
		}
	}
	for id := range twinIDs {
		r.Twins = append(r.Twins, symMatchFor(v, repoRoot, id))
	}
	sort.Slice(r.Twins, func(i, j int) bool { return r.Twins[i].ID < r.Twins[j].ID })

	for _, p := range v.Projections {
		if p.Status == "deleted" || p.Kind == "twin" {
			continue
		}
		if p.To == primaryID && entityNodeLive(v, p.From) {
			appendProjSite(r, p.Kind, ProjSite{SymMatch: symMatchFor(v, repoRoot, p.From), Via: "direct"})
		}
	}
	for twinID := range twinIDs {
		for _, p := range v.Projections {
			if p.Status == "deleted" || p.Kind == "twin" || p.To != twinID || !entityNodeLive(v, p.From) {
				continue
			}
			appendProjSite(r, p.Kind, ProjSite{SymMatch: symMatchFor(v, repoRoot, p.From), Via: "twin:" + twinID})
		}
	}
	sortProjSites(r.Typed)
	sortProjSites(r.Handroll)
	return r, nil
}

func entityPrimary(v *View, arg string, ids []string) (string, error) {
	var goIDs []string
	for _, id := range ids {
		if !strings.HasPrefix(v.Nodes[id].File, "web/") {
			goIDs = append(goIDs, id)
		}
	}
	switch {
	case len(goIDs) == 1:
		return goIDs[0], nil
	case len(goIDs) > 1:
		return "", fmt.Errorf("数据实体 %q 同侧多义，请用节点 id: %s", arg, strings.Join(goIDs, ", "))
	case len(ids) == 1:
		return ids[0], nil
	default:
		return "", fmt.Errorf("数据实体 %q 同侧多义，请用节点 id: %s", arg, strings.Join(ids, ", "))
	}
}

func projectionTwinOther(p ViewProjection, id string) (string, bool) {
	if p.Kind != "twin" {
		return "", false
	}
	if p.From == id {
		return p.To, true
	}
	if p.To == id {
		return p.From, true
	}
	return "", false
}

func entityModelLive(v *View, id string) bool {
	n, ok := v.Nodes[id]
	return ok && n.Kind == "model" && n.Status != "deleted"
}

func entityNodeLive(v *View, id string) bool {
	n, ok := v.Nodes[id]
	return ok && n.Status != "deleted"
}

func appendProjSite(r *EntityResult, kind string, site ProjSite) {
	// The kind is validated at graph boundaries; malformed in-memory views are ignored here.
	switch kind {
	case "typed":
		r.Typed = append(r.Typed, site)
	case "handroll":
		r.Handroll = append(r.Handroll, site)
	}
}

func sortProjSites(sites []ProjSite) {
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].ID != sites[j].ID {
			return sites[i].ID < sites[j].ID
		}
		return sites[i].Via < sites[j].Via
	})
}
