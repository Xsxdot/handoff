// desktopwake_material_test.go —— B434 G1 拉起材料端点的缝级断言。
//
// 职责：从声明缝 GET /api/cards/{id}/coordinator/desktop-launch（契约 §3.9）、
// desktopLaunchMaterial（§3.5，G3 复用）与 desktopBrief（§3.5 模板）锁住契约
// 条 1–14、200–202；另附一支声明过的内部锁（源码守卫）。
// 边界：真 SQLite 账本 + 真 manager（newNoPTYLedgerEnv，不装自动化层）；不碰
// 唤醒信箱、opener、keystone（归 V/G2/G3）。
package agentd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

const (
	b434MaterialProject  = "handoff"
	b434MaterialRoot     = "/repo/handoff"
	b434MaterialBase     = "feat/b434-base"
	b434MaterialOwner    = "user:sy"
	b434MaterialLauncher = "local_0f3c2a9e-1111-2222-3333-444455556666"
	b434MaterialCLI      = "9d1f2e3a-aaaa-bbbb-cccc-ddddeeeeffff"
)

// b434MaterialPath 是材料端点路径。
func b434MaterialPath(card string) string {
	return "/api/cards/" + card + "/coordinator/desktop-launch"
}

// newB434MaterialEnv 起一个**未装配自动化层**的 agentd（keystone/scheduling 均 nil，
// 条 13 的前提）并登记项目位置 handoff → /repo/handoff。
func newB434MaterialEnv(t *testing.T) *ledgerEnv {
	t.Helper()
	env := newNoPTYLedgerEnv(t)
	if env.srv.keystone != nil || env.srv.scheduling != nil {
		t.Fatalf("前提不成立：夹具必须不装自动化层 keystone=%v scheduling=%v", env.srv.keystone, env.srv.scheduling)
	}
	if err := env.st.CreateProjectLocation(&proto.ProjectLocation{
		Name: b434MaterialProject, Path: b434MaterialRoot}); err != nil {
		t.Fatalf("登记项目位置: %v", err)
	}
	return env
}

// b434SessionCard 建「父卡带基线、子卡继承、子卡入群（群主 user:sy）」的卡，返回子卡号。
func b434SessionCard(t *testing.T, env *ledgerEnv) string {
	t.Helper()
	parent, err := env.ledger.CreateCard(ledger.NewCard{Title: "材料父卡", Project: b434MaterialProject,
		Workflow: "attachment-gates", BaseBranch: b434MaterialBase, Actor: "test"})
	if err != nil {
		t.Fatalf("建父卡: %v", err)
	}
	child, err := env.ledger.CreateCard(ledger.NewCard{Title: "材料 子卡", Project: b434MaterialProject,
		Parent: parent.ID, Workflow: "attachment-gates", Actor: "test"})
	if err != nil {
		t.Fatalf("建子卡: %v", err)
	}
	sess, err := env.ledger.CreateSession("材料群", b434MaterialOwner, "test")
	if err != nil {
		t.Fatalf("建会话: %v", err)
	}
	if err := env.ledger.JoinCardToSession(sess.ID, child.ID, "test"); err != nil {
		t.Fatalf("拉卡进群: %v", err)
	}
	return child.ID
}

// b434BareCard 建一张无基线、不入群的卡。
func b434BareCard(t *testing.T, env *ledgerEnv, project string) string {
	t.Helper()
	card, err := env.ledger.CreateCard(ledger.NewCard{Title: "材料裸卡", Project: project,
		Workflow: "attachment-gates", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	return card.ID
}

// b434RegisterLauncher 为 handoff 项目登记拉起器。
func b434RegisterLauncher(t *testing.T, env *ledgerEnv) {
	t.Helper()
	if err := env.ledger.RegisterDesktopLauncher(ledger.DesktopLauncher{Project: b434MaterialProject,
		DesktopSession: b434MaterialLauncher, CLISession: b434MaterialCLI, Actor: "test"}); err != nil {
		t.Fatalf("登记拉起器: %v", err)
	}
}

// b434DecodeMaterial 把 200 体解析成结构体与「键 → 原始值」两种视图；后者用来
// 区分「缺键」与「空串」（序列化边界，契约条 201/202）。
func b434DecodeMaterial(t *testing.T, body string) (proto.DesktopLaunchMaterial, map[string]json.RawMessage) {
	t.Helper()
	var m proto.DesktopLaunchMaterial
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("200 体解析失败: %v body=%s", err, body)
	}
	keys := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(body), &keys); err != nil {
		t.Fatalf("200 体按键解析失败: %v body=%s", err, body)
	}
	return m, keys
}

