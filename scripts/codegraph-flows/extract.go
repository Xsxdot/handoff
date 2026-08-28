// Package main extracts bounded Go control-flow steps for the codegraph
// baseline. It never changes nodes or edges and deliberately ignores TS/TSX.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Graph is the subset of a codegraph baseline needed to choose and extract
// flow subjects. The scanner does not mutate the graph's nodes or edges.
type Graph struct {
	Nodes      map[string]Node      `json:"nodes"`
	Containers map[string]Container `json:"containers"`
	Edges      [][]string           `json:"edges"`
	Implements [][]string           `json:"implements"`
}

// Node describes a codegraph node used by flow extraction.
type Node struct {
	Kind      string     `json:"kind"`
	Container string     `json:"container"`
	Name      string     `json:"name"`
	File      string     `json:"file"`
	Line      int        `json:"line"`
	Fields    [][]string `json:"fields"`
}

// Container provides the domain and kind needed by the seam selector.
type Container struct {
	Label  string `json:"label"`
	Kind   string `json:"kind"`
	Domain string `json:"domain"`
	Entry  bool   `json:"entry"`
}

// Flow is one function's ordered control-flow representation.
type Flow struct {
	Steps []FlowStep `json:"steps"`
}

// FlowStep is a call, branch, loop, or return in a Flow. Child IDs are kept
// in the parent step so a guard return is not mistaken for a sequential root.
type FlowStep struct {
	ID    string   `json:"id"`
	Order int      `json:"order"`
	Kind  string   `json:"kind"`
	To    string   `json:"to,omitempty"`
	Cond  string   `json:"cond,omitempty"`
	Line  int      `json:"line"`
	Then  []string `json:"then,omitempty"`
	Else  []string `json:"else,omitempty"`
	Body  []string `json:"body,omitempty"`
	Iface bool     `json:"iface,omitempty"`
}

// Extract is the extraction seam for selected function IDs. It returns only
// successfully parsed Go functions and keeps calls constrained to existing
// outgoing graph edges.
func Extract(g *Graph, ids []string, repoRoot string) map[string]Flow {
	flows := make(map[string]Flow)
	log := slog.Default().With("component", "codegraph-flows")
	log.Info("Go flow 抽取入口", "ids", len(ids), "repo", repoRoot)
	if g == nil {
		log.Error("Go flow 抽取跳过", "reason", "graph is nil")
		return flows
	}
	for _, id := range ids {
		node, ok := g.Nodes[id]
		if !ok {
			log.Warn("Go flow 符号跳过", "id", id, "reason", "node missing")
			continue
		}
		if node.Kind != "func" {
			log.Debug("Go flow 符号跳过", "id", id, "reason", "node is not func", "kind", node.Kind)
			continue
		}
		if !strings.HasSuffix(node.File, ".go") {
			log.Debug("Go flow 符号跳过", "id", id, "reason", "file is not Go", "file", node.File)
			continue
		}
		flow, reason, ok := extractOne(g, id, node, repoRoot)
		if !ok {
			log.Warn("Go flow 符号跳过", "id", id, "file", node.File, "reason", reason)
			continue
		}
		flows[id] = flow
		log.Info("Go flow 符号完成", "id", id, "file", node.File, "steps", len(flow.Steps))
	}
	log.Info("Go flow 抽取完成", "requested", len(ids), "written", len(flows))
	return flows
}

