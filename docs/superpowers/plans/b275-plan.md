# B275：IM 会话面、看板映射与卡抽屉整合实现计划

## 0. 边界与权威输入

- 当前分支是 cards/B275-charter，基线已按用户指定对齐到 origin/acc/b156.2-156.3 的 ac4a3cad；不假定 main。
- 规格权威是 docs/superpowers/specs/2026-08-28-b275-im-console-design.md。
- 形态权威是 prototypes/b275-frontend-proto/：实现者先读 README.md，再以 index.html 的 IM 段、pages/board.html 和 pages/flows.html 对照实现；规格不重新转述样式。
- 本卡只实现前端消费面、unread 投影和 attach 投影；不改变 B156.2 五个房间端点、kind 白名单、read_only、Live、消息排序或已读游标语义。
- 本节点的产出只有本计划文档；这里的代码块给 implement 节点使用，不在本节点落实现代码。

## 1. 基线门禁与源码事实

### 1.1 已在修正基线跑过的门禁

以下结果已逐行记入 docs/superpowers/ledgers/2026-08-28-b275-spec-ledger.md。每个 implement task 仍须在修改前重跑自己的最小命令：

| 范围 | 命令 | 已跑结果 |
|---|---|---|
| Go 房间/协议 | go test ./internal/proto ./internal/collab | 退出 0；ok github.com/Xsxdot/handoff/internal/proto 0.006s；ok github.com/Xsxdot/handoff/internal/collab 4.148s |
| Go gateway | go test ./internal/agentd | 退出 0；ok github.com/Xsxdot/handoff/internal/agentd 142.972s |
| Web tests | cd web && npm test -- --run | 退出 0；Test Files 113 passed (113)，Tests 1139 passed (1139)；仅有既有 jsdom HTMLCanvasElement getContext 提示 |
| Web typecheck | cd web && npm run typecheck | 退出 0 |
| Web build | cd web && npm run build | 退出 0；Vite 6.4.3，1971 modules transformed；仅有既有 chunk 大小提示 |
| Web lint | cd web && npm run lint | 退出 1；唯一 error 是既有 web/src/app/flows/NodeEditor.test.tsx:50:9 prefer-const，另有 18 条 warning |

实现后的最终 lint 必须退出 0；NodeEditor.test.tsx 的既有 view 变量改成 const，并确认没有新增 error。

### 1.2 代码图和源码查证

- 已执行 go run . graph context d_web --repo . --with-source --max-tokens 18000 --source-span 30：truncated=false，未报告 fociTruncated；warning 是基线仍有 6 个未扫描入口和缺少 codegraph/domains/d_web.json。actual 显示 d_web 容器分散在 d_web_admin、d_web_cards、d_web_command、d_web_contract、d_web_shell、d_web_workbench；以下文件清单因此也包含图外源码查证债。
- Service.ListRooms(project string) ([]proto.RoomSummary, error) 在 internal/collab/service.go:250；Service.Unread(member, roomID string) (int, error) 在 internal/collab/service.go:344。
- handleRoomsList(w http.ResponseWriter, r *http.Request) 在 internal/agentd/roomsapi.go:47；roomUserActor(r) 是服务端注入的已读成员，不来自请求体。
- RoomSummary 当前仅有 id/kind/project/title/bound_session/live/read_only/last_activity；unread 和 attach 均缺失。
- Card.DriverSession 是 opaque driver identity，现有值形如 cli:new@h；它不是 task id，不能由它拼出 attach 命令。
- ledger.Store.TasksOf(cardID string) ([]ledger.TaskLink, error) 在 internal/ledger/tasks.go:37，按 created_at 升序；本机任务由 store.Store.GetTask(id string) (*proto.Task, error) 读取；远端任务由 client.Client.Attach(ctx context.Context, taskID string) (*client.AttachInfo, error) 读取。
- proto.Task.Workdir() string 在 internal/proto/proto.go:335，非空 WorkDir 优先，否则回退 RepoPath。
- 当前 CLI 真语法是 handoff attach [task]，见 cmd/attach.go；原型里的 handoff attach --session 是占位文本，不能实现成不存在的参数。
- web/src/api/types.ts:733-750 已有 CreatePtySessionReq.init_command?；web/src/app/workbench/TerminalTab.tsx:74-78,217 已将 initCommand 透传为 init_command。因此本卡不是新增 PTY wire，只把命令保存进 TabContent 并由 Shell 接线。
- nextTerminalSeq(wb Workbench) number 在 web/src/app/workbench/tabs.ts:361 已导出；WorkbenchApi.open(c: TabContent, b?: BaseDir, group?: number) 是打开 attach terminal 的既有入口。
- runCardStep(id: string, step: string) 在 web/src/api/ledger.ts:250，卡抽屉不新建 coordinator endpoint。

## 2. 冻结的增量接口

### 2.1 RoomSummary unread 与 attach

internal/proto/rooms.go 增加以下类型和字段。Unread 使用非指针 int，故 JSON 中 0 必须存在；Attach 使用指针，缺失和有值必须可区分：

~~~go
// RoomAttach 是房间详情可执行的任务 attach 投影。
// Target 为空表示当前 agentd；WorkDir 是任务 Workdir() 的结果；Command 是
// 当前 CLI 能执行的完整命令。它不把 BoundSession 当作 task/session。
type RoomAttach struct {
    Target  string `json:"target,omitempty"`
    TaskID  string `json:"task_id"`
    WorkDir string `json:"work_dir"`
    Command string `json:"command"`
}

type RoomSummary struct {
    ID           string      `json:"id"`
    Kind         string      `json:"kind"`
    Project      string      `json:"project,omitempty"`
    Title        string      `json:"title"`
    BoundSession string      `json:"bound_session,omitempty"`
    Live         bool        `json:"live"`
    ReadOnly     bool        `json:"read_only"`
    LastActivity time.Time   `json:"last_activity"`
    Unread       int         `json:"unread"`
    Attach       *RoomAttach `json:"attach,omitempty"`
}
~~~

保留存量 ListRooms，并增加 member-aware 装配点。现有 ListRooms 函数体整体抽成 listRooms，既有读卡、读事件、Live、终态沉底和排序不变：

