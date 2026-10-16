// desktopwake_slice_test.go —— B434 直通竖切（契约 §9.3 / 欠账 §10.4，重档法定步骤）。
//
// 职责：从声明缝 Server.consumeAutomationEventsOnce 进（spec 测试缝 5 的调用方），用
// 真 SQLite 账本 + 真 keystone（假 runner）+ t.TempDir() 作 HOME + opener spy，一路穿过
// resolveWakeCard → wakeCoordinatorRoundID（bind 早退处的桌面端分支）→ wakeDesktopSeat
// （缝 4，本机分支）→ desktopLetter → renderDesktopLetter → writeDesktopLetter →
// claudeContinueURL → opener，证明桌面端唤醒主缝接得上。
//
// 边界：只锁快乐路径（条 67、70–73、83、85、89–92）、无记录反面、祖先冒泡（条 87）与
// 信箱键安全（不出信箱目录）与队列出队不写信（契约 r10 第 10 条）。失败段计数、跨机转交、needs_human 唤醒、会话消息/工单/
// needs_human 三种来信段归 B434 G2，不在本文件。不直调 wakeDesktopSeat——否则测不到
// resolveWakeCard 那一段（拆解稿 §5.5 假绿条）。本文件的帮手供 G2 同包复用。
package agentd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// desktopOpenerSpy 记录交给 opener 的每条深链（Server.desktopOpen 的测试替身）。
// err 非 nil 时 open 记录后返回它（契约 r10 第 11 条，供 G2 注入失败 opener）；本文件用例均为 nil。
type desktopOpenerSpy struct {
	mu   sync.Mutex
	urls []string
	err  error
}

func (o *desktopOpenerSpy) open(link string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.urls = append(o.urls, link)
	return o.err
}

func (o *desktopOpenerSpy) calls() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.urls...)
}

// newDesktopSliceEnv 装配真账本自动化环境，注入 opener spy 与信箱根目录
// Server.desktopHome = t.TempDir()（信箱 = <home>/.handoff/desktop-wake；契约 r10 第 11 条，
// 不改进程 HOME）。返回值末项为注入的信箱根目录。
func newDesktopSliceEnv(t *testing.T) (*ledgerEnv, *queueTraceRunner, *desktopOpenerSpy, string) {
	t.Helper()
	home := t.TempDir()
	env, runner := newNoPTYAutomationEnv(t)
	spy := &desktopOpenerSpy{}
	env.srv.desktopOpen = spy.open
	env.srv.desktopHome = home
	return env, runner, spy, home
}

// bindDesktopSeat 让卡坐 bind 席位 cli:claude#<cliID>；desktopSession 非空时再经 L 的
// SetSeatDesktop 写桌面端记录（Machine = 本机 hostname，与 card bind 缺省一致；Via = page，
// 词表内非空——拆解稿 §3.10 r3）。返回席位身份。
func bindDesktopSeat(t *testing.T, env *ledgerEnv, cardID, cliID, desktopSession string) string {
	t.Helper()
	identity, err := proto.EncodeSeatIdentity("claude", cliID)
	if err != nil {
		t.Fatalf("编码 bind 席位身份 %q: %v", cliID, err)
	}
	if err := env.ledger.BindSeat(cardID, identity, proto.SeatSourceBind, ledger.SeatBearing{}); err != nil {
		t.Fatalf("卡 %s bind: %v", cardID, err)
	}
	if desktopSession == "" {
		return identity
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("取本机 hostname: %v", err)
	}
	if err := env.ledger.SetSeatDesktop(cardID, identity, ledger.SeatDesktop{
		Session: desktopSession, Machine: host, Via: proto.SeatDesktopViaPage,
	}); err != nil {
		t.Fatalf("写桌面端记录: %v", err)
	}
	return identity
}