// SeedGoSeams selects non-folded cross-domain Go function targets.
func SeedGoSeams(g *Graph) []string {
	log := slog.Default().With("component", "codegraph-flows")
	if g == nil {
		log.Error("Go flow 缝选择失败", "reason", "graph is nil")
		return nil
	}
	adj := make(map[string][]string)
	for _, edge := range g.Edges {
		if len(edge) != 2 {
			log.Warn("Go flow 缝边跳过", "reason", "edge must have two endpoints", "edge", edge)
			continue
		}
		adj[edge[0]] = append(adj[edge[0]], edge[1])
	}

	reuse := make(map[string]int)
	for entryID, entry := range g.Nodes {
		if entry.Kind != "entry" {
			continue
		}
		seen := map[string]bool{entryID: true}
		queue := []string{entryID}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for _, next := range adj[current] {
				if seen[next] {
					continue
				}
				seen[next] = true
				queue = append(queue, next)
				reuse[next]++
			}
		}
	}

	seen := make(map[string]bool)
	for _, edge := range g.Edges {
		if len(edge) != 2 || seen[edge[1]] {
			continue
		}
		from, fromOK := g.Nodes[edge[0]]
		to, toOK := g.Nodes[edge[1]]
		if !fromOK || !toOK || to.Kind != "func" || !strings.HasSuffix(to.File, ".go") {
			continue
		}
		fromDomain := g.Containers[from.Container].Domain
		toContainer := g.Containers[to.Container]
		if fromDomain == "" || toContainer.Domain == "" || fromDomain == toContainer.Domain {
			continue
		}
		if isFoldedContainer(toContainer.Kind) && reuse[edge[1]] >= 10 {
			log.Debug("Go flow 缝跳过", "id", edge[1], "reason", "folded container reused by entries", "reuse", reuse[edge[1]])
			continue
		}
		seen[edge[1]] = true
	}

	seeds := make([]string, 0, len(seen))
	for id := range seen {
		seeds = append(seeds, id)
	}
	sort.Strings(seeds)
	log.Info("Go flow 缝选择完成", "seeds", len(seeds))
	return seeds
}

func isFoldedContainer(kind string) bool {
	return kind == "函数组" || kind == "TypeScript 函数组"
}

func extractOne(g *Graph, id string, node Node, repoRoot string) (Flow, string, bool) {
	path := filepath.Join(repoRoot, filepath.FromSlash(node.File))
	source, err := os.ReadFile(path)
	if err != nil {
		return Flow{}, "read source: " + err.Error(), false
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, 0)
	if err != nil {
		return Flow{}, "parse source: " + err.Error(), false
	}
	decl := findFuncDecl(file, fset, node.Name, node.Line)
	if decl == nil || decl.Body == nil {
		return Flow{}, "function declaration or body missing", false
	}
	fx := &flowExtractor{
		graph:          g,
		currentID:      id,
		source:         source,
		fileSet:        fset,
		node:           node,
		outgoing:       outgoingNodes(g, id),
		interfaceNodes: interfaceNodes(g),
		receiverTypes:  functionValueTypes(decl),
	}
	fx.walkBlock(decl.Body)
	steps := fx.steps
	if steps == nil {
		steps = []FlowStep{}
	}
	return Flow{Steps: steps}, "", true
}

func findFuncDecl(file *ast.File, fset *token.FileSet, wanted string, line int) *ast.FuncDecl {
	var exact, byLine, byShort *ast.FuncDecl
	shortWanted := shortSymbolName(wanted)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := funcDeclName(fn)
		if name == wanted {
			exact = fn
		}
		if line > 0 && fn.Pos().IsValid() {
			if fset.Position(fn.Pos()).Line == line {
				byLine = fn
			}
		}
		if shortSymbolName(name) == shortWanted {
			byShort = fn
		}
	}
	if exact != nil {
		return exact
	}
	if byLine != nil {
		return byLine
	}
	return byShort
}

func funcDeclName(fn *ast.FuncDecl) string {
	if fn == nil || fn.Name == nil {
		return ""
	}
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return receiverTypeName(fn.Recv.List[0].Type) + "." + fn.Name.Name
}

func receiverTypeName(expr ast.Expr) string {
	switch typ := expr.(type) {
	case *ast.Ident:
		return typ.Name
	case *ast.StarExpr:
		return receiverTypeName(typ.X)
	case *ast.IndexExpr:
		return receiverTypeName(typ.X)
	case *ast.IndexListExpr:
		return receiverTypeName(typ.X)
	case *ast.SelectorExpr:
		return typ.Sel.Name
	default:
		return ""
	}
}

func shortSymbolName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func outgoingNodes(g *Graph, id string) map[string]bool {
	out := make(map[string]bool)
	for _, edge := range g.Edges {
		if len(edge) == 2 && edge[0] == id {
			if _, ok := g.Nodes[edge[1]]; ok {
				out[edge[1]] = true
			}
		}
	}
	return out
}