// ---------- T1：最薄路径（条 4、5、13；404） ----------

// TestB434MaterialRouteServesWithoutAutomation 锁条 4、5、13：未装自动化层的 agentd 上
// 材料端点 200，Folder 等于 resolveCoordWorkdir 同一解析点的结果（且非空，防两边同空假绿）；
// 同一 Server 的 withCoordinator 路由 503，证明夹具确实未装配。
func TestB434MaterialRouteServesWithoutAutomation(t *testing.T) {
	env := newB434MaterialEnv(t)
	card := b434BareCard(t, env, b434MaterialProject)

	if code, body := ledgerGet(t, env.testAgentdEnv, "/api/cards/"+card+"/coordinator"); code != http.StatusServiceUnavailable {
		t.Fatalf("对照：withCoordinator 路由应 503（自动化层未装配），得 %d %s", code, body)
	}
	code, body := ledgerGet(t, env.testAgentdEnv, b434MaterialPath(card))
	if code != http.StatusOK {
		t.Fatalf("条 4/13：材料端点应 200，得 %d %s", code, body)
	}
	m, _ := b434DecodeMaterial(t, body)
	if m.Card != card {
		t.Fatalf("card = %q，期望 %q", m.Card, card)
	}
	want := env.srv.resolveCoordWorkdir(card)
	if want == "" || m.Folder != want {
		t.Fatalf("条 5：folder = %q，resolveCoordWorkdir = %q（须相等且非空）", m.Folder, want)
	}
}

// TestB434MaterialUnknownCard404 锁契约 §3.9「404 卡不存在（ledgerErr）」：必须是
// ledgerErr 的 JSON 体且 error 含卡号——路由未注册时 mux 的 404 是纯文本
// 「404 page not found」，只断状态码会假绿。
func TestB434MaterialUnknownCard404(t *testing.T) {
	env := newB434MaterialEnv(t)
	code, body := ledgerGet(t, env.testAgentdEnv, b434MaterialPath("B999"))
	if code != http.StatusNotFound {
		t.Fatalf("未知卡应 404，得 %d %s", code, body)
	}
	var e map[string]string
	if err := json.Unmarshal([]byte(body), &e); err != nil || !strings.Contains(e["error"], "B999") {
		t.Fatalf("404 体须为 {\"error\":…含卡号}，得 %s（解析错误 %v）", body, err)
	}
}

// ---------- T2：简报（条 6–12；§3.5 模板） ----------

// TestB434DesktopBriefTemplate 锁 §3.5 模板逐字（入群 @ 群主 / 未入群打标 两种末句）。
func TestB434DesktopBriefTemplate(t *testing.T) {
	inSession := desktopBrief(desktopBriefInput{Card: "B7", Title: "标题甲", BaseBranch: "feat/x",
		Session: "session:3", Owner: "user:sy", Folder: "/repo/a b", Via: proto.SeatDesktopViaPage})
	wantIn := "[handoff] 你是卡 B7 的协调者，坐在这场 Claude 桌面端会话里。\n" +
		"\n" +
		"- 卡号：B7\n" +
		"- 标题：标题甲\n" +
		"- 有效基线分支：feat/x\n" +
		"- 所在会话：session:3\n" +
		"- 项目根目录：/repo/a b\n" +
		"\n" +
		"请按三步开始：\n" +
		"1. 读 handoff skill。\n" +
		"2. 用桌面端会话管理工具读到你自己的桌面端会话 id（local_ 开头），然后执行：\n" +
		"   handoff card bind B7 --desktop-session <你的桌面端会话 id> --via page\n" +
		"3. 之后有事 handoff 会叫醒你，不必挂订阅；推卡按 charter 节点逐节点点火，升级按三档，需要人就 `@user:sy`。"
	if inSession != wantIn {
		t.Fatalf("入群简报不符模板：\n得：\n%s\n期望：\n%s", inSession, wantIn)
	}
	bare := desktopBrief(desktopBriefInput{Card: "B8", Title: "标题乙", Folder: "/repo/b",
		Via: proto.SeatDesktopViaLauncher})
	for _, want := range []string{
		"- 有效基线分支：未设\n",
		"- 所在会话：未入群\n",
		"   handoff card bind B8 --desktop-session <你的桌面端会话 id> --via launcher\n",
		"，升级按三档，需要人就在卡上打 needs_human。",
	} {
		if !strings.Contains(bare, want) {
			t.Fatalf("未入群简报缺 %q：\n%s", want, bare)
		}
	}
	if strings.Contains(bare, "@") {
		t.Fatalf("未入群简报不得 @ 任何人：\n%s", bare)
	}
	// 契约 r10 第 2 条：已入群但群主为空 → 末句「需要人就打 needs_human」（与未入群的
	// 「在卡上打」区分），且不得渲染出空 @。
	ownerless := desktopBrief(desktopBriefInput{Card: "B9", Title: "标题丙", Session: "session:4",
		Folder: "/repo/c", Via: proto.SeatDesktopViaPage})
	if want := "，升级按三档，需要人就打 needs_human。"; !strings.HasSuffix(ownerless, want) {
		t.Fatalf("入群无群主简报末句应为 %q：\n%s", want, ownerless)
	}
	if strings.Contains(ownerless, "@") || !strings.Contains(ownerless, "- 所在会话：session:4\n") {
		t.Fatalf("入群无群主简报不得 @、须保留会话 id：\n%s", ownerless)
	}
}