~~~go
func (s *Service) ListRooms(project string) ([]proto.RoomSummary, error) {
    return s.listRooms(project, "")
}

func (s *Service) ListRoomsForMember(project, member string) ([]proto.RoomSummary, error) {
    return s.listRooms(project, member)
}
~~~

将当前 ListRooms 的完整函数体改名为 listRooms，并在其现有排序代码前插入下面的完整新增片段；该函数体中读卡、读事件、Live、终态沉底和排序代码逐行保留：

~~~go
if member != "" {
    for i := range rooms {
        unread, err := s.Unread(member, rooms[i].ID)
        if err != nil {
            log().Warn("会话列表组装失败：读未读数",
                "project", project, "member", member, "room", rooms[i].ID, "cause", err)
            return nil, err
        }
        rooms[i].Unread = unread
    }
}
~~~

internal/agentd/roomsapi.go 的 list handler 使用真实成员并在成功列表后补 attach：

~~~go
func (s *Server) handleRoomsList(w http.ResponseWriter, r *http.Request) {
    project := r.URL.Query().Get("project")
    member := s.roomUserActor(r)
    rooms, err := s.rooms.ListRoomsForMember(project, member)
    if err != nil {
        s.log.Warn("会话列表读取失败", "project", project, "member", member, "cause", err)
        writeErr(w, http.StatusInternalServerError, err)
        return
    }
	s.enrichRoomAttachments(r.Context(), rooms)
	writeJSON(w, http.StatusOK, map[string]any{"rooms": rooms})
}
~~~

逐房间 attach 查找失败不能让主列表变 500；它必须 Warn、留下 Attach=nil，让 UI 显示禁用原因。该 helper 不返回 error，因为其每个 lookup 错误都已被上下文日志和 nil 投影承接：

~~~go
// enrichRoomAttachments 给 card 房间补最新可解析的挂账 task；群房间没有 task，
// 保持 Attach=nil。失败不吞：逐项 Warn，UI 得到禁用态；列表主查询仍可用。
func (s *Server) enrichRoomAttachments(ctx context.Context, rooms []proto.RoomSummary) {
    for i := range rooms {
        if rooms[i].Kind != room.KindCard || s.ledger == nil {
            continue
        }
        links, err := s.ledger.TasksOf(rooms[i].ID)
        if err != nil {
            s.log.Warn("读取房间挂账失败", "room", rooms[i].ID, "cause", err)
            continue
        }
        for linkIndex := len(links) - 1; linkIndex >= 0; linkIndex-- {
            link := links[linkIndex]
            attach, lookupErr := s.lookupRoomAttach(ctx, link)
            if lookupErr != nil {
                s.log.Warn("房间 attach 目标不可解析", "room", rooms[i].ID,
                    "target", link.Target, "task", link.TaskID, "cause", lookupErr)
                continue
            }
            rooms[i].Attach = attach
            break
        }
    }
}

// lookupRoomAttach 只接受真实任务详情和 Workdir()；不从 bound_session 猜测 task。
func (s *Server) lookupRoomAttach(ctx context.Context, link ledger.TaskLink) (*proto.RoomAttach, error) {
    var workDir string
    if link.Target == "" {
        task, err := s.st.GetTask(link.TaskID)
        if err != nil {
            return nil, fmt.Errorf("读取本机任务 %s: %w", link.TaskID, err)
        }
        workDir = task.Workdir()
    } else {
        peer, err := s.pool.For(link.Target)
        if err != nil {
            return nil, fmt.Errorf("获取 target %s 客户端: %w", link.Target, err)
        }
        info, err := peer.Attach(ctx, link.TaskID)
        if err != nil {
            return nil, fmt.Errorf("读取远端任务 %s: %w", link.TaskID, err)
        }
        workDir = info.Task.Workdir()
    }
    if strings.TrimSpace(workDir) == "" {
        return nil, errors.New("任务没有可用工作目录")
    }
    return &proto.RoomAttach{
        Target: link.Target, TaskID: link.TaskID, WorkDir: workDir,
        Command: "handoff attach " + link.TaskID,
    }, nil
}
~~~

实现者补齐当前 roomsapi.go 的 context、fmt 和 collab/room import，删除重复 import；不改 Server 字段。最新可解析挂账 task 是精确选择规则，避免悬空 link 阻断可用 link。

web/src/api/rooms.ts 镜像必须逐字匹配：

~~~ts
export interface RoomAttach {
  target?: string
  task_id: string
  work_dir: string
  command: string
}

export interface RoomSummary {
  id: string
  kind: 'card' | 'project' | 'global'
  project?: string
  title: string
  bound_session?: string
  live: boolean
  read_only: boolean
  last_activity: string
  unread: number
  attach?: RoomAttach
}
~~~

列表 preview 不增加第三条 wire：RoomPanel 用既有 fetchRoomMessages(id, { limit: 1 }) 在内存建立 preview；单项失败保留旧值、结构化 Warn、显示暂无预览。

### 2.2 Workflow BoardLayout wire

列名和顺序可配，所以布局属于 WorkflowDef，不属于 NodeDef。internal/proto/ledger.go 增加：

~~~go
// BoardLayout 是工作流状态到看板列的持久化投影。
// Columns 必须恰有五个非空唯一值；映射值和 Fallback 必须在 Columns 中。
type BoardLayout struct {
    Columns       []string          `json:"columns"`
    StateToColumn map[string]string `json:"state_to_column"`
    Fallback      string            `json:"fallback"`
}

type FlowDetail struct {
    Name    string       `json:"name"`
    Version int          `json:"version"`
    Nodes   []NodeDef    `json:"nodes"`
    States  []string     `json:"states"`
    Board   *BoardLayout `json:"board,omitempty"`
}
~~~

internal/ledger/types.go 的 WorkflowDef 增加 Board *proto.BoardLayout `json:"board,omitempty"`。internal/ledger/workflows.go 增加：

~~~go
var defaultBoardColumns = []string{"代办", "沟通中", "进行中", "审核中", "结束"}

