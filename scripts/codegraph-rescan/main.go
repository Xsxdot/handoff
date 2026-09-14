// B233.23 基线全量重扫扫描器（B233.26 刀3 复用）。
// 输入：handoff 仓库工作树（只读）；旧 codegraph/baseline.json（只读：容器标签复用、
// web 容器映射、lifecycle/handroll 重锚）。
// 输出：-out 指定的 baseline JSON + 同目录 report.txt。
//
// B233.26 刀3：domOverride 里钉死的 k_agentd_Hub/Shutdown/pullTracker/fn/model→d_gateway
// 是 D1 留驻面。方向反转后这些类型已迁 orchestration，容器 id 应变为
// k_orchestration_* 并由 pkgDomain 归 d_orchestration；那五条 override 必须删掉，
// 否则会把已迁走的符号继续钉在 gateway。
package main

import (
	"encoding/json"
	"os/exec"
	"regexp"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const modulePath = "github.com/Xsxdot/handoff"

var (
	repoRoot string
	oldPath  string
	outPath  string
	fset     = token.NewFileSet()
	oldBase  *Graph
	notes    []string

	oldLabels = map[string]string{}
	bestDom   = map[string]string{}
	// 阶段二拟登记的 best 映射（对旧 best 有意修订处），阶段一 baseline 的
	// container.domain 按本表 + 包→域映射写，保证两阶段一致。
	// B233.26 刀3：D1 方向反转后，B233.23 钉死的五条 gateway override 全部删除——
	// k_agentd_Hub/Shutdown/pullTracker 已随迁 orchestration（容器 id 变
	// k_orchestration_*，由 pkgDomain 归 d_orchestration），k_agentd_fn/model 剩留
	// gateway 文件仍由 best.json 登记为 d_gateway，无需 override 兜底。保留空表表示
	// 本卡对所有既存 best 登记不再覆盖；新结论以重扫后的 container.domain 为准。
	domOverride = map[string]string{}

	scanned   []*pkgInfo
	pkgByDir  = map[string]*pkgInfo{}
	pkgByPath = map[string]*pkgInfo{}
)

// ---------- graph shapes ----------

type Meta struct {
	Project   string `json:"project"`
	Branch    string `json:"branch"`
	Commit    string `json:"commit"`
	ScannedAt string `json:"scannedAt"`
	Generator string `json:"generator"`
}
type Domain struct {
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Summary string `json:"summary,omitempty"`
	Desc    string `json:"desc,omitempty"`
	Parent  string `json:"parent,omitempty"`
}
type Container struct {
	Label  string `json:"label"`
	Kind   string `json:"kind"`
	Entry  bool   `json:"entry,omitempty"`
	Domain string `json:"domain,omitempty"`
}
type TestRef struct {
	Name string `json:"name"`
	File string `json:"file"`
}
type Node struct {
	Kind      string        `json:"kind"`
	Container string        `json:"container"`
	Order     int           `json:"order,omitempty"`
	Name      string        `json:"name"`
	File      string        `json:"file"`
	Line      int           `json:"line"`
	Signature string        `json:"signature,omitempty"`
	Params    [][]string    `json:"params,omitempty"`
	Returns   string        `json:"returns,omitempty"`
	Summary   string        `json:"summary,omitempty"`
	Tests     []TestRef     `json:"tests,omitempty"`
	Fields    [][]string    `json:"fields,omitempty"`
	ModelKind string        `json:"modelKind,omitempty"`
	Channel   string        `json:"channel,omitempty"`
	Body      *ast.FuncDecl `json:"-"`
	PkgDir    string        `json:"-"`
	Decl      ast.Decl      `json:"-"`
}
type LifecycleRef struct {
	Who   string `json:"who"`
	Model string `json:"model"`
	Kind  string `json:"kind"`
	Field string `json:"field,omitempty"`
}
type Graph struct {
	Meta        Meta                  `json:"meta"`
	Domains     map[string]Domain     `json:"domains,omitempty"`
	Containers  map[string]*Container `json:"containers"`
	Nodes       map[string]*Node      `json:"nodes"`
	Edges       [][2]string           `json:"edges"`
	Implements  [][2]string           `json:"implements,omitempty"`
	Projections [][3]string           `json:"projections,omitempty"`
	Lifecycle   []LifecycleRef        `json:"lifecycle,omitempty"`
	Packages    map[string]Package    `json:"packages,omitempty"`
}
type Package struct {
	Summary string `json:"summary"`
}

func note(format string, args ...any) {
	notes = append(notes, fmt.Sprintf(format, args...))
}

func main() {
	flag.StringVar(&repoRoot, "repo", ".", "repo root")
	flag.StringVar(&oldPath, "old", "", "old baseline.json")
	flag.StringVar(&outPath, "out", "scripts/codegraph-rescan/out/baseline.new.json", "output")
	flag.Parse()
	// packages.Load 返回的 p.Dir 恒为绝对路径；-repo 传相对路径（如 ../..）时
	// filepath.Rel(相对, 绝对) 报错，scanGo 会把全部 Go 包静默跳过、只扫到 TS。
	// 这里统一折叠成绝对路径，保证相对与绝对 -repo 两种调用同结果。
	if abs, err := filepath.Abs(repoRoot); err == nil {
		repoRoot = abs
	}
	if oldPath != "" {
		raw, err := os.ReadFile(oldPath)
		must(err)
		must(json.Unmarshal(raw, &oldBase))
		for id, c := range oldBase.Containers {
			oldLabels[id] = c.Label
		}
	}
	loadBest()
	head := gitHead()
	g := &Graph{
		Meta: Meta{
			Project:   "handoff",
			Branch:    gitBranch(),
			Commit:    head,
			ScannedAt: "2026-09-14",
			Generator: "handoff-executor/b233.26-full-rescan",
		},

		Containers: map[string]*Container{},
		Nodes:      map[string]*Node{},
		Packages:   map[string]Package{},
	}
	scanGo(g)
	scanTS(g)
	buildCLIEntries(g)
	buildHTTPWSEntries(g)
	buildMainEntry(g)
	linkWebEntry(g)
	reanchorLifecycle(g)
	reanchorHandroll(g)
	assignModelKind(g)
	deriveTypedProjections(g)
	deriveTwins(g)
	deriveImplements(g)
	deriveTests(g)
	assignContainerDomains(g)
	buildDomains(g)
	buildPackagesSection(g)
	assignOrders(g)
	checkReferences(g)
	emit(g)
	report(g)
}

func gitHead() string {
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitBranch() string {
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func loadBest() {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "codegraph", "best.json"))
	must(err)
	var best struct {
		Domains    map[string]struct {
			Label  string `json:"label"`
			Parent string `json:"parent"`
			Type   string `json:"type"`
		} `json:"domains"`
		Containers map[string]string `json:"containers"`
	}
	must(json.Unmarshal(raw, &best))
	bestDom = best.Containers
}

// ---------- scope ----------

func inScopeDir(rel string) bool {
	if rel == "." {
		return true
	}
	for _, p := range strings.Split(rel, "/") {
		if p == "testdata" || p == "test" || p == "node_modules" {
			return false
		}
	}
	for _, pre := range []string{"codegraph", "docs", "scripts", "desktop", "skills", "mobile"} {
		if rel == pre || strings.HasPrefix(rel, pre+"/") {
			return false
		}
	}
	return true
}

func relFile(p string) string {
	rel, err := filepath.Rel(repoRoot, p)
	must(err)
	return rel
}

// ---------- Go scan ----------

type pkgInfo struct {
	dir     string
	pkgPart string
	syntax  []*ast.File
	types   *types.Package
	info    *types.Info
	doc     string
}

func scanGo(g *Graph) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedImports | packages.NeedDeps | packages.NeedModule,
		Dir:     repoRoot,
		Tests: false,
		Fset:  fset,
		ParseFile: func(fset *token.FileSet, filename string, src []byte) (*ast.File, error) {
			return parser.ParseFile(fset, filename, src, parser.ParseComments)
		},
		Env: append(os.Environ(), "GOFLAGS=-mod=mod"),
	}
	pkgs, err := packages.Load(cfg, "./...")
	must(err)
	var typeErrs []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if p.Module == nil || p.Module.Path != modulePath {
			return
		}
		if len(p.Syntax) == 0 || len(p.GoFiles) == 0 {
			return
		}
		rel, err := filepath.Rel(repoRoot, p.Dir)
		if err != nil || !inScopeDir(rel) {
			return
		}
		pi := &pkgInfo{dir: rel, syntax: p.Syntax, types: p.Types, info: p.TypesInfo}
		scanned = append(scanned, pi)
		pkgByDir[rel] = pi
		pkgByPath[p.Types.Path()] = pi
		for _, e := range p.Errors {
			typeErrs = append(typeErrs, p.PkgPath+": "+e.Msg)
		}
	})
	if len(typeErrs) > 0 {
		note("类型检查报错 %d 处（前 5）:", len(typeErrs))
		for i, e := range typeErrs {
			if i >= 5 {
				break
			}
			note("  %s", e)
		}
	}
	// pkgPart
	leafCount := map[string]int{}
	for _, pi := range scanned {
		leafCount[filepath.Base(pi.dir)]++
	}
	for _, pi := range scanned {
		leaf := filepath.Base(pi.dir)
		pi.pkgPart = sanitizePart(leaf)
		if leafCount[leaf] > 1 {
			segs := strings.Split(pi.dir, "/")
			if len(segs) >= 2 {
				pi.pkgPart = sanitizePart(strings.Join(segs[len(segs)-2:], "_"))
			}
		}
		if part := oldPkgPartForDir(pi.dir); part != "" {
			pi.pkgPart = part
		}
		if pi.dir == "." {
			pi.pkgPart = "main"
		}
	}
	for _, pi := range scanned {
		pi.doc = packageDoc(pi)
	}

	// duplicate (pkg, symbol) files ordering for build-tag suffixes
	type idKey struct{ pkg, sym string }
	fileOrder := map[idKey][]string{}
	for _, pi := range scanned {
		for _, f := range pi.syntax {
			fname := relFile(fset.Position(f.Pos()).Filename)
			if strings.HasSuffix(fname, "_test.go") {
				continue
			}
			for _, d := range f.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					recvName, canon := funcIdent(d)
					display := canon
					if recvName != "" {
						display = recvName + "." + canon
					}
					k := idKey{pi.dir, display}
					if !contains(fileOrder[k], fname) {
						fileOrder[k] = append(fileOrder[k], fname)
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						if ts, ok := spec.(*ast.TypeSpec); ok {
							k := idKey{pi.dir, ts.Name.Name}
							if !contains(fileOrder[k], fname) {
								fileOrder[k] = append(fileOrder[k], fname)
							}
						}
					}
				}
			}
		}
	}
	for k := range fileOrder {
		sort.Strings(fileOrder[k])
	}

	// decls → nodes
	for _, pi := range scanned {
		for _, f := range pi.syntax {
			fname := relFile(fset.Position(f.Pos()).Filename)
			if strings.HasSuffix(fname, "_test.go") {
				continue
			}
			for _, d := range f.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					recvName, canon := funcIdent(d)
					k := idKey{pi.dir, canon}
					idx := indexOf(fileOrder[k], fname)
					suffix := dupSuffix(idx)
					var id, name, container string
					if recvName != "" {
						id = "n_" + pi.pkgPart + "_" + recvName + "_" + canon + suffix
						name = recvName + "." + canon
						container = "k_" + pi.pkgPart + "_" + recvName
					} else {
						id = "n_" + pi.pkgPart + "_" + canon + suffix
						name = canon
						container = "k_" + pi.pkgPart + "_fn"
					}
					n := &Node{
						Kind: "func", Container: container, Name: name,
						File: fname, Line: fset.Position(d.Pos()).Line,
						Summary: docText(d.Doc), Body: d, PkgDir: pi.dir, Decl: d,
					}
					n.Signature = sigBuf(d)
					n.Params, n.Returns = paramsOf(d)
					if prev, ok := g.Nodes[id]; ok {
						note("节点 id 冲突: %s（%s:%d 与 %s:%d）", id, prev.File, prev.Line, fname, n.Line)
					}
					g.Nodes[id] = n
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						ts, ok := spec.(*ast.TypeSpec)
						if !ok {
							continue
						}
						doc := d.Doc
						if ts.Doc != nil {
							doc = ts.Doc
						}
						k := idKey{pi.dir, ts.Name.Name}
						idx := indexOf(fileOrder[k], fname)
						suffix := dupSuffix(idx)
						id := "m_" + pi.pkgPart + "_" + ts.Name.Name + suffix
						n := &Node{
							Kind: "model", Container: "k_" + pi.pkgPart + "_model",
							Name: ts.Name.Name, File: fname,
							Line: fset.Position(ts.Pos()).Line,
							Summary: docText(doc), PkgDir: pi.dir, Decl: d,
						}
						if st, ok := ts.Type.(*ast.StructType); ok {
							n.Fields = fieldsOf(st)
						}
						n.Signature = "type " + sigBuf(ts)
						registerNodeID(pi, ts.Name.Name, id)
						if prev, ok := g.Nodes[id]; ok {
							note("节点 id 冲突: %s（%s:%d 与 %s:%d）", id, prev.File, prev.Line, fname, n.Line)
						}
						g.Nodes[id] = n
					}
				}
			}
		}
	}
	// containers
	for _, n := range g.Nodes {
		switch {
		case strings.HasSuffix(n.Container, "_fn"):
			ensureContainer(g, n.Container, pkgByDir[n.PkgDir].pkgPart+"（包级函数）", "函数组")
		case strings.HasSuffix(n.Container, "_model"):
			ensureContainer(g, n.Container, pkgByDir[n.PkgDir].pkgPart+" 实体", "实体")
		default:
			recv := n.Name[:strings.Index(n.Name, ".")]
			ensureContainer(g, n.Container, pkgByDir[n.PkgDir].pkgPart+"."+recv, "类型方法")
		}
	}
	// call edges
	for id, n := range g.Nodes {
		if n.Kind != "func" || n.Body == nil {
			continue
		}
		pi := pkgByDir[n.PkgDir]
		walkCalls(g, pi, id, n.Body)
	}
}

