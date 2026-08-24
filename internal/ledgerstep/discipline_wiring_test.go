// B229 缝 1 派发侧接线的回归测试：Dispatcher 数据字段携带的已解析三元组要
// 原样到达 Transport 与 dispatched 快照，未点名模板仍携带纯平台层正文，
// 正文原文不进日志。测试允许 import discipline——测试不进代码图，生产
// import 面零变化（契约 §5 直通竖切同款边界）。
package ledgerstep

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/ledger"
)

// captureLogs 把默认 logger 换成写进缓冲区的文本 handler，返回缓冲区。
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// resolveLikeCaller 按两个调用方装配处的同款三行闭包绑 lookup，经缝 1 解析。
func resolveLikeCaller(t *testing.T, st *ledger.Store, name string, cap *bool) discipline.ResolvedDiscipline {
	t.Helper()
	lookup := func(n string) (int, string, error) {
		d, err := st.GetDiscipline(n, 0)
		if err != nil {
			return 0, "", err
		}
		return d.Version, d.Body, nil
	}
	res, err := discipline.ResolveDispatch(lookup, discipline.DisciplineRef{Name: name}, true, cap)
	if err != nil {
		t.Fatalf("ResolveDispatch(%q): %v", name, err)
	}
	return res
}

// readDispatchPayload 读卡事件流里第一条 dispatched 事件的原始 payload。
func readDispatchPayload(t *testing.T, st *ledger.Store, cardID string) map[string]any {
	t.Helper()
	events, err := st.EventsFromAsc([]string{cardID}, 0, 50)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	for _, event := range events {
		if event.Type != ledger.EvDispatched {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(event.Payload, &raw); err != nil {
			t.Fatalf("解 dispatched payload 原文: %v", err)
		}
		return raw
	}
	t.Fatal("缺 dispatched 事件")
	return nil
}

// TestViaTemplateCarriesResolvedDiscipline 钉住缝 1 的消费端契约：调用方以数据
// 字段携带的已解析三元组必须原样到达 Transport（wire 字段）与快照（版本号）。
// 版本号断言穿原始 JSON 键，不只看 struct 回读——漏投影要在 wire 层变红。
func TestViaTemplateCarriesResolvedDiscipline(t *testing.T) {
	st, card := dispatchTestCard(t)
	if _, err := st.PutDiscipline(discipline.NameImplement, "实现纪律正文B229MARKER"); err != nil {
		t.Fatalf("种子账本纪律块: %v", err)
	}
	yes := true
	res := resolveLikeCaller(t, st, discipline.NameImplement, &yes)

	var got DispatchOpts
	d := &Dispatcher{
		St: st, Actor: "tester",
		DisciplineText:    res.Text,
		DisciplineVersion: res.Version,
		Transport: func(ctx context.Context, opts DispatchOpts) (string, error) {
			got = opts
			return "T-b229-named", nil
		},
	}
	result, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02"})
	if err != nil {
		t.Fatalf("ViaTemplate: %v", err)
	}

	if got.DisciplineText != res.Text {
		t.Fatalf("Transport 收到的 DisciplineText 未到位：长度 %d，want %d", len(got.DisciplineText), len(res.Text))
	}
	if !strings.Contains(got.DisciplineText, "平台不变量") || !strings.Contains(got.DisciplineText, "实现纪律正文B229MARKER") {
		t.Fatalf("下发的正文应是平台层+角色层组装产物，实得前 80 字节: %q", truncateRunes(got.DisciplineText, 80))
	}
	if got.DisciplineVersion != 1 {
		t.Fatalf("Transport 收到的 DisciplineVersion = %d, want 1（账本最新版）", got.DisciplineVersion)
	}
	if result.DisciplineVersion != 1 {
		t.Fatalf("DispatchResult.DisciplineVersion = %d, want 1", result.DisciplineVersion)
	}

	raw := readDispatchPayload(t, st, card.ID)
	if v, ok := raw["discipline_version"]; !ok {
		t.Fatalf("dispatched payload 缺 discipline_version 键: %v", raw)
	} else if v != float64(1) {
		t.Fatalf("payload discipline_version = %v, want 1", v)
	}
}

// TestViaTemplateUnnamedStillPlatforms 钉住 §3.1 的未点名形态：模板没点名纪律块
// 时派发照样携带纯平台层正文（拒发闸覆盖一切带正文派发），版本号记 0。
func TestViaTemplateUnnamedStillPlatforms(t *testing.T) {
	st, card := dispatchTestCard(t)
	if _, err := st.PutTemplate("bare-impl", ledger.TemplateDef{
		Executor: "opencode", Purpose: "implement", BranchPrefix: "cards",
		Prompt: "做 {{TITLE}}",
	}); err != nil {
		t.Fatalf("写无点名模板: %v", err)
	}
	yes := true
	res := resolveLikeCaller(t, st, "", &yes)

	var got DispatchOpts
	d := &Dispatcher{
		St: st, Actor: "tester",
		DisciplineText:    res.Text,
		DisciplineVersion: res.Version,
		Transport: func(ctx context.Context, opts DispatchOpts) (string, error) {
			got = opts
			return "T-b229-unnamed", nil
		},
	}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "bare-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("ViaTemplate: %v", err)
	}

	if !strings.Contains(got.DisciplineText, "平台不变量") {
		t.Fatalf("未点名模板的派发仍须携带纯平台层正文（§3.1），实得: %q", truncateRunes(got.DisciplineText, 80))
	}
	if got.DisciplineVersion != 0 {
		t.Fatalf("未点名时 DisciplineVersion = %d, want 0", got.DisciplineVersion)
	}
	raw := readDispatchPayload(t, st, card.ID)
	if _, ok := raw["discipline_version"]; ok {
		t.Fatalf("版本为 0 时 payload 不应带 discipline_version 键（omitempty）：%v", raw["discipline_version"])
	}
	var snap ledger.DispatchSnapshot
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 50)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	for _, event := range events {
		if event.Type != ledger.EvDispatched {
			continue
		}
		if err := json.Unmarshal(event.Payload, &snap); err != nil {
			t.Fatalf("解 dispatched payload: %v", err)
		}
		if snap.DisciplineVersion != 0 {
			t.Fatalf("未点名时快照版本应为 0，实得 %d", snap.DisciplineVersion)
		}
		return
	}
	t.Fatal("缺 dispatched 事件")
}