func DefaultBoardLayout(states []string) proto.BoardLayout {
    mapping := map[string]string{
        "待办": "代办", "已出spec": "沟通中", "已出 spec": "沟通中",
        "进行中": "进行中", "待审阅": "审核中", "待合并": "审核中",
        "已完成": "结束", "终止": "结束",
    }
    for _, state := range states {
        if _, ok := mapping[state]; !ok {
            mapping[state] = "进行中"
        }
    }
    return proto.BoardLayout{
        Columns: append([]string(nil), defaultBoardColumns...),
        StateToColumn: mapping, Fallback: "进行中",
    }
}

func validateBoardLayout(layout *proto.BoardLayout) error {
    if layout == nil {
        return nil
    }
    if len(layout.Columns) != 5 {
        return fmt.Errorf("看板列必须恰好五列: %w", ErrBadState)
    }
    seen := make(map[string]bool, len(layout.Columns))
    for _, column := range layout.Columns {
        if strings.TrimSpace(column) == "" || seen[column] {
            return fmt.Errorf("看板列名必须非空且唯一: %q: %w", column, ErrBadState)
        }
        seen[column] = true
    }
    if !seen[layout.Fallback] {
        return fmt.Errorf("看板兜底列 %q 不在列序中: %w", layout.Fallback, ErrBadState)
    }
    for state, column := range layout.StateToColumn {
        if !seen[column] {
            return fmt.Errorf("状态 %q 映射到不存在的看板列 %q: %w", state, column, ErrBadState)
        }
    }
    return nil
}
~~~

PutWorkflow 在 validateNodes 前调用 validateBoardLayout。老版本 Board=nil 不改库；handleFlows 和 handleFlowGet 在 JSON 响应层用 DefaultBoardLayout 补默认 board；handleFlowPut 接收 board 指针并交给 PutWorkflow。handleFlowPut 请求体的唯一形状：

~~~go
var body struct {
    Nodes []ledger.NodeDef  `json:"nodes"`
    Board *proto.BoardLayout `json:"board,omitempty"`
}
version, err := s.ledger.PutWorkflow(name, ledger.WorkflowDef{
    Nodes: body.Nodes, Board: body.Board,
})
~~~

保留现有 JSON 解码、nodes 为空 400、ErrBadState 400、版本化写入、成功响应和错误日志；新增日志字段 board_columns，nil 记 0。

web/src/api/ledger.ts 精确增量：

~~~ts
export interface BoardLayout {
  columns: string[]
  state_to_column: Record<string, string>
  fallback: string
}

export interface FlowDetail {
  name: string
  version: number
  nodes: NodeDef[]
  states: string[]
  board?: BoardLayout
}

export interface WorkflowWire {
  name: string
  version: number
  def: { states: string[]; gates?: Record<string, unknown>; nodes?: NodeDef[]; board?: BoardLayout }
}

export const putFlow = (name: string, nodes: NodeDef[], board?: BoardLayout) =>
  putJSON<{ name: string; version: number }>(
    "/api/flows/" + encodeURIComponent(name),
    board === undefined ? { nodes } : { nodes, board },
  )
~~~

## 3. 实现 DAG

所有 task 文件集有界。全量 go test ./... 和 Web 全量门禁只在 Task 5 运行，不归属于单个实现 task。

### Task 1：房间 unread/attach 投影和双侧金样本

文件集：

- internal/proto/rooms.go
- internal/proto/rooms_fixture_test.go
- internal/collab/service.go
- internal/collab/readmodel_test.go
- internal/agentd/roomsapi.go
- internal/agentd/roomsapi_test.go
- web/src/api/rooms.ts
- web/src/api/rooms.test.ts
- web/src/api/rooms.fetch.test.ts
- web/src/api/testdata/RoomsFixture.json

Interfaces：

- Consumes Service.Unread(member: string, roomID: string) (int, error)、Store.TasksOf(cardID: string) ([]ledger.TaskLink, error)、Store.GetTask(id: string) (*proto.Task, error)、Client.Attach(ctx: context.Context, taskID: string) (*client.AttachInfo, error)。
- Produces Service.ListRoomsForMember(project: string, member: string) ([]proto.RoomSummary, error)、RoomSummary.Unread int、RoomSummary.Attach *RoomAttach、fetchRooms(project?: string): Promise<RoomSummary[]>。

步骤：

1. 修改前重跑 go test ./internal/proto ./internal/collab ./internal/agentd 和 cd web && npm test -- --run src/api/rooms.test.ts src/api/rooms.fetch.test.ts，并把原始输出追加台账。
2. 先写并跑红测。Go 金样本必须用真实 json.Marshal：RoomSummary{Unread:0, Attach:&RoomAttach{Target:"devbox",TaskID:"T1",WorkDir:"/w/B1",Command:"handoff attach T1"}} 断言 unread 键为 float64(0)、attach 四字段逐字相等；另 Marshal 一个无 Attach 的 global，断言没有 attach 键。TS RoomsFixture.json 添加同一 room-summary，rooms.test.ts 断言 fetchRooms 保留 unread 0、有 attach 四字段，缺失 attach 解码为 undefined。
3. 通过 GET /api/rooms 缝测试：复用 newRoomsEnv 和真实 SQLite，发两条 room message 后断言 card room unread==2；POST /read 到第二条，再 GET 断言 unread==0 且键仍存在。project/global 行也断言 unread 键存在。
4. attach 测试用 env.st.CreateTask 创建 ID T1、RepoPath /repo、WorkDir /work/B1，用 env.ledger.LinkTask(card.ID, "", "T1", ledger.PurposeImplement, "test") 挂账；GET /api/rooms 断言 attach.target 缺席、task_id T1、work_dir /work/B1、command handoff attach T1。无挂账卡断言 JSON 不含 attach；远端 target 查找失败只 Warn、列表仍 200、该行 Attach 缺失。
5. 最小实现后跑 go test ./internal/proto ./internal/collab ./internal/agentd 和两支 Web API 测试；记录原始输出。成功路径日志带 project/member/room/unread 或 room/target/task/workdir；错误带 cause，禁用 print。
6. 新类型、导出方法和 helper 写职责/边界/注意事项注释，特别说明 bound_session 不可解析为 task、Attach=nil 是明确禁用态。

行为验收：unread 0 在线；attach 缺失不出键；真实挂账 task 给出 Workdir 和 handoff attach task 命令；HTTP 调用链穿过 handleRoomsList、ListRoomsForMember、Unread。