func dupSuffix(idx int) string {
	if idx > 0 {
		return fmt.Sprintf("_%d", idx+1)
	}
	return ""
}

func ensureContainer(g *Graph, id, label, kind string) *Container {
	if c, ok := g.Containers[id]; ok {
		return c
	}
	l := label
	if old, ok := oldLabels[id]; ok {
		l = old
	}
	c := &Container{Label: l, Kind: kind}
	g.Containers[id] = c
	return c
}

func sanitizePart(s string) string {
	return strings.NewReplacer(".", "_", "-", "_").Replace(s)
}

func oldPkgPartForDir(dir string) string {
	if oldBase == nil || dir == "." {
		return ""
	}
	parts := map[string]int{}
	for id, n := range oldBase.Nodes {
		if filepath.Dir(n.File) != dir {
			continue
		}
		// 从旧容器 id 推 pkg 段：k_<part>_fn / k_<part>_model 直接给 part；
		// 其余（k_<part>_<Recv>）按 n_/m_ 前缀段兜底
		cid := n.Container
		if strings.HasPrefix(cid, "k_") {
			mid := strings.TrimPrefix(cid, "k_")
			if strings.HasSuffix(mid, "_fn") || strings.HasSuffix(mid, "_model") {
				p := strings.TrimSuffix(strings.TrimSuffix(mid, "_model"), "_fn")
				if p != "" {
					parts[p] += 2
					continue
				}
			}
			// 类型方法容器 k_<part>_<Recv>：Recv 是 CamelCase 尾段，剥掉即 part
			if i := strings.LastIndex(mid, "_"); i > 0 {
				recv := mid[i+1:]
				if recv != "" && isCamel(recv) {
					parts[mid[:i]]++
				}
			}
		}
		body := strings.TrimPrefix(strings.TrimPrefix(id, "m_"), "n_")
		if i := strings.Index(body, "_"); i > 0 {
			parts[body[:i]]++
		}
	}
	best, bestN := "", 0
	for p, c := range parts {
		if c > bestN || (c == bestN && p < best) {
			best, bestN = p, c
		}
	}
	if best == "" {
		return ""
	}
	// 旧段权威：与 leaf 一致或为多段（collab_client、ledger_api、web_*）都采纳，
	// 防止 leaf 撞名时（client vs collab/client）偏离旧 id。
	return best
}

func packageDoc(pi *pkgInfo) string {
	// 优先 doc.go / package_name.go，否则第一个带 doc 的文件
	var fallback string
	for _, f := range pi.syntax {
		fname := relFile(fset.Position(f.Pos()).Filename)
		if strings.HasSuffix(fname, "_test.go") {
			continue
		}
		if f.Doc != nil {
			t := docText(f.Doc)
			if t == "" {
				continue
			}
			base := filepath.Base(fname)
			if base == "doc.go" || base == filepath.Base(pi.dir)+".go" {
				return t
			}
			if fallback == "" {
				fallback = t
			}
		}
	}
	return fallback
}

func funcIdent(d *ast.FuncDecl) (recvName, canon string) {
	canon = d.Name.Name
	if d.Recv != nil && len(d.Recv.List) > 0 {
		t := d.Recv.List[0].Type
		if sp, ok := t.(*ast.StarExpr); ok {
			t = sp.X
		}
		if ix, ok := t.(*ast.IndexExpr); ok {
			t = ix.X
		}
		if id, ok := t.(*ast.Ident); ok {
			return id.Name, canon
		}
	}
	return "", canon
}

func docText(d *ast.CommentGroup) string {
	if d == nil {
		return ""
	}
	var lines []string
	for _, c := range d.List {
		t := strings.TrimSpace(c.Text)
		t = strings.TrimPrefix(t, "//")
		t = strings.TrimPrefix(t, "/*")
		t = strings.TrimSuffix(t, "*/")
		t = strings.TrimPrefix(t, "*")
		t = strings.TrimSpace(t)
		if t == "" || strings.HasPrefix(t, "go:") {
			continue
		}
		lines = append(lines, t)
	}
	return strings.TrimSpace(strings.Join(lines, " "))
}

func sigBuf(n ast.Node) string {
	var buf strings.Builder
	if err := printer.Fprint(&buf, fset, n); err != nil {
		return ""
	}
	return buf.String()
}

// sigOf prints a func decl without body/doc.
func sigOf(d *ast.FuncDecl) string {
	cp := *d
	cp.Body = nil
	cp.Doc = nil
	return sigBuf(&cp)
}

