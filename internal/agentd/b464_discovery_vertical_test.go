// b464_discovery_vertical_test.go —— B464 段 1 首故事的竖切（plan §7 红→绿入口 `Discovery.*Vertical`）。
//
// 职责：按 cmd/agentd.go 组装点同形接线（账号域 + 编制域 + 生产发现器 accountwork.Discoverer + 真 quota
// 离线取材 + AfterRound 存量迁移），在夹具主 HOME 与测试账本上走完：发现认出登录 → 存量载体按 §5
// 规则 1 自动对上登录 → HTTP 新建载体选主 HOME 登录（HOME 镜像为空）→ 检测（WakeHome）→ 两个载体各
// 冻结派发一次 → 任务行 HomeDir/账号快照与执行器 env `HOME`（主 HOME 不覆盖、隔离 HOME 覆盖为登录 HOME）；全程断言 HOME 供给零凭据写入（V15：检测不复制、
// 任务启动经镜像不写凭据；协调者供给点的零复制由 orchestration 的 CoordinatorHome 测试锁）。
//
// 边界：执行器是改名的 fake（不起真 CLI）；检测的 opencode 是 PATH 上的假脚本；进程 HOME 钉到夹具，
// 不碰本机真实凭据；不起任何 agentd 进程、不连生产账本。
package agentd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/agentd/accountwork"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// recordingFake 记下每次 Start 的请求（看执行器拿到的 env HOME），其余行为同 namedFake。
type recordingFake struct {
	namedFake
	mu   sync.Mutex
	reqs []executor.StartReq
}

func (r *recordingFake) Start(ctx context.Context, req executor.StartReq) error {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.mu.Unlock()
	return r.namedFake.Start(ctx, req)
}

func (r *recordingFake) started() []executor.StartReq {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]executor.StartReq{}, r.reqs...)
}

// fileFacts 是 HOME 下一个文件的可比对事实（内容摘要 + 修改时间 + 权限）。
type fileFacts struct {
	sum   string
	mtime time.Time
	mode  fs.FileMode
}

func snapshotHome(t *testing.T, root string) map[string]fileFacts {
	t.Helper()
	out := map[string]fileFacts{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		rel, _ := filepath.Rel(root, path)
		out[rel] = fileFacts{sum: hex.EncodeToString(sum[:]), mtime: info.ModTime(), mode: info.Mode()}
		return nil
	})
	if err != nil {
		t.Fatalf("快照 HOME: %v", err)
	}
	return out
}

// credentialNames 是各 CLI 凭据文件的基名（新出现任何一个都算「HOME 供给写了凭据」）。
var credentialNames = map[string]bool{"auth.json": true, "opencode.db": true, ".credentials.json": true,
	"credentials.json": true, "oauth_creds.json": true, "token.json": true}

func assertNoCredentialWrites(t *testing.T, root string, before map[string]fileFacts, where string) {
	t.Helper()
	after := snapshotHome(t, root)
	for rel, b := range before {
		a, ok := after[rel]
		if !ok || a != b {
			t.Fatalf("%s：HOME 里已有文件 %s 被改动或删除（前 %+v 后 %+v）", where, rel, b, a)
		}
	}
	for rel := range after {
		if _, ok := before[rel]; !ok && credentialNames[filepath.Base(rel)] {
			t.Fatalf("%s：HOME 里新出现凭据文件 %s（凭据不复制，spec S8）", where, rel)
		}
	}
}