### Task 2：五列 BoardLayout、看板逻辑和 flows 配置

文件集：

- internal/proto/ledger.go
- internal/proto/contract_fixture_test.go
- internal/ledger/types.go
- internal/ledger/workflows.go
- internal/ledger/workflows_test.go
- internal/agentd/ledgerapi.go
- internal/agentd/ledgerapi_test.go
- web/src/api/ledger.ts
- web/src/api/ledger.test.ts
- web/src/api/contract.test.ts
- web/src/api/testdata/FlowDetail.json
- web/src/app/cards/columns.ts
- web/src/app/cards/columns.test.ts
- web/src/app/cards/CardsPage.tsx
- web/src/app/cards/CardsPage.test.tsx
- web/src/app/cards/CardDrawer.tsx
- web/src/app/flows/FlowsPage.tsx
- web/src/app/flows/FlowsPage.test.tsx
- web/src/app/flows/NodeEditor.test.tsx

Interfaces：

- Consumes Store.PutWorkflow(name: string, def: ledger.WorkflowDef) (int, error)、GET/PUT /api/flows/{name}、fetchFlow(name: string): Promise<FlowDetail>。
- Produces proto.BoardLayout{Columns []string, StateToColumn map[string]string, Fallback string}；JSON {columns:string[],state_to_column:Record<string,string>,fallback:string}；putFlow(name: string, nodes: NodeDef[], board?: BoardLayout)。

步骤：

1. 修改前重跑 go test ./internal/ledger ./internal/agentd 和 cd web && npm test -- --run src/app/cards/columns.test.ts src/api/ledger.test.ts src/app/flows/FlowsPage.test.tsx，记录原始输出。
2. 先跑红测：PUT 自定义五列后 GET，逐字段断言 columns、state_to_column、fallback；4 列、重复列、fallback 不在 columns、映射值不在 columns 各断言 400。columns seam 先断言默认 [代办,沟通中,进行中,审核中,结束]、待办→代办、终止→结束、未知态→进行中。
3. 加入 BoardLayout、DefaultBoardLayout、validateBoardLayout；PutWorkflow 先校验 board。handleFlows/handleFlowGet 给旧 board=nil 做默认响应投影，handleFlowPut 保存 board。更新 Go FlowDetail fixture 和 TS FlowDetail.json/contract test，老 fixture 无 board 时由前端 default helper 接住。
4. web/src/app/cards/columns.ts 同位替换的完整纯逻辑：

~~~ts
import type { CardView } from '../../api/ledger'

export interface BoardLayout {
  columns: string[]
  state_to_column: Record<string, string>
  fallback: string
}

export const DEFAULT_BOARD_COLUMNS = ['代办', '沟通中', '进行中', '审核中', '结束']

export function defaultBoardLayout(states: string[]): BoardLayout {
  const state_to_column: Record<string, string> = {
    待办: '代办', 已出spec: '沟通中', '已出 spec': '沟通中',
    进行中: '进行中', 待审阅: '审核中', 待合并: '审核中',
    已完成: '结束', 终止: '结束',
  }
  for (const state of states) if (!(state in state_to_column)) state_to_column[state] = '进行中'
  return { columns: [...DEFAULT_BOARD_COLUMNS], state_to_column, fallback: '进行中' }
}

export function normalizeBoardLayout(layout: BoardLayout | undefined, states: string[]): BoardLayout {
  const fallback = layout ?? defaultBoardLayout(states)
  if (fallback.columns.length !== 5 || new Set(fallback.columns).size !== 5 || fallback.columns.some((column) => column.trim() === '')) {
    return defaultBoardLayout(states)
  }
  const columns = [...fallback.columns]
  const state_to_column = { ...fallback.state_to_column }
  const safeFallback = columns.includes(fallback.fallback) ? fallback.fallback : columns[0]
  for (const state of states) {
    if (!state_to_column[state] || !columns.includes(state_to_column[state])) state_to_column[state] = safeFallback
  }
  return { columns, state_to_column, fallback: safeFallback }
}

export function boardColumnFor(status: string, layout: BoardLayout): string {
  const mapped = layout.state_to_column[status]
  return mapped && layout.columns.includes(mapped) ? mapped : layout.fallback
}

export function boardColumns(states: string[], layout?: BoardLayout): string[] {
  return normalizeBoardLayout(layout, states).columns
}

export function cardsInColumn(cards: CardView[], column: string, layout?: BoardLayout): CardView[] {
  const resolved = normalizeBoardLayout(layout, cards.map((card) => card.status))
  return cards.filter((card) => boardColumnFor(card.status, resolved) === column && !card.following)
}

export function visibleColumns(columns: string[], cards: CardView[], collapseEmpty: boolean, layout?: BoardLayout): string[] {
  if (!collapseEmpty) return columns
  return columns.filter((column) => cardsInColumn(cards, column, layout).length > 0)
}
~~~

保留 needsAttention、filterNeeds、mergeStateOrder。CardsPage 选中单个 workflow 时使用该 workflow 的 board，缺失用 default；全部 workflow 使用 default 五列和合并状态列表，不能把不同 workflow 的自定义列无规则拼接。CardDrawer 增加 boardLayout?: BoardLayout，用 boardColumns 画五个投影列并用 boardColumnFor 高亮，不改变 moveCard 使用的真实 status。
5. FlowsPage 的 WorkflowCard 状态增加 board，编辑初值 detail.board ?? defaultBoardLayout(detail.states)，保存调用 putFlow(workflow.name, nodes, board)。新增“看板列映射”表：列名输入按中文/英文逗号或顿号拆成五项，提供列上移/下移、每个状态的列 select 和 fallback select；重复/空/不足五项给可见错误，后端仍做 400 闸门。
6. 测试从 HTTP、CardsPage、FlowsPage 入口断言 board 持久化和映射；不要只测 helper。日志带 workflow name/version/board_columns；保存失败显示 errorMessage 原文。修正 NodeEditor.test.tsx 的 prefer-const。新类型和 normalize 的为什么写注释。

行为验收：老 workflow 可读且响应带默认五列；自定义列序刷新后保持；非法布局 400；未知状态可见并进入 fallback；看板永远五列。