func paramsOf(d *ast.FuncDecl) ([][]string, string) {
	var params [][]string
	if d.Type.Params != nil {
		for _, f := range d.Type.Params.List {
			t := sigBuf(f.Type)
			if len(f.Names) == 0 {
				params = append(params, []string{"", t, ""})
				continue
			}
			for _, name := range f.Names {
				params = append(params, []string{name.Name, t, ""})
			}
		}
	}
	var rets []string
	if d.Type.Results != nil {
		for _, f := range d.Type.Results.List {
			rets = append(rets, sigBuf(f.Type))
		}
	}
	return params, strings.Join(rets, ", ")
}

func fieldsOf(st *ast.StructType) [][]string {
	var out [][]string
	for _, f := range st.Fields.List {
		t := sigBuf(f.Type)
		if len(f.Names) == 0 {
			out = append(out, []string{t, t, ""})
			continue
		}
		for _, name := range f.Names {
			out = append(out, []string{name.Name, t, ""})
		}
	}
	return out
}

// ---------- call edges ----------

func walkCalls(g *Graph, pi *pkgInfo, callerID string, body ast.Node) {
	ast.Inspect(body, func(n ast.Node) bool {
		if e, ok := n.(*ast.CallExpr); ok {
			calleeID := resolveCallExpr(pi, e)
			if calleeID != "" && calleeID != callerID {
				addEdge(g, callerID, calleeID)
			}
		}
		if lit, ok := n.(*ast.CompositeLit); ok {
			// 模型字面量按“使用”连边（旧基线同口径：构造 wire DTO 即消费）
			if t := pi.info.TypeOf(lit); t != nil {
				if nn := namedOf(t); nn != nil && nn.Obj() != nil && nn.Obj().Pkg() != nil {
					if _, inRepo := pkgByPath[nn.Obj().Pkg().Path()]; inRepo {
						key := nn.Obj().Pkg().Path()
						if tp, ok2 := pkgByPath[key]; ok2 {
							mid := "m_" + tp.pkgPart + "_" + nn.Obj().Name()
							if _, exists := g.Nodes[mid]; exists && mid != callerID {
								addEdge(g, callerID, mid)
							}
						}
					}
				}
			}
		}
		return true
	})
}

func resolveCallExpr(pi *pkgInfo, e *ast.CallExpr) string {
	switch fn := e.Fun.(type) {
	case *ast.Ident:
		if obj := pi.info.Uses[fn]; obj != nil {
			return funcObjNode(obj)
		}
	case *ast.SelectorExpr:
		if sel := pi.info.Selections[fn]; sel != nil {
			return methodSelNode(sel)
		}
		if obj := pi.info.Uses[fn.Sel]; obj != nil {
			return funcObjNode(obj)
		}
	}
	return ""
}

func funcObjNode(obj types.Object) string {
	fn, ok := obj.(*types.Func)
	if !ok {
		return ""
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return ""
	}
	if sig.Recv() != nil {
		return methodNodeOf(fn)
	}
	pkg := fn.Pkg()
	if pkg == nil {
		return ""
	}
	tp, ok := pkgByPath[pkg.Path()]
	if !ok {
		return ""
	}
	if id, ok := nodeRegistry[tp.dir+"#"+fn.Name()]; ok {
		return id
	}
	return "n_" + tp.pkgPart + "_" + fn.Name()
}

func methodNodeOf(fn *types.Func) string {
	sig := fn.Type().(*types.Signature)
	recv := sig.Recv().Type()
	if ptr, ok := recv.(*types.Pointer); ok {
		recv = ptr.Elem()
	}
	named, ok := recv.(*types.Named)
	if !ok {
		return ""
	}
	if _, isIface := named.Underlying().(*types.Interface); isIface {
		return "" // 接口分派不入边（宁缺毋滥）
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	tp, ok := pkgByPath[obj.Pkg().Path()]
	if !ok {
		return ""
	}
	if id, ok := nodeRegistry[tp.dir+"#"+obj.Name()+"."+fn.Name()]; ok {
		return id
	}
	return "n_" + tp.pkgPart + "_" + obj.Name() + "_" + fn.Name()
}

func methodSelNode(sel *types.Selection) string {
	if sel.Kind() != types.MethodVal && sel.Kind() != types.MethodExpr {
		return ""
	}
	fn, ok := sel.Obj().(*types.Func)
	if !ok {
		return ""
	}
	return methodNodeOf(fn)
}

// nodeRegistry：pkgdir#symbol → 实际节点 id（含构建标签重名 _2 后缀的消解）。
var nodeRegistry = map[string]string{}

func registerNodeID(pi *pkgInfo, display, id string) {
	k := pi.dir + "#" + display
	if _, ok := nodeRegistry[k]; !ok {
		nodeRegistry[k] = id
	}
}

var edgeSet = map[[2]string]bool{}

func addEdge(g *Graph, from, to string) {
	k := [2]string{from, to}
	if from == to || from == "" || to == "" {
		return
	}
	if edgeSet[k] {
		return
	}
	edgeSet[k] = true
	g.Edges = append(g.Edges, k)
}

// ---------- implements ----------

func deriveImplements(g *Graph) {
	var impls [][2]string
	seen := map[[2]string]bool{}
	for _, pi := range scanned {
		for _, f := range pi.syntax {
			if strings.HasSuffix(relFile(fset.Position(f.Pos()).Filename), "_test.go") {
				continue
			}
			ast.Inspect(f, func(n ast.Node) bool {
				vs, ok := n.(*ast.ValueSpec)
				if !ok || len(vs.Values) == 0 || len(vs.Names) != 1 || vs.Names[0].Name != "_" {
					return true
				}
				var ifaceT types.Type
				if vs.Type != nil {
					ifaceT = pi.info.TypeOf(vs.Type)
				} else {
					ifaceT = pi.info.TypeOf(vs.Values[0])
				}
				if ifaceT == nil {
					return true
				}
				ifaceNamed := namedOf(ifaceT)
				if ifaceNamed == nil {
					return true
				}
				if _, ok := ifaceNamed.Underlying().(*types.Interface); !ok {
					return true
				}
				for _, v := range vs.Values {
					implT := pi.info.TypeOf(v)
					implNamed := namedOf(implT)
					if implNamed == nil {
						continue
					}
					if !types.Implements(implT, ifaceNamed.Underlying().(*types.Interface)) {
						continue
					}
					ifaceID := modelIDOfType(ifaceNamed)
					implID := modelIDOfType(implNamed)
					if ifaceID == "" || implID == "" || implID == ifaceID {
						continue
					}
					k := [2]string{implID, ifaceID}
					if !seen[k] {
						seen[k] = true
						impls = append(impls, k)
					}
				}
				return true
			})
		}
	}
	// 组装适配器 curated 候补（无 var _ 惯用法的结构化满足，按方法集核证）：
	// facadeAsRegistry（server.go 组装点）把账本门面适配成编制域持久化端口。
	for _, pair := range [][2]string{
		{"m_agentd_facadeAsRegistry", "m_schedclient_Registry"},
	} {
		implNode, ifaceNode := g.Nodes[pair[0]], g.Nodes[pair[1]]
		if implNode == nil || ifaceNode == nil {
			continue
		}
		implT, ifaceT := modelTypeOf(implNode), modelTypeOf(ifaceNode)
		if implT == nil || ifaceT == nil {
			continue
		}
		ifaceN, ok1 := ifaceT.(*types.Named)
		if !ok1 {
			continue
		}
		ifaceU, ok2 := ifaceN.Underlying().(*types.Interface)
		if !ok2 {
			continue
		}
		if types.Implements(implT, ifaceU) || types.Implements(types.NewPointer(implT), ifaceU) {
			k := [2]string{pair[0], pair[1]}
			if !seen[k] {
				seen[k] = true
				impls = append(impls, k)
			}
		} else {
			note("curated implements 不成立: %s→%s", pair[0], pair[1])
		}
	}
	// 旧基线 implements 重锚：端点重映射后按方法集复核（types.Implements），
	// 非 var _ 惯用法的绑定缝（如 Facade→Registry）由此补齐
	if oldBase != nil {
		for _, e := range oldBase.Implements {
			implID := remapNode(g, e[0])
			ifaceID := remapNode(g, e[1])
			if implID == "" || ifaceID == "" {
				note("implements 丢弃 %s→%s：端点未复现", e[0], e[1])
				continue
			}
			implT := modelTypeOf(g.Nodes[implID])
			ifaceT := modelTypeOf(g.Nodes[ifaceID])
			if implT == nil || ifaceT == nil {
				note("implements 丢弃 %s→%s：类型不可得", e[0], e[1])
				continue
			}
			ifaceN, ok1 := ifaceT.(*types.Named)
			if !ok1 {
				continue
			}
			ifaceU, ok2 := ifaceN.Underlying().(*types.Interface)
			if !ok2 || (!types.Implements(implT, ifaceU) && !types.Implements(types.NewPointer(implT), ifaceU)) {
				note("implements 丢弃 %s→%s：方法集复核不成立", e[0], e[1])
				continue
			}
			k := [2]string{implID, ifaceID}
			if !seen[k] {
				seen[k] = true
				impls = append(impls, k)
			}
		}
	}
	sort.Slice(impls, func(i, j int) bool {
		if impls[i][0] != impls[j][0] {
			return impls[i][0] < impls[j][0]
		}
		return impls[i][1] < impls[j][1]
	})
	g.Implements = impls
}

func namedOf(t types.Type) *types.Named {
	if t == nil {
		return nil
	}
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	n, _ := t.(*types.Named)
	return n
}

func modelIDOfType(n *types.Named) string {
	obj := n.Obj()
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	tp, ok := pkgByPath[obj.Pkg().Path()]
	if !ok {
		return ""
	}
	return "m_" + tp.pkgPart + "_" + obj.Name()
}

// ---------- CLI entries ----------

type cmdDecl struct {
	varName string
	use     string
	file    string
	line    int
	runE    ast.Expr // func literal / ident / selector
	runExpr bool     // Run instead of RunE
}

func buildCLIEntries(g *Graph) {
	pi := pkgByDir["cmd"]
	if pi == nil {
		return
	}
	cmds := map[string]*cmdDecl{} // varName -> decl
	// collect command literals
	for _, f := range pi.syntax {
		fname := relFile(fset.Position(f.Pos()).Filename)
		if strings.HasSuffix(fname, "_test.go") {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, v := range vs.Values {
				if u, ok := v.(*ast.UnaryExpr); ok && u.Op == token.AND {
					v = u.X
				}
				cl, ok := v.(*ast.CompositeLit)
				if !ok {
					continue
				}
				if !isCobraCommand(pi, cl) {
					continue
				}
				if i >= len(vs.Names) {
					continue
				}
				vname := vs.Names[i].Name
				cd := &cmdDecl{varName: vname, file: fname, line: fset.Position(cl.Pos()).Line}
				for _, elt := range cl.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, _ := kv.Key.(*ast.Ident)
					if key == nil {
						continue
					}
					switch key.Name {
					case "Use":
						if bl, ok := kv.Value.(*ast.BasicLit); ok {
							cd.use = strings.Trim(bl.Value, "`\"")
						}
					case "RunE":
						cd.runE = kv.Value
						cd.runExpr = false
					case "Run":
						if cd.runE == nil {
							cd.runE = kv.Value
							cd.runExpr = true
						}
					}
				}
				cmds[vname] = cd
			}
			return true
		})
	}
	// AddCommand wiring
	children := map[string][]string{}
	parentOf := map[string]string{}
	for _, f := range pi.syntax {
		ast.Inspect(f, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := ce.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "AddCommand" {
				return true
			}
			recvIdent, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if _, isPkg := pi.info.Uses[recvIdent].(*types.PkgName); isPkg {
				return true
			}
			for _, arg := range ce.Args {
				ai, ok := arg.(*ast.Ident)
				if !ok {
					continue
				}
				if _, ok := cmds[ai.Name]; !ok {
					continue
				}
				children[recvIdent.Name] = append(children[recvIdent.Name], ai.Name)
				parentOf[ai.Name] = recvIdent.Name
			}
			return true
		})
	}
	// root: command not added to any other
	var roots []string
	for name := range cmds {
		if parentOf[name] == "" {
			roots = append(roots, name)
		}
	}
	sort.Strings(roots)
	for _, root := range roots {
		walkCmdTreeRoot(g, pi, cmds, children, root)
	}
}

