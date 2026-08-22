package codegraph

import (
	"strings"
	"testing"
)

func TestProjectionValidate(t *testing.T) {
	g := &Graph{
		Containers: map[string]Container{"c": {Label: "c"}},
		Nodes: map[string]Node{
			"p":  {Kind: "func", Container: "c"},
			"m":  {Kind: "model", Container: "c"},
			"m2": {Kind: "model", Container: "c"},
		},
		Projections: []Projection{
			{"p", "m", "typed"},
			{"p", "m", "handroll"},
			{"m", "m2", "twin"},
		},
	}
	if issues := Validate(g); len(issues) != 0 {
		t.Fatalf("合法投影不应报错: %v", issues)
	}
	g.Projections = append(g.Projections,
		Projection{"p", "m", "xx"},
		Projection{"p", "ghost", "typed"},
	)
	issues := Validate(g)
	if len(issues) != 2 {
		t.Fatalf("非法投影应各报一条: %v", issues)
	}
	for _, want := range []string{"投影", "xx", "ghost"} {
		found := false
		for _, issue := range issues {
			if strings.Contains(issue, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("投影问题缺少 %q: %v", want, issues)
		}
	}
}

func TestProjectionMerge(t *testing.T) {
	g := &Graph{
		Containers: map[string]Container{"c": {Label: "c"}},
		Nodes: map[string]Node{
			"p":  {Kind: "func", Container: "c"},
			"m":  {Kind: "model", Container: "c"},
			"m2": {Kind: "model", Container: "c"},
		},
		Projections: []Projection{{"p", "m", "typed"}},
	}
	d := &Diff{
		View:               "projection-view",
		ProjectionsAdded:   []Projection{{"p", "m", "handroll"}, {"m", "m2", "twin"}},
		ProjectionsDeleted: []Projection{{"p", "m", "typed"}},
	}
	v := Merge(g, d)
	status := map[string]string{}
	for _, p := range v.Projections {
		status[p.From+"\x00"+p.To+"\x00"+p.Kind] = p.Status
	}
	if status["p\x00m\x00typed"] != "deleted" ||
		status["p\x00m\x00handroll"] != "added" ||
		status["m\x00m2\x00twin"] != "added" {
		t.Fatalf("投影状态: %v", status)
	}
}

func TestProjectionAbsorb(t *testing.T) {
	g := &Graph{
		Containers: map[string]Container{"c": {Label: "c"}},
		Nodes: map[string]Node{
			"p":    {Kind: "func", Container: "c"},
			"m":    {Kind: "model", Container: "c"},
			"dead": {Kind: "func", Container: "c"},
		},
		Projections: []Projection{{"p", "m", "typed"}, {"dead", "m", "handroll"}},
	}
	d := &Diff{
		ProjectionsAdded:   []Projection{{"p", "m", "handroll"}, {"dead", "m", "twin"}},
		ProjectionsDeleted: []Projection{{"p", "m", "typed"}},
		NodesDeleted:       []string{"dead"},
	}
	out := Absorb(g, d)
	want := []Projection{{"p", "m", "handroll"}}
	if len(out.Projections) != len(want) || out.Projections[0] != want[0] {
		t.Fatalf("吸收后的投影: got=%v want=%v", out.Projections, want)
	}
}