### Task 3：单组件三态 RoomPanel 与 attach 确认流

文件集：

- web/src/app/rooms/RoomPanel.tsx
- web/src/app/rooms/roomPanelModel.ts
- web/src/app/rooms/roomLog.ts
- web/src/app/rooms/RoomPanel.test.tsx
- web/src/app/rooms/pollInterval.test.tsx
- web/src/api/rooms.ts
- web/src/app/workbench/tabs.ts
- web/src/app/shell/Shell.tsx（仅 terminal initCommand 渲染接线）

Interfaces：

- Consumes fetchRooms(): Promise<RoomSummary[]>、fetchInbox(): Promise<InboxItem[]>、fetchRoomMessages(id: string, opts?: { before?: number; limit?: number }): Promise<RoomHistoryItem[]>、sendRoomMessage(id: string, body: string): Promise<{seq: number}>、markRoomRead(id: string, uptoSeq: number): Promise<{ok: boolean}>、WorkbenchApi.open(c: TabContent, b?: BaseDir, group?: number): void。
- Produces RoomPanel({ workbench: WorkbenchApi, persistent: boolean }): JSX.Element；TabContent terminal 增加 initCommand?: string。

步骤：

1. 修改前重跑 cd web && npm test -- --run src/app/rooms/RoomsListPage.test.tsx src/app/rooms/RoomDetailPage.test.tsx src/app/rooms/InboxPage.test.tsx src/app/rooms/pollInterval.test.tsx，并记录真实输出。旧页测试仅用于确认基线行为，路由测试在 Task 4 删除。
2. 新增 roomLog.ts，所有入口/请求/错误统一用结构化字段，不记录正文：

~~~ts
export type RoomLogLevel = 'debug' | 'warn' | 'error'

// RoomPanel 的入口、外部请求和错误分支统一走此结构化日志；不记录消息正文。
export function logRoom(level: RoomLogLevel, event: string, fields: Record<string, unknown> = {}): void {
  const payload = { subsystem: 'rooms', event, ...fields }
  if (level === 'error') console.error(payload)
  else if (level === 'warn') console.warn(payload)
  else console.debug(payload)
}
~~~

新文件头写职责与边界；字段只记录 room id、view、request、error，避免日志泄露。
3. 新增 roomPanelModel.ts。该文件不发请求、不操作 workbench，完整内容：

~~~ts
import type { BaseDir } from '../workbench/useWorkbench'
import type { RoomHistoryItem, RoomMessage, RoomSummary } from '../../api/rooms'

export type RoomPanelView = 'list' | 'room' | 'detail'

export function messageBody(event: RoomHistoryItem | undefined): string {
  const payload = (event?.payload ?? {}) as Partial<RoomMessage>
  return typeof payload.body === 'string' ? payload.body : ''
}

export function roomPreview(events: RoomHistoryItem[]): string {
  return messageBody(events[events.length - 1]) || '暂无预览'
}

export function roomNeedsReply(room: RoomSummary, needRoomIDs: ReadonlySet<string>): boolean {
  return room.kind === 'card' && needRoomIDs.has(room.id)
}

export function visibleRooms(
  rooms: RoomSummary[],
  project: string,
  needsOnly: boolean,
  needRoomIDs: ReadonlySet<string>,
): RoomSummary[] {
  return rooms.filter((room) => {
    const projectMatch = project === '' || room.kind === 'global' || room.project === project
    return projectMatch && (!needsOnly || roomNeedsReply(room, needRoomIDs))
  })
}

export function orderRooms(rooms: RoomSummary[], needRoomIDs: ReadonlySet<string>): RoomSummary[] {
  return [...rooms].sort(
    (left, right) => Number(roomNeedsReply(right, needRoomIDs)) - Number(roomNeedsReply(left, needRoomIDs)),
  )
}

export function roomInitials(room: RoomSummary): string {
  if (room.kind === 'global') return '全'
  if (room.kind === 'project') return room.project?.slice(0, 2) || '项'
  return room.id.slice(0, 3)
}

export function attachBase(room: RoomSummary): BaseDir | null {
  if (!room.attach) return null
  const { target, work_dir: path } = room.attach
  const label = path.split('/').filter(Boolean).pop() || path
  return {
    key: 'room-attach:' + (target ?? '') + ':' + path,
    kind: 'workspace',
    path,
    label,
    projectName: room.project ?? '',
    machine: target ?? '',
  }
}
~~~

4. 先写并跑 RoomPanel 缝级红测，入口必须是 RoomPanel，不可只测 model：

~~~tsx
it('列表把待回复置顶、显示数量、全员房不受项目过滤', async () => {
  vi.mocked(fetchRooms).mockResolvedValue([cardA, globalRoom, cardB])
  vi.mocked(fetchInbox).mockResolvedValue([
    { origin: 'mention', title: '@你', card_id: 'B2', ref_id: '1' },
  ])
  render(<RoomPanel workbench={workbench} persistent={false} />)
  expect(await screen.findByText('⚑ 需要你 1')).toBeInTheDocument()
  const rows = await screen.findAllByRole('button', { name: /会话/ })
  expect(rows[0]).toHaveTextContent('B2')
  await user.click(screen.getByText('▦ 全部项目 ∨'))
  await user.selectOptions(screen.getByRole('combobox', { name: '项目' }), 'p1')
  expect(screen.getByText('全员')).toBeInTheDocument()
  expect(screen.queryByText('B2')).not.toBeInTheDocument()
})

it('打开房间即 mark read，发送直达当前房间，更多进入详情', async () => {
  vi.mocked(fetchRooms).mockResolvedValue([cardA])
  vi.mocked(fetchRoomMessages).mockResolvedValue([messageA])
  render(<RoomPanel workbench={workbench} persistent />)
  await user.click(await screen.findByRole('button', { name: /卡房间/ }))
  await waitFor(() => expect(markRoomRead).toHaveBeenCalledWith('B1', messageA.seq))
  await user.type(screen.getByRole('textbox', { name: '发送消息' }), '继续')
  await user.click(screen.getByRole('button', { name: '发送' }))
  await waitFor(() => expect(sendRoomMessage).toHaveBeenCalledWith('B1', '继续'))
  await user.click(screen.getByRole('button', { name: '更多' }))
  expect(await screen.findByText('协调者')).toBeInTheDocument()
})