func contains(s []string, x string) bool { return indexOf(s, x) >= 0 }
func indexOf(s []string, x string) int {
	for i, v := range s {
		if v == x {
			return i
		}
	}
	return -1
}

func isCobraCommand(pi *pkgInfo, cl *ast.CompositeLit) bool {
	t := pi.info.TypeOf(cl.Type)
	if t == nil {
		return false
	}
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	return named.Obj() != nil && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == "github.com/spf13/cobra" && named.Obj().Name() == "Command"
}

// walkCmdTreeRoot：根命令自身不产 entry（由 e_main 承担），其 token 也不进子命令路径。
func walkCmdTreeRoot(g *Graph, pi *pkgInfo, cmds map[string]*cmdDecl, children map[string][]string, name string) {
	for _, ch := range children[name] {
		walkCmdTree(g, pi, cmds, children, ch, nil)
	}
}

func walkCmdTree(g *Graph, pi *pkgInfo, cmds map[string]*cmdDecl, children map[string][]string, name string, path []string) {
	cd := cmds[name]
	use0 := strings.Fields(cd.use)
	first := ""
	if len(use0) > 0 {
		first = strings.TrimLeft(use0[0], "-_")
	}
	full := path
	if first != "" {
		full = append(append([]string{}, path...), first)
	}
	if cd.runE != nil || len(children[name]) > 0 {
		id := "e_cli_" + strings.Join(full, "_")
		nameStr := strings.Join(full, " ")
		n := &Node{
			Kind: "entry", Container: "c_cli", Name: nameStr,
			File: cd.file, Line: cd.line, Channel: "cli",
		}
		g.Nodes[id] = n
		ensureEntryContainer(g, "c_cli", "CLI 命令", "d_cli")
		// run node
		if cd.runE != nil {
			if lit, ok := cd.runE.(*ast.FuncLit); ok {
				field := "RunE"
				if cd.runExpr {
					field = "Run"
				}
				rid := "n_cmd_" + cd.varName + "_" + field
				rn := &Node{
					Kind: "func", Container: "k_cmd_fn", Name: cd.varName + "." + field,
					File: cd.file, Line: fset.Position(lit.Pos()).Line,
					Summary: "",
					Signature: "func(cmd *cobra.Command, args []string) error",
					Params:    [][]string{{"cmd", "*cobra.Command", ""}, {"args", "[]string", ""}},
					Returns:   "error", Body: &ast.FuncDecl{Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{}}, Body: lit.Body},
					PkgDir: "cmd",
				}
				_ = rn.Summary
				if _, ok := g.Nodes[rid]; !ok {
					g.Nodes[rid] = rn
				}
				addEdge(g, id, rid)
				walkCalls(g, pi, rid, lit.Body)
			} else {
				// RunE: ident or selector — link resolved node(s)
				linkHandlerExpr(g, pi, id, cd.runE)
			}
		}
		for _, ch := range children[name] {
			walkCmdTree(g, pi, cmds, children, ch, full)
		}
	}
}

func ensureEntryContainer(g *Graph, id, label, domain string) {
	if _, ok := g.Containers[id]; !ok {
		g.Containers[id] = &Container{Label: label, Kind: "入口", Entry: true, Domain: domain}
	}
}

// ---------- HTTP/WS entries ----------

func buildHTTPWSEntries(g *Graph) {
	for _, pi := range scanned {
		for _, f := range pi.syntax {
			fname := relFile(fset.Position(f.Pos()).Filename)
			if strings.HasSuffix(fname, "_test.go") {
				continue
			}
			ast.Inspect(f, func(n ast.Node) bool {
				ce, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := ce.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle" {
					return true
				}
				if len(ce.Args) < 2 {
					return true
				}
				bl, ok := ce.Args[0].(*ast.BasicLit)
				if !ok {
					return true
				}
				route := strings.Trim(bl.Value, "`\"")
				if route == "" {
					return true
				}
				method, path := splitRoute(route)
				if method == "" {
					method = "GET"
				}
				isWS := strings.HasPrefix(path, "/ws/")
				container := "c_http"
				channel := "http"
				prefix := "e_http"
				if isWS {
					container = "c_ws"
					channel = "ws"
					prefix = "e_ws"
				}
				id := prefix + "_" + strings.ToLower(method) + "_" + manglePath(path)
				en := &Node{
					Kind: "entry", Container: container, Name: route,
					File: fname, Line: fset.Position(ce.Pos()).Line,
					Channel: channel,
				}
				if _, ok := g.Nodes[id]; !ok {
					g.Nodes[id] = en
				}
				if isWS {
					ensureEntryContainer(g, "c_ws", "长连接/WS", "d_gateway")
				} else {
					ensureEntryContainer(g, "c_http", "HTTP API", "d_gateway")
				}
				linkHandlerExpr(g, pi, id, ce.Args[1])
				return true
			})
		}
	}
}

func splitRoute(route string) (method, path string) {
	i := strings.Index(route, " ")
	if i > 0 && strings.ToUpper(route[:i]) == route[:i] && len(route[:i]) <= 7 {
		return route[:i], route[i+1:]
	}
	return "", route
}

func manglePath(p string) string {
	if p == "/" {
		return "root"
	}
	p = strings.TrimPrefix(p, "/")
	p = strings.ReplaceAll(p, "{", "")
	p = strings.ReplaceAll(p, "}", "")
	p = strings.ReplaceAll(p, "/", "_")
	p = strings.ReplaceAll(p, ".", "")
	p = strings.ReplaceAll(p, "-", "_")
	p = strings.ReplaceAll(p, ":", "_")
	return p
}

// linkHandlerExpr resolves handler expressions to function nodes and links entry→node.
func linkHandlerExpr(g *Graph, pi *pkgInfo, entryID string, e ast.Expr) {
	switch v := e.(type) {
	case *ast.ParenExpr:
		linkHandlerExpr(g, pi, entryID, v.X)
	case *ast.UnaryExpr:
		if v.Op == token.AND {
			linkHandlerExpr(g, pi, entryID, v.X)
		}
	case *ast.SelectorExpr:
		if sel := pi.info.Selections[v]; sel != nil {
			if id := methodSelNode(sel); id != "" {
				addEdge(g, entryID, id)
			}
		} else if obj := pi.info.Uses[v.Sel]; obj != nil {
			if id := funcObjNode(obj); id != "" {
				addEdge(g, entryID, id)
			}
		}
	case *ast.Ident:
		if obj := pi.info.Uses[v]; obj != nil {
			if id := funcObjNode(obj); id != "" {
				addEdge(g, entryID, id)
			}
		}
	case *ast.CallExpr:
		if id := resolveCallExpr(pi, v); id != "" {
			addEdge(g, entryID, id)
		}
		for _, a := range v.Args {
			linkHandlerExpr(g, pi, entryID, a)
		}
	case *ast.FuncLit:
		// inline handler: no node（旧基线同样不建）；把函数体内的调用记到入口名下
		walkCalls(g, pi, entryID, v.Body)
	}
}

