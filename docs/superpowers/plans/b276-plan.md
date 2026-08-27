# B276 实现计划：静默失效与错误归因

状态：spec r1 已批准。本节点只产出计划，不写实现代码、不改 charter 仓、不切分支。当前分支为 cards/B276-charter，合并目标为 fix/silent-wrong；台账为 docs/superpowers/specs/b276-ledger.md。

## 0. 约束、图查询与基线

spec 为 docs/superpowers/specs/b276.md。仓内有 codegraph，已按 best.json 词表查询 d_cli、d_policy、d_gateway、d_ledger、d_maintenance、d_protocol。各 context 有 fociTruncated，不能把配额外的空结果当作无调用方。renderStatusWithLookup 的 codegraph sym 原始结果已登记为：
Error: 符号 "renderStatusWithLookup" 不在图中（图未覆盖或名字有误）；近似候选: []
因此本计划对该符号显式记 grep 覆盖债。

本卡接口证据：

~~~go
func resolveBareDiscipline(ctx context.Context, cli *client.Client, rawFile string) (discipline.ResolvedDiscipline, error)
func resolveCardDispatchDiscipline(ctx context.Context, st *ledger.Store, name, target string) (discipline.ResolvedDiscipline, error)
func (s *Server) resolveStepDiscipline(node ledger.NodeDef, reqTarget string) (discipline.ResolvedDiscipline, error)
func ResolveDispatch(lookup DisciplineLookup, ref DisciplineRef, platformEnabled bool, targetCap *bool) (ResolvedDiscipline, error)
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request)
func renderStatusWithLookup(w io.Writer, addr string, cli proto.BuildInfo, st *proto.StatusResp, lookup func(taskID string) (cardID, driver string, heartbeatAt time.Time, ok bool))
func resolveServiceBinFrom(exe string, candidates []string) (string, error)
func isEphemeralBin(path string) bool
func (s *Store) SetAcceptance(id, criteria, actor string) error
func IsSelfCommand(s string) (hit bool, sub string)
func Compose(base Block, platformEnabled bool) Block
~~~

先验收命令必须使用任务专属 GOMODCACHE、GOCACHE、GOSUMDB=off；本节点已经亲自跑到的原始结果已逐条写入台账：

- B245 裸派发最小测试：ok github.com/Xsxdot/handoff/cmd 0.030s，退出 0。
- B245 agentd 最小测试：ok github.com/Xsxdot/handoff/internal/agentd 0.752s，退出 0。
- B211 agentd 最小测试：ok github.com/Xsxdot/handoff/internal/agentd 0.630s，退出 0；proto fixture：ok github.com/Xsxdot/handoff/internal/proto 0.010s，退出 0。
- B256 cmd 服务测试：ok github.com/Xsxdot/handoff/cmd 0.004s，退出 0；当前树不在 /tmp，/tmp 检出场景未验证。
- B261 ledger/cmd/agentd 最小测试：分别 ok github.com/Xsxdot/handoff/internal/ledger 0.590s、ok github.com/Xsxdot/handoff/cmd 0.355s、ok github.com/Xsxdot/handoff/internal/agentd 0.644s，均退出 0。
- B259 permgate/cmd 门禁：分别 ok 0.004s、ok 0.041s，均退出 0。
- canonical 查图 validate 已输出 containers 239、edges 4735、issues null、edgeIssues null、unscannedEntries 6，退出 0。
- 带 embedweb 的 setup 测试原始失败为：FAIL github.com/Xsxdot/handoff/internal/agentd [setup failed]；internal/webui/embed.go:15:12: pattern all:dist: no matching files found。带 embedweb 真机构建当前未验证。

## 1. Task B245：探活错误不得冒充版本过旧

### 文件与接口

有界文件：

- cmd/dispatch.go：resolveBareDiscipline
- cmd/card_dispatch.go：resolveCardDispatchDiscipline
- internal/agentd/cardstep.go：resolveStepDiscipline
- cmd/dispatch_discipline_test.go：裸派发测试
- cmd/card_dispatch_test.go：模板卡派发测试
- internal/agentd/cardstep_discipline_test.go：环节派发测试

Consumes：

~~~go
func resolveBareDiscipline(ctx context.Context, cli *client.Client, rawFile string) (discipline.ResolvedDiscipline, error)
func resolveCardDispatchDiscipline(ctx context.Context, st *ledger.Store, name, target string) (discipline.ResolvedDiscipline, error)
func (s *Server) resolveStepDiscipline(node ledger.NodeDef, reqTarget string) (discipline.ResolvedDiscipline, error)
func ResolveDispatch(lookup DisciplineLookup, ref DisciplineRef, platformEnabled bool, targetCap *bool) (ResolvedDiscipline, error)
~~~

Produces：成功探活仍进入 ResolveDispatch；Status 成功但能力位 nil/false 仍得到现有 ErrUnsupportedTarget 升级文案；目标客户端获取失败或 Status 失败则直接返回包含 cause、含目标机可达/agentd 运行/token 一致处置、且不含升级归因的探活失败错误。

### 基线与测试范围

先跑：

~~~sh
go test ./cmd -run 'TestBareDispatch(CarriesAssembledDiscipline|RefusesUnsupportedTarget)$' -count=1
go test ./internal/agentd -run 'Test(CardStepDeliversResolvedDiscipline|StartCardStepRejectsUnsupportedTarget)$' -count=1
~~~

预期是台账的两行 ok；只跑 cmd 与 internal/agentd。

### 实现步骤

1. 2–5 分钟：在 resolveBareDiscipline 的 cli.Status 错误分支直接返回，保留成功分支和 ResolveDispatch：
   ~~~go
   status, err := cli.Status(ctx)
   if err != nil {
       slog.Error("裸派发前目标机探活失败", "target", targetName, "cause", err)
       return discipline.ResolvedDiscipline{}, fmt.Errorf(
           "目标机探活失败：请确认目标机可达、agentd 正在运行且 token 一致：%w", err)
   }
   cap := status.DisciplinesSupported
   ~~~
   函数注释要明确能力位缺席才交给 ResolveDispatch；探活错误不再保持 cap=nil 继续下行。
