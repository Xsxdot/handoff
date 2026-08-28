package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fixtureGraph(t *testing.T) (*Graph, string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	root := filepath.Join(filepath.Dir(file), "testdata", "mod")
	data, err := os.ReadFile(filepath.Join(root, "baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g Graph
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	return &g, root
}

func TestGuardReturnIsBranchChildNotSequentialRoot(t *testing.T) {
	g, root := fixtureGraph(t)
	flows := Extract(g, []string{"n_run"}, root)
	flow, ok := flows["n_run"]
	if !ok {
		t.Fatal("Extract did not write n_run")
	}
	var branch *FlowStep
	steps := make(map[string]FlowStep, len(flow.Steps))
	for i := range flow.Steps {
		step := flow.Steps[i]
		steps[step.ID] = step
		if step.Kind == "branch" && strings.Contains(step.Cond, "err != nil") {
			branch = &flow.Steps[i]
		}
	}
	if branch == nil {
		t.Fatal("guard branch missing")
	}
	if len(branch.Then) == 0 && len(branch.Else) == 0 {
		t.Fatal("guard branch has no child")
	}
	var returnID string
	for _, id := range append(append([]string{}, branch.Then...), branch.Else...) {
		if steps[id].Kind == "return" {
			returnID = id
		}
	}
	if returnID == "" {
		t.Fatal("guard return is not referenced by branch")
	}
	for _, step := range flow.Steps {
		if step.ID == returnID && step.Kind == "return" {
			for _, rootID := range topLevelStepIDs(flow.Steps) {
				if rootID == returnID {
					t.Fatalf("guard return %s remained a sequential root", returnID)
				}
			}
		}
	}
}

func TestInterfaceCallSetsIfaceAndToIsInterface(t *testing.T) {
	g, root := fixtureGraph(t)
	flow := Extract(g, []string{"n_run"}, root)["n_run"]
	var found bool
	for _, step := range flow.Steps {
		if step.Kind == "call" && step.To == "n_put_iface" {
			found = true
			if !step.Iface {
				t.Fatal("interface call is not marked iface")
			}
		}
		if step.Kind == "call" && step.To == "n_put_mem" {
			t.Fatal("interface call guessed concrete implementation")
		}
	}
	if !found {
		t.Fatal("interface call missing")
	}
}

func TestCallToMustExist(t *testing.T) {
	g, root := fixtureGraph(t)
	delete(g.Nodes, "n_save")
	flow := Extract(g, []string{"n_run"}, root)["n_run"]
	for _, step := range flow.Steps {
		if step.Kind == "call" && step.To == "n_save" {
			t.Fatalf("call points at missing node: %#v", step)
		}
	}
}

func TestSeedGoSeamsSkipsEntryAndTS(t *testing.T) {
	g := &Graph{
		Nodes: map[string]Node{
			"e_entry": {Kind: "entry", Container: "c_entry", File: "cmd/main.go"},
			"n_go":    {Kind: "func", Container: "c_go", File: "internal/go.go"},
			"n_ts":    {Kind: "func", Container: "c_ts", File: "web/app.tsx"},
			"n_same":  {Kind: "func", Container: "c_same", File: "internal/same.go"},
		},
		Containers: map[string]Container{
			"c_entry": {Kind: "入口", Entry: true, Domain: "d_entry"},
			"c_go":    {Kind: "类型方法", Domain: "d_b"},
			"c_ts":    {Kind: "TypeScript 函数组", Domain: "d_b"},
			"c_same":  {Kind: "类型方法", Domain: "d_entry"},
		},
		Edges: [][]string{{"e_entry", "n_go"}, {"e_entry", "n_ts"}, {"e_entry", "n_same"}},
	}
	got := SeedGoSeams(g)
	if len(got) != 1 || got[0] != "n_go" {
		t.Fatalf("SeedGoSeams() = %#v, want [n_go]", got)
	}
}

func topLevelStepIDs(steps []FlowStep) []string {
	refs := map[string]bool{}
	for _, step := range steps {
		for _, id := range append(append(append([]string{}, step.Then...), step.Else...), step.Body...) {
			refs[id] = true
		}
	}
	var roots []string
	for _, step := range steps {
		if !refs[step.ID] {
			roots = append(roots, step.ID)
		}
	}
	return roots
}
