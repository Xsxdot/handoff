# B205 实现计划：工作树分支与卡基线的首次派发前接续

本文件是 B205 的 implement 计划。当前 charter/plan 节点只提交计划文档，不实现
T1–T5 代码。

冻结物：

- spec：docs/superpowers/specs/2026-08-23-card-baseline-at-worktree-creation.md
- contract：docs/superpowers/specs/2026-08-23-b205-contract.md
- breakdown：docs/superpowers/specs/2026-08-23-b205-breakdown.md
- Ticket 0：f181e312；当前基线：501ee703
- 目标图：codegraph/target.json、codegraph/diffs/cards-B205-charter.json

冻结语义：首次 EvDispatched（包含 PurposeReview）在账本事务内冻结基线；空串只清除卡
自身覆盖值；建树成功后才逐卡写账；跨机请求只把建树请求发给目标，收到请求的一侧用
返回的 Workspace.Branch 写本地卡；账本未启用时建树静默忽略卡号且省略结果；不新增
状态列、事件类型或 HTTP 端点。

## 1. 基线复核与依赖事实

出稿前在当前基线真实执行：

| 命令 | 真实结果 |
| --- | --- |
| go test ./internal/proto ./internal/ledger -count=1 | proto 通过；ledger 单独复核为 ok，10.092s |
| go test ./cmd -run 'TestCard(AddListShowMove\|AddChildAndBaseBranch)' -count=1 | ok github.com/Xsxdot/handoff/cmd 0.298s |
| go test ./internal/agentd -run 'TestPatchCard\|TestProjectWorktreeCreate\|TestForward' -count=1 | ok github.com/Xsxdot/handoff/internal/agentd 1.225s |
| go test ./internal/proto -run TestContractFixtures -count=1 | ok，0.012s |
| go build ./... | exit 0 |
| go vet ./internal/ledger ./internal/agentd ./internal/proto | exit 0 |
| gofmt -l 触及的 Go 文件 | exit 0，无输出 |
| git diff --check | exit 0，无输出 |
| go run . graph validate --repo . --stale | exit 1；issues=null、edgeIssues=null，但报告 600 个既有失鲜节点与 unscannedEntries=2 |
| go run . graph check --repo . --view cards-B205-charter | exit 0；fails=[]，仅有既有 dead-assembly/legacy warns |
| go run . graph resolve --repo . --view cards-B205-charter --doc docs/superpowers/specs/2026-08-23-b205-contract.md | exit 0；契约锚点为 ok 或既有 moved |
| cd web && npx tsc -b | 基线环境失败：npm 缓存只读，EROFS；没有执行 TypeScript 编译 |
| cd web && npm test -- --run src/api/contract.test.ts | 基线环境失败：vitest not found，依赖未安装 |

Web 两条红是环境事实，不是代码判据。implement 节点必须取得可写 npm 缓存、执行
npm ci 后重新运行并记录原文，不能把这两条写成 pass。

> **协调者补充（2026-08-23）——T5 的验证落点**：上面这条纪律照办，但要加一条出口。
> 若在执行机上仍拿不到可写 npm 缓存（EROFS 依旧），**不要把 T5 的实现也停下来**：
> 把 T5 的代码按计划写完，**验证记为「未验（执行机无 web 工具链）」并如实交回**，
> 由协调者在本地补跑 `npx tsc -b --noEmit` 与 `npx vitest run` 并把原文落卡。
> 三种做法都不许：① 把环境红写成 pass；② 因为验不了就跳过 T5 的实现；
> ③ 用组件测试之外的东西（比如「类型看起来对」）冒充验证。
> 协调者本机的 web 工具链是好的（今天已跑过多轮 116 文件 / 1132 用例全绿）。

计划引用的库和对侧行为已经查证：

| 事实 | 出处 |
| --- | --- |
| mutate 内 PG 取 advisory lock、SQLite 单连接串行、提交后才触发 listener | internal/ledger/store.go:152-185 |
| Store.now、tval、toTime 是账本时间测试缝和两方言时间编码 | internal/ledger/store.go:35-47,124-145 |
| appendEventAt 在同一事务追加事件，PG 用 RETURNING、SQLite 用 LastInsertId | internal/ledger/events.go:23-58 |
| EventsFromAsc 是独立读查询，seq ASC，limit<=0 为 1000 | internal/ledger/events.go:60-103 |
| DispatchSnapshot 含 branch/purpose，RecordDispatch 写 EvDispatched | internal/ledger/events.go:109-143 |
| EffectiveBaseBranch 自身优先、否则沿父链，全空不猜 main/master | internal/ledger/relations.go:194-227 |
| ErrNotFound/ErrBadState 由 ledgerErr 翻译为 404/409 | internal/ledger/ledger.go:20-29、internal/agentd/ledgerapi.go:64-75 |
| 手工建树 Git 上限 2 分钟，成功后回读失败不谎报建树失败 | internal/agentd/workspace.go:106-113、internal/agentd/manualworktree.go:71-89 |
| 建树和通用转发请求体上限均为 1 MiB，显式转发跟随原 context | internal/agentd/projectadmin.go:920-925、internal/agentd/forward.go:30-34,72-83 |
| json.Decoder 当前未启用 DisallowUnknownFields | /usr/local/go/src/encoding/json/stream.go:41-44、internal/agentd/projectadmin.go:920-925 |

codegraph sym 已核对 Store.SetCardBaseBranch、Store.UpdateCardMeta、
Server.handleCardPatch、Server.handleProjectWorktreeCreate、CreateManualWorktree 的
现状签名。codegraph who-calls 对 Ticket 0 空壳报告 unscannedEntries: 2；这是新增调用
尚未落图的覆盖债，implement 后必须补跑 graph check，不能把空查询解释为没有调用者。

## 2. Interfaces

### Existing consumers

- 账本：func (s *Store) SetCardBaseBranch(id, branch, actor string) error
- 有效基线：func (s *Store) EffectiveBaseBranch(id string) (string, error)
- 派发记录：func (s *Store) RecordDispatch(cardID string, snap DispatchSnapshot) error
- 手工建树：func CreateManualWorktree(ctx context.Context, repo, worktreesDir string, req proto.CreateWorktreeReq) (proto.Workspace, error)
- 卡 patch handler：func (s *Server) handleCardPatch(w http.ResponseWriter, r *http.Request)
- 建树 handler：func (s *Server) handleProjectWorktreeCreate(w http.ResponseWriter, r *http.Request)
- Web 建树：export function createWorktree(name: string, req: CreateWorktreeReq, machine?: string): Promise<Workspace>
- Web patch：export const patchCard = (id: string, patch: CardPatch) => Promise<{ ok: boolean }>
- Web 卡列表：export const fetchCards = (params?: string) => Promise<{ cards: CardView[]; unlinked: UnlinkedSummary }>
- Web 卡详情：export const fetchCardDetail = (id: string) => Promise<CardDetail>