it('attach 无投影时置灰并说明；有投影时确认后打开带 initCommand 的终端', async () => {
  vi.mocked(fetchRooms).mockResolvedValue([{ ...cardA, attach: undefined }])
  render(<RoomPanel workbench={workbench} persistent={false} />)
  await user.click(await screen.findByRole('button', { name: /卡房间/ }))
  await user.click(screen.getByRole('button', { name: '更多' }))
  expect(screen.getByRole('button', { name: 'attach' })).toBeDisabled()
  expect(screen.getByText('暂无可 attach 的任务')).toBeInTheDocument()
})
~~~

测试夹具必须给 unread、RoomAttach、payload.body 完整小写 snake_case 字段。再用有 attach 的 room，点头像和 attach 两条入口都断言同一个 ConfirmDialog；确认后断言 workbench.open 接到 {kind:'terminal', seq:1, initCommand:'handoff attach T1'} 和 attachBase(room)，取消不调用 open。
5. 实现 RoomPanel：

- 顶层显式状态只有 view、roomID、collapsed、project、needsOnly、attachConfirm、draft；list/room/detail 在同一组件条件渲染，不导航 /rooms。
- fetchRooms 和 fetchInbox 用两条独立 usePoll，断线保留最后数据并显示已断开及原因，首次失败显示 alert 和重试，401 交给 usePoll 终止。列表轮询即使收起也继续；preview 只在展开并有列表数据时拉取。
- needRoomIDs 是 fetchInbox 的非空 card_id 去重集合；N 是唯一 card room 数，不是 inbox 条数；global 没有 card_id，永远不因 needsOnly 被纳入。
- project 过滤只过滤非 global 的有项目项；orderRooms 只把需要你置顶，保留服务端同组活动顺序。
- 预览 effect 对每个可见 room 调 fetchRoomMessages(id, {limit: 1})，使用 roomPreview；单项失败保留旧值、logRoom warn、显示暂无预览。
- 列表行照 prototype B：44px 圆头像，标题/preview/时间，unread>0 红 badge 压头像角，待回复行琥珀背景并在预览前加 [待回复]。过滤项是纯文字，不能改成按钮。
- 房间态 header 为返回/title/更多；对方气泡为 rgba(255,255,255,.65)+blur(12px)，自己气泡为 rgba(17,24,39,.85) 白字，底部胶囊输入。read_only 时输入/发送 disabled 且显示原因；发送成功清 draft 并刷新，失败显示原文并记录 error。
- 打开房间或历史刷新得到 max seq 后调用 markRoomRead(room.id,maxSeq)，仅 maxSeq>0 调用；失败不抹消息，面板内显示 alert。
- 详情显示协调者卡：bound_session 作为载体/身份文字，live 在线态，attach task/workdir；头像和 attach 都调用同一 openAttachConfirm。attach nil 时 disabled、title/说明为暂无可 attach 的任务。
- ConfirmDialog 确认后，用 attachBase(room) 和 nextTerminalSeq(workbench.wb) 构造 TabContent，调用 workbench.open({kind:'terminal',seq,initCommand:room.attach.command}, base)。先记录入口日志，再关闭弹层；不直接调用 createPtySession。
- persistent=true 渲染右侧 360px 独立占位；persistent=false 渲染 fixed right:20px bottom:80px z-40 的 FAB，展开同样约 360×520 浮窗。收起 persistent 面板只保留 FAB，不复制第二套 panel。
6. 在 web/src/app/workbench/tabs.ts 的 terminal union 增加 initCommand?: string，保留去重 key、标题、序号规则。Shell terminal 分支传 initCommand={c.initCommand ?? launcher?.command}；普通 terminal 旧对象不增加键。
7. 跑 Task 3 scoped tests。新增断言入口必须穿过 RoomPanel、fetchRooms/fetchInbox/fetchRoomMessages、markRoomRead/sendRoomMessage 和 Workbench open；不以 model 单测顶替 seam。日志覆盖入口、请求前后、成功和每条错误分支；新文件头和导出函数注释写参数、返回、边界及为何不碰路由/PTY。

行为验收：三态同组件；无独立收件箱页；项目/global/需要你语义正确；打开即已读和只读/错误反馈正确；attach 必须二次确认，有值才打开对应目录终端并带真实命令；两种挂载同构。

### Task 4：Shell、ProjectTree、CardDrawer 和旧资产收口

文件集：

- web/src/app/shell/Shell.tsx
- web/src/app/shell/Shell.test.tsx
- web/src/app/tree/ProjectTree.tsx
- web/src/app/tree/ProjectTree.test.tsx
- web/src/app/cards/CardsPage.tsx
- web/src/app/cards/CardDrawer.tsx
- web/src/app/cards/CardDrawer.test.tsx
- web/src/app/cards/CardsPage.test.tsx
- web/src/app/update/UpdateToasts.tsx（删除）
- web/src/app/update/UpdateToasts.test.tsx（删除）
- web/src/app/rooms/RoomsListPage.tsx（删除）
- web/src/app/rooms/RoomsListPage.test.tsx（删除）
- web/src/app/rooms/RoomDetailPage.tsx（删除）
- web/src/app/rooms/RoomDetailPage.test.tsx（删除）
- web/src/app/rooms/InboxPage.tsx（删除）
- web/src/app/rooms/InboxPage.test.tsx（删除）
- web/src/app/rooms/pollInterval.test.tsx（Task 3 改为 RoomPanel 轮询测试后保留）
- prototypes/b275-frontend-proto/pages/conversations.html（删除）

Interfaces：

- Consumes RoomPanel({workbench: wb, persistent: boolean})、runCardStep(id: string, step: string)、ProjectTree 现有除 onOpenRooms/onOpenInbox 外的 props、WorkbenchPage.renderContent(content: TabContent, base: BaseDir, group: number, tabId: string)。
- Produces /cards 中央区旁 persistent RoomPanel、其它页面 floating RoomPanel；CardDrawer absolute 且动作区调用 runCardStep；删除旧 rooms/inbox 路由、左栏入口、UpdateToasts 和孤儿 prototype 页。

