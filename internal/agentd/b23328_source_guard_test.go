// b23328_source_guard_test.go —— B233.28 的包级静态面断言（内部锁，附属于 S1/S2）。
//
// 职责：钉住「任务/项目收口面涉及的 gateway 生产文件不再出现 s.st.<方法> 选择器」。
// 边界：只扫 §3.1 列出的 10 个生产文件；不扫登录/工作台/auth（本卡不动）、不扫
// server.go（assembly 装配点，含 s.st.SetEventHook 等既有插线）。
//
// 为什么是内部锁而非缝级：从任何声明缝（HTTP/handler 入口）构造不出「整包不再直打
// store」这条静态属性——运行时用例可能恰巧走别的分支而漏掉某处直打。
package agentd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

func TestB23328GatewayNoDirectStoreTaskProjectCalls(t *testing.T) {
	files := []string{
		"handlers.go", "tasksfanout.go", "taskroute.go", "taskplan.go",
		"roomsapi.go", "update.go", "projectadmin.go", "codegraph.go",
		"coordapi.go", "workspacefiles.go",
	}
	// 本卡收口面：这些方法在 gateway 生产文件里必须只经 s.mgr
	closure := map[string]bool{
		"ListTasks": true, "GetTask": true, "PendingTickets": true,
		"EventsFrom": true, "EventsFromAsc": true, "LatestEvent": true,
		"CountEvents": true, "ListMirrorTasks": true, "MirrorTaskTarget": true,
		"MirrorEventsFrom": true, "GetTicket": true, "AnswerTicketApplied": true,
		"AppendEvent": true, "UpdateTaskState": true,
		"GetProjectLocationByName": true, "UpdateProjectLocation": true,
		"ListProjectLocations": true,
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	dir := filepath.Dir(thisFile)
	fset := token.NewFileSet()
	found := 0
	for _, f := range files {
		path := filepath.Join(dir, f)
		af, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s: %v", f, err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			// 只匹配形如 s.st.Method 的选择器：X 必须是 s.st
			inner, ok := sel.X.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := inner.X.(*ast.Ident)
			if !ok || id.Name != "s" || inner.Sel.Name != "st" {
				return true
			}
			if closure[sel.Sel.Name] {
				found++
				t.Errorf("%s:%d 生产路径仍直打 store: s.st.%s（应经 s.mgr）",
					f, fset.Position(sel.Pos()).Line, sel.Sel.Name)
			}
			return true
		})
	}
	if found != 0 {
		t.Fatalf("本卡收口面仍有 %d 处 s.st 直打", found)
	}
}