### Ticket 0 wire

~~~go
type CreateWorktreeReq struct {
    Mode    string   `json:"mode"`
    Branch  string   `json:"branch"`
    Base    string   `json:"base"`
    CardIDs []string `json:"card_ids,omitempty"`
}

type CardBaseBranchResult struct {
    ID    string `json:"id"`
    OK    bool   `json:"ok"`
    Error string `json:"error,omitempty"`
}

type Workspace struct {
    Path        string                 `json:"path"`
    Branch      string                 `json:"branch"`
    Head        string                 `json:"head"`
    IsMain      bool                   `json:"is_main"`
    Managed     bool                   `json:"managed"`
    CreatedAt   time.Time              `json:"created_at"`
    CardResults []CardBaseBranchResult `json:"card_results,omitempty"`
}
~~~

~~~ts
export interface CreateWorktreeReq {
  mode: 'new_branch' | 'existing_branch'
  branch: string
  base: string
  card_ids?: string[]
}

export interface Workspace {
  path: string
  branch: string
  head: string
  is_main: boolean
  managed: boolean
  created_at: string
  card_results?: CardBaseBranchResult[]
}

export interface CardBaseBranchResult {
  id: string
  ok: boolean
  error?: string
}

export interface CardPatch {
  title?: string
  priority?: string
  acceptance_criteria?: string
  base_branch?: string
}
~~~

### Cross-task produces/consumes

| Producer | Exact output | Consumer |
| --- | --- | --- |
| T1 | Store.SetCardBaseBranch；成功写 cards.base_branch/updated_at 与 EvComment，冻结错包装 ErrBadState | T2、T3、T4 |
| T2 | handoff card update <id> --base-branch <branch>；缺 flag 不调用门面，空值传空串 | CLI 用户 |
| T3 | PATCH /api/cards/{id}；缺 base_branch 不动，空值清除，404/409/503 分流 | T5 patchCard |
| T4 | 建树返回 Workspace；非空卡号按请求顺序返回 CardBaseBranchResult | T5 createWorktree、目标 agentd |
| T5 | 非空选择才发送 card_ids；抽屉发送 base_branch 字符串或空串 | T4 HTTP、T3 PATCH |

参数名和 JSON 键逐字符相同；不以“结构大致相同”替代。

## 3. Task 1：账本门与事件审计

### Files

- 修改 internal/ledger/cards.go
- 修改 internal/ledger/cards_test.go
- 不修改 internal/ledger/events.go、internal/ledger/relations.go、schema

### Interfaces

Consumes：Ticket 0 的 SetCardBaseBranch、EffectiveBaseBranch、RecordDispatch、
EvDispatched、DispatchSnapshot、Store.mutate、appendEventAt。

Produces：func (s *Store) SetCardBaseBranch(id, branch, actor string) error。一次 mutate
内完成取卡、按 seq ASC 取首条 EvDispatched、冻结判定、cards 更新和 EvComment。

### Step 1：写失败测试并先跑红

先在基线运行已有判据，预期现有继承测试为 ok：

~~~text
go test ./internal/ledger -run TestEffectiveBaseBranch -count=1
~~~

然后在 cards_test.go 加入 TestSetCardBaseBranch，复用已有 seedStore、mk、mustChild
夹具。测试代码可以照抄以下完整关键实现；已有 import 增加 encoding/json 与 time：

