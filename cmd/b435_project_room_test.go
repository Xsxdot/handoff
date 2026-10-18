// b435_project_room_test.go —— B435 O1：项目房间 CLI（`project room <p>`、`session sit --project <p>`）与
// `card show` 无组织者提示（契约 C-514–C-517、§3.5；O1 plan Q4/Q5/Q11/Q15）。
//
// 夹具：真 SQLite 账本 + 真 collab（roomServiceFor，CLI 组装点）+ 真 cobra 入口（runLedgerCLI）。卡由
// createCardWaitFixture 建（project=demo）。
package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

const b435CLIProject = "demo"

// b435ProjectRoomSummary 解析 `project room` 的 stdout：恰一行 SessionSummary JSON（原始键表 + 结构体）。
func b435ProjectRoomSummary(t *testing.T, out string) (map[string]json.RawMessage, proto.SessionSummary) {
	t.Helper()
	line := b435SingleLine(t, out)
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &keys); err != nil {
		t.Fatalf("stdout 不是 JSON 对象: %v %q", err, line)
	}
	var s proto.SessionSummary
	if err := json.Unmarshal([]byte(line), &s); err != nil {
		t.Fatal(err)
	}
	return keys, s
}

func b435ResidentRows(s proto.SessionSummary) []proto.SessionMember {
	var out []proto.SessionMember
	for _, m := range s.Members {
		if m.Kind == proto.SessionMemberResident {
			out = append(out, m)
		}
	}
	return out
}

// Q5 甲 / R-K1 (a)：首跑创建并打印一行 SessionSummary（project、owner=agent:handoff），stderr 报「已创建」与
// 无驻场者时的坐下命令；再跑同一 id、不再报「已创建」；有驻场者时 members 恰一行 resident 带其身份。
func TestB435ProjectRoomCommandCreatesAndShows(t *testing.T) {
	b435UnsetSessionEnv(t)
	e := newB435WaitEnv(t)
	out, errOut, err := runLedgerCLI(t, e.dir, "project", "room", b435CLIProject)
	if err != nil {
		t.Fatalf("project room: %v（stderr %q）", err, errOut)
	}
	keys, first := b435ProjectRoomSummary(t, out)
	if string(keys["project"]) != `"`+b435CLIProject+`"` || string(keys["owner"]) != `"`+proto.SystemNotifierIdentity+`"` {
		t.Fatalf("stdout 应带 project=%s、owner=%s，得 %s", b435CLIProject, proto.SystemNotifierIdentity, out)
	}
	if !regexp.MustCompile(`^session:\d+$`).MatchString(first.ID) || first.ID == e.session {
		t.Fatalf("项目房间应是新会话 session:<n>，得 %q（夹具会话 %s）", first.ID, e.session)
	}
	if !strings.Contains(errOut, "已创建项目房间 "+first.ID) || !strings.Contains(errOut, "handoff session sit --project "+b435CLIProject) {
		t.Fatalf("首跑 stderr 应报已创建并给坐下命令，得 %q", errOut)
	}
	if len(b435ResidentRows(first)) != 0 {
		t.Fatalf("新建项目房间不应有驻场者: %+v", first.Members)
	}
	if _, err := e.svc.SitResident(proto.SessionResident{Session: first.ID, Identity: "agent:org"}); err != nil {
		t.Fatal(err)
	}
	out, errOut, err = runLedgerCLI(t, e.dir, "project", "room", b435CLIProject)
	if err != nil {
		t.Fatalf("再跑 project room: %v", err)
	}
	_, again := b435ProjectRoomSummary(t, out)
	if again.ID != first.ID || strings.Contains(errOut, "已创建") || strings.Contains(errOut, "session sit") {
		t.Fatalf("再跑应同 id、stderr 无「已创建」与坐下提示: id=%s stderr=%q", again.ID, errOut)
	}
	rows := b435ResidentRows(again)
	if len(rows) != 1 || rows[0].Identity != "agent:org" {
		t.Fatalf("members 应恰一行 resident 且身份 agent:org: %+v", again.Members)
	}
	if _, _, err := runLedgerCLI(t, e.dir, "project", "room", "a b"); err == nil {
		t.Fatal("非法项目名应非 0")
	}
}