func interfaceNodes(g *Graph) map[string]bool {
	out := make(map[string]bool)
	for _, pair := range g.Implements {
		if len(pair) == 2 {
			out[pair[1]] = true
		}
	}
	return out
}

type flowExtractor struct {
	graph          *Graph
	currentID      string
	source         []byte
	fileSet        *token.FileSet
	node           Node
	outgoing       map[string]bool
	interfaceNodes map[string]bool
	receiverTypes  map[string]string
	steps          []FlowStep
}

func (fx *flowExtractor) walkBlock(block *ast.BlockStmt) []string {
	if block == nil {
		return nil
	}
	var ids []string
	for _, stmt := range block.List {
		ids = append(ids, fx.walkStmt(stmt)...)
	}
	return ids
}

func (fx *flowExtractor) walkStmt(stmt ast.Stmt) []string {
	switch current := stmt.(type) {
	case *ast.IfStmt:
		ids := fx.callsInStmt(current.Init)
		ids = append(ids, fx.callsInExpr(current.Cond)...)
		branchID := fx.addStep(FlowStep{
			Kind: "branch",
			Cond: fx.sourceText(current.Cond),
			Line: fx.line(current.Pos()),
			Then: []string{},
			Else: []string{},
		})
		thenIDs := fx.walkBlock(current.Body)
		elseIDs := fx.walkElse(current.Else)
		if len(thenIDs) == 0 && len(elseIDs) == 0 && strings.Contains(fx.sourceText(current.Cond), "err") {
			fx.steps = fx.steps[:len(fx.steps)-1]
			return ids
		}
		fx.setChildren(branchID, thenIDs, elseIDs, nil)
		return append(ids, branchID)
	case *ast.ForStmt:
		ids := fx.callsInStmt(current.Init)
		ids = append(ids, fx.callsInExpr(current.Cond)...)
		loopID := fx.addStep(FlowStep{Kind: "loop", Cond: fx.sourceText(current.Cond), Line: fx.line(current.Pos()), Body: []string{}})
		bodyIDs := fx.walkBlock(current.Body)
		fx.setChildren(loopID, nil, nil, bodyIDs)
		return append(ids, loopID)
	case *ast.RangeStmt:
		ids := fx.callsInExpr(current.X)
		loopID := fx.addStep(FlowStep{Kind: "loop", Cond: fx.sourceText(current.X), Line: fx.line(current.Pos()), Body: []string{}})
		bodyIDs := fx.walkBlock(current.Body)
		fx.setChildren(loopID, nil, nil, bodyIDs)
		return append(ids, loopID)
	case *ast.ReturnStmt:
		ids := fx.callsInExprList(current.Results)
		ids = append(ids, fx.addStep(FlowStep{Kind: "return", Line: fx.line(current.Pos())}))
		return ids
	default:
		return fx.callsInNode(stmt)
	}
}

func (fx *flowExtractor) walkElse(stmt ast.Stmt) []string {
	switch current := stmt.(type) {
	case *ast.BlockStmt:
		return fx.walkBlock(current)
	case nil:
		return nil
	default:
		return fx.walkStmt(current)
	}
}

func (fx *flowExtractor) addStep(step FlowStep) string {
	step.ID = fmt.Sprintf("s%d", len(fx.steps)+1)
	step.Order = len(fx.steps) + 1
	fx.steps = append(fx.steps, step)
	return step.ID
}

func (fx *flowExtractor) setChildren(id string, thenIDs, elseIDs, bodyIDs []string) {
	for i := range fx.steps {
		if fx.steps[i].ID != id {
			continue
		}
		if thenIDs != nil {
			fx.steps[i].Then = thenIDs
		}
		if elseIDs != nil {
			fx.steps[i].Else = elseIDs
		}
		if bodyIDs != nil {
			fx.steps[i].Body = bodyIDs
		}
		return
	}
}

func (fx *flowExtractor) callsInStmt(stmt ast.Stmt) []string {
	return fx.callsInNode(stmt)
}