~~~go
func TestSetCardBaseBranch(t *testing.T) {
    t.Run("write and audit", func(t *testing.T) {
        s := seedStore(t)
        card := mk(t, s, "基线可写")
        if err := s.SetCardBaseBranch(card.ID, "cards/B205-charter", "test"); err != nil {
            t.Fatalf("写基线: %v", err)
        }
        got, err := s.GetCard(card.ID)
        if err != nil || got.BaseBranch != "cards/B205-charter" {
            t.Fatalf("读回自身基线: %+v err=%v", got, err)
        }
        effective, err := s.EffectiveBaseBranch(card.ID)
        if err != nil || effective != "cards/B205-charter" {
            t.Fatalf("读回有效基线: %q err=%v", effective, err)
        }
        events, err := s.EventsFromAsc([]string{card.ID}, 0, 100)
        if err != nil {
            t.Fatal(err)
        }
        var payload map[string]any
        found := false
        for _, event := range events {
            if event.Type == EvComment && strings.Contains(string(event.Payload), "更新卡基线") {
                if err := json.Unmarshal(event.Payload, &payload); err != nil {
                    t.Fatal(err)
                }
                found = true
            }
        }
        if !found || payload["base_branch"] != "cards/B205-charter" {
            t.Fatalf("comment payload 不完整: found=%v payload=%v", found, payload)
        }
    })
    t.Run("clear preserves parent", func(t *testing.T) {
        s := seedStore(t)
        parent, err := s.CreateCard(NewCard{Title: "父卡", Project: "p", BaseBranch: "parent/base", Actor: "test"})
        if err != nil {
            t.Fatal(err)
        }
        child := mustChild(t, s, parent.ID, "子卡")
        if err := s.SetCardBaseBranch(child.ID, "", "test"); err != nil {
            t.Fatal(err)
        }
        got, _ := s.GetCard(child.ID)
        if got.BaseBranch != "" {
            t.Fatalf("自身覆盖未清除: %q", got.BaseBranch)
        }
        effective, err := s.EffectiveBaseBranch(child.ID)
        if err != nil || effective != "parent/base" {
            t.Fatalf("父链继承错误: %q err=%v", effective, err)
        }
        events, _ := s.EventsFromAsc([]string{child.ID}, 0, 100)
        var payload map[string]any
        found := false
        for _, event := range events {
            if event.Type == EvComment && strings.Contains(string(event.Payload), "更新卡基线") {
                _ = json.Unmarshal(event.Payload, &payload)
                found = true
            }
        }
        value, exists := payload["base_branch"]
        if !found || !exists || value != "" {
            t.Fatalf("清除必须保留存在且为空的键: found=%v exists=%v value=%v", found, exists, value)
        }
        if err := s.SetCardBaseBranch(child.ID, "child/override", "test"); err != nil {
            t.Fatal(err)
        }
        parentAfter, _ := s.GetCard(parent.ID)
        if parentAfter.BaseBranch != "parent/base" {
            t.Fatalf("子卡写入改了父卡: %q", parentAfter.BaseBranch)
        }
    })
    t.Run("missing card", func(t *testing.T) {
        s := seedStore(t)
        if err := s.SetCardBaseBranch("B205-missing", "cards/missing", "test"); !errors.Is(err, ErrNotFound) {
            t.Fatalf("err=%v, want ErrNotFound", err)
        }
    })
    t.Run("first dispatched including review freezes", func(t *testing.T) {
        s := seedStore(t)
        card := mk(t, s, "已派发")
        first := DispatchSnapshot{Template: "feature-impl", Target: "acc", TaskID: "impl-1",
            Branch: "cards/B205-first", Purpose: PurposeImplement, Actor: "test"}
        review := DispatchSnapshot{Template: "review-generic", Target: "acc", TaskID: "review-1",
            Branch: "cards/B205-review", Purpose: PurposeReview, Actor: "test"}
        if err := s.RecordDispatch(card.ID, first); err != nil {
            t.Fatal(err)
        }
        if err := s.RecordDispatch(card.ID, review); err != nil {
            t.Fatal(err)
        }
        events, _ := s.EventsFromAsc([]string{card.ID}, 0, 100)
        var firstEvent Event
        for _, event := range events {
            if event.Type == EvDispatched {
                firstEvent = event
                break
            }
        }
        err := s.SetCardBaseBranch(card.ID, "cards/should-reject", "test")
        if !errors.Is(err, ErrBadState) {
            t.Fatalf("err=%v, want ErrBadState", err)
        }
        for _, want := range []string{"cards/B205-first", firstEvent.CreatedAt.Format(time.RFC3339Nano)} {
            if !strings.Contains(err.Error(), want) {
                t.Fatalf("冻结错误缺少 %q: %v", want, err)
            }
        }
        got, _ := s.GetCard(card.ID)
        if got.BaseBranch != "" {
            t.Fatalf("拒绝路径改了卡: %q", got.BaseBranch)
        }
    })
    t.Run("comment failure rolls back card", func(t *testing.T) {
        s := seedStore(t)
        card := mk(t, s, "事务回滚")
        _, err := s.db.Exec("CREATE TRIGGER fail_base_comment BEFORE INSERT ON card_events WHEN NEW.type = 'comment' BEGIN SELECT RAISE(ABORT, 'forced base comment failure'); END")
        if err != nil {
            t.Fatal(err)
        }
        err = s.SetCardBaseBranch(card.ID, "cards/rollback", "test")
        if err == nil || !strings.Contains(err.Error(), "forced base comment failure") {
            t.Fatalf("comment 失败未透出: %v", err)
        }
        got, _ := s.GetCard(card.ID)
        if got.BaseBranch != "" {
            t.Fatalf("事件失败后基线仍存在: %q", got.BaseBranch)
        }
        events, _ := s.EventsFromAsc([]string{card.ID}, 0, 100)
        for _, event := range events {
            if event.Type == EvComment && strings.Contains(string(event.Payload), "cards/rollback") {
                t.Fatalf("事件失败后仍有 comment: %+v", event)
            }
        }
    })
}
~~~

加入测试后先跑红：

~~~text
go test ./internal/ledger -run TestSetCardBaseBranch -count=1
~~~

预期空壳门面使测试失败；失败必须来自新断言，而不是编译或夹具错误。

断言必须覆盖：不存在卡 ErrNotFound；非空写入和 EffectiveBaseBranch；空串清除与父链；
首条派发分支/时间；PurposeReview 仍冻结；payload 的空值键；comment 失败整体回滚。
这些测试复用既有包内 harness，形态差异按允许的 harness 例外处理，断言不能删减。

### Step 2：最小实现

把 cards.go:534-540 空壳替换为：

~~~go
// SetCardBaseBranch 为尚未出现任何 dispatched 事件的卡设置或清除显式基线。
// id 是卡号，branch 非空为显式分支、空串清除自身覆盖值，actor 是审计主体。
// 首次派发判定、cards 更新和 EvComment 必须在同一个 mutate 事务内完成。
func (s *Store) SetCardBaseBranch(id, branch, actor string) error {
    log().Info("设置卡基线进入", "card", id, "branch", branch, "actor", actor)
    err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
        if _, err := getCardTx(s, tx, id); err != nil {
            log().Warn("设置卡基线失败：卡不存在或读取失败", "card", id, "cause", err)
            return fmt.Errorf("设置卡基线：卡 %s: %w", id, err)
        }
        var raw string
        var createdAt any
        err := tx.QueryRow(s.q(`SELECT payload, created_at FROM card_events
            WHERE card_id = ? AND type = ? ORDER BY seq ASC LIMIT 1`), id, EvDispatched).Scan(&raw, &createdAt)
        switch {
        case err == nil:
            var snapshot DispatchSnapshot
            if decodeErr := json.Unmarshal([]byte(raw), &snapshot); decodeErr != nil {
                log().Error("首条 dispatched payload 损坏", "card", id, "cause", decodeErr)
                return fmt.Errorf("卡 %s 的首次派发快照损坏: %w", id, ErrBadState)
            }
            at := toTime(createdAt).Format(time.RFC3339Nano)
            log().Warn("设置卡基线被拒：基线已冻结", "card", id,
                "first_branch", snapshot.Branch, "first_dispatched_at", at, "actor", actor)
            return fmt.Errorf("卡 %s 已在分支 %q 于 %s 首次派发，基线已冻结: %w",
                id, snapshot.Branch, at, ErrBadState)
        case !errors.Is(err, sql.ErrNoRows):
            log().Error("查询 dispatched 事件失败", "card", id, "cause", err)
            return fmt.Errorf("查询卡 %s 的 dispatched 事件: %w", id, err)
        }
        now := s.timeNow()
        if _, err := tx.Exec(s.q(`UPDATE cards SET base_branch = ?, updated_at = ? WHERE id = ?`),
            branch, s.tval(now), id); err != nil {
            log().Error("写 cards 基线失败", "card", id, "branch", branch, "cause", err)
            return fmt.Errorf("写卡 %s 基线: %w", id, err)
        }
        payload := map[string]any{"kind": "普通", "base_branch": branch,
            "body": fmt.Sprintf("更新卡基线：%q", branch)}
        if _, err := s.appendEventAt(tx, sink, id, EvComment, actor, payload, now); err != nil {
            log().Error("落基线 comment 失败", "card", id, "branch", branch, "cause", err)
            return fmt.Errorf("记录卡 %s 基线变更: %w", id, err)
        }
        log().Info("设置卡基线完成", "card", id, "branch", branch, "actor", actor)
        return nil
    })
    if err != nil {
        log().Warn("设置卡基线未提交", "card", id, "branch", branch, "cause", err)
    }
    return err
}
~~~