// Q4 甲：sit --project 先确保项目房间再走 R 的 SitResident 同一入口；位置参数与 --project 互斥、必有其一。
func TestB435SessionSitProjectCreatesRoomAndSits(t *testing.T) {
	b435UnsetSessionEnv(t)
	e := newB435WaitEnv(t)
	out, errOut, err := runLedgerCLI(t, e.dir, "session", "sit", "--project", b435CLIProject, "--as", "agent:org")
	if err != nil {
		t.Fatalf("sit --project: %v（stderr %q）", err, errOut)
	}
	line := b435SingleLine(t, out)
	if keys := b435JSONKeys(t, line); keys != "identity,sat_at,session" {
		t.Fatalf("stdout 键集应为 C-406 第二形态，得 %s", keys)
	}
	roomID, ok, err := e.st.ProjectRoom(b435CLIProject)
	if err != nil || !ok {
		t.Fatalf("sit --project 应已建项目房间: ok=%v err=%v", ok, err)
	}
	var got proto.SessionResident
	if err := json.Unmarshal([]byte(line), &got); err != nil || got.Session != roomID || got.Identity != "agent:org" {
		t.Fatalf("stdout 应为项目房间驻场者一行: %+v err=%v", got, err)
	}
	showOut, _, err := runLedgerCLI(t, e.dir, "project", "room", b435CLIProject)
	if err != nil {
		t.Fatal(err)
	}
	if _, s := b435ProjectRoomSummary(t, showOut); len(b435ResidentRows(s)) != 1 || b435ResidentRows(s)[0].Identity != "agent:org" {
		t.Fatalf("project room 应显示该驻场者: %+v", s.Members)
	}
	_, _, err = runLedgerCLI(t, e.dir, "session", "sit", "--project", b435CLIProject, "--as", "agent:x")
	b435NonZeroNot124(t, err, "第二次 sit --project")
	if r, ok, _ := e.st.SessionResident(roomID); !ok || r.Identity != "agent:org" {
		t.Fatalf("原驻场者不得变: %+v ok=%v", r, ok)
	}
	_, _, err = runLedgerCLI(t, e.dir, "session", "sit", e.session, "--project", "other", "--as", "agent:y")
	b435NonZeroNot124(t, err, "位置参数与 --project 同给")
	if _, ok, _ := e.st.ProjectRoom("other"); ok {
		t.Fatal("互斥被拒不得创建项目房间")
	}
	if _, ok, _ := e.st.SessionResident(e.session); ok {
		t.Fatal("互斥被拒不得登记驻场者")
	}
	_, _, err = runLedgerCLI(t, e.dir, "session", "sit", "--as", "agent:y")
	b435NonZeroNot124(t, err, "位置参数与 --project 都不给")
}

func b435NoOrganizerHint() string {
	return fmt.Sprintf(cardShowNoOrganizerHint, b435CLIProject, b435CLIProject)
}