// TestB434MaterialBriefOverHTTP 锁条 6–12（经 HTTP 真序列化边界）：入群卡的简报
// 以 "[handoff] " 开头，含卡号、**继承自父卡**的有效基线、会话 id、--desktop-session、
// --via page，且不含「一次性回合」；裸卡含「未设」「未入群」。
func TestB434MaterialBriefOverHTTP(t *testing.T) {
	env := newB434MaterialEnv(t)
	card := b434SessionCard(t, env)
	session, err := env.ledger.SessionOfCard(card)
	if err != nil || session == "" {
		t.Fatalf("前提：卡应已入群，session=%q err=%v", session, err)
	}
	code, body := ledgerGet(t, env.testAgentdEnv, b434MaterialPath(card))
	if code != http.StatusOK {
		t.Fatalf("材料端点应 200，得 %d %s", code, body)
	}
	m, _ := b434DecodeMaterial(t, body)
	if !strings.HasPrefix(m.Brief, "[handoff] ") {
		t.Fatalf("条 6：brief 须以 \"[handoff] \" 开头：%q", m.Brief)
	}
	for i, want := range []string{
		"- 卡号：" + card,                      // 条 7
		"- 有效基线分支：" + b434MaterialBase,      // 条 8（父卡继承，证明走 EffectiveBaseBranch）
		"- 所在会话：" + session,                 // 条 9
		"--desktop-session",                 // 条 10
		"--via page",                        // 条 11
		"- 项目根目录：" + m.Folder,               // 模板一致性
		"需要人就 `@" + b434MaterialOwner + "`", // 群主末句
	} {
		if !strings.Contains(m.Brief, want) {
			t.Fatalf("断言 %d：brief 缺 %q：\n%s", i, want, m.Brief)
		}
	}
	if strings.Contains(m.Brief, "一次性回合") { // 条 12
		t.Fatalf("条 12：brief 不得含「一次性回合」：\n%s", m.Brief)
	}

	bare := b434BareCard(t, env, b434MaterialProject)
	code, body = ledgerGet(t, env.testAgentdEnv, b434MaterialPath(bare))
	if code != http.StatusOK {
		t.Fatalf("裸卡材料端点应 200，得 %d %s", code, body)
	}
	bm, _ := b434DecodeMaterial(t, body)
	for _, want := range []string{"- 有效基线分支：未设", "- 所在会话：未入群", "needs_human"} {
		if !strings.Contains(bm.Brief, want) {
			t.Fatalf("条 8/9：裸卡 brief 缺 %q：\n%s", want, bm.Brief)
		}
	}
}

// TestB434MaterialViaLauncher 锁 G3 消费面（契约 §3.9.1）：desktopLaunchMaterial(card,
// SeatDesktopViaLauncher) 的简报写 --via launcher 而非 page；词表外 via 报错。
func TestB434MaterialViaLauncher(t *testing.T) {
	env := newB434MaterialEnv(t)
	card := b434BareCard(t, env, b434MaterialProject)
	m, err := env.srv.desktopLaunchMaterial(card, proto.SeatDesktopViaLauncher)
	if err != nil {
		t.Fatalf("via=launcher 材料: %v", err)
	}
	if !strings.Contains(m.Brief, "--via launcher") || strings.Contains(m.Brief, "--via page") {
		t.Fatalf("via=launcher 简报应只含 --via launcher：\n%s", m.Brief)
	}
	if _, err := env.srv.desktopLaunchMaterial(card, proto.SeatDesktopViaManual); err == nil {
		t.Fatalf("via=manual 不是拉起材料的合法来源，应报错")
	}
}