### Step 3：跑绿与提交

~~~text
gofmt -w internal/ledger/cards.go internal/ledger/cards_test.go
go test ./internal/ledger -run 'TestSetCardBaseBranch' -count=1
go test ./internal/ledger -run 'Test(SetCardBaseBranch|EffectiveBaseBranch)' -count=1
git diff --check
~~~

预期两条测试均为 ok 且命中新测试。Task 1 提交：feat(ledger): freeze card base branch after dispatch。

## 4. Task 2：CLI card update --base-branch

### Files

- 修改 cmd/card.go、cmd/card_test.go
- 不修改 cmd/ledgercli.go、printCardJSON、card add --base-branch

### Interfaces

Consumes：T1 门面；既有 runLedgerCLI(t, dir, args ...string) (string, string, error)。

Produces：handoff card update <id> --base-branch <branch>；pflag Changed=false 时不调用
T1；显式空串时调用 T1 且传入空串。

### Step 1：基线先跑与失败测试

~~~text
go test ./cmd -run 'TestCard(AddListShowMove|AddChildAndBaseBranch)' -count=1
~~~

已真实通过，输出 ok github.com/Xsxdot/handoff/cmd 0.298s。新增测试必须执行真实 CLI、
临时 SQLite 和 card show 读回，覆盖非空、空串、缺 flag、已派发失败四种状态；已派发
反例从真实 RecordDispatch 取首条 branch 与 CreatedAt，断言错误包含两者且 BaseBranch
未变。加入测试后先跑红，预期缺少 --base-branch flag 或门面调用使 TestCardUpdateBaseBranch
失败；失败必须来自基线功能缺口而不是 harness。

测试形态复用 cmd/ledgercli_test.go 的 runLedgerCLI 与 cmd/card_dispatch.go 的
swapDispatchTransport，不另造数据库入口；逐条 pass/fail 断言为：非空写后 card show
含分支；显式空串后含 `"base_branch":""`；缺少 flag 后仍保持空值且命令成功；真实派发
后更新命令失败、错误同时含首条分支与 CreatedAt、card show 的 BaseBranch 保持不变。

~~~go
func TestCardUpdateBaseBranch(t *testing.T) {
    dir := t.TempDir()
    out, _, err := runLedgerCLI(t, dir, "card", "add", "基线卡", "--project", "demo")
    if err != nil { t.Fatal(err) }
    var created struct{ ID string `json:"id"` }
    if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &created); err != nil { t.Fatal(err) }
    if _, _, err := runLedgerCLI(t, dir, "card", "update", created.ID, "--base-branch", "cards/B205-charter"); err != nil {
        t.Fatal(err)
    }
    show, _, err := runLedgerCLI(t, dir, "card", "show", created.ID)
    if err != nil || !strings.Contains(show, "cards/B205-charter") {
        t.Fatalf("设置后读回: err=%v show=%s", err, show)
    }
    if _, _, err := runLedgerCLI(t, dir, "card", "update", created.ID, "--base-branch", ""); err != nil {
        t.Fatal(err)
    }
    show, _, err = runLedgerCLI(t, dir, "card", "show", created.ID)
    if err != nil || !strings.Contains(show, "\"base_branch\":\"\"") {
        t.Fatalf("清除后读回: err=%v show=%s", err, show)
    }
    if _, _, err := runLedgerCLI(t, dir, "card", "update", created.ID, "--title", "只改标题"); err != nil {
        t.Fatal(err)
    }
    show, _, err = runLedgerCLI(t, dir, "card", "show", created.ID)
    if err != nil || !strings.Contains(show, "\"base_branch\":\"\"") {
        t.Fatalf("缺 flag 不应隐式写基线: err=%v show=%s", err, show)
    }
}
~~~

### Step 2：最小实现

在 card.go 包级变量加入 cardUpdateBase string；在既有 cardUpdateCmd.RunE 中，保留
附件、判据和元信息操作，并加入以下唯一调用：

~~~go
if cmd.Flags().Changed("base-branch") {
    if err := st.SetCardBaseBranch(id, cardUpdateBase, actor); err != nil {
        return err
    }
}
~~~

在既有单一 init 的 flag 注册段加入：

~~~go
cardUpdateCmd.Flags().StringVar(&cardUpdateBase, "base-branch", "", "设/清除显式基线（空串=清除）")
~~~

顺序固定为：附件、摘附件、验收、基线、标题/优先级、读卡输出；不读取公开事件、不
在 CLI 自判冻结。账本门负责结构化日志，CLI 不使用 print。

### Step 3：跑绿与提交

~~~text
gofmt -w cmd/card.go cmd/card_test.go
go test ./cmd -run 'TestCard(UpdateBaseBranch|AddListShowMove|AddChildAndBaseBranch)' -count=1
git diff --check
~~~

预期 ok 且无 no tests to run。Task 2 提交：feat(cli): expose card base branch update。

## 5. Task 3：HTTP PATCH 卡基线

### Files

- 修改 internal/agentd/ledgerapi.go、internal/agentd/ledgerapi_test.go
- 不修改 server.go 路由、writeJSON 和 ledgerErr

### Interfaces

Consumes：现有 title/priority/acceptance_criteria 指针字段与 T1 门面。
Produces：BaseBranch *string；缺键不动，空串清除；成功 200，未知卡 404，冻结 409，
账本 nil 503。

### Step 1：基线先跑与失败测试

~~~text
go test ./internal/agentd -run 'TestPatchCard|TestMigrateAPIRejectsInFlightWith409' -count=1
~~~

已真实通过，输出 ok github.com/Xsxdot/handoff/internal/agentd 1.225s。用既有
newLedgerEnv、seedCard、ledgerPatch（文件 internal/agentd/ledgerapi_test.go）穿真实 HTTP
增加测试；沿用该 harness 而不展开 HTTP server 样板，逐条断言：