2. 2–5 分钟：在 resolveCardDispatchDiscipline 中，targetClient 失败和 cl.Status 失败都直接返回相同用户语义；done 只在成功拿到 cl 后 defer。两个错误分支均用 slog.Error 带 target/cause，lookup、ResolveDispatch、成功 Info 不改。
3. 2–5 分钟：在 resolveStepDiscipline 中，s.pool.For 和 cl.Status 失败都直接返回相同语义，s.log.Error 带 node/target/cause。target 为空的旧异步路径仍返回空结果和 nil；成功能力位和拒发包装保留。
4. 2–5 分钟：更新三个函数注释，说明网络/认证根因必须可见；禁止增加 ResolveDispatch 的 probeErr 参数，禁止抽公共框架；所有成功路径继续结构化日志。
5. 2–5 分钟：先加以下裸派发红测，再跑单测；入口是 root dispatch，经现有 runBareDispatchAgainstFake 真实访问 Status：
   ~~~go
   func TestBareDispatchProbeFailureDoesNotClaimUnsupported(t *testing.T) {
       _, errOut, target, err := runBareDispatchAgainstFake(t, "not-json")
       if err == nil {
           t.Fatal("Status 失败时裸派发必须返回错误")
       }
       joined := err.Error() + errOut
       if !strings.Contains(joined, "探活失败") {
           t.Fatalf("错误必须说明探活失败：%s", joined)
       }
       if !strings.Contains(joined, "invalid character") {
           t.Fatalf("错误必须保留 Status cause：%s", joined)
       }
       if strings.Contains(joined, "升级到同批版本") {
           t.Fatalf("探活失败不得归因成版本升级：%s", joined)
       }
       if n := target.tasks(); n != 0 {
           t.Fatalf("探活失败不得发送任务，实际 %d 次", n)
       }
   }
   ~~~
   红测必须保留 cause 断言，不能只断言“失败”。
6. 2–5 分钟：在 cmd/card_dispatch_test.go 建独立 httptest 目标，不复用裸派发 captureTarget；/api/status 写 not-json，/api/tasks 被调用时立即让测试失败。用 setupDisciplineGateFixture、runLedgerCLI card add/card dispatch、card show 真实走 root，断言错误含探活失败与 invalid character、不含升级到同批版本，且 driver_session 为空。
7. 2–5 分钟：在 internal/agentd/cardstep_discipline_test.go 建独立 probeErrorTargetMachine，不复用 cmd 目标机；/api/status 写 not-json，/api/tasks 记录调用并让测试失败。注册目标后调用 startCardStep，断言错误含探活失败与 invalid character、不含升级到同批版本、dispatch count 为 0。存量 nil/false 能力位升级测试不改。
8. 2–5 分钟：gofmt 三个生产文件和三个测试文件，跑三支新增测试及两支存量测试，记录原始输出。

三条新增测试的完整代码形态如下；已有 harness 的 import 若已存在则不重复添加，JSON 标签必须保持原样：

