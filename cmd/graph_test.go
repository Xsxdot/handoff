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
		r["nodes"].(float64) != 8 || r["unscannedEntries"].(float64) != 1 {
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

func TestGraphSym(t *testing.T) {
	out, err := runGraph(t, "sym", "Do", "--repo", fixtureRepo)
	if err != nil {
		t.Fatalf("sym 应通过: %v\n%s", err, out)
	}
	var r struct {
		Matches []struct {
			ID        string `json:"id"`
			Anchor    string `json:"anchor"`
			Line      int    `json:"line"`
			Signature string `json:"signature"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("非法 JSON: %v\n%s", err, out)
	}
	if len(r.Matches) != 1 || r.Matches[0].ID != "n_do" || r.Matches[0].Anchor != "ok" ||
		r.Matches[0].Line != 4 || r.Matches[0].Signature == "" {
		t.Fatalf("sym 结果: %s", out)
	}
}

func TestGraphSymMiss(t *testing.T) {
	out, err := runGraph(t, "sym", "Nope", "--repo", fixtureRepo)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("图未覆盖")) {
		t.Fatalf("sym 未命中错误: err=%v out=%s", err, out)
	}
}

func TestGraphEntity(t *testing.T) {
	out, err := runGraph(t, "entity", "Task", "--repo", fixtureRepo)
	if err != nil {
		t.Fatalf("entity 应通过: %v\n%s", err, out)
	}
	var r struct {
		Model    map[string]any   `json:"model"`
		Typed    []map[string]any `json:"typed"`
		Handroll []map[string]any `json:"handroll"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("非法 JSON: %v\n%s", err, out)
	}
	if r.Model["id"] != "m_task" || len(r.Typed) == 0 || len(r.Handroll) == 0 {
		t.Fatalf("entity 输出形状: %s", out)
	}
}

func TestGraphResolveDoc(t *testing.T) {
	repo := t.TempDir()
	copyFixtureRepo(t, fixtureRepo, repo)
	doc := filepath.Join(repo, "doc.md")
	if err := os.WriteFile(doc, []byte("`svc/server.go#Do` `svc/server.go#Gone`"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runGraph(t, "resolve", "--doc", doc, "--repo", repo)
	if err == nil || !bytes.Contains([]byte(out), []byte(`"anchor"`)) || !bytes.Contains([]byte(out), []byte(`"vanished"`)) {
		t.Fatalf("坏文档锚点应非零并输出结果: err=%v out=%s", err, out)
	}
}

func TestGraphContractSet(t *testing.T) {
	repo := t.TempDir()
	copyFixtureRepo(t, fixtureRepo, repo)
	out, err := runGraph(t, "contract", "set", "--from", "d_cmd", "--to", "d_svc", "--entries", "svc.Server", "--budget", "3", "--repo", repo)
	if err != nil {
		t.Fatalf("contract set 应通过: %v\n%s", err, out)
	}
	if !bytes.Contains([]byte(out), []byte(`"before"`)) || !bytes.Contains([]byte(out), []byte(`"after"`)) {
		t.Fatalf("应输出前后对照: %s", out)
	}
	target, err := codegraph.LoadTarget(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(target.Contracts) != 1 || target.Contracts[0].LegacyBudget != 3 || len(target.Contracts[0].Entries) != 1 {
		t.Fatalf("contract set 写回: %+v", target.Contracts)
	}
	_, err = runGraph(t, "contract", "set", "--from", "d_cmd", "--to", "d_svc", "--entries", "svc.Other", "--repo", repo)
	if err != nil {
		t.Fatalf("未传 budget 的 contract set 应通过: %v", err)
	}
	target, err = codegraph.LoadTarget(repo)
	if err != nil {
		t.Fatal(err)
	}
	if target.Contracts[0].LegacyBudget != 3 || len(target.Contracts[0].Entries) != 1 || target.Contracts[0].Entries[0] != "svc.Other" {
		t.Fatalf("未传 budget 不应覆盖旧值: %+v", target.Contracts)
	}
	_, err = runGraph(t, "contract", "set", "--from", "d_cmd", "--to", "d_svc", "--budget", "0", "--repo", repo)
	if err != nil {
		t.Fatalf("显式 budget=0 的 contract set 应通过: %v", err)
	}
	target, err = codegraph.LoadTarget(repo)
	if err != nil {
		t.Fatal(err)
	}
	if target.Contracts[0].LegacyBudget != 0 || len(target.Contracts[0].Entries) != 1 || target.Contracts[0].Entries[0] != "svc.Other" {
		t.Fatalf("显式 budget=0 或清单写回不对: %+v", target.Contracts)
	}
}

func TestGraphSummary(t *testing.T) {
	out, err := runGraph(t, "summary", "--repo", fixtureRepo)
	if err != nil {
		t.Fatalf("summary 应通过: %v\n%s", err, out)
	}
	if !bytes.Contains([]byte(out), []byte("节点")) || !bytes.Contains([]byte(out), []byte("graph sym")) {
		t.Fatalf("summary 内容: %s", out)
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