// ---------- main entry ----------

func buildMainEntry(g *Graph) {
	pi := pkgByDir["."]
	if pi == nil {
		return
	}
	for _, f := range pi.syntax {
		fname := relFile(fset.Position(f.Pos()).Filename)
		if fname != "main.go" {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok || fd.Name.Name != "main" {
				return true
			}
			g.Nodes["e_main"] = &Node{
				Kind: "entry", Container: "c_main", Name: "handoff binary",
				File: fname, Line: fset.Position(fd.Pos()).Line, Channel: "cli",
			}
			ensureEntryContainer(g, "c_main", "handoff 二进制入口", "d_cli")
			walkCalls(g, pi, "e_main", fd.Body)
			return true
		})
	}
}

// ---------- TS scan ----------

type tsSym struct {
	kind   string // func | model
	name   string
	line   int
	sig    string
	summary string
}

func scanTS(g *Graph) {
	tsRoot := filepath.Join(repoRoot, "web", "src")
	var files []string
	filepath.Walk(tsRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".ts") && !strings.HasSuffix(p, ".tsx") {
			return nil
		}
		if strings.HasSuffix(p, ".test.ts") || strings.HasSuffix(p, ".test.tsx") {
			return nil
		}
		rel, _ := filepath.Rel(repoRoot, p)
		if !inScopeDir(rel) {
			return nil
		}
		files = append(files, p)
		return nil
	})
	sort.Strings(files)
	// dir -> container mapping: reuse old baseline (dir of old web nodes), else mint
	dirCont := map[string]string{}
	dirContModel := map[string]string{}
	if oldBase != nil {
		for id, n := range oldBase.Nodes {
			if !strings.HasPrefix(n.File, "web/") {
				continue
			}
			d := filepath.Dir(n.File)
			if strings.HasPrefix(id, "m_web") || strings.HasPrefix(id, "m_webui") {
				if _, ok := dirContModel[d]; !ok {
					dirContModel[d] = n.Container
				}
			} else {
				if _, ok := dirCont[d]; !ok {
					dirCont[d] = n.Container
				}
			}
		}
	}
	// also record old web container kinds/labels implicitly via oldLabels
	symCount := map[string]int{} // pkgpart|name -> n
	for _, p := range files {
		rel, _ := filepath.Rel(repoRoot, p)
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		lines := strings.Split(string(raw), "\n")
		dir := filepath.Dir(rel)
		base := filepath.Base(rel)
		if base == "main.tsx" {
			continue // entry only
		}
		part := tsPartForFile(rel, dir)
		fnCont, modelCont := "k_"+part, "k_"+part+"_model"
		fnKind, modelKind := "React 组件/函数", "TypeScript 模型"
		if strings.Contains(dir, "/api") {
			fnKind = "TypeScript 函数组"
			modelKind = "TypeScript 实体"
		}
		// 容器懒建：零节点容器不入图（配方红线）
		tsFnKind, tsModelKind := fnKind, modelKind
		for i, line := range lines {
			ln := i + 1
			trimmed := strings.TrimSpace(line)
			var sym *tsSym
			if m := tsTypeRe.FindStringSubmatch(trimmed); m != nil {
				sym = &tsSym{kind: "model", name: m[4], line: ln, sig: trimmed}
			} else if m := tsFnRe.FindStringSubmatch(trimmed); m != nil {
				sym = &tsSym{kind: "func", name: m[4], line: ln, sig: firstSigLine(lines, i)}
			} else if m := tsConstRe.FindStringSubmatch(trimmed); m != nil {
				sym = &tsSym{kind: "func", name: m[2], line: ln, sig: firstSigLine(lines, i)}
			} else if m := tsCompRe.FindStringSubmatch(trimmed); m != nil {
				// forwardRef/memo 组件
				sym = &tsSym{kind: "func", name: m[2], line: ln, sig: firstSigLine(lines, i)}
			} else if m := tsValueConstRe.FindStringSubmatch(trimmed); m != nil {
				// 大写常量按模型收（旧基线同口径：BINDING_LABEL/COLLAB_POLL_MS）
				sym = &tsSym{kind: "model", name: m[2], line: ln, sig: trimmed}
			}
			if sym == nil {
				continue
			}
			// JSDoc above
			sym.summary = tsDocAbove(lines, i)
			if sym.kind == "model" {
				id := "m_" + part + "_" + sym.name
				if _, exists := g.Nodes[id]; exists {
					continue
				}
				ensureContainer(g, modelCont, oldLabels[modelCont], tsModelKind)
				g.Nodes[id] = &Node{
					Kind: "model", Container: modelCont, Name: sym.name,
					File: rel, Line: sym.line, Signature: sym.sig,
					Summary: sym.summary,
				}
			} else {
				key := part + "|" + sym.name
				symCount[key]++
				suffix := dupSuffix(symCount[key] - 1)
				id := "n_" + part + "_" + sym.name + suffix
				if _, exists := g.Nodes[id]; exists {
					continue
				}
				ensureContainer(g, fnCont, oldLabels[fnCont], tsFnKind)
				g.Nodes[id] = &Node{
					Kind: "func", Container: fnCont, Name: sym.name,
					File: rel, Line: sym.line, Signature: sym.sig,
					Summary: sym.summary,
				}
			}
		}
	}
}

// tsPartForFile：web 侧模块键。旧基线权威映射（api 按文件、app 按目录）优先，
// 新文件按同构规则派生（api/<stem> 按文件、其余按目录；lib 沿用 utils 旧名）。
func tsPartForFile(rel, dir string) string {
	if oldPart := oldWebPartForFile(rel); oldPart != "" {
		return oldPart
	}
	stem := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	if strings.HasPrefix(dir, "web/src/api") {
		return "web_api_" + stem
	}
	if dir == "web/src" {
		return "web_App"
	}
	if dir == "web/src/lib" {
		return "web_utils"
	}
	return "web_" + strings.ReplaceAll(strings.TrimPrefix(dir, "web/src/"), "/", "_")
}

func isCamel(s string) bool {
	if s == "" || strings.ToLower(s) == s {
		return false
	}
	return !strings.ContainsAny(s, "_-")
}

func oldWebPartForFile(rel string) string {
	if oldBase == nil {
		return ""
	}
	parts := map[string]int{}
	for _, n := range oldBase.Nodes {
		if n.File != rel {
			continue
		}
		cid := n.Container
		if !strings.HasPrefix(cid, "k_") {
			continue
		}
		mid := strings.TrimPrefix(cid, "k_")
		mid = strings.TrimSuffix(mid, "_model")
		parts[mid] += 2
		body := strings.TrimPrefix(strings.TrimPrefix(n.Container, "m_"), "")
		_ = body
	}
	best, bestN := "", 0
	for p, c := range parts {
		if c > bestN || (c == bestN && p < best) {
			best, bestN = p, c
		}
	}
	return best
}

var (
	tsTypeRe  = regexp.MustCompile(`^(export\s+)?(default\s+)?(interface|type|class|enum)\s+([A-Za-z_$][\w$]*)`)
	tsFnRe    = regexp.MustCompile(`^(export\s+)?(default\s+)?(async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)`)
	tsConstRe = regexp.MustCompile(`^(export\s+)?const\s+([A-Za-z_$][\w$]*)\s*(?::[^=]+)?=\s*(async\s*)?\([^)]*\)\s*(?::[^=]+)?=>`)
	tsCompRe   = regexp.MustCompile(`^(export\s+)?const\s+([A-Za-z_$][\w$]*)\s*(?::[^=]+)?=\s*(React\.)?(forwardRef|memo)[<(]`)
	tsValueConstRe = regexp.MustCompile(`^(export\s+)?const\s+([A-Z][A-Z0-9_]*)\s*=\s*['"\x60\d]`)
)

func firstSigLine(lines []string, i int) string {
	l := strings.TrimSpace(lines[i])
	if j := strings.Index(l, "{"); j >= 0 {
		l = strings.TrimSpace(l[:j])
	}
	return l
}

func tsDocAbove(lines []string, i int) string {
	// scan upward for a */ ... /** block immediately above
	j := i - 1
	for j >= 0 && strings.TrimSpace(lines[j]) == "" {
		j--
	}
	if j < 0 || !strings.HasSuffix(strings.TrimSpace(lines[j]), "*/") {
		return ""
	}
	var docLines []string
	for k := j; k >= 0; k-- {
		t := strings.TrimSpace(lines[k])
		docLines = append([]string{t}, docLines...)
		if strings.HasPrefix(t, "/**") {
			break
		}
		if !strings.HasPrefix(t, "*") && !strings.HasSuffix(t, "*/") {
			return ""
		}
	}
	joined := strings.Join(docLines, " ")
	joined = strings.TrimPrefix(joined, "/**")
	joined = strings.TrimSuffix(joined, "*/")
	joined = strings.ReplaceAll(joined, "*", " ")
	return strings.TrimSpace(joinWords(joined))
}