// C-514/515/516：无项目房间、有房间无驻场者 → stderr 逐字节 = §3.5 提示行 + "\n"；有驻场者 → stderr 空；
// 三种情形 stdout 逐字节相同、退出码 0。
func TestB435CardShowNoOrganizerHint(t *testing.T) {
	b435UnsetSessionEnv(t)
	e := newB435WaitEnv(t)
	card := createCardWaitFixture(t, e.dir)
	if want := "提示：项目无组织者（项目 " + b435CLIProject + "；handoff project room " + b435CLIProject +
		" 取项目房间后 session sit 坐下）\n"; b435NoOrganizerHint() != want {
		t.Fatalf("提示行常量与契约 §3.5 不符:\n got=%q\nwant=%q", b435NoOrganizerHint(), want)
	}
	noRoomOut, noRoomErr, err := runLedgerCLI(t, e.dir, "card", "show", card)
	if err != nil {
		t.Fatalf("card show: %v", err)
	}
	if noRoomErr != b435NoOrganizerHint() {
		t.Fatalf("无项目房间 stderr 应逐字节为提示行，得 %q", noRoomErr)
	}
	room, _, err := e.svc.EnsureProjectRoom(b435CLIProject, "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	noResOut, noResErr, err := runLedgerCLI(t, e.dir, "card", "show", card)
	if err != nil {
		t.Fatal(err)
	}
	if noResErr != b435NoOrganizerHint() {
		t.Fatalf("有房间无驻场者 stderr 应逐字节为提示行，得 %q", noResErr)
	}
	if _, err := e.svc.SitResident(proto.SessionResident{Session: room.ID, Identity: "agent:org"}); err != nil {
		t.Fatal(err)
	}
	seatedOut, seatedErr, err := runLedgerCLI(t, e.dir, "card", "show", card)
	if err != nil {
		t.Fatal(err)
	}
	if seatedErr != "" {
		t.Fatalf("有驻场者不得出提示，得 %q", seatedErr)
	}
	if noRoomOut != noResOut || noRoomOut != seatedOut {
		t.Fatalf("三种情形 stdout 应逐字节相同:\n%s\n%s\n%s", noRoomOut, noResOut, seatedOut)
	}
}

// C-517：项目房间绑定或驻场者读失败 → 不出提示、stdout 与退出码不变（不得把读失败说成「项目无组织者」）。
func TestB435CardShowHintReadFailureSilent(t *testing.T) {
	b435UnsetSessionEnv(t)
	for _, tc := range []struct {
		name  string
		stmts []string
	}{
		{"驻场者读失败", []string{`DROP TABLE session_residents`, `CREATE VIEW session_residents AS SELECT 'x' AS session`}},
		{"绑定读失败", []string{`DROP TABLE project_rooms`, `CREATE VIEW project_rooms AS SELECT 'x' AS broken`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newB435WaitEnv(t)
			card := createCardWaitFixture(t, e.dir)
			if _, _, err := e.svc.EnsureProjectRoom(b435CLIProject, "user:tester"); err != nil {
				t.Fatal(err)
			}
			baseline, baseErr, err := runLedgerCLI(t, e.dir, "card", "show", card)
			if err != nil || baseErr != b435NoOrganizerHint() {
				t.Fatalf("前提：读正常时有提示行: err=%v stderr=%q", err, baseErr)
			}
			for _, stmt := range tc.stmts {
				e.exec(t, stmt)
			}
			out, errOut, err := runLedgerCLI(t, e.dir, "card", "show", card)
			if err != nil {
				t.Fatalf("读失败不得改退出码: %v", err)
			}
			if errOut != "" {
				t.Fatalf("读失败不得出提示行，得 %q", errOut)
			}
			if out != baseline {
				t.Fatalf("读失败不得改 stdout:\n%s\n%s", baseline, out)
			}
		})
	}
}

// C-514 的真进程面（review F1）：提示行之外，进程 fd 2 上不得有任何别的输出——未经 logx.Setup 的默认 slog
// 会把账本 INFO 行直接写到进程 stderr，「恰一行」在真进程里就成了多行。cobra 的 ErrOrStderr 缓冲捕不到
// 那些行，所以这里同时接管 os.Stderr 与 log 包默认输出（默认 slog 落在后者）。测试基座自己的输出
// （seed 时的账本 Open、config 退休键 WARN）在接管之前完成或根本不产生，泄漏只可能来自命令本身。
func TestB435CardShowProcessStderrOnlyHint(t *testing.T) {
	b435UnsetSessionEnv(t)
	e := newB435WaitEnv(t)
	card := createCardWaitFixture(t, e.dir)
	cfgPath := filepath.Join(e.dir, "config.yaml")
	if err := config.Save(cfgPath, &config.Config{Listen: "127.0.0.1:0", Token: "t", DataDir: e.dir, StallTimeout: 2 * time.Hour}); err != nil {
		t.Fatalf("写测试配置: %v", err)
	}
	args := []string{"card", "show", card}
	seedCardCLIForTest(t, e.dir, args)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStderr, origLogOut := os.Stderr, log.Writer()
	os.Stderr = w
	log.SetOutput(w)
	t.Cleanup(func() { os.Stderr = origStderr; log.SetOutput(origLogOut) })
	resetAllFlags(rootCmd)
	var out, errb bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	rootCmd.SetArgs(append([]string{"--config", cfgPath}, args...))
	runErr := Execute()
	resetAllFlags(rootCmd)
	configPath = cfgPath
	os.Stderr = origStderr
	log.SetOutput(origLogOut)
	_ = w.Close()
	leaked, _ := io.ReadAll(r)
	if runErr != nil {
		t.Fatalf("card show: %v", runErr)
	}
	if errb.String() != b435NoOrganizerHint() {
		t.Fatalf("stderr 应逐字节为提示行，得 %q", errb.String())
	}
	if len(leaked) != 0 {
		t.Fatalf("进程 stderr 除提示行外不得有别的输出，泄漏 %d 字节：%q", len(leaked), string(leaked))
	}
}
