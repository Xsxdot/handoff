package cmd

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestCardAddListShowMove(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "第一张卡", "--project", "demo", "--workflow", "feature")
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

func TestCardUpdateAttachmentKindsAndDetachMessages(t *testing.T) {
	var logOutput bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logOutput, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "附件身份", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &created); err != nil {
		t.Fatalf("add 输出: %v", err)
	}

	path := "docs/superpowers/specs/b250.md"
	if _, _, err := runLedgerCLI(t, dir, "card", "update", created.ID, "--attach", "spec:"+path); err != nil {
		t.Fatalf("attach spec: %v", err)
	}
	if out, _, err = runLedgerCLI(t, dir, "card", "update", created.ID, "--attach", "plan:"+path); err != nil {
		t.Fatalf("attach plan: %v", err)
	} else {
		var card ledger.Card
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
			t.Fatalf("双 kind 输出: %v", err)
		}
		if len(card.Attachments) != 2 || card.Attachments[0].Kind != "spec" || card.Attachments[1].Kind != "plan" {
			t.Fatalf("双 kind 未保留且顺序不稳: %+v", card.Attachments)
		}
	}

	_, stderr, err := runLedgerCLI(t, dir, "card", "update", created.ID, "--attach", "spec:"+path)
	if err != nil {
		t.Fatalf("重复 attach: %v", err)
	}
	if strings.Count(stderr, "附件已存在，跳过：spec:"+path) != 1 {
		t.Fatalf("重复 attach 应在 stderr 出声，stderr=%q", stderr)
	}
	if strings.Contains(stderr, "level=INFO") || strings.Contains(logOutput.String(), "附件已存在，跳过：spec:"+path) {
		t.Fatalf("用户提示必须是 stderr 普通输出且只出现一次，stderr=%q logs=%q", stderr, logOutput.String())
	}

	out, stderr, err = runLedgerCLI(t, dir, "card", "update", created.ID, "--detach", "spec:"+path)
	if err != nil {
		t.Fatalf("精确 detach: %v", err)
	}
	if !strings.Contains(stderr, "摘掉附件 1 条") || !strings.Contains(stderr, "spec:"+path) {
		t.Fatalf("精确 detach 应报告条数与清单，stderr=%q", stderr)
	}
	var card ledger.Card
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatalf("精确 detach 输出: %v", err)
	}
	if len(card.Attachments) != 1 || card.Attachments[0].Kind != "plan" {
		t.Fatalf("精确 detach 摘错附件: %+v", card.Attachments)
	}

	out, stderr, err = runLedgerCLI(t, dir, "card", "update", created.ID, "--detach", path)
	if err != nil {
		t.Fatalf("裸 path detach: %v", err)
	}
	if !strings.Contains(stderr, "摘掉附件 1 条") || !strings.Contains(stderr, "plan:"+path) {
		t.Fatalf("裸 path detach 应报告条数与清单，stderr=%q", stderr)
	}
	card = ledger.Card{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatalf("裸 path detach 输出: %v", err)
	}
	if len(card.Attachments) != 0 {
		t.Fatalf("裸 path 应摘掉同 path 全部附件: %+v", card.Attachments)
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

func TestCardUpdateBaseBranch(t *testing.T) {
	dir := t.TempDir()
	// B229 起裸卡派发在认领前过拒发闸，闸的前提由共享夹具满足（本测试钉的是
	// 基线分支更新语义）。
	setupDisciplineGateFixture(t, dir, `{"disciplines_supported":true}`)
	out, _, err := runLedgerCLI(t, dir, "card", "add", "可更新基线", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatal(err)
	}

	out, _, err = runLedgerCLI(t, dir, "card", "update", card.ID, "--base-branch", "cards/keep")
	if err != nil {
		t.Fatalf("设置基线: %v", err)
	}
	var updated struct {
		BaseBranch string `json:"base_branch"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &updated); err != nil || updated.BaseBranch != "cards/keep" {
		t.Fatalf("设置基线输出: err=%v card=%+v output=%q", err, updated, out)
	}

	// 未提供 --base-branch 时，组合 update 不能隐式清除既有基线。
	out, _, err = runLedgerCLI(t, dir, "card", "update", card.ID, "--title", "标题更新")
	if err != nil {
		t.Fatalf("无基线 flag 的元信息更新: %v", err)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &updated); err != nil || updated.BaseBranch != "cards/keep" {
		t.Fatalf("无基线 flag 改动了基线: err=%v card=%+v output=%q", err, updated, out)
	}

	// 显式空串必须走 presence 语义，清除自身覆盖值。
	out, _, err = runLedgerCLI(t, dir, "card", "update", card.ID, "--base-branch", "")
	if err != nil {
		t.Fatalf("清除基线: %v", err)
	}
	if strings.Contains(out, "cards/keep") {
		t.Fatalf("清除基线输出仍含旧值: %q", out)
	}

	out, _, err = runLedgerCLI(t, dir, "card", "add", "已派发卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var dispatched struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &dispatched); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "update", dispatched.ID, "--accept", "测试全绿"); err != nil {
		t.Fatalf("设置派发判据: %v", err)
	}
	restore := swapDispatchTransport(func(prompt, branch, target, project string) (string, error) {
		return "T-b205-update-base", nil
	})
	_, _, err = runLedgerCLI(t, dir, "card", "dispatch", dispatched.ID,
		"--template", "feature-impl", "--target", "mac-02", "--discipline-override", "implement")
	restore()
	if err != nil {
		t.Fatalf("准备首次派发: %v", err)
	}

	var shown struct {
		Events []struct {
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
			CreatedAt time.Time       `json:"created_at"`
		} `json:"events"`
	}
	out, _, err = runLedgerCLI(t, dir, "card", "show", dispatched.ID)
	if err != nil {
		t.Fatalf("读派发事件: %v", err)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &shown); err != nil {
		t.Fatal(err)
	}
	var firstBranch string
	var firstAt time.Time
	for _, event := range shown.Events {
		if event.Type != "dispatched" {
			continue
		}
		var payload struct {
			Branch string `json:"branch"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		firstBranch, firstAt = payload.Branch, event.CreatedAt
		break
	}
	if firstBranch == "" || firstAt.IsZero() {
		t.Fatalf("未找到首次派发事件: %+v", shown.Events)
	}

	_, stderr, err := runLedgerCLI(t, dir, "card", "update", dispatched.ID, "--base-branch", "cards/rejected")
	if err == nil {
		t.Fatal("已派发卡修改基线应失败")
	}
	if !strings.Contains(err.Error()+stderr, firstBranch) ||
		!strings.Contains(err.Error()+stderr, firstAt.Format(time.RFC3339Nano)) {
		t.Fatalf("冻结错误缺少首次派发出处: err=%v stderr=%q", err, stderr)
	}
	out, _, err = runLedgerCLI(t, dir, "card", "show", dispatched.ID)
	if err != nil || strings.Contains(out, `"base_branch":"cards/rejected"`) {
		t.Fatalf("拒绝后基线被改写: err=%v output=%q", err, out)
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