func joinWords(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func linkWebEntry(g *Graph) {
	// e_web_main → App
	g.Nodes["e_web_main"] = &Node{
		Kind: "entry", Container: "c_web_main", Name: "Web 控制台",
		File: "web/src/main.tsx", Line: 6, Channel: "web",
	}
	ensureEntryContainer(g, "c_web_main", "Web 应用入口", "d_web_shell")
	if _, ok := g.Nodes["n_web_App_App"]; ok {
		addEdge(g, "e_web_main", "n_web_App_App")
	} else {
		note("e_web_main 未找到 App 节点")
	}
}

// ---------- lifecycle re-anchor ----------

// writerWhoOverride：写点在重构中下沉到同容器 helper，重锚时改指真实写入函数。
var writerWhoOverride = map[string]string{
	"n_ledger_Store_MoveCard|writer": "n_ledger_Store_moveCardTx",
}

func reanchorLifecycle(g *Graph) {
	if oldBase == nil {
		return
	}
	kept, dropped := 0, 0
	for _, ref := range oldBase.Lifecycle {
		who := remapNode(g, ref.Who)
		// 写位下沉到同容器 helper 的登记修正（B233.x 重构后写点在 moveCardTx）
		if alt, ok := writerWhoOverride[ref.Who+"|"+ref.Kind]; ok {
			if n2 := remapNode(g, alt); n2 != "" {
				who = n2
			}
		}
		model := remapNode(g, ref.Model)
		if who == "" || model == "" {
			dropped++
			note("lifecycle 丢弃 %s（%s→%s）：符号未在重扫图复现", ref.Kind, ref.Who, ref.Model)
			continue
		}
		if !verifyLifecycle(g, who, model, ref.Kind, ref.Field) {
			dropped++
			note("lifecycle 丢弃 %s（%s→%s %s）：源码复核不成立", ref.Kind, who, model, ref.Field)
			continue
		}
		g.Lifecycle = append(g.Lifecycle, LifecycleRef{Who: who, Model: model, Kind: ref.Kind, Field: ref.Field})
		kept++
	}
	note("lifecycle 重锚：保留 %d，丢弃 %d", kept, dropped)
}

// remapNode maps an old node id to the new node id by symbol suffix match.
func remapNode(g *Graph, oldID string) string {
	if _, ok := g.Nodes[oldID]; ok {
		return oldID // same-package symbol kept its id
	}
	body := strings.TrimPrefix(oldID, "n_")
	body = strings.TrimPrefix(body, "m_")
	prefix := "n_"
	if strings.HasPrefix(oldID, "m_") {
		prefix = "m_"
	}
	if i := strings.Index(body, "_"); i >= 0 {
		body = body[i+1:]
	}
	var cands []string
	for id := range g.Nodes {
		if strings.HasPrefix(id, prefix) && strings.HasSuffix(id, "_"+body) {
			cands = append(cands, id)
		}
	}
	if len(cands) == 1 {
		return cands[0]
	}
	if len(cands) > 1 {
		sort.Strings(cands)
		note("lifecycle 重锚歧义 %s -> %v（取首）", oldID, cands)
		return cands[0]
	}
	// 大小写重命名重锚：B233.26 十文件搬迁把 pullTracker→PullTracker 等导出化，
	// 符号本体未消亡只是首字母变大写。精确后缀匹配落空时做一次大小写不敏感回退，
	// 仍要求唯一命中（多处命中不猜，宁可丢弃并记备注）。
	lowerBody := strings.ToLower(body)
	for id := range g.Nodes {
		if !strings.HasPrefix(id, prefix) || !strings.HasSuffix(strings.ToLower(id), "_"+lowerBody) {
			continue
		}
		cands = append(cands, id)
	}
	if len(cands) == 1 {
		note("lifecycle 大小写重锚 %s -> %s（导出化改名）", oldID, cands[0])
		return cands[0]
	}
	if len(cands) > 1 {
		sort.Strings(cands)
		note("lifecycle 大小写重锚歧义 %s -> %v（丢弃）", oldID, cands)
	}
	return ""
}

func verifyLifecycle(g *Graph, who, model, kind, field string) bool {
	wn := g.Nodes[who]
	mn := g.Nodes[model]
	if wn == nil || mn == nil || wn.Kind != "func" {
		return false
	}
	// model 的类型对象：直接查模型所在包的 types.Scope
	if wn.Body == nil {
		// TS 节点走文本核验
		return tsVerifyLifecycle(wn, mn, kind, field)
	}
	mType := modelTypeOf(mn)
	if mType == nil {
		note("lifecycle debug: model 类型找不到 %s（%s）", model, mn.PkgDir)
		return false
	}
	mNamed, ok := mType.(*types.Named)
	if !ok {
		return false
	}
	pi := pkgByDir[wn.PkgDir]
	okCreator, okWriter := false, false
	raw := nodeSource(wn)
	ast.Inspect(wn.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CompositeLit:
			if t := pi.info.TypeOf(v); t != nil && sameNamed(t, mNamed) {
				okCreator = true
			}
		case *ast.AssignStmt:
			for _, lhs := range v.Lhs {
				sel, ok2 := lhs.(*ast.SelectorExpr)
				if !ok2 {
					continue
				}
				s := pi.info.Selections[sel]
				if s == nil || s.Recv() == nil {
					continue
				}
				r := s.Recv()
				if ptr, ok3 := r.(*types.Pointer); ok3 {
					r = ptr.Elem()
				}
				if sameNamed(r, mNamed) && fieldsMatch(sel.Sel.Name, field) {
					okWriter = true
				}
			}
		}
		return true
	})
	if kind == "creator" {
		// 签名返回类型即证明（配方：从函数返回类型能证明产出该 model）
		if sigReturnsModel(wn, pi, mNamed) {
			return true
		}
		// 持久化构造（SQL INSERT 等）：模型名出现在函数源文
		if okCreator || containsFold(raw, mn.Name) {
			return true
		}
		return false
	}
	// writer：内存字段赋值，或函数源文含该字段名（SQL 等持久化写入的转录证据）
	return okWriter || (field != "" && raw != "" && rawFieldMatch(raw, field))
}

func modelTypeOf(mn *Node) types.Type {
	pi := pkgByDir[mn.PkgDir]
	if pi == nil || pi.types == nil {
		return nil
	}
	obj := pi.types.Scope().Lookup(mn.Name)
	if obj == nil {
		return nil
	}
	return obj.Type()
}