1. 非空 base_branch 返回 200，GetCard.BaseBranch 和 EffectiveBaseBranch 都是新分支。
2. {} 或仅 priority 后基线不变。
3. base_branch:"" 返回 200，自身覆盖为空，父卡场景有效基线回父链。
4. 未知卡 404；真实 RecordDispatch 后修改返回 409，正文含首条分支和时间，卡未改。
5. 同时 title 与冻结 base 时按既有顺序返回 409、title 已改、base 未改。
6. ledger nil 返回 503，正文含账本未配置且无 ok:true。

加入测试后运行其新增名称，预期基线因 request struct 没有 BaseBranch 分支而失败；失败
必须来自断言，不得是 HTTP harness、账本初始化或编译错误。

### Step 2：最小实现

将局部 request struct 从现有三字段扩为四字段，并在 acceptance 分支之后加入以下完整
分支；JSON tag 使用现有命名：

~~~go
var body struct {
    Title              *string `json:"title"`
    Priority           *string `json:"priority"`
    AcceptanceCriteria *string `json:"acceptance_criteria"`
    BaseBranch         *string `json:"base_branch"`
}
~~~

~~~go
if body.BaseBranch != nil {
    if err := s.ledger.SetCardBaseBranch(id, *body.BaseBranch, actor); err != nil {
        s.log.Warn("写卡基线失败", "card", id, "branch", *body.BaseBranch, "cause", err)
        ledgerErr(w, err)
        return
    }
    s.log.Info("已写卡基线", "card", id, "branch", *body.BaseBranch, "actor", actor)
}
~~~

保留现有 JSON 解码、GetCard、meta、acceptance 和成功响应；GetCard 失败也改用 ledgerErr
以保证 ErrNotFound 仍为 404。meta/acceptance 失败继续使用现有 400，只有新基线分支
调用 ledgerErr，不能把 ErrBadState 吞成 400。

### Step 3：跑绿与提交

~~~text
gofmt -w internal/agentd/ledgerapi.go internal/agentd/ledgerapi_test.go
go test ./internal/agentd -run 'TestPatchCard(BaseBranch|PartialOrder|WithoutLedger|OmittedFields|UpdatesMeta)' -count=1
git diff --check
~~~

预期所有新增测试命中且为 ok。Task 3 提交：feat(api): patch card base branch with freeze errors。

## 6. Task 4：建树成功后逐卡挂接与跨机裁剪

### Files

- 修改 internal/agentd/projectadmin.go、internal/agentd/forward.go
- 修改 internal/agentd/projectadmin_test.go、internal/agentd/forward_test.go
- 不修改 internal/agentd/manualworktree.go 和 Ticket 0 proto

### Interfaces

Consumes：CreateManualWorktree；本机 s.ledger（nil 合法）；s.ledgerActor；T1 门面。
Produces：私有 func (s *Server) attachCardBaseBranches(ws proto.Workspace, ids []string, actor string) proto.Workspace；
私有 func (s *Server) forwardWorktreeIfRequested(w http.ResponseWriter, r *http.Request) bool；私有
func stripWorktreeCardIDs(raw []byte) ([]byte, error)；私有 func (s *Server) forwardJSON(r *http.Request,
name string, c *client.Client, token string, body []byte) (status int, headers http.Header,
payload []byte, err error)；私有 func copyForwardHeaders(w http.ResponseWriter, headers http.Header)。
前者串行按请求顺序写卡；route-specific 转发只删除转发给目标的 card_ids，目标响应回本地后再写本地卡。

### Step 1：基线先跑与失败测试

~~~text
go test ./internal/agentd -run 'TestProjectWorktreeCreate(OK|RejectsDuplicateBranch)|TestForward(ProjectAddToNamedMachine|PreservesHandoffHeaders|ForwardedRequestNeverForwardsAgain)' -count=1
~~~

已真实通过，输出 ok ... internal/agentd 1.225s。复用 newTestServerWithManager、
registerWorktreeTestProject、initGitRepo、doWorktreeReq，增加：

1. Git 成功后 N 张未派发卡按请求顺序得到 N 个成功结果、卡字段等于 ws.Branch、目录存在。
2. 第一张未派发、第二张真实 RecordDispatch 冻结时，第一项成功、第二项失败、树不删除。
3. ledger nil 带 card_ids 仍 200，JSON 不含 card_results，CardResults 为 nil。
4. Git 失败不含 card_results，任何卡不写基线。
5. machine=devbox 真实 httptest relay：目标请求键集合无 card_ids；目标只建树；协调者本地
   账本按返回分支写卡；目标账本无卡事件；结果与本地读回一致。
6. 目标 400/500 原样透传、不可达 502、context 取消不写本地卡；既有 relay/header/
   防环测试继续通过；目标请求还要保留 mode/branch/base 和额外未知键，只删除 card_ids。

加入测试后先跑红，预期基线没有逐卡挂接和 route-specific 裁剪，至少卡结果、目标请求
键集和跨机本地账本断言失败；失败不得来自 Git fixture 或 relay harness 初始化。

### Step 2：最小实现

projectadmin.go 加入以下完整逐卡 helper，成功/失败各有日志，失败不删除树：

~~~go
func (s *Server) attachCardBaseBranches(ws proto.Workspace, ids []string, actor string) proto.Workspace {
    results := make([]proto.CardBaseBranchResult, 0, len(ids))
    s.log.Info("建树后开始逐卡挂基线", "branch", ws.Branch, "card_count", len(ids), "actor", actor)
    for _, id := range ids {
        result := proto.CardBaseBranchResult{ID: id}
        if err := s.ledger.SetCardBaseBranch(id, ws.Branch, actor); err != nil {
            result.Error = err.Error()
            s.log.Warn("建树后挂卡失败：工作树保留", "card", id, "branch", ws.Branch, "cause", err)
        } else {
            result.OK = true
            s.log.Info("建树后挂卡完成", "card", id, "branch", ws.Branch, "actor", actor)
        }
        results = append(results, result)
    }
    ws.CardResults = results
    s.log.Info("建树后逐卡挂基线完成", "branch", ws.Branch, "card_count", len(results))
    return ws
}
~~~

projectadmin.go 的 handler 整体替换为以下控制流；原有项目查找、错误状态和 Git 错误文案
保持不变，只插入专用转发、卡号计数日志和建树成功后的本地逐卡挂接：