// TestViaTemplateLogOmitsBodyText 钉住 §3.4：派发日志带计量字段（版本号、字节数），
// 不带正文原文——纪律块可能含运营指令与敏感措辞。
func TestViaTemplateLogOmitsBodyText(t *testing.T) {
	st, card := dispatchTestCard(t)
	const secretRole = "绝密运营指令DO_NOT_LOG_B229"
	if _, err := st.PutDiscipline(discipline.NameImplement, secretRole); err != nil {
		t.Fatalf("种子账本纪律块: %v", err)
	}
	yes := true
	res := resolveLikeCaller(t, st, discipline.NameImplement, &yes)

	logs := captureLogs(t)
	d := &Dispatcher{
		St: st, Actor: "tester",
		DisciplineText:    res.Text,
		DisciplineVersion: res.Version,
		Transport: func(ctx context.Context, opts DispatchOpts) (string, error) {
			return "T-b229-log", nil
		},
	}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("ViaTemplate: %v", err)
	}

	out := logs.String()
	if !strings.Contains(out, "discipline_version=1") {
		t.Fatalf("派发日志应含计量字段 discipline_version，实得：%s", truncateRunes(out, 400))
	}
	if strings.Contains(out, secretRole) {
		t.Fatalf("派发日志不得含正文原文（§3.4）：%s", truncateRunes(out, 400))
	}
	if strings.Contains(out, "平台不变量（恒在层）") {
		t.Fatalf("派发日志不得含平台层正文原文：%s", truncateRunes(out, 400))
	}
}

// TestDispatchSnapshotVersionKeyRegression 契约 §5 快照回归两判据：
// ① RecordDispatch 落的 payload 带 discipline_version 键；
// ② 无该键的老事件反序列化得 0 且不报错（append-only 不回填）。
func TestDispatchSnapshotVersionKeyRegression(t *testing.T) {
	st, card := dispatchTestCard(t)
	if err := st.RecordDispatch(card.ID, ledger.DispatchSnapshot{
		Template: "feature-impl", TemplateVersion: 1, DisciplineName: "implement",
		DisciplineVersion: 7, Target: "mac-02", TaskID: "T-reg", Branch: "cards/B1-implement",
		Purpose: "implement", Actor: "tester",
	}); err != nil {
		t.Fatalf("RecordDispatch: %v", err)
	}
	raw := readDispatchPayload(t, st, card.ID)
	if v, ok := raw["discipline_version"]; !ok || v != float64(7) {
		t.Fatalf("RecordDispatch payload 应含 discipline_version=7，实得 %v（present=%v）", v, ok)
	}

	var old ledger.DispatchSnapshot
	oldEvent := `{"template":"feature-impl","template_version":1,"discipline_name":"implement",` +
		`"target":"mac-02","task_id":"T-old","branch":"cards/B1-implement"}`
	if err := json.Unmarshal([]byte(oldEvent), &old); err != nil {
		t.Fatalf("老事件反序列化不应报错: %v", err)
	}
	if old.DisciplineVersion != 0 {
		t.Fatalf("老事件缺键时应得 0，实得 %d", old.DisciplineVersion)
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