// nodeSource 返回节点所在函数的源文（用于 writer 的持久化写入转录核验）。
func nodeSource(n *Node) string {
	if n.Body == nil || n.PkgDir == "" {
		return ""
	}
	f, err := os.ReadFile(filepath.Join(repoRoot, n.File))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(f), "\n")
	start := n.Line - 1
	if start < 0 || start >= len(lines) {
		return ""
	}
	// 取到函数结束的大致范围：从声明行到下一个顶级声明前，上限 400 行
	end := start + 1
	depth := 0
	opened := false
	for i := start; i < len(lines) && i < start+400; i++ {
		for _, ch := range lines[i] {
			if ch == '{' {
				depth++
				opened = true
			} else if ch == '}' {
				depth--
			}
		}
		end = i + 1
		if opened && depth <= 0 {
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

// tsVerifyLifecycle：TS hook 的 creator/writer 文本核验（creator 看模型名出现，
// writer 看 .field 赋值形态）。
func tsVerifyLifecycle(wn, mn *Node, kind, field string) bool {
	raw := tsNodeSource(wn)
	if raw == "" {
		return false
	}
	if kind == "creator" {
		return strings.Contains(raw, mn.Name)
	}
	return field != "" && containsFold(raw, field)
}

func tsNodeSource(n *Node) string {
	f, err := os.ReadFile(filepath.Join(repoRoot, n.File))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(f), "\n")
	start := n.Line - 1
	if start < 0 || start >= len(lines) {
		return ""
	}
	end := start + 1
	depth := 0
	opened := false
	for i := start; i < len(lines) && i < start+400; i++ {
		for _, ch := range lines[i] {
			if ch == '{' {
				depth++
				opened = true
			} else if ch == '}' {
				depth--
			}
		}
		end = i + 1
		if opened && depth <= 0 {
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

// sigReturnsModel：函数签名（结果位）出现该 model 类型（含指针/命名别名）。
func sigReturnsModel(wn *Node, pi *pkgInfo, mNamed *types.Named) bool {
	if wn.Body == nil || wn.Body.Type == nil || wn.Body.Type.Results == nil {
		return false
	}
	found := false
	for _, f := range wn.Body.Type.Results.List {
		ast.Inspect(f.Type, func(x ast.Node) bool {
			if idn, ok := x.(*ast.Ident); ok {
				if t := pi.info.TypeOf(idn); t != nil {
					if sameNamed(t, mNamed) || sameNamedPtr(t, mNamed) {
						found = true
					}
				}
			}
			return true
		})
		if found {
			break
		}
	}
	return found
}

// rawFieldMatch：源文含字段名的任一形态（Go 名、snake、去下划线小写）。
func rawFieldMatch(raw, field string) bool {
	if containsFold(raw, field) || strings.Contains(raw, snake(field)) {
		return true
	}
	return strings.Contains(strings.ToLower(strings.ReplaceAll(raw, "_", "")), strings.ToLower(strings.ReplaceAll(field, "_", "")))
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

// fieldsMatch 宽松匹配 Go 字段名与登记字段名（Stage/stage、ExpiresAt/expires_at）。
func fieldsMatch(a, b string) bool {
	if strings.EqualFold(a, b) {
		return true
	}
	return snake(a) == snake(b)
}

func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + 32)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func sameNamedPtr(t types.Type, n *types.Named) bool {
	if ptr, ok := t.(*types.Pointer); ok {
		return sameNamed(ptr.Elem(), n)
	}
	return false
}

func sameNamed(t types.Type, n *types.Named) bool {
	if t == nil {
		return false
	}
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	nn, ok := t.(*types.Named)
	return ok && nn == n
}

// ---------- handroll carry ----------

func reanchorHandroll(g *Graph) {
	if oldBase == nil {
		return
	}
	for _, p := range oldBase.Projections {
		if p[2] != "handroll" {
			continue
		}
		a, b := remapNode(g, p[0]), remapNode(g, p[1])
		if a == "" || b == "" {
			note("handroll 投影丢弃 %s→%s：端点未复现", p[0], p[1])
			continue
		}
		g.Projections = append(g.Projections, [3]string{a, b, "handroll"})
	}
}

// ---------- modelKind ----------

func assignModelKind(g *Graph) {
	hasLifecycle := map[string]bool{}
	for _, r := range g.Lifecycle {
		hasLifecycle[r.Model] = true
	}
	for id, n := range g.Nodes {
		if n.Kind != "model" {
			continue
		}
		switch {
		case hasLifecycle[id]:
			n.ModelKind = "entity"
		case strings.HasPrefix(n.File, "internal/proto/"), strings.HasPrefix(n.File, "web/src/api/"):
			n.ModelKind = "dto"
		case strings.HasSuffix(n.Name, "Config"), strings.HasSuffix(n.Name, "Options"):
			n.ModelKind = "config"
		default:
			n.ModelKind = "dto"
		}
		// 保留旧基线中已人工判过的取值（entity/config），不以兜底覆盖
		if oldBase != nil {
			if on, ok := oldBase.Nodes[id]; ok && (on.ModelKind == "entity" || on.ModelKind == "config") {
				if !(on.ModelKind == "entity" && !hasLifecycle[id] && n.ModelKind == "dto") {
					n.ModelKind = on.ModelKind
				}
			}
		}
	}
}

// ---------- projections ----------

func deriveTypedProjections(g *Graph) {
	// func/method 节点签名引用 proto model → typed 投影
	protoModels := map[string]*Node{} // type name -> node
	for _, n := range g.Nodes {
		if n.Kind == "model" && strings.HasPrefix(n.File, "internal/proto/") {
			protoModels[n.Name] = n
		}
	}
	seen := map[[2]string]bool{}
	for id, n := range g.Nodes {
		if n.Kind != "func" || n.Body == nil {
			continue
		}
		if strings.HasPrefix(n.File, "internal/proto/") {
			continue // proto 自身引用自身不构成跨缝投影（旧基线口径）
		}
		pi := pkgByDir[n.PkgDir]
		if pi == nil {
			continue
		}
		fd := n.Body
		refTypes := map[string]bool{}
		collect := func(ft *ast.FuncType) {
			if ft == nil {
				return
			}
			for _, fl := range []*ast.FieldList{ft.Params, ft.Results} {
				if fl == nil {
					continue
				}
				for _, f := range fl.List {
					ast.Inspect(f.Type, func(x ast.Node) bool {
						if idn, ok := x.(*ast.Ident); ok {
							if t := pi.info.TypeOf(idn); t != nil {
								if nn := namedOf(t); nn != nil && nn.Obj() != nil && nn.Obj().Pkg() != nil && nn.Obj().Pkg().Path() == modulePath+"/internal/proto" {
									refTypes[nn.Obj().Name()] = true
								}
							}
						}
						return true
					})
				}
			}
		}
		collect(fd.Type)
		for tname := range refTypes {
			if mn, ok := protoModels[tname]; ok {
				key := [2]string{id, mnKey(mn)}
				if !seen[key] {
					seen[key] = true
					g.Projections = append(g.Projections, [3]string{id, key[1], "typed"})
				}
			}
		}
	}
}

func mnKey(mn *Node) string {
	return "m_" + tsLikePart(mn) + "_" + mn.Name
}

func tsLikePart(n *Node) string {
	body := strings.TrimPrefix(n.Container, "k_")
	return strings.TrimSuffix(body, "_model")
}

func deriveTwins(g *Graph) {
	protoModels := map[string]string{}
	for id, n := range g.Nodes {
		if n.Kind == "model" && strings.HasPrefix(n.File, "internal/proto/") {
			protoModels[n.Name] = id
		}
	}
	webTypes := map[string]string{}
	for id, n := range g.Nodes {
		if n.Kind == "model" && n.Container == "k_web_api_types_model" {
			webTypes[n.Name] = id
		}
	}
	var names []string
	for name := range protoModels {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if wid, ok := webTypes[name]; ok {
			g.Projections = append(g.Projections, [3]string{protoModels[name], wid, "twin"})
		}
	}
}

// ---------- tests ----------

func deriveTests(g *Graph) {
	// per package: parse _test.go, find Test functions, ident usage
	for _, pi := range scanned {
		testFns := map[string]testLoc{} // test name -> {file, line}
		testIdents := map[string][]string{} // test name -> ident names
		for _, f := range pi.syntax {
			fname := relFile(fset.Position(f.Pos()).Filename)
			if !strings.HasSuffix(fname, "_test.go") {
				continue
			}
			ast.Inspect(f, func(n ast.Node) bool {
				fd, ok := n.(*ast.FuncDecl)
				if !ok || !strings.HasPrefix(fd.Name.Name, "Test") {
					return true
				}
				testFns[fd.Name.Name] = testLoc{file: fname, line: fset.Position(fd.Pos()).Line}
				names := map[string]bool{}
				ast.Inspect(fd.Body, func(x ast.Node) bool {
					if id, ok := x.(*ast.Ident); ok {
						names[id.Name] = true
					}
					return true
				})
				for k := range names {
					testIdents[fd.Name.Name] = append(testIdents[fd.Name.Name], k)
				}
				return true
			})
		}
		if len(testFns) == 0 {
			continue
		}
		for _, n := range g.Nodes {
			if n.PkgDir != pi.dir {
				continue
			}
			short := n.Name
			if i := strings.LastIndex(short, "."); i >= 0 {
				short = short[i+1:]
			}
			for tn, tf := range testFns {
				if contains(testIdents[tn], short) {
					n.Tests = append(n.Tests, TestRef{Name: tn, File: fmt.Sprintf("%s:%d", tf.file, tf.line)})
				}
			}
			sort.Slice(n.Tests, func(i, j int) bool { return n.Tests[i].Name < n.Tests[j].Name })
		}
	}
}

type testLoc struct {
	file string
	line int
}

// ---------- domains ----------

func assignContainerDomains(g *Graph) {
	for id, c := range g.Containers {
		if c.Entry {
			continue // entry containers already set
		}
		if d, ok := bestDom[id]; ok {
			// 旧 best 已登记的容器：除非本卡有意改登记（domOverride），沿用 best
			if o, has := domOverride[id]; has {
				c.Domain = o
			} else {
				c.Domain = d
			}
			continue
		}
		c.Domain = containerDomainFromNodes(g, id)
	}
}

func containerDomainFromNodes(g *Graph, cid string) string {
	count := map[string]int{}
	for _, n := range g.Nodes {
		if n.Container != cid {
			continue
		}
		d := pkgDomain(n.PkgDir, n.File)
		if d == "" && strings.HasPrefix(n.File, "web/src/") {
			// web：按容器族（k_<part>[_model]）在旧 best 的登记定域
			fam := strings.TrimSuffix(cid, "_model")
			if bd, ok := bestDom[fam]; ok {
				d = bd
			}
		}
		count[d]++
	}
	best, bestN := "", 0
	names := make([]string, 0, len(count))
	for d := range count {
		names = append(names, d)
	}
	sort.Strings(names)
	for _, d := range names {
		if count[d] > bestN {
			best, bestN = d, count[d]
		}
	}
	return best
}

var goPkgDomain = map[string]string{
	"internal/agentd": "d_gateway",
	"internal/orchestration": "d_orchestration",
	"internal/orchestration/internal/cacheplan": "d_orchestration",
	"internal/approval":            "d_orchestration",
	"internal/store":               "d_orchestration",
	"internal/workspace":           "d_workspace",
	"internal/workspace/internal/gitproc": "d_workspace",
	"internal/launcher":            "d_workspace",
	"internal/localsync":           "d_workspace",
	"internal/projectid":           "d_workspace",
	"internal/scheduling":          "d_scheduling",
	"internal/scheduling/internal/logging": "d_scheduling",
	"internal/schedclient":         "d_scheduling",
	"internal/ledger":              "d_ledger",
	"internal/ledgermirror":        "d_ledger",
	"internal/ledgerstep":          "d_ledger",
	"internal/ledger/api":          "d_ledger",
	"internal/collab":              "d_collab",
	"internal/collab/room":         "d_collab",
	"internal/collab/cursor":       "d_collab",
	"internal/collab/client":       "d_collab",
	"internal/keystone":            "d_keystone",
	"internal/keysclient":          "d_keystone",
	"internal/executor":            "d_execution_contract",
	"internal/executor/turn":       "d_execution_contract",
	"internal/executor/claudecode": "d_execution_adapters",
	"internal/executor/codex":      "d_execution_adapters",
	"internal/executor/grok":       "d_execution_adapters",
	"internal/executor/opencode":   "d_execution_adapters",
	"internal/executor/agy":        "d_execution_adapters",
	"internal/executor/fake":       "d_execution_adapters",
	"internal/executor/rawtap":     "d_execution_adapters",
	"internal/hostapi":             "d_execution_host",
	"internal/prochost":            "d_execution_host",
	"internal/client":              "d_transport_channel",
	"internal/mobilecore":          "d_transport_channel",
	"internal/targetclient":        "d_transport_channel",
	"internal/relay":               "d_transport_tunnel",
	"internal/proxycfg":            "d_transport_tunnel",
	"internal/ptyapi":              "d_sessions",
	"internal/ptyhost":             "d_sessions",
	"internal/ptyhost/engine":      "d_sessions",
	"internal/ptyhost/hostproc":    "d_sessions",
	"internal/ptyhost/sessdir":     "d_sessions",
	"internal/ptyhost/wire":        "d_sessions",
	"internal/proto":               "d_protocol",
	"internal/config":              "d_policy",
	"internal/discipline":          "d_policy",
	"internal/envfile":             "d_policy",
	"internal/initflow":            "d_policy",
	"internal/logx":                "d_policy",
	"internal/pathenv":             "d_policy",
	"internal/permgate":            "d_policy",
	"internal/buildinfo":           "d_maintenance",
	"internal/release":             "d_maintenance",
	"internal/selfupdate":          "d_maintenance",
	"internal/service":             "d_maintenance",
	"internal/skill":               "d_maintenance",
	"internal/toolchain":           "d_maintenance",
	"internal/upgrade":             "d_maintenance",
	"internal/webui":               "d_web_shell",
	"cmd":                          "d_cli",
	"internal/testhttp":            "d_gateway",
	"internal/testperm":            "d_policy",
	"internal/termseq":             "d_sessions",
	"internal/ptytestroot":         "d_sessions",
	"internal/approval/internal/decision": "d_orchestration",
}

func pkgDomain(pkgDir, file string) string {
	if d, ok := goPkgDomain[pkgDir]; ok {
		return d
	}
	if strings.HasPrefix(file, "web/src/") {
		// web 容器域沿旧 best 的 fn 容器登记（k_web_* → d_web_*）
		return ""
	}
	if pkgDir == "." {
		return "d_cli"
	}
	return ""
}

var webDirContainerCache = map[string]string{}

func containerGuessForWebDir(dir string) string {
	if c, ok := webDirContainerCache[dir]; ok {
		return c
	}
	return ""
}

var domainKindRole = map[string]string{
	"d_orchestration": "任务编排", "d_gateway": "HTTP/WS 门面", "d_workspace": "工作区",
	"d_execution": "执行域", "d_execution_contract": "执行契约", "d_execution_adapters": "执行器适配",
	"d_execution_host": "进程承载", "d_sessions": "终端会话", "d_transport": "跨机通道",
	"d_transport_channel": "客户端路由", "d_transport_tunnel": "直连与中继", "d_protocol": "wire 契约",
	"d_ledger": "卡片账本", "d_collab": "协作房间", "d_cli": "命令面", "d_web": "Web 控制台",
	"d_policy": "策略配置", "d_maintenance": "安装换版", "d_scheduling": "编制调度",
	"d_keystone": "协调者控制面", "d_web_contract": "契约层", "d_web_command": "任务指挥",
	"d_web_workbench": "工作台", "d_web_cards": "账本看板", "d_web_admin": "机器与设置",
	"d_web_shell": "壳与共用",
}

func buildDomains(g *Graph) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "codegraph", "best.json"))
	must(err)
	var best struct {
		Domains map[string]struct {
			Label  string `json:"label"`
			Parent string `json:"parent"`
		} `json:"domains"`
	}
	must(json.Unmarshal(raw, &best))
	domains := map[string]Domain{}
	for id, d := range best.Domains {
		domains[id] = Domain{Label: d.Label, Kind: domainKindRole[id], Parent: d.Parent}
	}
	// summary/desc from domains/*.json decls
	declDir := filepath.Join(repoRoot, "codegraph", "domains")
	entries, _ := os.ReadDir(declDir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(declDir, e.Name()))
		if err != nil {
			continue
		}
		var decl struct {
			Domain         string `json:"domain"`
			Responsibility string `json:"responsibility"`
		}
		if json.Unmarshal(raw, &decl) == nil && decl.Domain != "" {
			dv := domains[decl.Domain]
			dv.Summary = decl.Responsibility
			domains[decl.Domain] = dv
		}
	}
	g.Domains = domains
}