~~~go
func (s *Server) handleProjectWorktreeCreate(w http.ResponseWriter, r *http.Request) {
    if s.forwardWorktreeIfRequested(w, r) {
        return
    }
    if s.forwardIfRequested(w, r) {
        return
    }
    name := r.PathValue("name")
    var req proto.CreateWorktreeReq
    if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
        s.log.Warn("建树请求体解析失败", "name", name, "status", http.StatusBadRequest, "cause", err)
        writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体必须是 JSON {mode, branch, base}"})
        return
    }
    s.log.Info("建树请求", "name", name, "machine", r.URL.Query().Get("machine"),
        "mode", req.Mode, "branch", req.Branch, "base", req.Base, "card_count", len(req.CardIDs))
    loc, err := s.st.GetProjectLocationByName(name)
    if err != nil {
        if errors.Is(err, store.ErrNotFound) {
            s.log.Warn("建树被拒：项目不存在", "name", name, "status", http.StatusNotFound, "cause", err)
            writeJSON(w, http.StatusNotFound, map[string]string{"error": "项目 " + name + " 未登记"})
            return
        }
        s.log.Error("建树失败：查询位置表", "name", name, "cause", err)
        writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
        return
    }
    ws, err := CreateManualWorktree(r.Context(), loc.Path, filepath.Join(s.conf().DataDir, "worktrees"), req)
    if err != nil {
        status := http.StatusInternalServerError
        if errors.Is(err, ErrBadWorktreeReq) {
            status = http.StatusBadRequest
        }
        s.log.Error("建树失败", "name", name, "repo", loc.Path, "mode", req.Mode,
            "branch", req.Branch, "status", status, "cause", err)
        writeJSON(w, status, map[string]string{"error": truncateRunes(err.Error(), 200)})
        return
    }
    if len(req.CardIDs) > 0 && s.ledger != nil {
        ws = s.attachCardBaseBranches(ws, req.CardIDs, s.ledgerActor(r))
    }
    s.log.Info("建树完成", "name", name, "dir", ws.Path, "branch", ws.Branch,
        "card_result_count", len(ws.CardResults))
    writeJSON(w, http.StatusOK, ws)
}
~~~

只有 len(req.CardIDs)>0 且 s.ledger!=nil 才调用 helper；Git 失败在 helper 之前返回且不带结果。

forward.go 增加 bytes、encoding/json、internal/proto 导入，并实现下列三个完整辅助函数。
stripWorktreeCardIDs 用 map[string]json.RawMessage 解码 object、delete card_ids、json.Marshal；
未知键保留。forwardJSON 复用 c.HTTPClient、target.Token、forwardURL、forwardBodyLimit、
原 context，读取目标响应后交给专用 handler 解码 Workspace。目标响应非 2xx 原样透传；2xx
非 Workspace 返回 502；所有分支用结构化日志。

~~~go
func stripWorktreeCardIDs(raw []byte) ([]byte, error) {
    var object map[string]json.RawMessage
    if err := json.Unmarshal(raw, &object); err != nil {
        return nil, err
    }
    delete(object, "card_ids")
    return json.Marshal(object)
}

func copyForwardHeaders(w http.ResponseWriter, headers http.Header) {
    if contentType := headers.Get("Content-Type"); contentType != "" {
        w.Header().Set("Content-Type", contentType)
    }
    for key, values := range headers {
        if !strings.HasPrefix(http.CanonicalHeaderKey(key), "X-Handoff-") {
            continue
        }
        for _, value := range values {
            w.Header().Add(key, value)
        }
    }
}

func (s *Server) forwardJSON(r *http.Request, name string, c *client.Client, token string, body []byte) (status int, headers http.Header, payload []byte, err error) {
    target, err := forwardURL(c.BaseURL(), r.URL)
    if err != nil {
        s.log.Error("建树 JSON 转发失败：目标地址不合法", "machine", name, "cause", err)
        return 0, nil, nil, err
    }
    req, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
    if err != nil {
        s.log.Error("建树 JSON 转发失败：构造请求", "machine", name, "cause", err)
        return 0, nil, nil, err
    }
    if token != "" {
        req.Header.Set("Authorization", "Bearer "+token)
    }
    if contentType := r.Header.Get("Content-Type"); contentType != "" {
        req.Header.Set("Content-Type", contentType)
    }
    req.Header.Set(forwardedHeader, "1")
    resp, err := c.HTTPClient().Do(req)
    if err != nil {
        s.log.Error("建树 JSON 转发失败：上游不可达", "machine", name, "cause", err)
        return 0, nil, nil, err
    }
    defer resp.Body.Close()
    payload, err = io.ReadAll(io.LimitReader(resp.Body, forwardBodyLimit+1))
    if err != nil {
        s.log.Error("建树 JSON 转发失败：读取响应", "machine", name, "status", resp.StatusCode, "cause", err)
        return 0, nil, nil, err
    }
    headers = resp.Header.Clone()
    s.log.Info("建树 JSON 转发完成", "machine", name, "status", resp.StatusCode, "bytes", len(payload))
    return resp.StatusCode, headers, payload, nil
}
~~~

现有 forwardTo 不改流式行为。

专用转发完整控制流如下；实现者须把此代码落在 route-specific 函数中：

