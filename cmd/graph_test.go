// graph 命令测试：validate/chain/who-calls 的 JSON 契约与退出语义。
package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/codegraph"
)

// runGraph 执行 handoff graph <args...>，返回 stdout 与 err。
func runGraph(t *testing.T, args ...string) (string, error) {
	t.Helper()
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(append([]string{"graph"}, args...))
	defer rootCmd.SetArgs(nil)
	err := rootCmd.Execute()
	return buf.String(), err
}

const fixtureRepo = "../internal/codegraph/testdata/repo"

func TestGraphValidate(t *testing.T) {
	out, err := runGraph(t, "validate", "--repo", fixtureRepo)
	if err != nil {
		t.Fatalf("validate 应通过: %v\n%s", err, out)
	}
	var r map[string]any
	if json.Unmarshal([]byte(out), &r) != nil ||
		r["nodes"].(float64) != 7 || r["unscannedEntries"].(float64) != 1 {
		t.Fatalf("统计 JSON 形状: %s", out)
	}
}

func TestGraphChainDefaultDepth(t *testing.T) {
	out, err := runGraph(t, "chain", "e_run", "--repo", fixtureRepo)
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Nodes   []map[string]any `json:"nodes"`
		Warning string           `json:"warning"`
	}
	if json.Unmarshal([]byte(out), &r) != nil {
		t.Fatalf("非法 JSON: %s", out)
	}
	// 默认深度 2：e_run + runE + do
	if len(r.Nodes) != 3 || r.Warning == "" {
		t.Fatalf("默认深度/警示: %d %q", len(r.Nodes), r.Warning)
	}
}

func TestGraphWhoCallsUnionByName(t *testing.T) {
	// 按名字解析 + 多参数并集 + --depth 0 不限
	out, err := runGraph(t, "who-calls", "Server.Save", "Server.Do", "--depth", "0", "--repo", fixtureRepo)
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Foci  []string         `json:"foci"`
		Nodes []map[string]any `json:"nodes"`
	}
	if json.Unmarshal([]byte(out), &r) != nil {
		t.Fatalf("非法 JSON: %s", out)
	}
	if len(r.Foci) != 2 || len(r.Nodes) != 4 {
		t.Fatalf("并集: foci=%v nodes=%d", r.Foci, len(r.Nodes))
	}
}

func TestGraphChainWithView(t *testing.T) {
	out, err := runGraph(t, "chain", "e_run", "--depth", "0", "--view", "branch-x", "--repo", fixtureRepo)
	if err != nil {
		t.Fatal(err)
	}
	// branch-x 视图里链路走 audit 不走 save
	if !bytes.Contains([]byte(out), []byte("n_audit")) || bytes.Contains([]byte(out), []byte("n_save")) {
		t.Fatalf("视图叠加没生效: %s", out)
	}
}

func TestGraphResolveErrorListsCandidates(t *testing.T) {
	out, err := runGraph(t, "chain", "Do", "--repo", fixtureRepo)
	if err == nil {
		t.Fatalf("模糊名应报错: %s", out)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("Server.Do")) {
		t.Fatalf("报错要带候选: %v", err)
	}
}

func TestGraphDomains(t *testing.T) {
	out, err := runGraph(t, "domains", "--repo", fixtureRepo)
	if err != nil {
		t.Fatalf("domains 应通过: %v\n%s", err, out)
	}
	var r struct {
		View    string `json:"view"`
		Domains []struct {
			ID         string   `json:"id"`
			Children   []string `json:"children"`
			Funcs      int      `json:"funcs"`
			Interfaces []string `json:"interfaces"`
		} `json:"domains"`
		Warning string `json:"warning"`
	}
	if json.Unmarshal([]byte(out), &r) != nil {
		t.Fatalf("非法 JSON: %s", out)
	}
	if len(r.Domains) != 4 || r.Domains[0].ID != "d_cli" || r.Warning != "" {
		t.Fatalf("领域树形状: %s", out)
	}
	if r.Domains[1].ID != "d_svc" || len(r.Domains[1].Children) != 2 {
		t.Fatalf("嵌套子领域没出来: %s", out)
	}
}

func TestGraphValidateReportsDomainCount(t *testing.T) {
	out, err := runGraph(t, "validate", "--repo", fixtureRepo)
	if err != nil {
		t.Fatalf("validate 应通过: %v\n%s", err, out)
	}
	var r map[string]any
	if json.Unmarshal([]byte(out), &r) != nil || r["domains"].(float64) != 4 {
		t.Fatalf("validate 要报领域计数: %s", out)
	}
}

// check：fixture target 与 baseline 套合后输出 Report JSON。
func TestGraphCheck(t *testing.T) {
	out, err := runGraph(t, "check", "--repo", fixtureRepo)
	if err != nil {
		t.Fatalf("check 应通过: %v\n%s", err, out)
	}
	for _, want := range []string{`"fails"`, `"warns"`} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Fatalf("check 输出缺字段 %s: %s", want, out)
		}
	}
}

func TestGraphCheckMissingTargetFails(t *testing.T) {
	// 指向一个没有 target.json 的仓：必须报错退出，不能静默通过。
	_, err := runGraph(t, "check", "--repo", t.TempDir())
	if err == nil {
		t.Fatal("无 target 的 check 必须失败")
	}
}

func TestGraphAbsorb(t *testing.T) {
	repo := t.TempDir()
	copyFixtureRepo(t, fixtureRepo, repo)
	out, err := runGraph(t, "absorb", "branch-x", "--repo", repo, "--commit", "abc123", "--branch", "main")
	if err != nil {
		t.Fatalf("absorb 应通过: %v\n%s", err, out)
	}
	g, err := codegraph.LoadGraph(repo)
	if err != nil {
		t.Fatal(err)
	}
	if g.Meta.Commit != "abc123" || g.Meta.Branch != "main" {
		t.Fatalf("meta 来源戳未刷新: %+v", g.Meta)
	}
	if _, ok := g.Nodes["n_audit"]; !ok {
		t.Fatal("added 节点未写入基线")
	}
	if _, ok := g.Nodes["n_save"]; ok {
		t.Fatal("deleted 节点仍在基线")
	}
	if _, err := os.Stat(filepath.Join(repo, "codegraph", "diffs", "branch-x.json")); !os.IsNotExist(err) {
		t.Fatalf("diff 应在写盘成功后删除，stat=%v", err)
	}
}

func copyFixtureRepo(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		from := filepath.Join(src, entry.Name())
		to := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := os.MkdirAll(to, 0o755); err != nil {
				t.Fatal(err)
			}
			copyFixtureRepo(t, from, to)
			continue
		}
		raw, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