// openDecisionSeq 在卡上开一条裁决（actor = cli:sycm@host），返回其 decision_opened 事件 seq。
func openDecisionSeq(t *testing.T, env *ledgerEnv, cardID, body string) int64 {
	t.Helper()
	if _, err := env.ledger.OpenDecision(cardID, body, []string{"先合 G1", "先合 V"}, "cli:sycm@host"); err != nil {
		t.Fatalf("开裁决: %v", err)
	}
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatalf("读卡事件: %v", err)
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == ledger.EvDecisionOpened {
			return events[i].Seq
		}
	}
	t.Fatalf("卡 %s 无 decision_opened 事件", cardID)
	return 0
}

// mdFilesUnder 列出 root 下全部 .md 文件（相对路径）：信箱键若逃出信箱目录也会在这里现形。
func mdFilesUnder(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".md") {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 %s: %v", root, err)
	}
	return out
}

// automationSeenHas 报告消费循环是否已把 seq 记为 seen。
func automationSeenHas(env *ledgerEnv, seq int64) bool {
	env.srv.automationMu.Lock()
	defer env.srv.automationMu.Unlock()
	_, ok := env.srv.automationSeen[seq]
	return ok
}

func TestB434DesktopWakeVerticalSlice(t *testing.T) {
	t.Run("本机桌面端席位_恰一封信恰一次continue_keystone零回合", func(t *testing.T) {
		env, runner, spy, home := newDesktopSliceEnv(t)
		cardID := createCoordCard(t, env)
		const cliID, desktopSession = "slice-cli-1", "local_slice-1"
		bindDesktopSeat(t, env, cardID, cliID, desktopSession)
		seq := openDecisionSeq(t, env, cardID, "先合 G1 还是 V？```rm -rf /```")
		var rounds []keystone.RoundResult
		env.srv.automationRoundHook = func(card string, r keystone.RoundResult) {
			if card == cardID {
				rounds = append(rounds, r)
			}
		}

		processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
		if err != nil || escalated {
			t.Fatalf("消费轮失败：processed=%d escalated=%v err=%v", processed, escalated, err)
		}
		if processed != 1 {
			t.Fatalf("processed=%d，want 1（恰一条 decision_opened 被唤醒消费，条 85）", processed)
		}

		dir := filepath.Join(home, proto.DesktopWakeDirRel)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("信箱目录 %s 未建（条 70）: %v", dir, err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		if len(names) != 1 {
			t.Fatalf("信箱应恰一个文件（条 89），实得 %v", names)
		}
		if !regexp.MustCompile(`^` + regexp.QuoteMeta(cliID) + `\.[0-9]{19}\.md$`).MatchString(names[0]) {
			t.Fatalf("信件文件名 %q 不匹配 ^<CLI id>\\.[0-9]{19}\\.md$（条 67）", names[0])
		}
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat 信箱目录: %v", err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("信箱目录权限 = %v，want 0700（条 70）", info.Mode().Perm())
		}

		data, err := os.ReadFile(filepath.Join(dir, names[0]))
		if err != nil {
			t.Fatalf("读信: %v", err)
		}
		letter := string(data)
		lines := strings.Split(letter, "\n")
		if !strings.HasPrefix(lines[0], proto.DesktopWakeMarker+" ") || !strings.Contains(lines[0], cardID) {
			t.Fatalf("首行 %q 应以 \"[handoff] \" 开头且含卡号 %s（条 71/72）", lines[0], cardID)
		}
		if lines[1] != desktopAuthorRosterPrefix+"cli:sycm@host" {
			t.Fatalf("第 2 行作者名册 = %q（条 263）", lines[1])
		}
		openLine := fmt.Sprintf("%s author=cli:sycm@host seq=%d kind=%s", desktopQuoteFenceOpen, seq, desktopQuoteKindEvent)
		if !slices.Contains(lines, openLine) {
			t.Fatalf("来信缺 decision_opened 的 event 围栏开栏行 %q（条 73/268）：\n%s", openLine, letter)
		}
		fences := 0
		for _, line := range lines {
			if strings.HasPrefix(line, "```") {
				fences++
			}
		}
		if fences != 2 || !strings.Contains(letter, "V？\\`\\`\\`rm -rf /\\`\\`\\`") {
			t.Fatalf("载荷里的 ``` 应逐个加反斜杠、全信恰 2 条围栏行（条 270/271），fences=%d：\n%s", fences, letter)
		}

		if got := spy.calls(); len(got) != 1 || got[0] != "claude://code/continue?session=local_slice-1" {
			t.Fatalf("opener 调用 = %v，want 恰一次 claude://code/continue?session=local_slice-1（条 83/90）", got)
		}
		if launches, resumes, _ := runner.snapshot(); launches != 0 || len(resumes) != 0 {
			t.Fatalf("桌面端席位不得经 keystone：launch=%d resumes=%v（条 91/92）", launches, resumes)
		}
		if rows := readWakeRoundCommentRows(t, env, cardID); len(rows) != 0 {
			t.Fatalf("桌面端分支不得进入 coordinate 回合（wake_round 行在 keystone.Wake 之前落）：%v", rows)
		}
		if !automationSeenHas(env, seq) {
			t.Fatalf("唤醒成功后 seq %d 应已标 seen", seq)
		}
		if len(rounds) != 1 || !rounds[0].Woke || rounds[0].SessionID != desktopSession {
			t.Fatalf("回合结果 = %+v，want 恰一次 Woke=true SessionID=%s", rounds, desktopSession)
		}
	})

	t.Run("无桌面端记录的bind席位_零信零opener", func(t *testing.T) {
		env, runner, spy, home := newDesktopSliceEnv(t)
		var logs bytes.Buffer
		env.srv.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		cardID := createCoordCard(t, env)
		bindDesktopSeat(t, env, cardID, "slice-cli-2", "")
		seq := openDecisionSeq(t, env, cardID, "无桌面端记录的裁决")

		processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
		if err != nil || escalated || processed != 0 {
			t.Fatalf("无记录 bind 不应被唤醒：processed=%d escalated=%v err=%v（条 86/93）", processed, escalated, err)
		}
		if _, statErr := os.Stat(filepath.Join(home, proto.DesktopWakeDirRel)); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("无桌面端记录不得建信箱（条 93）：stat err=%v", statErr)
		}
		if files := mdFilesUnder(t, home); len(files) != 0 {
			t.Fatalf("无桌面端记录不得写信（条 93）：%v", files)
		}
		if got := spy.calls(); len(got) != 0 {
			t.Fatalf("无桌面端记录不得调 opener（条 93）：%v", got)
		}
		if launches, resumes, _ := runner.snapshot(); launches != 0 || len(resumes) != 0 {
			t.Fatalf("bind 席位不得经 keystone：launch=%d resumes=%v", launches, resumes)
		}
		if !automationSeenHas(env, seq) {
			t.Fatalf("无人可叫醒的事件应按既有语义标 seen：seq=%d", seq)
		}
		if !strings.Contains(logs.String(), "自动化事件无 coordinate 席位可叫醒") {
			t.Fatalf("应留「自动化事件无 coordinate 席位可叫醒」日志，实得：\n%s", logs.String())
		}
	})

	t.Run("祖先冒泡_有桌面端记录的bind父卡接手子卡唤醒", func(t *testing.T) {
		env, runner, spy, home := newDesktopSliceEnv(t)
		parentID := createCoordCard(t, env)
		const parentCLI, parentDesktop = "slice-parent-cli", "local_slice-parent"
		bindDesktopSeat(t, env, parentID, parentCLI, parentDesktop)
		childID := splitCoordChild(t, env, parentID)
		seq := openDecisionSeq(t, env, childID, "子卡要裁决")

		processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
		if err != nil || escalated || processed != 1 {
			t.Fatalf("子卡事件应冒泡到桌面端父卡（条 87）：processed=%d escalated=%v err=%v", processed, escalated, err)
		}
		files := mdFilesUnder(t, home)
		wantPrefix := filepath.Join(proto.DesktopWakeDirRel, parentCLI) + "."
		if len(files) != 1 || !strings.HasPrefix(files[0], wantPrefix) {
			t.Fatalf("应恰一封信进父卡席位信箱（前缀 %s），实得 %v", wantPrefix, files)
		}
		data, err := os.ReadFile(filepath.Join(home, files[0]))
		if err != nil {
			t.Fatalf("读信: %v", err)
		}
		if !strings.Contains(string(data), fmt.Sprintf("seq=%d kind=%s", seq, desktopQuoteKindEvent)) {
			t.Fatalf("来信应含子卡事件 seq %d：\n%s", seq, data)
		}
		if got := spy.calls(); len(got) != 1 || got[0] != "claude://code/continue?session=local_slice-parent" {
			t.Fatalf("opener 调用 = %v，want 恰一次父卡桌面端会话", got)
		}
		if launches, resumes, _ := runner.snapshot(); launches != 0 || len(resumes) != 0 {
			t.Fatalf("桌面端父卡不得经 keystone：launch=%d resumes=%v", launches, resumes)
		}
	})

	t.Run("队列出队不写信", func(t *testing.T) {
		// 契约 r10 第 10 条：WakeQueueRelease 合成唤醒（drainIgnitionRequest 传 nil raws）没有
		// 账本事件可写信，桌面端分支须与 B434 前 bind 席位同款放行（zero, nil），否则
		// scheddrain 每轮回填并停清队、堵死整条队列。直调 wakeCoordinatorRoundID 是因为
		// 队列出队不经消费循环（这正是被测入口）。
		env, runner, spy, home := newDesktopSliceEnv(t)
		var logs bytes.Buffer
		env.srv.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		cardID := createCoordCard(t, env)
		bindDesktopSeat(t, env, cardID, "slice-cli-queue", "local_slice-queue")

		res, err := env.srv.wakeCoordinatorRoundID(context.Background(), cardID,
			[]keystone.WakeEvent{{Kind: keystone.WakeQueueRelease, Card: cardID}}, nil, "")
		if err != nil {
			t.Fatalf("队列出队唤醒桌面端席位应直接放行，实得 err=%v（undelivered=%v）", err, desktopUndelivered(err))
		}
		if res != (keystone.RoundResult{}) {
			t.Fatalf("队列出队应返回零回合结果，实得 %+v", res)
		}
		if files := mdFilesUnder(t, home); len(files) != 0 {
			t.Fatalf("队列出队不得写信：%v", files)
		}
		if got := spy.calls(); len(got) != 0 {
			t.Fatalf("队列出队不得调 opener：%v", got)
		}
		if launches, resumes, _ := runner.snapshot(); launches != 0 || len(resumes) != 0 {
			t.Fatalf("桌面端席位不得经 keystone：launch=%d resumes=%v", launches, resumes)
		}
		if !strings.Contains(logs.String(), "桌面端席位队列出队不写信，直接进节点再入口") {
			t.Fatalf("应留「桌面端席位队列出队不写信，直接进节点再入口」日志，实得：\n%s", logs.String())
		}
	})

	for i, cliID := range []string{"../evil", "/../evil", `back\slash`, ".hidden"} {
		t.Run(fmt.Sprintf("信箱键不安全_不出信箱且报未投递_%d", i), func(t *testing.T) {
			env, _, spy, home := newDesktopSliceEnv(t)
			cardID := createCoordCard(t, env)
			bindDesktopSeat(t, env, cardID, cliID, "local_slice-unsafe")
			openDecisionSeq(t, env, cardID, "不安全键的裁决")

			_, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
			if !desktopUndelivered(err) {
				t.Fatalf("信箱键 %q 不安全时应返回「未投递」错误（契约条 106 路径），实得 %v", cliID, err)
			}
			if files := mdFilesUnder(t, home); len(files) != 0 {
				t.Fatalf("信箱键 %q 不安全时不得落任何信件：%v", cliID, files)
			}
			if got := spy.calls(); len(got) != 0 {
				t.Fatalf("信未落盘不得调 opener（条 107）：%v", got)
			}
		})
	}
}