~~~go
func (s *Server) forwardWorktreeIfRequested(w http.ResponseWriter, r *http.Request) bool {
    name := r.URL.Query().Get("machine")
    if name == "" || isForwarded(r) {
        return false
    }
    s.log.Info("建树请求开始专用转发", "machine", name, "path", r.URL.Path)
target, ok := s.conf().Targets[name]
if !ok {
    s.log.Warn("建树专用转发被拒：机器未定义", "machine", name)
    writeJSON(w, http.StatusBadRequest, map[string]string{"error": "机器 " + name + " 未定义"})
    return true
}
c, err := s.pool.For(name)
if err != nil {
    s.log.Error("建树专用转发失败：取目标客户端", "machine", name, "cause", err)
    writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发到 " + name + " 失败: " + err.Error()})
    return true
}
raw, err := io.ReadAll(io.LimitReader(r.Body, forwardBodyLimit+1))
if err != nil {
    s.log.Error("建树专用转发失败：读取请求", "machine", name, "cause", err)
    writeJSON(w, http.StatusBadGateway, map[string]string{"error": "读取转发请求失败: " + err.Error()})
    return true
}
var original proto.CreateWorktreeReq
if err := json.Unmarshal(raw, &original); err != nil {
    s.log.Warn("建树专用转发失败：请求 JSON 无法解析", "machine", name, "cause", err)
    writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体必须是 JSON {mode, branch, base}"})
    return true
}
body, stripErr := stripWorktreeCardIDs(raw)
if stripErr != nil {
    s.log.Warn("建树专用转发失败：请求对象无法裁剪", "machine", name, "cause", stripErr)
    writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体必须是 JSON object"})
    return true
}
status, headers, payload, err := s.forwardJSON(r, name, c, target.Token, body)
if err != nil {
    s.log.Error("建树专用转发失败：目标请求", "machine", name, "cause", err)
    writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发到 " + name + " 失败: " + err.Error()})
    return true
}
if status < http.StatusOK || status >= http.StatusMultipleChoices {
    s.log.Warn("建树专用转发原样回送目标错误", "machine", name, "status", status)
    copyForwardHeaders(w, headers)
    w.WriteHeader(status)
    _, _ = w.Write(payload)
    return true
}
var ws proto.Workspace
if err := json.Unmarshal(payload, &ws); err != nil {
    s.log.Error("建树专用转发失败：目标响应不是 Workspace", "machine", name, "status", status, "cause", err)
    writeJSON(w, http.StatusBadGateway, map[string]string{"error": "目标建树响应无法解析: " + err.Error()})
    return true
}
ws.CardResults = nil
if len(original.CardIDs) > 0 && s.ledger != nil {
    ws = s.attachCardBaseBranches(ws, original.CardIDs, s.ledgerActor(r))
}
copyForwardHeaders(w, headers)
writeJSON(w, http.StatusOK, ws)
s.log.Info("建树专用转发完成并在本机挂卡", "machine", name, "branch", ws.Branch,
    "card_result_count", len(ws.CardResults))
return true
}
~~~

实现时不得把目标请求改成原始 raw，也不得在 Git 成功前写卡；转发到目标时必须删除
card_ids，只有接收响应的一侧才允许调用 attachCardBaseBranches。

### Step 3：跑绿与提交

~~~text
gofmt -w internal/agentd/projectadmin.go internal/agentd/forward.go internal/agentd/projectadmin_test.go internal/agentd/forward_test.go
go test ./internal/agentd -run 'TestProjectWorktreeCreate(AttachesCardsAfterGit|PartialCardFailureKeepsTree|LedgerDisabledOmitsResults|GitFailureOmitsResults)|TestForwardWorktree(StripsCardIDsAndAttachesLocally|ErrorAndCancel)|TestForward(ProjectAddToNamedMachine|PreservesHandoffHeaders|ForwardedRequestNeverForwardsAgain)' -count=1
git diff --check
~~~

预期所有目标测试通过；目标 body 无 card_ids，本地结果和真实卡读回一致。Task 4 提交：
feat(agentd): attach card bases after worktree creation。

## 7. Task 5：Web 选择器、结果与基线三态

### Files

- 修改 web/src/app/tree/NewWorktreeDialog.tsx、NewWorktreeDialog.test.tsx
- 修改 web/src/app/cards/CardDrawer.tsx、CardDrawer.test.tsx
- 不修改 web/src/api/types.ts、web/src/api/ledger.ts、web/src/api/client.ts；Ticket 0 已冻结字段和函数签名

### Interfaces

Consumes：fetchCards、fetchCardDetail、createWorktree、patchCard、Workspace、
CardBaseBranchResult、CardDetail。
Produces：非空选择才有 card_ids；结果面板逐项读取 id/ok/error；CardDrawer 自设/继承/
未设置三态；保存分支或空串只调用 patchCard。

### Step 1：基线先跑与失败测试

依赖可安装后运行：

~~~text
cd web
npm ci
npx vitest run src/app/tree/NewWorktreeDialog.test.tsx src/app/cards/CardDrawer.test.tsx
npm run typecheck
~~~

依赖安装后先用同一命令跑基线，必须真实看到两个文件通过且无 no tests to run、typecheck
exit 0；随后在 NewWorktreeDialog.test.tsx 与 CardDrawer.test.tsx 的既有 render harness
中加入下列测试并先跑红，预期现有 UI 缺少 card_ids、结果面板和编辑三态断言。
新增 Web 测试断言：

- fetchCards 按 project 参数读取候选；fetchCardDetail 事件含 dispatched 的候选仍显示但
  checkbox disabled，并说明冻结；这避免新增未冻结的 CardView.dispatched wire 字段。
- 选择两张卡时 createWorktree 精确收到 card_ids，零选择时 Object.keys 不含 card_ids。
- Workspace 混合 card_results 同时显示“工作树已创建”和每项 error；缺结果不显示“全部成功”。
- fetchCards 503 显示账本不可用，分支表仍能创建且请求不带 card_ids。
- CardDrawer 自设、继承、空三态分别显示；保存/清除的 patch payload 精确为分支/空串；
  409 原文可见且旧值不乐观改变。

### Step 2：最小实现

NewWorktreeDialog 新增 CardOption {card: CardView; dispatched: boolean; detailError: string}、
cardOptions、selectedCardIDs、cardLoadError、createdWorkspace 状态。open 时用 alive cleanup
拉 fetchCards(project=encodeURIComponent(projectName))，逐项 fetchCardDetail；detail.events
含 type dispatched 则 disabled。卡列表失败只显示 warning，不阻止分支建树。

submit 先构造现有 mode/branch/base 请求，仅 selectedCardIDs.length>0 时赋值
request.card_ids；成功设置 createdWorkspace，不立即 onCreated/onClose。结果区显示：
card_results 缺席=“没有请求挂接卡”，存在则逐项显示成功或 error；点击“完成”才调用
onCreated(createdWorkspace) 与 onClose，防止结果被父组件立刻卸载。现有分支表 effect 的
alive cleanup 和错误原文必须保留。

CardDrawer 在 detail 派生值后加入：

~~~tsx
const ownBase = value<string>(card, 'base_branch', '')
const effectiveBase = value<string>(detail, 'effective_base_branch', '')
const baseLabel = ownBase !== '' ? '自设 ' + ownBase
  : effectiveBase !== '' ? '继承 ' + effectiveBase
  : '未设置/回落项目主线'
const [baseEditing, setBaseEditing] = useState(false)
const [baseDraft, setBaseDraft] = useState('')
const [baseBusy, setBaseBusy] = useState(false)
const [baseError, setBaseError] = useState('')