func (fx *flowExtractor) callsInNode(node ast.Node) []string {
	if node == nil {
		return nil
	}
	var ids []string
	ast.Inspect(node, func(child ast.Node) bool {
		call, ok := child.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id := fx.resolveCall(call); id != "" {
			ids = append(ids, fx.addStep(FlowStep{Kind: "call", To: id, Line: fx.line(call.Pos()), Iface: fx.interfaceNodes[id]}))
		}
		return true
	})
	return ids
}

func (fx *flowExtractor) callsInExpr(expr ast.Expr) []string {
	if expr == nil {
		return nil
	}
	var ids []string
	ast.Inspect(expr, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id := fx.resolveCall(call); id != "" {
			ids = append(ids, fx.addStep(FlowStep{Kind: "call", To: id, Line: fx.line(call.Pos()), Iface: fx.interfaceNodes[id]}))
		}
		return true
	})
	return ids
}

func (fx *flowExtractor) callsInExprList(exprs []ast.Expr) []string {
	var ids []string
	for _, expr := range exprs {
		ids = append(ids, fx.callsInExpr(expr)...)
	}
	return ids
}

func (fx *flowExtractor) resolveCall(call *ast.CallExpr) string {
	name, receiver := callName(call.Fun)
	if name == "" {
		return ""
	}
	candidates := make([]string, 0)
	for id := range fx.outgoing {
		node, ok := fx.graph.Nodes[id]
		if ok && (node.Kind == "func" || fx.interfaceNodes[id]) && shortSymbolName(node.Name) == name {
			candidates = append(candidates, id)
		}
	}
	if len(candidates) == 0 {
		return fx.uniqueInterfaceMethod(name, receiver)
	}
	sort.Strings(candidates)
	if receiver != "" {
		if typ := fx.receiverTypes[receiver]; typ != "" {
			for _, id := range candidates {
				if receiverTypeNameFromSymbol(fx.graph.Nodes[id].Name) == typ {
					return id
				}
			}
		}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	for _, id := range candidates {
		if fx.interfaceNodes[id] {
			return id
		}
	}
	return ""
}

func (fx *flowExtractor) uniqueInterfaceMethod(method, receiver string) string {
	var matches []string
	for id := range fx.interfaceNodes {
		node, ok := fx.graph.Nodes[id]
		if !ok || node.Kind != "model" || shortSymbolName(node.Name) == "" {
			continue
		}
		if receiver != "" && fx.receiverTypes[receiver] != "" && node.Name != fx.receiverTypes[receiver] {
			continue
		}
		for _, field := range node.Fields {
			if len(field) > 0 && shortSymbolName(field[0]) == method {
				matches = append(matches, id)
				break
			}
		}
	}
	if len(matches) != 1 {
		return ""
	}
	return matches[0]
}

func callName(fun ast.Expr) (name, receiver string) {
	switch current := fun.(type) {
	case *ast.Ident:
		return current.Name, ""
	case *ast.SelectorExpr:
		if ident, ok := current.X.(*ast.Ident); ok {
			return current.Sel.Name, ident.Name
		}
		return current.Sel.Name, ""
	default:
		return "", ""
	}
}

func receiverTypeNameFromSymbol(name string) string {
	parts := strings.Split(name, ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2]
}

func functionValueTypes(fn *ast.FuncDecl) map[string]string {
	types := make(map[string]string)
	if fn.Recv != nil {
		for _, field := range fn.Recv.List {
			for _, name := range field.Names {
				types[name.Name] = receiverTypeName(field.Type)
			}
		}
	}
	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			for _, name := range field.Names {
				types[name.Name] = receiverTypeName(field.Type)
			}
		}
	}
	return types
}

func (fx *flowExtractor) sourceText(node ast.Node) string {
	if node == nil {
		return ""
	}
	start := fx.fileSet.Position(node.Pos()).Offset
	end := fx.fileSet.Position(node.End()).Offset
	if start < 0 || end < start || end > len(fx.source) {
		return ""
	}
	return string(fx.source[start:end])
}

func (fx *flowExtractor) line(pos token.Pos) int {
	return fx.fileSet.Position(pos).Line
}