~~~go
func TestCardDispatchProbeFailureDoesNotClaimUnsupported(t *testing.T) {
    dir := t.TempDir()
    setupDisciplineGateFixture(t, dir, "not-json")
    out, _, err := runLedgerCLI(t, dir, "card", "add", "探活错误卡", "--project", "demo", "--workflow", "bug")
    if err != nil {
        t.Fatalf("建卡: %v", err)
    }
    var created struct {
        ID string \`json:"id"\`
    }
    if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &created); err != nil {
        t.Fatalf("解码建卡: %v", err)
    }
    _, errOut, err := runLedgerCLI(t, dir, "card", "dispatch", created.ID)
    if err == nil {
        t.Fatal("Status 失败时模板卡派发必须返回错误")
    }
    joined := err.Error() + errOut
    if !strings.Contains(joined, "探活失败") ||
        !strings.Contains(joined, "invalid character") {
        t.Fatalf("错误必须含探活语义和 cause：%s", joined)
    }
    if strings.Contains(joined, "升级到同批版本") {
        t.Fatalf("探活失败不得归因成版本升级：%s", joined)
    }
    show, _, err := runLedgerCLI(t, dir, "card", "show", created.ID)
    if err != nil {
        t.Fatalf("读回卡: %v", err)
    }
    var card struct {
        DriverSession string \`json:"driver_session"\`
    }
    if err := json.Unmarshal([]byte(strings.TrimSpace(show)), &card); err != nil {
        t.Fatalf("解码卡: %v", err)
    }
    if card.DriverSession != "" {
        t.Fatalf("探活失败不得认领卡，driver_session=%q", card.DriverSession)
    }
}
~~~

~~~go
type probeErrorTargetMachine struct {
    ts        *httptest.Server
    mu        sync.Mutex
    dispatches int
}

func newProbeErrorTargetMachine(t *testing.T) *probeErrorTargetMachine {
    t.Helper()
    target := &probeErrorTargetMachine{}
    target.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path == "/api/status" {
            w.Header().Set("Content-Type", "application/json")
            fmt.Fprint(w, "not-json")
            return
        }
        if r.URL.Path == "/api/tasks" && r.Method == http.MethodPost {
            target.mu.Lock()
            target.dispatches++
            target.mu.Unlock()
            http.Error(w, "probe failure test must not dispatch", http.StatusInternalServerError)
            return
        }
        http.NotFound(w, r)
    }))
    t.Cleanup(target.ts.Close)
    return target
}

func registerProbeErrorTarget(t *testing.T, s *Server, target *probeErrorTargetMachine) {
    t.Helper()
    addr := strings.TrimPrefix(target.ts.URL, "http://")
    if err := s.swapConf(func(c *config.Config) error {
        c.Targets["probe-error"] = config.Target{Addr: addr, Token: testToken}
        return nil
    }); err != nil {
        t.Fatalf("登记探活错误目标: %v", err)
    }
}

func (target *probeErrorTargetMachine) dispatchCount() int {
    target.mu.Lock()
    defer target.mu.Unlock()
    return target.dispatches
}

func TestCardStepProbeFailureDoesNotClaimUnsupported(t *testing.T) {
    env := newLedgerEnv(t)
    seedCardWithProject(t, env.srv, "handoff")
    card, err := env.ledger.GetCard("B1")
    if err != nil {
        t.Fatal(err)
    }
    seedDisciplineOnLedger(t, env, discipline.NameImplement, "探活失败不应下发")
    target := newProbeErrorTargetMachine(t)
    registerProbeErrorTarget(t, env.srv, target)

    err = env.srv.startCardStep(card.ID, proto.CardStepReq{
        Step: "进行中", Target: "probe-error", Actor: "test",
    })
    if err == nil {
        t.Fatal("Status 失败时环节派发必须返回错误")
    }
    if !strings.Contains(err.Error(), "探活失败") ||
        !strings.Contains(err.Error(), "invalid character") {
        t.Fatalf("错误必须含探活语义和 cause：%v", err)
    }
    if strings.Contains(err.Error(), "升级到同批版本") {
        t.Fatalf("探活失败不得归因成版本升级：%v", err)
    }
    if got := target.dispatchCount(); got != 0 {
        t.Fatalf("探活失败不得发送任务，实际 %d 次", got)
    }
}
~~~

### 验收

三支新增测试入口分别穿过三个声明符号；网络/认证错误与能力位 nil/false 由不同断言分流；错误带 cause 和行动建议、无升级归因、无任务请求/认领。缺陷族 A 通过。只跑 cmd、internal/agentd 触及测试，日志用 slog。

## 2. Task B211：status 三态和 stub 可见性

### 文件与接口

有界文件：

- internal/proto/status.go：StatusResp
- internal/agentd/server.go：Handler、handleStatus
- internal/agentd/status_test.go：真实 GET
- internal/agentd/server_test.go：启动日志
- cmd/status.go：renderStatusWithLookup
- cmd/status_test.go：真实 status CLI
- internal/proto/contract_fixture_test.go：statusSample

Consumes：

~~~go
func (s *Server) Handler() http.Handler
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request)
func renderStatusWithLookup(w io.Writer, addr string, cli proto.BuildInfo, st *proto.StatusResp, lookup func(taskID string) (cardID, driver string, heartbeatAt time.Time, ok bool))
func (e *statusEnv) getStatus(t *testing.T) *proto.StatusResp
~~~

Produces：

~~~go
type StatusResp struct {
    WebEmbedded *bool \`json:"web_embedded,omitempty"\`
}
~~~

### 基线与测试范围

先跑：

~~~sh
go test ./internal/agentd -run 'TestStatus(Reports|TaskCounts|ListenAux)|TestStatusAlwaysReportsUpdate' -count=1
go test ./internal/proto -run 'TestContractFixtures' -count=1
~~~

预期是台账中 agentd 0.630s、proto 0.010s 的两行 ok；只跑 internal/agentd、internal/proto、cmd status 测试。embedweb setup 失败原文已经记录，不得写成通过。

### 实现步骤

1. 2–5 分钟：在 PtySupported 邻近加入字段和三态注释：
   ~~~go
   // WebEmbedded 报告本机是否编译进 Web 控制台。
   //
   // nil = 对端未上报，false = 当前二进制是 stub，true = 已嵌入。
   // 使用指针保证非 nil 的 false 不被 omitempty 省略。
   WebEmbedded *bool \`json:"web_embedded,omitempty"\`
   ~~~
2. 2–5 分钟：Handler 只调用一次 webui.Embedded；true 保持 INFO，false 替换旧 INFO 为：
   ~~~go
   embedded := webui.Embedded()
   if embedded {
       s.log.Info("控制台前端", "embedded", true)
   } else {
       s.log.Warn("控制台前端是 stub：请使用带 -tags embedweb 的发布构建",
           "embedded", false, "consequence", "当前控制台页面只是说明页")
   }
   ~~~
   删除旧的无条件 INFO，避免同一事实同时 INFO/WARN；Manager 不 import webui。
3. 2–5 分钟：handleStatus 成功取得 resp 后、writeJSON 前写：
   ~~~go
   webEmbedded := webui.Embedded()
   resp.WebEmbedded = &webEmbedded
   ~~~
   保留请求、未就绪、聚合错误和完成 Info。
4. 2–5 分钟：statusSample 增加 webOK := false 与 WebEmbedded: &webOK，注释锁定默认构建 false。
5. 2–5 分钟：在 renderStatusWithLookup 数据区和空行前加入：
   ~~~go
   if st.WebEmbedded != nil && !*st.WebEmbedded {
       fmt.Fprintln(w, "控制台  前端未嵌入（当前页面是 stub）；请用带 -tags embedweb 的发布构建")
   }
   ~~~
   注释说明 nil 是旧 peer 缺键，true/nil 均不画 stub。
6. 2–5 分钟：写 internal/agentd 真实 GET 红测，使用 newStatusEnv/getStatus，默认构建断言 WebEmbedded 非 nil 且为 false；不可改成直接调用 Manager.Status。
7. 2–5 分钟：扩展 cmd/status_test.go 的 runStatus 真实 httptest 表，逐条断言：false 人读含 stub 和 -tags embedweb；true 人读不含 stub；缺席人读不含 stub；false JSON 的 agentd.web_embedded 存在且为 false；true 存在且为 true；缺席不含键。入口必须是 root status Execute。
8. 2–5 分钟：用 server_test.go signalHandler 捕获 Handler 构造日志；默认构建断言 WARN record 的 embedded=false、消息含 stub 和 -tags embedweb。true 分支保持 INFO；带 embedweb 真机构建因 all:dist 缺失标未验证。
9. 2–5 分钟：gofmt，跑 agentd/proto/cmd B211 测试，记录每条原始结果。

新增测试的完整形态：

~~~go
func TestStatusReportsWebEmbeddedStubOverHTTP(t *testing.T) {
    env := newStatusEnv(t, &probeStub{alive: true})
    got := env.getStatus(t)
    if got.WebEmbedded == nil {
        t.Fatal("默认构建的真实 /api/status 必须带 web_embedded=false")
    }
    if *got.WebEmbedded {
        t.Fatal("默认构建不应报告已嵌入 Web 控制台")
    }
}
~~~

~~~go
func TestStatusWebEmbeddedJSONAndTextStates(t *testing.T) {
    cases := []struct {
        name       string
        field      string
        wantStub   bool
        wantJSON   string
        absentJSON bool
    }{
        {name: "false", field: ",\"web_embedded\":false", wantStub: true, wantJSON: "\"web_embedded\":false"},
        {name: "true", field: ",\"web_embedded\":true", wantJSON: "\"web_embedded\":true"},
        {name: "nil", absentJSON: true},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                body := "{\"listen\":\"l\",\"data_dir\":\"d\",\"task_counts\":{},\"active\":[]" + tc.field + "}"
                w.Header().Set("Content-Type", "application/json")
                _, _ = w.Write([]byte(body))
            }))
            t.Cleanup(ts.Close)

            human, err := runStatus(t, writeStatusConfig(t), ts.URL)
            if err != nil {
                t.Fatalf("human status: %v", err)
            }
            if strings.Contains(human, "stub") != tc.wantStub {
                t.Fatalf("human stub 行存在=%v，want %v；输出：%s",
                    strings.Contains(human, "stub"), tc.wantStub, human)
            }
            jsonOut, err := runStatus(t, writeStatusConfig(t), ts.URL, "--json")
            if err != nil {
                t.Fatalf("json status: %v", err)
            }
            if tc.absentJSON {
                if strings.Contains(jsonOut, "web_embedded") {
                    t.Fatalf("nil/缺键不得投影 web_embedded：%s", jsonOut)
                }
            } else if !strings.Contains(jsonOut, tc.wantJSON) {
                t.Fatalf("JSON 缺少 %q：%s", tc.wantJSON, jsonOut)
            }
        })
    }
}
~~~

~~~go
func TestHandlerWarnsWhenWebConsoleIsStub(t *testing.T) {
    records := make(chan slog.Record, 32)
    env := newTestEnvWithLogger(t, slog.New(&signalHandler{
        h: slog.NewTextHandler(io.Discard, nil),
        on: func(record slog.Record) {
            records <- record
        },
    }))
    _ = env
    for {
        select {
        case record := <-records:
            if record.Level != slog.LevelWarn ||
                !strings.Contains(record.Message, "stub") ||
                !strings.Contains(record.Message, "-tags embedweb") {
                continue
            }
            embedded := true
            record.Attrs(func(attr slog.Attr) bool {
                if attr.Key == "embedded" {
                    embedded = attr.Value.Bool()
                }
                return true
            })
            if embedded {
                t.Fatal("stub WARN 必须带 embedded=false")
            }
            return
        default:
            t.Fatal("Handler 构造时未记录 stub WARN")
        }
    }
}
~~~

### 验收与序列化边界

真实 GET 默认 JSON 含 web_embedded:false；CLI JSON 保留 false/true，nil 缺键；human 仅 false 画 stub。序列化边界逐处覆盖 StatusResp 标签、handleStatus、client.Status 解码、statusJSON.Agentd 再编码、renderStatusWithLookup。缺陷族 B 通过；日志用 slog。

## 3. Task B256：修测试夹具，不动生产临时路径规则

### 文件与接口

有界文件：

- cmd/service_test.go：回退测试及辅助
- cmd/service.go：只读核对，禁止修改

Consumes：

~~~go
func resolveServiceBinFrom(exe string, candidates []string) (string, error)
func isEphemeralBin(path string) bool
func regularFileExists(path string) bool
~~~

Produces：测试创建的候选是普通文件；创建后先断言 regularFileExists 为真且 !isEphemeralBin，否则 Fatal 并带完整路径和原因；稳定候选被选择；临时目录候选仍被拒绝；service.go 生产规则字节不变。

### 基线与测试范围

先跑：

~~~sh
go test ./cmd -run 'Test(IsEphemeralBin|ResolveServiceBin)' -count=1
~~~

当前真实结果为 ok 0.004s，但树不在 /tmp；/tmp 检出未验证。只跑 cmd 服务测试。

### 实现步骤

1. 2–5 分钟：移除 filepath.Abs("service.go") 候选，不使用仓源文件、t.TempDir 文件或 HOME。
2. 2–5 分钟：加入不假设 HOME 的稳定根辅助函数；不可写根继续尝试，所有失败 Fatal，不 Skip：
   ~~~go
   func makeDurableServiceFixture(t *testing.T) (string, func()) {
       t.Helper()
       roots := []string{"/var/cache/handoff-b256-fixture"}
       if parent := filepath.Dir(filepath.Clean(os.TempDir())); parent != "/" {
           roots = append(roots, filepath.Join(parent, "handoff-b256-fixture"))
       }
       for _, root := range roots {
           if err := os.MkdirAll(root, 0o755); err != nil {
               continue
           }
           if isEphemeralBin(root) {
               continue
           }
           dir, err := os.MkdirTemp(root, "case-")
           if err != nil {
               continue
           }
           return dir, func() { _ = os.RemoveAll(dir) }
       }
       t.Fatalf("找不到不在临时目录且可写的服务夹具根；os.TempDir=%q", os.TempDir())
       return "", func() {}
   }
   ~~~
   非 Unix 平台使用等价的系统稳定根，但仍逐项断言 !isEphemeralBin；不能退回 HOME。
3. 2–5 分钟：把回退测试替换为：
   ~~~go
   func TestResolveServiceBinFallsBackFromGoBuildCache(t *testing.T) {
       dir, cleanup := makeDurableServiceFixture(t)
       defer cleanup()
       durable := filepath.Join(dir, "handoff")
       if err := os.WriteFile(durable, []byte("ordinary service fixture"), 0o644); err != nil {
           t.Fatalf("写稳定候选 %q: %v", durable, err)
       }
       if !regularFileExists(durable) {
           t.Fatalf("稳定候选不是普通文件：%q", durable)
       }
       if isEphemeralBin(durable) {
           t.Fatalf("稳定候选仍被判为临时文件：%q；不能用它覆盖 /tmp 回退判据", durable)
       }
       got, err := resolveServiceBinFrom(
           "/Users/x/Library/Caches/go-build/44/aa-d/handoff",
           []string{durable})
       if err != nil {
           t.Fatalf("有稳定普通文件时应回退：%v", err)
       }
       resolved := durable
       if r, err := filepath.EvalSymlinks(durable); err == nil {
           resolved = r
       }
       if got != resolved {
           t.Fatalf("got %q, want %q", got, resolved)
       }
   }
   ~~~
4. 2–5 分钟：增加 t.TempDir 临时候选测试，创建普通文件并先断言 regularFileExists、再断言 isEphemeralBin 为真，调用 resolveServiceBinFrom 后断言错误含临时或 go run。保留 /tmp 规则。
5. 2–5 分钟：gofmt，跑 cmd B256 测试，确认 git diff 中 service.go 无修改。测试-only task 无生产 logger；Fatal 已带失败上下文，禁止 print。

### 验收

不读 service.go、不依赖仓路径/HOME、不 Skip；候选先经过两个显式判据，临时候选仍拒绝。缺陷族 C 通过；未实际跑 /tmp 只能写未验证。

## 4. Task B259：删除 graph 别名并迁移活查图文档

### 文件与接口

有界文件：

- cmd/graph.go：整文件删除
- internal/permgate/selfcmd.go：删除 graph 嵌套白名单
- internal/permgate/selfcmd_test.go、permgate_test.go：fail-closed
- internal/discipline/platform.go、platform_test.go：平台正文与 Compose
- cmd/root_test.go：新增 unknown 测试
- cmd/graph_gate_test.go：保持既有 contract gate，不改 charter import
- docs/codegraph-scan-recipe.md：所有活跃查图入口

Consumes：

~~~go
func Execute() error
func IsSelfCommand(s string) (hit bool, sub string)
func Compose(base Block, platformEnabled bool) Block
~~~

Produces：根命令无 graph；进程内 Execute 的 graph --help 返回 unknown；IsSelfCommand 对 graph resolve/inspect fail-closed；Compose 文本含 canonical go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . 且不含 handoff graph 执行入口；活跃 recipe 不含 handoff graph 或 go run . graph。

### 基线与测试范围

先跑：

~~~sh
go test ./internal/permgate -run 'Test(IsSelfCommand|JudgeUnknownGraphSubcommand)' -count=1
go test ./cmd -run 'TestRepoContractGate' -count=1
go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . validate
~~~

预期是台账中的 permgate/cmd 两行 ok 与 canonical validate 的 239 containers、4735 edges、issues null、edgeIssues null。只跑 cmd、internal/permgate、internal/discipline 与 canonical 查图命令；不调用 handoff CLI。

### 实现步骤

1. 2–5 分钟：删除 cmd/graph.go 全文件；不新增 graph command，不把 Short 改成仍可用。
2. 2–5 分钟：删除 selfCmdNestedReadOnly 的 graph/resolve；保留未知候选通用 fail-closed；更新旧注释，不能留下“Keeping graph”语义。
3. 2–5 分钟：selfcmd_test.go 将 graph resolve 改为 true/resolve，graph inspect 继续 true；permgate_test.go 断言两者均非 AutoAllow + RuleSafeCommand。
4. 2–5 分钟：新增以下进程内根命令测试；不启子进程、不执行 handoff CLI：
   ~~~go
   func TestRootRejectsDeletedGraphCommand(t *testing.T) {
       resetAllFlags(rootCmd)
       rootCmd.SetArgs([]string{"graph", "--help"})
       t.Cleanup(func() { rootCmd.SetArgs(nil) })
       err := Execute()
       if err == nil {
           t.Fatal("删除 graph 后 root 必须返回 unknown command")
       }
       if !strings.Contains(strings.ToLower(err.Error()), "unknown command") {
           t.Fatalf("错误应明确为 unknown command：%v", err)
       }
   }
   ~~~
   若 Cobra 实际错误本地化，先以真实错误原文固定等价断言，不可在未运行前猜测。
5. 2–5 分钟：platformInvariantHead 使用以下完整正文：
   ~~~text
   1. 不要派发、不要调用 handoff CLI、不要起任何新的 executor 进程或子任务。
   2. 查图使用 go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . <子命令>；也可使用已安装的 codegraph；两者均不可用时再 grep。
   ~~~
   platform_test.go 断言 Compose(base,true) 含 canonical 且不含 handoff graph；关闭平台层的旧断言保留。此处无外部调用，文本测试是用户可见成功路径。
6. 2–5 分钟：recipe 第 7 行迁移到 canonical validate，第 391 行迁移到 canonical validate/domains，其他活跃 handoff graph 说明改成 canonical 命令或 codegraph validate。运行：
   ~~~sh
   rg -n 'handoff graph|go run \\. graph' docs/codegraph-scan-recipe.md
   ~~~
   预期无输出；这是文件扫描，不执行 CLI。
7. 2–5 分钟：gofmt，跑 permgate、cmd 根/graph gate、discipline platform 最小测试，再跑 canonical validate；失败原文落台账。

### 验收

cmd/graph.go 不存在；root unknown、权限 fail-closed、平台正文和活 recipe 给出 canonical go run。charter 仓不改。缺陷族 D 通过。删除命令无成功路径，错误和替代入口文本提供可见诊断。

## 5. Task B258：skill 404 按 target-aware pending_tickets 处置

### 文件与边界接口

有界文件：

- skills/handoff/SKILL.md，仅改约 178、254、596
- 不改 internal/agentd/server.go、不改 HTTP 状态码
- 不改约 279、592、622 的 consumed-ticket 语义

Boundary contract：以任务实际所在机器执行 handoff show <task> --target <机器>，读取 pending_tickets；ticket 仍在列表就原样重发 reply，不在列表才跳过。stale 计数不能独自决定 404 处置。

文档无可调用声明符号，内部锁合法理由是无法从 declaration seam 构造运行时调用；唯一测试是逐条内容脚本，并保护正确 consumed-ticket 行。

### 基线与测试范围

旧文案预期使以下只读脚本失败；执行者必须把真实失败原文落台账：

~~~sh
python3 - <<'PY'
from pathlib import Path
text = Path("skills/handoff/SKILL.md").read_text()
assert "handoff show <task> --target <机器>" in text
assert "pending_tickets" in text
assert "404——正常，跳过即可" not in text
PY
~~~

只读 SKILL.md，不跑全量。

### 实现步骤

1. 2–5 分钟：cursor 段替换为：
   ~~~text
   这不是 bug，是“事件即信号、show 即权威”分工的推论。所以纪律固定为：醒来先 show。发现 reply 返回 404 后，必须用任务实际所在机器执行 handoff show <task> --target <机器>，读取 pending_tickets：ticket 仍在列表就原样重发 reply；不在列表才按已消耗处理。历史 completed 是否代表当前状态仍由 state 决定。
   ~~~
2. 2–5 分钟：backlog_summary 的 stale 段替换为：
   ~~~text
   - stale 只是间隙里已经被审批链答掉的工单数，不能单独决定某个 404 是否可跳过。遇到 404，先在任务实际所在机器执行 handoff show <task> --target <机器>，检查 pending_tickets；仍在列表就原样重发，列表没有才跳过。不要把 backlog_summary 的计数当成当前欠办清单。
   ~~~
3. 2–5 分钟：重开 follow 排障行替换为：
   ~~~text
   | 重开 follow 后吐出旧事件 | cursor 只在 wait 交付时推进；show/reply 不推进，换机接管从 0 起 | 以 show 为准；若 reply 404，先在任务实际所在机器执行 handoff show <task> --target <机器> 并检查 pending_tickets；仍在列表就原样重发，不在列表才跳过 |
   ~~~
4. 2–5 分钟：保护同一个 ticket 第二次回答 404 与 resume 后挂起项已无的既有语义，不改 server.go。
5. 2–5 分钟：运行完整脚本。文档 task 无运行时 logger；正文显式提供 target/pending_tickets 边界，脚本逐条失败并显示字符串。

完整脚本：

~~~sh
python3 - <<'PY'
from pathlib import Path

text = Path("skills/handoff/SKILL.md").read_text()
required = [
    "handoff show <task> --target <机器>",
    "pending_tickets",
    "ticket 仍在列表就原样重发 reply",
    "不在列表才按已消耗处理",
]
for needle in required:
    assert needle in text, f"缺少正文判据: {needle}"
assert "404——正常，跳过即可" not in text, "仍有无前提的 404 跳过句"
assert "补 reply 会 404，跳过即可" not in text, "stale 仍把 404 导向跳过"
assert "同一个 ticket 回答两次，第二次 404。" in text, "已消耗保护句被误伤"
assert "工单已被消耗" in text, "consumed-ticket 保护语义被误伤"
print("B258 skill 三处 target-aware 判据通过")
PY
~~~

### 验收

三处目标句都按 target-aware show + pending_tickets 决定重发/跳过，stale 不先下结论，consumed-ticket 保护语义在。缺陷族 E 通过。只改 SKILL.md；文档无运行时日志，脚本是合法内部锁。

## 6. Task B261：SetAcceptance 统一查询并覆盖 CLI/HTTP

### 文件与接口

有界文件：

- internal/ledger/cards.go：SetAcceptance、导出常量
- cmd/card.go：CLI --accept stderr 投影
- cmd/card_acceptance_inflight_test.go：新增真实 CLI
- internal/agentd/ledgerapi_test.go：真实 PATCH
- internal/ledger/taskstate.go、events.go、mirror.go：只读既有 API，不改状态机

Consumes：

~~~go
func (s *Store) SetAcceptance(id, criteria, actor string) error
func (s *Store) LatestTaskStates(cardID string) ([]TaskStateRow, error)
func (s *Store) AddComment(cardID, body, kind, actor string) (Event, error)
func (s *Store) MaxSeq() (int64, error)
func (s *Store) EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]Event, error)
func (s *Store) LinkTask(cardID, target, taskID, purpose, actor string) error
func (s *Store) AppendMirroredEvent(cardID string, ev MirroredEvent) (bool, error)
~~~

Produces：

~~~go
// AcceptanceInFlightNotice 是新判据对已启动轮次的明确影响说明。
const AcceptanceInFlightNotice = "本次修改对正在跑的轮次无效，将从下一轮 \`card dispatch --step\` 生效"
~~~

在飞规则固定为 LastType 不是 archived 且不是 failed；空、completed、turn_failed 都在飞。警告使用现有 EvComment，不新增事件类型；HTTP 正常路径仍 200 和 ok=true。

### 基线与测试范围

先跑：

~~~sh
go test ./internal/ledger -run 'Test(LiveMirrorTargets|LatestTaskStates|CardStepInFlight)' -count=1
go test ./cmd -run 'TestCardUpdate' -count=1
go test ./internal/agentd -run 'TestPatchCard' -count=1
~~~

预期为台账中的 ledger 0.590s、cmd 0.355s、agentd 0.644s 三行 ok。只跑三包触及测试。

### 实现步骤

1. 2–5 分钟：cards.go 导出常量并更新 SetAcceptance 注释，写明参数、返回值及写成功后查询实况的理由。
2. 2–5 分钟：保留原 UPDATE + 普通 EvComment 事务，提交成功后查询 LatestTaskStates。实现核心完整形态：
   ~~~go
   func (s *Store) SetAcceptance(id, criteria, actor string) error {
       log().Info("更新验收判据进入", "card", id, "actor", actor, "criteria_bytes", len(criteria))
       if err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
           result, err := tx.Exec(s.q(\`UPDATE cards SET acceptance_criteria = ?, updated_at = ? WHERE id = ?\`),
               criteria, s.tval(time.Now()), id)
           if err != nil {
               return fmt.Errorf("写判据: %w", err)
           }
           if count, _ := result.RowsAffected(); count == 0 {
               return fmt.Errorf("卡 %s: %w", id, ErrNotFound)
           }
           _, err = s.appendEvent(tx, sink, id, EvComment, actor,
               map[string]any{"kind": "普通", "body": "更新验收判据"})
           return err
       }); err != nil {
           log().Error("更新验收判据失败", "card", id, "actor", actor, "cause", err)
           return err
       }

       states, err := s.LatestTaskStates(id)
       if err != nil {
           log().Error("判据已写入但读取在飞 task 失败", "card", id, "actor", actor, "cause", err)
           return nil
       }
       liveIDs := make([]string, 0, len(states))
       for _, state := range states {
           if state.LastType != "archived" && state.LastType != "failed" {
               liveIDs = append(liveIDs, state.Target+"/"+state.TaskID)
           }
       }
       if len(liveIDs) == 0 {
           log().Info("验收判据更新完成，无在飞 task", "card", id, "actor", actor)
           return nil
       }

       body := AcceptanceInFlightNotice + "：" + strings.Join(liveIDs, "、")
       if _, err := s.AddComment(id, body, "普通", actor); err != nil {
           log().Error("判据已写入但在飞提示落账失败", "card", id, "actor", actor,
               "tasks", liveIDs, "cause", err)
           return fmt.Errorf("写在飞判据提示: %w", err)
       }
       log().Warn("更新验收判据影响在飞轮次", "card", id, "actor", actor, "tasks", liveIDs)
       return nil
   }
   ~~~
   查询失败记录 Error 且不伪装成无在飞；提示评论失败返回带 cause 的错误，避免强制提示静默丢失。
3. 2–5 分钟：cmd/card.go --accept 之前取 beforeSeq := st.MaxSeq；SetAcceptance 成功后 EventsFromAsc 读取新增 EvComment，json.Unmarshal body，只将含 ledger.AcceptanceInFlightNotice 的 body 写到 cmd.ErrOrStderr。辅助函数：
   ~~~go
   func printAcceptanceInFlightNotice(cmd *cobra.Command, st *ledger.Store, cardID string, fromSeq int64) error {
       events, err := st.EventsFromAsc([]string{cardID}, fromSeq, 100)
       if err != nil {
           return fmt.Errorf("读判据更新提示: %w", err)
       }
       for _, event := range events {
           if event.Type != ledger.EvComment {
               continue
           }
           var payload struct {
               Body string \`json:"body"\`
           }
           if err := json.Unmarshal(event.Payload, &payload); err != nil {
               return fmt.Errorf("解码判据更新提示事件 %d: %w", event.Seq, err)
           }
           if !strings.Contains(payload.Body, ledger.AcceptanceInFlightNotice) {
               continue
           }
           if _, err := fmt.Fprintln(cmd.ErrOrStderr(), payload.Body); err != nil {
               return fmt.Errorf("输出判据更新提示: %w", err)
           }
       }
       return nil
   }
   ~~~
   CLI 只投影本次新增评论，不复制 LastType 判定；HTTP 不调用此 helper，正常仍 200/ok。
4. 2–5 分钟：先写 cmd 真实 CLI 红测。每个 t.Run 独立 t.TempDir；runLedgerCLI card add 建卡；LinkTask；空状态不追加镜像；completed/archived 用 AppendMirroredEvent。card update <id> --accept 新判据后逐条断言：
   - 空 LastType：stderr 和 EvComment body 含 notice、target/task-id。
   - completed：stderr 和 EvComment body 含 notice、target/task-id。
   - archived：stderr 与事件无 notice。
   - archived + empty：只列 empty task-id，不列 archived task-id。
   - archived + completed：只列 completed task-id，不列 archived task-id。
   事件必须从 EventsFromAsc 解码，不以事件总数替代断言；关闭 seed store 后再执行 CLI。
5. 2–5 分钟：ledgerapi_test.go 用 newLedgerEnv、seedCard、ledgerPatch 打真实 PATCH /api/cards/<id>；empty/completed task 断言 code 200、JSON ok=true、卡事件有 notice/task-id；archived-only 断言 200 且无 notice。不能直接调用 handler。
6. 2–5 分钟：为 cards.go、card.go 和新增测试补职责头、导出参数/返回/注意事项；说明“两次评论”是先提交判据、再以既有 comment 表达当前轮次无效。入口、写失败、查询失败、无在飞、警告成功均有结构化日志；CLI 用户提示只能 stderr。
7. 2–5 分钟：gofmt，跑 ledger/cmd/agentd B261 最小测试，确认无新增事件类型、taskstate 终态未改。

cmd/card_acceptance_inflight_test.go 的完整测试骨架如下；它只复用已有 runLedgerCLI harness，所有判据均穿过真实 root Execute 和事件流：

~~~go
func seedAcceptanceTask(t *testing.T, dir, cardID, target, taskID, lastType string) {
    t.Helper()
    st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
    if err != nil {
        t.Fatalf("打开验收测试账本: %v", err)
    }
    defer st.Close()
    if err := st.LinkTask(cardID, target, taskID, ledger.PurposeImplement, "test"); err != nil {
        t.Fatalf("挂账 %s: %v", taskID, err)
    }
    if lastType == "" {
        return
    }
    if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
        Target: target, Task: taskID, SourceSeq: 1, Type: lastType,
        Payload: []byte("{}"), CreatedAt: time.Now(),
    }); err != nil {
        t.Fatalf("写 task 实况 %s: %v", taskID, err)
    }
}

func acceptanceCommentBodies(t *testing.T, dir, cardID string) []string {
    t.Helper()
    st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
    if err != nil {
        t.Fatalf("打开事件测试账本: %v", err)
    }
    defer st.Close()
    events, err := st.EventsFromAsc([]string{cardID}, 0, 1000)
    if err != nil {
        t.Fatalf("读卡事件: %v", err)
    }
    var bodies []string
    for _, event := range events {
        if event.Type != ledger.EvComment {
            continue
        }
        var payload struct {
            Body string \`json:"body"\`
        }
        if err := json.Unmarshal(event.Payload, &payload); err != nil {
            t.Fatalf("解码卡事件 %d: %v", event.Seq, err)
        }
        bodies = append(bodies, payload.Body)
    }
    return bodies
}

func TestCardUpdateAcceptanceReportsInFlightTasks(t *testing.T) {
    cases := []struct {
        name string
        tasks []struct {
            target, taskID, lastType string
        }
        liveIDs []string
        deadIDs []string
    }{
        {
            name: "empty",
            tasks: []struct{ target, taskID, lastType string }{{"mac-empty", "T-empty", ""}},
            liveIDs: []string{"mac-empty/T-empty"},
        },
        {
            name: "completed",
            tasks: []struct{ target, taskID, lastType string }{{"mac-completed", "T-completed", "completed"}},
            liveIDs: []string{"mac-completed/T-completed"},
        },
        {
            name: "archived",
            tasks: []struct{ target, taskID, lastType string }{{"mac-archived", "T-archived", "archived"}},
            deadIDs: []string{"mac-archived/T-archived"},
        },
        {
            name: "archived-plus-empty",
            tasks: []struct{ target, taskID, lastType string }{
                {"mac-archived", "T-archived", "archived"},
                {"mac-empty", "T-empty", ""},
            },
            liveIDs: []string{"mac-empty/T-empty"},
            deadIDs: []string{"mac-archived/T-archived"},
        },
        {
            name: "archived-plus-completed",
            tasks: []struct{ target, taskID, lastType string }{
                {"mac-archived", "T-archived", "archived"},
                {"mac-completed", "T-completed", "completed"},
            },
            liveIDs: []string{"mac-completed/T-completed"},
            deadIDs: []string{"mac-archived/T-archived"},
        },
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            dir := t.TempDir()
            out, _, err := runLedgerCLI(t, dir, "card", "add", "验收影响卡",
                "--project", "demo", "--workflow", "bug")
            if err != nil {
                t.Fatalf("建卡: %v", err)
            }
            var card struct {
                ID string \`json:"id"\`
            }
            if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
                t.Fatalf("解码建卡: %v", err)
            }
            for _, task := range tc.tasks {
                seedAcceptanceTask(t, dir, card.ID, task.target, task.taskID, task.lastType)
            }
            _, stderr, err := runLedgerCLI(t, dir, "card", "update", card.ID,
                "--accept", "新判据")
            if err != nil {
                t.Fatalf("更新判据: %v", err)
            }
            bodies := acceptanceCommentBodies(t, dir, card.ID)
            eventText := strings.Join(bodies, "\n")
            hasNotice := len(tc.liveIDs) > 0
            if strings.Contains(stderr, ledger.AcceptanceInFlightNotice) != hasNotice {
                t.Fatalf("stderr notice=%v，want %v：%s", strings.Contains(stderr,
                    ledger.AcceptanceInFlightNotice), hasNotice, stderr)
            }
            if strings.Contains(eventText, ledger.AcceptanceInFlightNotice) != hasNotice {
                t.Fatalf("事件 notice=%v，want %v：%s", strings.Contains(eventText,
                    ledger.AcceptanceInFlightNotice), hasNotice, eventText)
            }
            for _, id := range tc.liveIDs {
                if !strings.Contains(stderr, id) || !strings.Contains(eventText, id) {
                    t.Fatalf("在飞 task %s 未同时出现在 stderr 和事件：%s / %s", id, stderr, eventText)
                }
            }
            for _, id := range tc.deadIDs {
                if strings.Contains(stderr, id) || strings.Contains(eventText, id) {
                    t.Fatalf("终态 task %s 不应出现在警告：%s / %s", id, stderr, eventText)
                }
            }
        })
    }
}
~~~

该测试文件需要明确导入 encoding/json、path/filepath、strings、testing、time 以及 internal/ledger；它不允许直接调用 SetAcceptance 代替 CLI。

PATCH 接缝测试的完整函数如下；它复用真实 httptest server，检查 HTTP 成功和卡事件两条投影：

~~~go
func TestPatchCardAcceptanceReportsInFlight(t *testing.T) {
    t.Run("completed", func(t *testing.T) {
        env := newLedgerEnv(t)
        card := seedCard(t, env, "PATCH 在飞")
        if err := env.ledger.LinkTask(card.ID, "mac-02", "T-patch", ledger.PurposeImplement, "test"); err != nil {
            t.Fatal(err)
        }
        if _, err := env.ledger.AppendMirroredEvent(card.ID, ledger.MirroredEvent{
            Target: "mac-02", Task: "T-patch", SourceSeq: 1, Type: "completed",
            Payload: []byte("{}"), CreatedAt: time.Now(),
        }); err != nil {
            t.Fatal(err)
        }
        code, body := ledgerPatch(t, env.testAgentdEnv, "/api/cards/"+card.ID,
            "{\"acceptance_criteria\":\"PATCH 新判据\"}")
        if code != http.StatusOK {
            t.Fatalf("PATCH 应 200，得到 %d：%s", code, body)
        }
        var response struct {
            OK *bool \`json:"ok"\`
        }
        if err := json.Unmarshal([]byte(body), &response); err != nil {
            t.Fatalf("解码 PATCH 响应：%v", err)
        }
        if response.OK == nil || !*response.OK {
            t.Fatalf("PATCH 响应必须 ok=true：%s", body)
        }
        events, err := env.ledger.EventsFromAsc([]string{card.ID}, 0, 1000)
        if err != nil {
            t.Fatalf("读 PATCH 卡事件：%v", err)
        }
        eventText := string(events[len(events)-1].Payload)
        if !strings.Contains(eventText, ledger.AcceptanceInFlightNotice) ||
            !strings.Contains(eventText, "mac-02/T-patch") {
            t.Fatalf("PATCH 后卡事件缺在飞提示：%s", eventText)
        }
    })

    t.Run("archived-only", func(t *testing.T) {
        env := newLedgerEnv(t)
        card := seedCard(t, env, "PATCH 已归档")
        if err := env.ledger.LinkTask(card.ID, "mac-02", "T-archived", ledger.PurposeImplement, "test"); err != nil {
            t.Fatal(err)
        }
        if _, err := env.ledger.AppendMirroredEvent(card.ID, ledger.MirroredEvent{
            Target: "mac-02", Task: "T-archived", SourceSeq: 1, Type: "archived",
            Payload: []byte("{}"), CreatedAt: time.Now(),
        }); err != nil {
            t.Fatal(err)
        }
        code, body := ledgerPatch(t, env.testAgentdEnv, "/api/cards/"+card.ID,
            "{\"acceptance_criteria\":\"PATCH 无警告\"}")
        if code != http.StatusOK {
            t.Fatalf("归档 task 的 PATCH 应 200，得到 %d：%s", code, body)
        }
        events, err := env.ledger.EventsFromAsc([]string{card.ID}, 0, 1000)
        if err != nil {
            t.Fatalf("读归档 PATCH 事件：%v", err)
        }
        for _, event := range events {
            if strings.Contains(string(event.Payload), ledger.AcceptanceInFlightNotice) {
                t.Fatalf("只有 archived 时不得写在飞提示：%s", event.Payload)
            }
        }
    })
}
~~~

### 验收与序列化边界

CLI 真跑 card update --accept；empty/completed/turn_failed 警告，archived/failed 无警告，混合只列在飞。stderr 与 EvComment 同含固定无效语义和 target/task-id。PATCH 真打 HTTP，正常 200/ok，评论进入时间线。SetAcceptance 是唯一 LastType 判定处。序列化链为 EvComment payload → SQLite → EventsFromAsc json.RawMessage → CLI/HTTP 解码。缺陷族 F 通过。

## 7. 接缝矩阵与总收口

实现顺序为 B245、B211、B256、B259、B258、B261。每 task 先基线，再锁缝红、最小实现、跑绿；日志/注释/纯映射不另造红绿周期。全量测试不归入任何单 task。

| 接缝 | 锁缝测试入口 |
|---|---|
| resolveBareDiscipline | runBareDispatchAgainstFake/root dispatch |
| resolveCardDispatchDiscipline | runLedgerCLI/root card dispatch |
| Server.resolveStepDiscipline | startCardStep 的独立 probe-error 目标 |
| handleStatus | statusEnv.getStatus 的真实 GET /api/status |
| renderStatusWithLookup | root statusCmd 的真实 human/JSON；图覆盖债已登记 |
| resolveServiceBinFrom/isEphemeralBin | 真实普通文件回退与临时文件拒绝 |
| root command tree | rootCmd 进程内 Execute unknown |
| Compose | platform_test Compose 文本断言 |
| IsSelfCommand/Judge | selfcmd/permgate 纯判定 |
| SKILL.md | 完整 Python 内容脚本；内部锁理由已写 |
| SetAcceptance | runLedgerCLI 真 CLI 与 ledgerPatch 真 HTTP |

测试到缝：每支实现测试入口都穿过上表，B211 不用单独 renderer 构造体顶替 GET。缝到测试：每条接缝至少一支锁缝断言。

最终只在当前分支执行：

~~~sh
git diff --check
go test ./cmd ./internal/agentd ./internal/discipline ./internal/ledger ./internal/permgate ./internal/proto
go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . validate
~~~

每条命令必须真实跑到结果；失败原文立即追加 docs/superpowers/specs/b276-ledger.md。提交前确认仅有本卡计划与台账变更，git add 两个文件并 commit，不 push。

## 8. 五项自审

- 缺陷族 A：三条 B245 seam 分别将获取/Status error 与能力位 nil/false 分流。
- 缺陷族 B：B211 指针和实际 GET/JSON/human 测试区分 nil、false、true；embedweb 真机构建未验证。
- 缺陷族 C：B256 候选自己创建、先 regularFileExists 与 !isEphemeralBin、失败 Fatal，生产规则不动。
- 缺陷族 D：B259 同时锁 root、权限、平台正文、活 recipe，canonical validate 已有基线结果。
- 缺陷族 E：B258 只由 target-aware pending_tickets 决定重发/跳过，保护 consumed-ticket 语义。
- 缺陷族 F：B261 SetAcceptance 查询收口，CLI/HTTP 均有卡事件和用户可见提示。

序列化边界逐项列入：B211 的 StatusResp 标签、handleStatus、client decode、statusJSON 再 encode、human renderer；B261 的 comment payload、SQLite、EventsFromAsc、CLI/HTTP decode；B259 的 Compose/recipe 文本投影；B245 的真实 Status JSON error。值为零与字段缺失均有单独断言。

上下文预算：每个 task 文件集有界；renderer 图未覆盖与 SKILL 文档无 callable seam 已显式记债。类型标注：WebEmbedded 为 *bool，TaskStateRow.LastType 终态规则固定，AcceptanceInFlightNotice 为 string，跨 task 签名逐字一致。

占位符扫描声明：未留任何空白占位或泛化错误处理指令。B258 是唯一复用既有文档边界的例外，已给出完整 Python harness、逐条字符串和保护断言。embedweb、/tmp 未跑到时只能记录未验证。