func writeOpencodeKey(t *testing.T, home, key string) {
	t.Helper()
	path := filepath.Join(home, acctCredRel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"opencode-go":{"type":"api","key":"`+key+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryCarrierLoginDispatchVertical(t *testing.T) {
	const machine = "linux-01"
	// 夹具主 HOME：主 HOME 一份 opencode-go key，隔离 HOME oc2 另一份 key（两个身份）。
	mainHome := t.TempDir()
	t.Setenv("HOME", mainHome)
	writeOpencodeKey(t, mainHome, "sk-b464-vertical-main")
	isoHome := filepath.Join(mainHome, ".handoff", "home", "oc2")
	writeOpencodeKey(t, isoHome, "sk-b464-vertical-oc2")
	installDetectCLI(t, `printf '%s\n' '{"type":"text","sessionID":"ses_detect","part":{"type":"text","text":"ok"}}'`)

	// 组装（同 cmd/agentd.go）：账本 + 编制域 + 账号域 + 改名 fake 执行器 opencode。
	lenv := newNoPTYLedgerEnv(t)
	SetupAutomationForTest(t, lenv.srv, lenv.ledger)
	rec := &recordingFake{namedFake: namedFake{fake.New(nil), "opencode"}}
	mgr := newManagerForServer(t, lenv.srv, map[string]executor.Adapter{"opencode": rec})
	projectID := registerTestProject(t, mgr, initTestRepo(t))
	putTarget(t, lenv.srv, machine, lenv.srv.conf().Listen)
	svc := mustScheduling(t, lenv.srv)
	acc := accounts.New(lenv.srv.SchedulingRegistry())
	lenv.srv.SetAccounts(acc)
	svc.SetAccounts(acc)
	acc.SetReferrers(svc.LoginReferrers)
	env := &schedEnv{testAgentdEnv: lenv.testAgentdEnv, svc: svc}

	// 存量载体（升级前形态：既无 login 也无 no_account，HOME 指向隔离 HOME oc2）。
	if err := svc.PutCarrier(scheduling.Carrier{Name: "legacy-oc", Machine: machine, CLI: "opencode",
		HomeDir: "~/.handoff/home/oc2"}, 0); err != nil {
		t.Fatalf("登记存量载体: %v", err)
	}
	before := snapshotHome(t, mainHome)

	// 发现一轮（生产发现器 + 真 quota 取材），AfterRound 同组装点接存量迁移。
	var migrated []scheduling.MigrationResult
	d := accountwork.NewDiscoverer(accountwork.DiscovererConfig{
		Accounts: acc, Quota: acctQuota(""), Machine: machine, MainHome: os.UserHomeDir,
		AfterRound: func(res accounts.ReconcileResult) {
			out, err := svc.MigrateCarrierLogins(res.Logins, lenv.srv.IsSelfMachine, hostapi.ExpandHomePath)
			if err != nil {
				t.Errorf("迁移: %v", err)
			}
			migrated = out
		},
	})
	res, err := d.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("发现: %v", err)
	}
	if len(res.Logins) != 2 || res.NewAccounts != 2 {
		t.Fatalf("应认出两条登录、两个新账号：%+v", res)
	}
	var mainLogin, isoLogin accounts.Login
	for _, l := range res.Logins {
		switch l.Home {
		case accounts.MainHome:
			mainLogin = l
		case "~/.handoff/home/oc2":
			isoLogin = l
		}
	}
	if mainLogin.ID == "" || isoLogin.ID == "" || mainLogin.Account == isoLogin.Account {
		t.Fatalf("主 HOME 与 oc2 应各一条登录、分属两个账号：%+v", res.Logins)
	}
	assertNoCredentialWrites(t, mainHome, before, "发现")

	// §5 规则 1：存量载体 HOME 与 oc2 登录相同、kind 兼容 → 自动写 login，home_dir 串不动。
	if len(migrated) != 1 || migrated[0].Carrier != "legacy-oc" || migrated[0].Login != isoLogin.ID {
		t.Fatalf("存量载体应按规则 1 对上 oc2 登录：%+v", migrated)
	}
	legacy, err := svc.Carrier("legacy-oc")
	if err != nil || legacy.Login != isoLogin.ID || legacy.HomeDir != "~/.handoff/home/oc2" {
		t.Fatalf("迁移后存量载体 = %+v / %v", legacy, err)
	}

	// 账号读面：登录与「上次发现」。
	code, body := schedReq(t, env, http.MethodGet, "/api/accounts", "")
	var accs proto.AccountsResp
	if code != http.StatusOK || json.Unmarshal([]byte(body), &accs) != nil || len(accs.Accounts) != 2 ||
		len(accs.Discovery) != 1 || accs.Discovery[0].Machine != machine || accs.Discovery[0].At == nil {
		t.Fatalf("GET /api/accounts: %d %s", code, body)
	}
	if strings.Contains(body, "sk-b464-vertical") {
		t.Fatal("账号读面泄露凭据")
	}

	// HTTP 新建载体选主 HOME 登录：服务端写 HOME 镜像 ""（主 HOME）。
	code, body = schedReq(t, env, http.MethodPut, "/api/squads/carriers/oc-main?expect=0",
		`{"machine":"`+machine+`","cli":"opencode","model":"opencode-go/glm-5","login":"`+mainLogin.ID+`","max_concurrency":1}`)
	if code != http.StatusOK {
		t.Fatalf("选登录建载体: %d %s", code, body)
	}
	c, raw := squadsCarrier(t, env, "oc-main")
	if c.HomeDir != "" || c.Login != mainLogin.ID || c.AccountID != mainLogin.Account || c.LoginState != "ok" {
		t.Fatalf("新载体投影：HOME 镜像应为空、带登录与账号：%s", raw)
	}

	// 检测（HOME 供给点 ①）：在登录 HOME 原样唤起，不复制、不建 HOME。
	for _, name := range []string{"oc-main", "legacy-oc"} {
		code, body = schedReq(t, env, http.MethodPost, "/api/squads/carriers/"+name+"/detect", "{}")
		if code != http.StatusOK || !strings.Contains(body, `"status":"online"`) {
			t.Fatalf("检测 %s: %d %s", name, code, body)
		}
	}
	assertNoCredentialWrites(t, mainHome, before, "检测")

	// 冻结派发（HOME 供给点 ③ 任务启动）：任务行 HomeDir 为镜像、账号快照为账号 id。主 HOME 登录 →
	// 执行器 env 不带 HOME 覆盖（沿用进程 HOME = 主 HOME）；隔离 HOME 登录 → env HOME = 该登录 HOME。
	envHome := func(req executor.StartReq) (string, bool) {
		home, set := "", false
		for _, kv := range req.Env {
			if v, ok := strings.CutPrefix(kv, "HOME="); ok {
				home, set = v, true
			}
		}
		return home, set
	}
	dispatch := func(carrier, homeDir string, login accounts.Login) proto.Task {
		t.Helper()
		rr := postDispatch(t, lenv.srv, dispatchBody(projectID,
			`,"carrier":"`+carrier+`","target":"","frozen_target":"`+machine+`","executor":"opencode","home_dir":"`+homeDir+`"`))
		if rr.Code != http.StatusOK {
			t.Fatalf("冻结派发 %s 应 200，实得 %d（%s）", carrier, rr.Code, rr.Body.String())
		}
		task := decodeDispatchTask(t, rr)
		stored, err := lenv.srv.st.GetTask(task.ID)
		if err != nil {
			t.Fatalf("读任务行: %v", err)
		}
		if stored.HomeDir != homeDir || stored.Account != login.Account || stored.AccountKind != accounts.KindOpencodeGo {
			t.Fatalf("%s 任务行 HOME/账号快照 = %q/%q/%q", carrier, stored.HomeDir, stored.Account, stored.AccountKind)
		}
		return task
	}
	mainTask := dispatch("oc-main", "", mainLogin)
	eventually(t, 10*time.Second, "执行器收到主 HOME 任务启动", func() bool { return len(rec.started()) >= 1 })
	if home, set := envHome(rec.started()[0]); set || os.Getenv("HOME") != mainHome {
		t.Fatalf("主 HOME 登录的任务不应覆盖 HOME（得 %q），进程 HOME 应为夹具主 HOME", home)
	}
	stopTask := func(id string) {
		t.Helper()
		if stop := runAction(lenv.srv, actionRequest(id, "stop", ""), lenv.srv.handleStop); stop.Code != http.StatusOK {
			t.Fatalf("清理任务返回 %d: %s", stop.Code, stop.Body.String())
		}
	}
	stopTask(mainTask.ID) // 同一仓库工作目录同时只许一个活跃任务
	isoTask := dispatch("legacy-oc", "~/.handoff/home/oc2", isoLogin)
	eventually(t, 10*time.Second, "执行器收到隔离 HOME 任务启动", func() bool { return len(rec.started()) >= 2 })
	if home, set := envHome(rec.started()[1]); !set || home != isoHome {
		t.Fatalf("隔离 HOME 登录的任务 env HOME = %q，want %q", home, isoHome)
	}
	assertNoCredentialWrites(t, mainHome, before, "任务启动")
	stopTask(isoTask.ID)
}
