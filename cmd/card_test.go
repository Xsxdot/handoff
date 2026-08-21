package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCardAddListShowMove(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "第一张卡", "--project", "demo")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// stdout 契约：单行 JSON，含分配的 id
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &created); err != nil {
		t.Fatalf("add 输出非单行 JSON: %q", out)
	}
	if created.ID == "" || created.Status != "待办" {
		t.Fatalf("建卡返回: %+v", created)
	}

	// list 人类表格默认走 stdout tabwriter；--json 一行一对象
	out, _, err = runLedgerCLI(t, dir, "card", "list", "--project", "demo", "--json")
	if err != nil || !strings.Contains(out, created.ID) {
		t.Fatalf("list --json: %v %q", err, out)
	}

	// show：卡 + 关系 + 事件
	out, _, err = runLedgerCLI(t, dir, "card", "show", created.ID)
	if err != nil || !strings.Contains(out, "第一张卡") {
		t.Fatalf("show: %v %q", err, out)
	}

	// move + gate：feature 流无 spec 附件进「已出spec」应拒且文案指明缺附件
	_, stderr, err := runLedgerCLI(t, dir, "card", "move", created.ID, "已出spec")
	if err == nil || !strings.Contains(err.Error()+stderr, "spec") {
		t.Fatalf("gate 应拒且提示: %v %q", err, stderr)
	}
	// update --attach 后放行
	if _, _, err := runLedgerCLI(t, dir, "card", "update", created.ID, "--attach", "spec:specs/x.md"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "move", created.ID, "已出spec"); err != nil {
		t.Fatalf("gate 放行: %v", err)
	}
	// --expect CAS 钉前值：错前值干净失败
	if _, _, err := runLedgerCLI(t, dir, "card", "move", created.ID, "进行中", "--expect", "待办"); err == nil {
		t.Fatal("错前值应失败")
	}
}

func TestCardAddChildAndBaseBranch(t *testing.T) {
	dir := t.TempDir()
	out, _, _ := runLedgerCLI(t, dir, "card", "add", "epic", "--project", "demo", "--base-branch", "desktop-shell")
	var epic struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &epic)
	out, _, err := runLedgerCLI(t, dir, "card", "add", "子项", "--project", "demo", "--parent", epic.ID)
	if err != nil {
		t.Fatalf("子卡: %v", err)
	}
	var child struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &child)
	if !strings.HasPrefix(child.ID, epic.ID+".") {
		t.Fatalf("子卡点号: %q", child.ID)
	}
	// 基线过滤能查到子卡（继承）
	out, _, _ = runLedgerCLI(t, dir, "card", "list", "--project", "demo", "--base-branch", "desktop-shell", "--json")
	if !strings.Contains(out, child.ID) {
		t.Fatalf("基线继承过滤: %q", out)
	}
}

func TestCardCloseConfirmAndMerge(t *testing.T) {
	dir := t.TempDir()
	mkCard := func(title string) string {
		out, _, err := runLedgerCLI(t, dir, "card", "add", title, "--project", "demo", "--workflow", "bug")
		if err != nil {
			t.Fatalf("add %s: %v", title, err)
		}
		var card struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &card)
		return card.ID
	}
	a, b, carrier := mkCard("a"), mkCard("b"), mkCard("carrier")

	// close 非交互无 --yes 拒绝（二次确认约定；只对不可逆的 取消|废弃 设门）
	if _, _, err := runLedgerCLI(t, dir, "card", "close", a, "--reason", "废弃"); err == nil {
		t.Fatal("无 --yes 应拒")
	}
	// 搁置可复活，不设确认门——无 --yes 也应直接成功
	if _, _, err := runLedgerCLI(t, dir, "card", "close", a, "--reason", "搁置"); err != nil {
		t.Fatalf("close 搁置不应要求确认: %v", err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "revive", a); err != nil {
		t.Fatalf("revive: %v", err)
	}

	// link 环检测透传
	if _, _, err := runLedgerCLI(t, dir, "card", "link", a, b); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "link", b, a); err == nil {
		t.Fatal("成环应拒")
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "unlink", a, b); err != nil {
		t.Fatalf("unlink: %v", err)
	}

	// merge --yes + 列表跟随 + unmerge
	if _, _, err := runLedgerCLI(t, dir, "card", "merge", a, b, "--into", carrier, "--yes"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	out, _, _ := runLedgerCLI(t, dir, "card", "list", "--project", "demo", "--json")
	if !strings.Contains(out, `"following":"`+carrier+`"`) {
		t.Fatalf("跟随未呈现: %q", out)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "unmerge", a); err != nil {
		t.Fatalf("unmerge: %v", err)
	}

	// split
	out, _, err := runLedgerCLI(t, dir, "card", "split", carrier, "拆出的子项")
	if err != nil || !strings.Contains(out, carrier+".") {
		t.Fatalf("split: %v %q", err, out)
	}

	// note 引用建边
	if _, _, err := runLedgerCLI(t, dir, "card", "note", a, "与 #"+b+" 同源"); err != nil {
		t.Fatalf("note: %v", err)
	}
	// note --reset-node：人工重置回合计数的落账入口（Plan C 消费）
	out2, _, err := runLedgerCLI(t, dir, "card", "note", a, "人工看过重新计数", "--reset-node", "review")
	if err != nil || !strings.Contains(out2, `"human_reset_node":"review"`) {
		t.Fatalf("note --reset-node: %v %q", err, out2)
	}

	// export markdown 快照
	out, _, err = runLedgerCLI(t, dir, "card", "export")
	if err != nil || !strings.Contains(out, "| "+carrier+" |") {
		t.Fatalf("export: %v %q", err, out)
	}
}