const submitBase = async (branch: string) => {
    setBaseBusy(true)
    setBaseError('')
    try {
        await patchCard(id, { base_branch: branch })
        setBaseEditing(false)
        load()
    } catch (err) {
        setBaseError(errorMessage(err))
    } finally {
        setBaseBusy(false)
    }
}
~~~

替换原“建卡时定，不可改”块为可编辑 input、保存、清除、取消按钮；清除调用
submitBase('')，409 保留 detail 旧值并显示 baseError。组件注释说明“effective 值不能
冒充自身覆盖”的原因；不在前端实现冻结判断。

### Step 3：跑绿与提交

~~~text
cd web
npx vitest run src/app/tree/NewWorktreeDialog.test.tsx src/app/cards/CardDrawer.test.tsx
npm run typecheck
npm run build
~~~

预期目标测试、typecheck、build 全部 exit 0。Task 5 提交：feat(web): choose cards and edit base branch states。

## 8. 缺陷族对抗审查

| 缺陷族 | 设问与结论 |
| --- | --- |
| 生命周期/状态机中断 | T1 trigger 证明 cards/comment 一起回滚；T4 固定 Git 成功→逐卡写账且失败保留树；T5 alive cleanup、确认后回调；跨机重启窗口只进真机清单 |
| 静默失败/误导报错 | T1 错误带首条分支/时间；T3 404/409/503 分流；T4 每卡 ok/error、Git 失败无结果；T5 warning、混合结果和 409 原文可见 |
| 跨平台假设 | SQL 走 q/tval/mutate，Git 复用 2 分钟实现；Windows、直连 target、WKWebView 不从 Linux fixture 推断 |
| 假红/假绿 | T1 真实 RecordDispatch+事件回读；T3 httptest→ledger；T4 真实 Git+HTTP+账本；T5 API 参数和 DOM 双断言；移除门禁或原样转发时反例变红 |
| 门禁绕过 | T2/T3/T4 只能调用 T1；T5 只禁用展示，手造已派发卡仍由 T1 失败 |
| 序列化边界 | §9 逐处列出 card_ids、card_results、base_branch、EvComment 的穿线测试，区分缺席和空值 |
| 枚举漂移 | 不新增状态、事件、purpose、kind；复用 EvDispatched、EvComment、ErrBadState |
| 承重安全属性 | T1 首次冻结，T4 目标无 card_ids 且本地事件唯一；并发/relay 进 §10 |

## 9. 序列化边界审计

1. NewWorktreeDialog 将可选 card_ids 交给 createWorktree；T5 断言缺席/非空两态。
2. client.ts createWorktree 把请求交给 postJSON；T5 spy 断言参数。
3. projectadmin.go Decoder 读取 CreateWorktreeReq、Workspace 写回；T4 真实 HTTP 穿线。
4. forward.go route-specific helper 删除且仅删除 card_ids；T4 目标 handler 断言键不存在。
5. 目标 Workspace 回本机后由 attachCardBaseBranches 投影 card_results；T4 断言本地读回。
6. types.ts 消费 card_results；T5 断言缺席/混合项，不以 TypeScript 编译代替运行时。
7. ledgerapi.go 的 BaseBranch *string 区分 JSON 键缺席和空串；T3 三种 HTTP body 分别读回。
8. cards.go 的 EvComment payload 保留 base_branch:""；T1 解 RawMessage 断言键存在且为空。

## 10. 边界型真机清单

以下行为由协调者执行，不派发、不调用 handoff 派发 CLI：

1. 真实账本建带 spec/contract 附件的卡，设置当前工作树分支为基线，派发 plan/实现节点，
   确认执行者工作树真实存在附件路径；不能只看 prompt 路径字符串。
2. 真实 relay machine 建树带卡号，确认目标只建树、无 card_ids、协调者侧基线等于目标分支；
   直连 target 当前无可达环境则如实记未覆盖。
3. 人工构造一张已派发卡与一张未派发卡，确认树保留、响应有逐项成功/失败。
4. 同卡双请求/多卡并发，确认只有一个首次写入成功；分别观察 SQLite 单连接和 PG advisory lock。
5. ledger.enabled=false 下无卡/带卡请求都建树，响应省略 card_results，页面显示账本不可用。
6. Windows/非 Unix、WKWebView、Git 成功后 agentd 故障注入本期不做，不从 Linux fixture 推断。

## 11. 用户故事归属

| 故事 | 归属 |
| --- | --- |
| Web 新建工作树勾选卡 | T4 + T5 + 真机清单 1 |
| Claude/其它 harness 建的树可挂基线 | T2 + T3 + T5 抽屉 |
| 派发从正确分支切出并读到附件 | T1–T4 链路 + 真机清单 1；不做自动化端到端 |
| 首次派发后明确拒绝 | T1、T2、T3、T5 |
| 抽屉显示自设/继承/空 | T1 + T5 |
| 建树成功但逐卡失败可见且不回滚 | T4 + T5 + 真机清单 3 |

依赖顺序：Ticket 0 → T1 → T2/T3/T4 并行 → T5 → 协调者真机。全量测试不归属任何
单个 task。

## 12. 收尾门

所有 task 定向测试通过后，由协调者执行：

~~~text
go test ./...
go build ./...
go vet ./...
gofmt -l .
git diff --check
cd web && npm run typecheck && npm test && npm run build
go run . graph validate --repo . --stale
go run . graph check --repo . --view cards-B205-charter
go run . graph resolve --repo . --view cards-B205-charter --doc docs/superpowers/specs/2026-08-23-b205-contract.md
~~~

必须有实际输出才能判 pass；预期 graph validate 的 issues 为 null、graph check 的 fails
为空、contract 锚点为 ok 或已说明的既有 moved。最终 commit 需包含 T1–T5 代码和测试，
不包含未授权的 wire/状态/端点扩张。

## 13. 自审扫描声明

本计划没有未决标记、跨任务占位引用、空泛的“补充错误处理”或未给 pass/fail 断言的生产改动。
测试部分复用的既有 harness 为 seedStore/mk/mustChild、runLedgerCLI、newLedgerEnv/
ledgerPatch、newTestServerWithManager/doWorktreeReq 和现有 Web render harness；每处已
逐条列出断言。Task 5 的现有表单只表示插入位置，implement 时必须保留完整 JSX，不得把
插入位置说明留进生产代码。

本节点的产物是本计划文档，不是实现代码。