步骤：

1. 修改前重跑 cd web && npm test -- --run src/app/shell/Shell.test.tsx src/app/tree/ProjectTree.test.tsx src/app/cards/CardDrawer.test.tsx src/app/cards/CardsPage.test.tsx，记录原始输出。
2. 先写并跑 Shell 红测：MemoryRouter /cards 断言 RoomPanel 右侧 persistent；/settings 断言有浮动按钮；/rooms 和 /inbox 不再渲染旧页/旧入口；DOM 不含 UpdateToasts。删除旧路由 fetchRooms/fetchInbox 测试，替换为 RoomPanel 挂载测试。
3. Shell 局部替换的精确形状：

~~~tsx
import { RoomPanel } from '../rooms/RoomPanel'
// 删除 InboxPage、RoomDetailPage、RoomsListPage、UpdateToasts import

const fullPageRoute = ['/cards', '/flows', '/settings', '/machines', '/codegraph']
  .some((path) => location.pathname.startsWith(path))
const cardsRoute = location.pathname.startsWith('/cards')

<div className={'min-w-0 flex-1 relative ' + (cardsRoute ? 'flex-row' : 'flex-col')}>
  <main className="min-h-0 min-w-0 flex-1">
    {/* 现有 Routes 原样保留，只删 /rooms、/rooms/:id、/inbox 三个 Route */}
  </main>
  {ledgerEnabled && <RoomPanel workbench={wb} persistent={cardsRoute} />}
</div>
~~~

实际修改保留原 wrapper 的 flex/min-w-0/flex-1 语义；cards 横向，其它页面纵向。RoomPanel 不放进 Routes。terminal renderContent 传 initCommand={c.initCommand ?? launcher?.command} 并保留 sessionId 回写。
4. ProjectTree 删除 props 类型、解构参数和底部会话/收件箱两个 dock 项，保留 cards/flows/tickets/settings/codegraph。测试删除两支旧回调测试，新增底部没有会话/收件箱入口的断言。
5. CardDrawer 根节点从 fixed inset-y-0 right-0 改为 absolute inset-y-0 right-0；CardsPage 根节点加 relative。协调者动作区紧邻既有环节动作，当前 status 对应 dispatch 节点才能启用：

~~~tsx
const coordinatorNode = nodes?.find((node) => node.name === status)
const coordinatorReady = coordinatorNode?.dispatch === true

<section className="mb-5" aria-label="协调者动作">
  <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">协调者</h3>
  <button
    type="button"
    disabled={!coordinatorReady || stepBusy !== null || stepStarted !== null}
    title={!coordinatorReady ? '当前状态没有可派发的协调者节点' : undefined}
    onClick={() => { if (coordinatorNode) void startStep(coordinatorNode.name) }}
    className="rounded-md border px-2.5 py-1 text-xs hover:bg-accent disabled:opacity-50"
  >
    ▶ 拉起协调者
  </button>
  {!coordinatorReady && <p className="mt-1 text-xs text-muted-foreground">当前状态未配置可派发节点。</p>}
  {stepStarted === status && <p className="mt-1 text-xs text-muted-foreground">已受理，进展见 Timeline。</p>}
  {stepError && <p role="alert" className="mt-1 break-words text-xs text-destructive">{stepError}</p>}
</section>
~~~

这个按钮只能走 startStep → runCardStep(id,status)，不能新建 endpoint 或直接触碰 keystone；不可执行时禁用并解释，409/其它错误显示后端原文。CardDrawer 测试从按钮入口断言 runCardStep 参数和错误展示。
6. 删除三页及测试、UpdateToasts 及测试、孤儿 conversations.html。删除前执行 rg -n "RoomsListPage|RoomDetailPage|InboxPage|UpdateToasts|onOpenRooms|onOpenInbox|conversations.html" web/src prototypes/b275-frontend-proto 找完引用；删除后同命令不得留下死 import。这个查找命令真实输出写台账。
7. 跑 Task 4 scoped tests；关键日志为 RoomPanel 挂载位置、CardDrawer coordinator step 受理；新增错误路径须可见。注释说明 absolute 依赖 CardsPage relative wrapper，路由删除是 IA 收敛而非后端端点删除。

行为验收：/cards 是 content+IM 横向 sibling，抽屉 absolute 不盖 IM；其它页面只显示 floating 同构面板；左栏无会话/收件箱；旧文件/路由/toast 彻底退役；拉起协调者只过 runCardStep 且失败可见。

### Task 5：收口验证与原型对照（由协调者执行，不派发）

文件集：

- docs/superpowers/ledgers/2026-08-28-b275-spec-ledger.md
- Task 1–4 的全部改动文件（只读验证）
- prototypes/b275-frontend-proto/index.html
- prototypes/b275-frontend-proto/pages/board.html
- prototypes/b275-frontend-proto/pages/flows.html

Interfaces：

- Consumes所有实现 task 的代码、测试输出和 prototype 代码。
- Produces全卡门禁结果、逐故事真机清单和台账；不产生运行时接口。

步骤：

1. 对 Task 1/2 改动的 Go 文件执行 gofmt -w；执行 git diff --check；真实输出追加台账。
2. 执行 scoped backend：go test ./internal/proto ./internal/collab ./internal/ledger ./internal/agentd；执行新增/改动 Web 测试；原始 stdout/stderr 全量追加台账。
3. 执行全量 go test ./...、cd web && npm test -- --run、npm run typecheck、npm run lint、npm run build。lint 必须不再有基线 error；NodeEditor.test.tsx:50:9 的 view 改为 const 后，lint 退出 0。失败逐字记录，不替命令归因。
4. 原型逐屏对照：/cards 五列及顺序、content+drawer+IM；其它路由 FAB 与 home 并排；列表 row/preview/time/unread/待回复琥珀；B 气泡/胶囊输入/详情卡/同一 attach confirm；flows 映射表改名/排序并刷新保持；settings 仍有更新常驻内容且右下没有 update toast。
5. 真机清单，结果必须追加台账，不能以单测替代：Chromium 本机任务 attach；配置 target 的远端任务 attach 与不可达禁用；断线保留旧数据/写操作禁用/恢复重拉；401 终态；/cards 抽屉与 IM 同时可用；/settings、工作台、/flows floating 可开关；resize 无遮罩；默认/未知 fallback/项目 global/需要你/打开即读各点验一次。
6. 执行 git status --short、git diff --stat、git diff --check；只允许本卡计划、台账和实现阶段的目标文件变更，不提交 web/node_modules。