// ---------- packages section ----------

func buildPackagesSection(g *Graph) {
	dirs := map[string]bool{}
	for _, n := range g.Nodes {
		d := filepath.Dir(n.File)
		dirs[d] = true
	}
	for d := range dirs {
		if d == "." {
			continue // 根目录键由 main.go 单文件产生，按 validate 口径为空串
		}
		sum := ""
		if pi, ok := pkgByDir[d]; ok {
			sum = pi.doc
		}
		g.Packages[d] = Package{Summary: sum}
	}
	if dirs["."] {
		sum := ""
		if pi, ok := pkgByDir["."]; ok {
			sum = pi.doc
		}
		g.Packages[""] = Package{Summary: sum}
	}
}

// ---------- orders / checks / emit ----------

func assignOrders(g *Graph) {
	perContainer := map[string][]*Node{}
	for _, n := range g.Nodes {
		perContainer[n.Container] = append(perContainer[n.Container], n)
	}
	for cid, list := range perContainer {
		sort.Slice(list, func(i, j int) bool {
			if list[i].File != list[j].File {
				return list[i].File < list[j].File
			}
			return list[i].Line < list[j].Line
		})
		for i, n := range list {
			n.Order = i + 1
		}
		_ = cid
	}
}

func checkReferences(g *Graph) {
	for id, n := range g.Nodes {
		if _, ok := g.Containers[n.Container]; !ok {
			note("节点 %s 引用未建容器 %s", id, n.Container)
		}
	}
	for _, e := range g.Edges {
		for _, end := range e {
			if _, ok := g.Nodes[end]; !ok {
				note("边端点悬空: %v", e)
			}
		}
	}
	for _, e := range g.Implements {
		for _, end := range e {
			if _, ok := g.Nodes[end]; !ok {
				note("implements 端点悬空: %v", e)
			}
		}
	}
	for _, p := range g.Projections {
		for _, end := range p[:2] {
			if _, ok := g.Nodes[end]; !ok {
				note("投影端点悬空: %v", p)
			}
		}
	}
	for _, r := range g.Lifecycle {
		if _, ok := g.Nodes[r.Who]; !ok {
			note("lifecycle who 悬空: %v", r)
		}
		if _, ok := g.Nodes[r.Model]; !ok {
			note("lifecycle model 悬空: %v", r)
		}
	}
	for d := range g.Packages {
		found := false
		for _, n := range g.Nodes {
			if filepath.Dir(n.File) == d {
				found = true
				break
			}
		}
		if !found {
			note("packages 悬空键: %s", d)
		}
	}
	// dto+writer contradiction
	writers := map[string]bool{}
	for _, r := range g.Lifecycle {
		if r.Kind == "writer" {
			writers[r.Model] = true
		}
	}
	for id, n := range g.Nodes {
		if n.Kind == "model" && n.ModelKind == "dto" && writers[id] {
			note("modelKind 矛盾: %s 标 dto 却有 writer", id)
		}
	}
}

func strip(n *Node) *Node {
	cp := *n
	cp.Body = nil
	cp.PkgDir = ""
	cp.Decl = nil
	return &cp
}

func emit(g *Graph) {
	must(os.MkdirAll(filepath.Dir(outPath), 0o755))
	nodes := map[string]*Node{}
	for id, n := range g.Nodes {
		nodes[id] = strip(n)
	}
	edges := dedupeEdges(g.Edges)
	payload := map[string]any{
		"meta":        g.Meta,
		"domains":     g.Domains,
		"containers":  g.Containers,
		"nodes":       nodes,
		"edges":       edges,
		"implements":  g.Implements,
		"projections": g.Projections,
		"lifecycle":   g.Lifecycle,
		"packages":    g.Packages,
	}
	raw, err := json.MarshalIndent(payload, "", " ")
	must(err)
	must(os.WriteFile(outPath, raw, 0o644))
	fmt.Fprintf(os.Stderr, "nodes=%d containers=%d edges=%d implements=%d projections=%d lifecycle=%d packages=%d\n",
		len(nodes), len(g.Containers), len(edges), len(g.Implements), len(g.Projections), len(g.Lifecycle), len(g.Packages))
}

func dedupeEdges(e [][2]string) [][2]string {
	seen := map[[2]string]bool{}
	var out [][2]string
	for _, x := range e {
		if seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

func report(g *Graph) {
	must(os.MkdirAll(filepath.Dir(outPath), 0o755))
	var b strings.Builder
	fmt.Fprintf(&b, "扫描规模: nodes=%d containers=%d edges=%d implements=%d projections=%d lifecycle=%d packages=%d\n",
		len(g.Nodes), len(g.Containers), len(g.Edges), len(g.Implements), len(g.Projections), len(g.Lifecycle), len(g.Packages))
	b.WriteString("\n== 备注 ==\n")
	for _, n := range notes {
		b.WriteString(n + "\n")
	}
	must(os.WriteFile(strings.TrimSuffix(outPath, ".json")+".report.txt", []byte(b.String()), 0o644))
}