## 4. 缺陷族对抗审查

按 docs/superpowers/specs/2026-08-21-handoff-instantiation-checklist.md:76-89 逐族结论：

1. 生命周期/中断：list/room/detail/collapsed 是显式状态；usePoll hidden 停表、断线保留数据；选中房间消失回 list；attach 先确认，取消/Escape 是 no-op。
2. 静默失败/误导：列表、历史、发送、已读、preview、attach 查找和 step 失败均有结构化日志与可见原因；nil attach 是禁用态而非假成功。
3. 跨平台：路径和 machine 走 BaseDir/PTY 既有解析，不拼 SSH、不读浏览器本地路径；命令是当前 CLI 的 handoff attach task。Chromium/桌面壳各完成 Task 5。
4. 假红/假绿：基线 lint 红点留痕；seam 先红后实现；最终全量测试/lint 只判行为，不判文件数。
5. 绕过 gate：coordinator 只走 runCardStep；workflow gate 和卡状态机仍唯一闸；BoardLayout 只经 PutWorkflow 校验。
6. 序列化/类型：RoomSummary unread 非可空，attach 可空；Go 金样本、RoomsFixture、TS fetch、HTTP roundtrip 区分 unread 0/attach 缺失；BoardLayout 穿过 Go store→API JSON→TS→Flows/Cards；五项列值由 Go 与 UI 两侧锁定。

## 5. 序列化边界与接缝覆盖

| 产生 | 手写投影 | 消费 | 回归断言 |
|---|---|---|---|
| Service.listRooms | RoomSummary.Unread | handleRoomsList JSON → fetchRooms | Go/TS 金样本、HTTP unread 2→0、0 在线 |
| lookupRoomAttach | RoomAttach pointer + command | handleRoomsList JSON → RoomPanel detail | 有挂账四字段、无挂账无键、不可达仍 200/禁用 |
| Store.PutWorkflow | WorkflowDef.Board JSON blob | flow GET → FlowDetail/WorkflowWire | PUT→GET roundtrip、fixture、非法 400 |
| RoomPanel | RoomMessage.payload preview | list row | body→preview、单项失败保留旧值并告警 |
| RoomPanel | TabContent.initCommand | Shell TerminalTab → CreatePtySessionReq | confirm→Workbench open、既有 init_command contract test |

spec seam 对应：

| seam | task | 入口测试 |
|---|---|---|
| #1 fetchRooms | Task 1 | rooms.fetch.test.ts |
| #2 RoomPanel 三态/筛选 | Task 3 | RoomPanel.test.tsx |
| #3 createPtySession init_command | Task 3 + Task 4 | attach confirm→Workbench open→TerminalTab/contract |
| #4 cards/columns | Task 2 | columns、CardsPage、FlowsPage 入口 |
| #5 unread/attach 双侧投影 | Task 1 | rooms_fixture_test.go、roomsapi_test.go、rooms.test.ts |

测试→缝：Task 1 新增入口是 HTTP handler、Go marshal 或 fetchRooms；Task 2 是 flow HTTP、CardsPage/FlowsPage 或 seam #4；Task 3 全部从 RoomPanel；Task 4 从 Shell、ProjectTree、CardDrawer。没有用内部 helper 顶替缝级断言。

缝→测试：五条 seam 各至少一支测试；#3 同时断言 RoomPanel 的 Workbench open 和已有 TerminalTab/contract init_command 链。不存在未声明的条件退路；若实现中测试意外先绿，不得换成直喂 helper。

## 6. 上下文预算、占位符和跨卡审计

- Task 1 圈房间 proto/collab/gateway/rooms API；Task 2 圈 workflow API/columns/flows/cards 映射；Task 3 圈 rooms panel/workbench tab；Task 4 圈壳/树/抽屉/删除项，均为有界文件集。
- 本计划不使用 TBD、同 Task N 或“加适当错误处理”；每个错误分支均给出日志字段、HTTP 状态或 UI 文案。
- roomPanelModel 仅是实现辅助，不单独拿 helper 测试替代 RoomPanel seam；columns 是明确的 spec seam；Go default/validate 由 flow HTTP 入口覆盖。
- Task 5 标注由协调者执行、不派发，因为它包含全量门禁、原型对照、真机和最终台账审计；本执行者不调用 handoff CLI、不启动子任务。
- L3→L2 用户裁决已在台账；本节点没有独立上下文跨卡审计能力，A/B Produces/Consumes 的逐字签名对照和跨卡用户故事归属交由协调者 review，结论在此标为待拍板，不冒充独立审计。

## 7. Spec 故事归属

| 故事 | 具体 task |
|---|---|
| 1 任何页面右下打开、无 update toast | Task 3 floating + Task 4 删除 |
| 2 工作项页常驻 IM、可收起 | Task 3 persistent + Task 4 Shell |
| 3 行式列表、未读、待回复置顶 | Task 1 unread + Task 3 list/preview |
| 4 项目筛选、需要你、global 不受限 | Task 3 visibleRooms 和组件入口测试 |
| 5 房间 header、B 气泡、发送 | Task 3 room view |
| 6 详情协调者/卡片信息 | Task 3 detail + Task 2 CardDrawer/chips |
| 7 头像/attach 确认后终端 | Task 1 projection + Task 3 confirm + Task 4 Shell |
| 8 五列与 workflow 可配映射 | Task 2 BoardLayout/columns/FlowsPage |
| 9 卡抽屉拉起协调者且不挡 IM | Task 4 CardDrawer/Shell |
| 10 打开即清未读 | Task 1 member unread + Task 3 markRoomRead |

收口逐条自查：spec 每条指向具体 task；代码图 warning/覆盖债、基线 lint 红点、两条 JSON 边界和删除项进台账；未执行命令只能写未验证，不能写成通过。
