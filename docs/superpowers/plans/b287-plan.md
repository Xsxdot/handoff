# Plan：B287 工作台修复批（五项）

> 状态：已出稿（协调者，2026-08-28），五项检查与占位符扫描见 §自审
> 上游：spec `docs/superpowers/specs/b287-spec.md`（已批准 2026-08-28，五项）
> 基线：`acc/b156.2-156.3` @ `24be42238`；卡分支 `cards/B287-charter-5`；工作树
> `/Users/xushixin/workspace/handoff/.worktrees/acc-b156.2-156.3`（本地执行）
> 台账：`docs/superpowers/ledgers/2026-08-28-b287-spec-ledger.md`（implement 期间继续追加）

---

## §0 全局约定

**命令**：Go 测试 `go test ./internal/collab/ ./internal/agentd/ -count=1`；
web 测试 `cd web && npx vitest run src/app/rooms src/app/cards src/app/settings`
（web/package.json:11 `test: vitest run`）。每个 task 只跑触及包（implement 三段律
的全量属收尾，不属任何单个 task）。

**提交**：每个 task 一条提交，消息前缀 `fix(B287):`；提交前该 task 的红绿周期必须
走完（红→绿→提交）。

**现状锚点**：本文 file:line 为 `24be42238` 读数，动手前以工作树复核（行号漂移以
符号定位为准）。

**红绿范围**：红绿周期只套在各 task 标注「缝级断言」的测试步骤上；样式类/文案类
改动不配独立红绿（它们由所在 task 的组件断言一并锁住）。

---

## Task 1 后端：回复即消费该房间提及（spec ①后端半边，接缝 1）

**Consumes**：`func (s *Service) Send(roomID string, msg proto.RoomMessage, actor string) (int64, error)`
（`internal/collab/service.go:80`，现状）；`func (s *Service) Consume(seq int64, consumer string) error`
（`:204`）；`room.ReadAllEvents / ConsumedSeqs / MentionsMember`（`internal/collab/room/room.go`）。
**Produces**：`func (s *Service) consumeRoomMentions(roomID, member string)`（未导出，
仅 Send 调用）。

**Interfaces 说明**：agentd 收件箱 mention 源用 `rooms.Mentions(s.roomUserActor(r), …)`
取数（`internal/agentd/roomsapi.go:423`）；本 task 的消费身份 = 发送 actor =
`roomUserActor`，两端同一把尺子。裁决/工单两源不动。

1. 判据基线跑：`go test ./internal/collab/ -count=1` → 全绿（基线是绿的，红了先停）。
2. **红**：在 `internal/collab/service_test.go` 追加（复用本文件 `newFixture`/
   `mustAnyCard`/`mustCard` 夹具——直通竖切、真 SQLite，与 `TestHistoryReturnsNewestWindow`
   同款，B289 刚加的那个）：

```go
// TestSendConsumesRoomMentions B287：用户回复即消费该房间 @本人 的未消费提及；
// 别的卡房间的提及不受牵连。缝级断言，入口 Service.Send。
func TestSendConsumesRoomMentions(t *testing.T) {
	svc, st := newFixture(t)
	card := mustAnyCard(t, svc, st)
	other := mustCard(t, svc, st, "另一张卡")
	lc := ledgerapi.New(st)
	if err := lc.BindDriver(card.ID, "agent:s1", "car-a", ""); err != nil {
		t.Fatal(err)
	}
	if err := lc.BindDriver(other.ID, "agent:s2", "car-b", ""); err != nil {
		t.Fatal(err)
	}
	// 协调者侧 @用户：relay 类必须由绑定者书写（room.VerifyWriter 矩阵）。
	if _, err := svc.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgRelay, Body: "看一下这个", Mentions: []string{"user:sy"}}, "agent:s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(other.ID, proto.RoomMessage{Kind: proto.RoomMsgRelay, Body: "那边也看一下", Mentions: []string{"user:sy"}}, "agent:s2"); err != nil {
		t.Fatal(err)
	}
	pending, err := svc.Mentions("user:sy", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("发送前应有两处未消费提及，got %d", len(pending))
	}
	if _, err := svc.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "收到"}, "user:sy"); err != nil {
		t.Fatal(err)
	}
	pending, err = svc.Mentions("user:sy", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("回复后本房间提及应清零、他卡提及保留，got %d", len(pending))
	}
	if pending[0].CardID != other.ID {
		t.Fatalf("保留的应是另一张卡的提及，got card %s", pending[0].CardID)
	}
}
```

   跑 `go test ./internal/collab/ -run TestSendConsumesRoomMentions -count=1` →
   **红**（`回复后本房间提及应清零…got 2`）。
3. **绿**：`internal/collab/service.go` 两处改动。

   Send 尾部（`RecordRoomMessage` 成功之后、日志之前）插入：

```go
	if msg.Kind == proto.RoomMsgUser {
		// B287：用户回复即清该房间的提及类待回复。best-effort：发送已成功，
		// 消费失败只告警不回滚（回滚要新账本事务面，超出本卡契约零增量约束）。
		s.consumeRoomMentions(roomID, actor)
	}
```

   在 Send 与 Pointer 之间加新方法：

```go
// consumeRoomMentions 用户消息落房后，把该房间内 @本人 且未消费的提及一并消费
// （B287 拍板「回复即清提及类」）。只处理卡房间：mentions 角标面只认 card_id
// （agentd handleInbox 用 ev.CardID 亮房间），群级提及本就不进角标；裁决与
// 工单两源不受影响——它们要求显式审批/答复，不因一句话消掉。
//
// 消费身份 = 发送 actor，与收件箱 mention 源（agentd handleInbox 的
// rooms.Mentions(s.roomUserActor(r),…)）同标识，保证「发前亮、回复后灭」
// 同一把尺子。幂等由 Consume 兜底；失败逐条告警，下次回复自然重试。
func (s *Service) consumeRoomMentions(roomID, member string) {
	events, err := room.ReadAllEvents(s.lc, 0)
	if err != nil {
		log().Warn("回复后清理提及失败：读事件流", "room", roomID, "member", member, "cause", err)
		return
	}
	consumed := room.ConsumedSeqs(events, member)
	for _, ev := range events {
		if ev.Type != room.RoomEventType || ev.CardID != roomID || consumed[ev.Seq] {
			continue
		}
		if !room.MentionsMember(ev, member) {
			continue
		}
		if err := s.Consume(ev.Seq, member); err != nil {
			log().Warn("回复后清理提及失败：消费落账", "room", roomID, "member", member, "seq", ev.Seq, "cause", err)
		}
	}
}
```

   跑 `go test ./internal/collab/ -count=1` → 全绿。
4. 提交 `fix(B287): 用户回复即消费该房间未消费提及（待回复清提及类）`。

---

## Task 2 前端：发送后立即刷新收件箱 + refresh 不吃旧在飞（spec ①前端半边，接缝 2）

**Consumes**：`usePoll(fetcher, intervalMs, opts?)` 返回 `{ …, refresh }`
（`web/src/app/data/usePoll.ts:28`，现状）；`RoomPanel` 的 `send()`
（`web/src/app/rooms/RoomPanel.tsx:235`）。
**Produces**：`usePoll` 内部行为变更（签名不变）：nonce 变更后的 effect 重跑不再
采纳旧 nonce 的在飞请求，改起新请求。

1. 判据基线跑：`cd web && npx vitest run src/app/rooms src/app/data -count=1`？——
   vitest 无 -count；跑 `cd web && npx vitest run src/app/rooms src/app/data` → 全绿。
2. **红**：`web/src/app/rooms/RoomPanel.test.tsx` 追加（夹具/`room()`/mock 全复用本文件
   现有 setup，`beforeEach` 已 mock `sendRoomMessage` 成功；需在文件头 import 里补
   `fireEvent`）：

```tsx
describe('RoomPanel 发送后刷新（B287）', () => {
  it('refresh 不复用旧 nonce 的在飞请求：发送后收件箱立即重取，不等下一轮周期', async () => {
    // 收件箱首拉故意挂起不放行：制造「发送那一刻有一个旧 nonce 在飞请求」的现场。
    // 无修复时 refresh 会采纳这个旧请求（内容不可能含新事实），不发新调用；
    // 有修复时 nonce 变更触发第二只真实请求。
    let releaseInbox!: (value: Awaited<ReturnType<typeof fetchInbox>>) => void
    vi.mocked(fetchInbox).mockImplementation(
      () => new Promise((resolve) => { releaseInbox = resolve }),
    )
    vi.mocked(fetchRooms).mockResolvedValue([room()])
    const user = userEvent.setup()
    render(<RoomPanel workbench={workbench()} persistent={false} />)
    await user.click(await screen.findByRole('button', { name: /会话 B1/ }))
    await user.type(await screen.findByLabelText('发送消息'), 'hi')
    await user.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(vi.mocked(fetchInbox).mock.calls.length).toBeGreaterThanOrEqual(2))
    releaseInbox([])
  })
})
```

   跑该用例 → **红**（calls 停在 1；5s 周期在真实计时器下不会到）。
3. **绿**（两处）。

   `web/src/app/data/usePoll.ts`：`inFlightRef` 声明（`:44`）后加一行，`poll` 开头
   （`:55-57`）改为：

```ts
  const inFlightRef = useRef<Promise<T> | null>(null)
  // 在飞请求所属的 nonce。refresh（nonce+1）之后，旧 nonce 的在飞请求内容
  // 不含有本次要看的变更，等它等于把新数据推迟一个周期——显式刷新改起一只
  // 新请求；旧请求走完后由各自 effect 的 stopped 守卫丢弃，不重复 setData。
  const inFlightNonceRef = useRef(0)
```

```ts
    const poll = async (subscribe = false) => {
      let request = inFlightRef.current
      if (request && !subscribe) return
      if (request && inFlightNonceRef.current !== nonce) request = null
```

   `poll` 里真正起请求处（`:59-62`）补记 nonce：

```ts
        if (!request) {
          request = fetcherRef.current()
          inFlightRef.current = request
          inFlightNonceRef.current = nonce
        }
```

   （StrictMode effect 重放、visibilitychange 恢复都是**同 nonce** 采纳在飞，行为
   不变；只有显式 refresh 换新请求。同流去重对 interval tick 照旧生效。）

   `web/src/app/rooms/RoomPanel.tsx` `send()` 成功分支（`:247-248`）加一行：

```tsx
      await sendRoomMessage(roomID, body)
      setDraft('')
      historyPoll.refresh()
      // B287：回复即清提及类的前端半边——后端已在发送路径消费该房间提及，
      // 这里立即重取收件箱把新事实拉进角标，不等下一个 5s 周期。
      inboxPoll.refresh()
```

   跑 `cd web && npx vitest run src/app/rooms src/app/data` → 全绿（含既有用例，
   若 data 目录无用例则该路径参数去掉）。
4. 提交 `fix(B287): 发送后立即刷新收件箱；usePoll 显式刷新不吃旧在飞请求`。

---

## Task 3 状态唯一化：看板卡右上角 chip + 抽屉头部单 chip（spec ②，接缝 3/4）

**Consumes**：`CardItemProps.nodeTag?: string`（现状不变）；`CardDrawer` 内
`nodeLabel`（`web/src/app/cards/CardDrawer.tsx:368`，现状不变）。
**Produces**：无新接口；两处渲染形态变更。

1. 判据基线跑：`cd web && npx vitest run src/app/cards` → 全绿。
2. **红**：`web/src/app/cards/CardItem.children.test.tsx` 追加：

```tsx
describe('CardItem 状态唯一化（B287）', () => {
  it('节点标签只出现在右上角 chip 一处，下方标签行不再重复', () => {
    render(<CardItem card={base} onOpen={vi.fn()} nodeTag="待审阅" />)
    expect(screen.getAllByText('待审阅')).toHaveLength(1)
    const chip = screen.getAllByText('待审阅')[0]!
    expect(chip.className).toContain('bg-slate-900')
  })

  it('无节点标签时右上角显示状态名，同样只出现一次', () => {
    render(<CardItem card={base} onOpen={vi.fn()} />)
    expect(screen.getAllByText('进行中')).toHaveLength(1)
    expect(screen.getAllByText('进行中')[0]!.className).toContain('bg-slate-900')
  })
})
```

   （现状：有 nodeTag 时「待审阅」出现两处 → 第一条红；右上角是灰文本无
   `bg-slate-900` → 第二条红。）跑 → **红**。
3. **绿**（两个文件）。

   `web/src/app/cards/CardItem.tsx`：
   `:56` 右上角纯文本改为 chip（`ml-auto` 保位、`shrink-0` 防挤压）：

```tsx
        <span className="ml-auto shrink-0 rounded-full bg-slate-900 px-1.5 py-0.5 text-[10px] text-white">{nodeTag ?? card.status}</span>
```

   `:84` 标签行里的黑色状态 pill 整行删除。文件内补一行注释（放 `Chip` 定义上方）：

```tsx
// 状态在卡片上只渲染一次（B287）：右上角 chip，文案 = 节点标签（多对一列显形，
// B279 语义不变）缺省回落状态名；下方标签行不再出现状态。
```

   `web/src/app/cards/CardDrawer.tsx` `:611-612` 两枚 pill 合并为一枚（黑 chip，
   节点标签优先，加载中回落保留）：

```tsx
          <span className="rounded-full bg-slate-900 px-2 py-0.5 text-xs text-white">{nodeLabel ?? (status || '加载中')}</span>
```

   （`??` 与 `||` 混用必须带括号，否则 tsc 报错。）
4. **抽屉侧断言**（`web/src/app/cards/CardDrawer.test.tsx` 追加；夹具照抄本文件既有
   `fetchCardDetail` mock 用例——618 行内已有完整 fixture，这里按占位符扫描节声明
   例外：断言逐条列全，harness 指认本文件）：
   - 渲染抽屉后，`role="dialog"` 头部区域文本 `status`（fixture 里的卡状态）以
     `getByText` 恰好命中 **1 次**；
   - 该元素 className 含 `bg-slate-900`（chip 形态）；
   - fixture 构造 `workflowStates` 使状态落在多节点列（参照本文件既有 nodeLabel
     相关用例的构造方式）时，chip 文本 = 节点标签而非裸状态名，且仍只 1 次。
   跑 `cd web && npx vitest run src/app/cards` → 全绿。
5. 提交 `fix(B287): 卡片状态只渲染一次（右上角 chip；抽屉头部合并双 pill）`。

---

## Task 4 悬浮球错开（spec ③，接缝 5 的位置半边）

**Produces**：无接口；一个 className 变更 + 一个测试断言。

1. **红**：`web/src/app/rooms/RoomPanel.test.tsx` 追加：

```tsx
describe('RoomPanel 悬浮球（B287）', () => {
  it('收起球上移错开 + 球：bottom-[104px]（+球占 44–88px，净距 16px）', async () => {
    vi.mocked(fetchRooms).mockResolvedValue([])
    render(<RoomPanel workbench={workbench()} persistent={false} />)
    const fab = await screen.findByRole('button', { name: '打开房间面板' })
    expect(fab.className).toContain('bottom-[104px]')
    expect(fab.className).not.toContain('bottom-20')
  })
})
```

   跑 → **红**。
2. **绿**：`web/src/app/rooms/RoomPanel.tsx:374` 收起球 className
   `fixed bottom-20 right-5 z-40 …` 改为 `fixed bottom-[104px] right-5 z-40 …`
   （其余不动；错误 toast `Shell.tsx:644` 在 `bottom-24` 且 z 更高，短暂遮挡可接受，
   两者不同时驻留）。
   跑 src/app/rooms → 全绿。
3. 提交 `fix(B287): 会话收起球上移至 bottom-[104px]，与 + 球错开`。

---

## Task 5 浮窗拖拽缩放 + 几何持久化（spec ④，接缝 5 的交互半边）

**Consumes**：`topInset()`（`web/src/app/lib/desktopShell.ts:37`，jsdom 下 0）；
RoomPanel 三态头部（list 的 `PanelHeader`、room/detail 的内联 header）。
**Produces**（新文件 `web/src/app/rooms/useRoomPanelGeom.ts`）：

```ts
export interface RoomGeom { x: number; y: number; w: number; h: number }
export function defaultRoomGeom(vw: number, vh: number): RoomGeom
export function useRoomPanelGeom(): {
  geom: RoomGeom | null            // null = 尚未定位（浮窗不渲染）
  ensurePlaced: () => void         // 首次打开浮窗时按当下视口定位（只此一次）
  onGeom: (g: Partial<RoomGeom>) => void
}
```

localStorage 键：`handoff:room-panel-geom.v1`。以及 RoomPanel 上的
`data-testid="room-panel-title"`（list 头部）与既有 `data-testid="room-panel"`。

1. 判据基线跑：`cd web && npx vitest run src/app/rooms` → 全绿（Task 2/4 后）。
2. **红**：`web/src/app/rooms/RoomPanel.test.tsx` 追加（import 里补 `fireEvent`）：

```tsx
describe('RoomPanel 浮窗几何（B287）', () => {
  it('浮窗按 geom 摆位：拖标题栏改 left/top，拉角落改宽高，最小尺寸钳制', async () => {
    window.localStorage.setItem('handoff:room-panel-geom.v1', JSON.stringify({ x: 100, y: 80, w: 360, h: 520 }))
    vi.mocked(fetchRooms).mockResolvedValue([])
    const user = userEvent.setup()
    render(<RoomPanel workbench={workbench()} persistent={false} />)
    await user.click(await screen.findByRole('button', { name: '打开房间面板' }))
    const panel = await screen.findByTestId('room-panel')
    expect(panel.style.left).toBe('100px')
    expect(panel.style.top).toBe('80px')
    fireEvent.pointerDown(screen.getByTestId('room-panel-title'), { clientX: 10, clientY: 10 })
    fireEvent.pointerMove(document, { clientX: 40, clientY: 30 })
    fireEvent.pointerUp(document)
    expect(panel.style.left).toBe('130px')
    expect(panel.style.top).toBe('100px')
    fireEvent.pointerDown(screen.getByTestId('room-panel-corner'), { clientX: 0, clientY: 0 })
    fireEvent.pointerMove(document, { clientX: -500, clientY: -500 })
    fireEvent.pointerUp(document)
    expect(panel.style.width).toBe('320px')
    expect(panel.style.height).toBe('360px')
  })

  it('几何本机持久化：拖动后写入 localStorage，重挂载恢复摆法', async () => {
    window.localStorage.clear()
    vi.mocked(fetchRooms).mockResolvedValue([])
    const user = userEvent.setup()
    const view = render(<RoomPanel workbench={workbench()} persistent={false} />)
    await user.click(await screen.findByRole('button', { name: '打开房间面板' }))
    await screen.findByTestId('room-panel')
    fireEvent.pointerDown(screen.getByTestId('room-panel-title'), { clientX: 0, clientY: 0 })
    fireEvent.pointerMove(document, { clientX: 15, clientY: 10 })
    fireEvent.pointerUp(document)
    const stored = JSON.parse(window.localStorage.getItem('handoff:room-panel-geom.v1') ?? '{}')
    expect(stored).toMatchObject({ w: 360, h: 520 })
    view.unmount()
    render(<RoomPanel workbench={workbench()} persistent={false} />)
    await user.click(await screen.findByRole('button', { name: '打开房间面板' }))
    const restored = await screen.findByTestId('room-panel')
    expect(restored.style.left).toBe(`${stored.x}px`)
    expect(restored.style.top).toBe(`${stored.y}px`)
  })
})
```

   跑 → **红**（title/corner testid 不存在、style 未接管）。
3. **绿**（一个新文件 + RoomPanel 五处改动）。

   新文件 `web/src/app/rooms/useRoomPanelGeom.ts`（全文）：

```ts
// useRoomPanelGeom —— 会话浮窗（RoomPanel 非持久形态）的几何：位置、尺寸、本机持久化。
//
// 职责：按视口给浮窗定位（贴右下收起球）、处理拖动/缩放增量并钳制下界、
// 把几何写进 localStorage（重开恢复用户的摆法）。
// 边界：不渲染任何元素；持久侧栏形态（persistent=true）不消费本 hook。
// 交互模式照抄 HomeWindow/useHomeDock：拖动把手 = 面板标题栏，缩放 = 右下角，
// 监听挂 document（指针拖出窗口也能收到 move）。
import { useCallback, useEffect, useRef, useState } from 'react'
import { topInset } from '../lib/desktopShell'

export interface RoomGeom { x: number; y: number; w: number; h: number }

// 悬浮球在视口里的位置与尺寸（px），与 RoomPanel.tsx 收起球的 Tailwind 类
// 一一对应：right-5 / bottom-[104px] / size-11。改那边要同步改这里。
const FAB_RIGHT = 20
const FAB_BOTTOM = 104
const FAB_SIZE = 44
const FAB_GAP = 8
const MARGIN = 8
const MIN_W = 320
const MIN_H = 360
const STORE_KEY = 'handoff:room-panel-geom.v1'

// defaultRoomGeom 算浮窗初始几何：尺寸取现状形态（360×520），右下贴收起球、
// 球上方留 FAB_GAP。视口装不下时被 MIN_* 与「视口减两倍边距」夹住，保证不出屏。
export function defaultRoomGeom(vw: number, vh: number): RoomGeom {
  const w = Math.max(MIN_W, Math.min(360, vw - MARGIN * 2))
  const h = Math.max(MIN_H, Math.min(520, vh - MARGIN * 2))
  return {
    x: Math.max(MARGIN, vw - FAB_RIGHT - w),
    y: Math.max(topInset() + MARGIN, vh - FAB_BOTTOM - FAB_SIZE - FAB_GAP - h),
    w,
    h,
  }
}

function loadStored(): RoomGeom | null {
  try {
    const raw = window.localStorage.getItem(STORE_KEY)
    if (!raw) return null
    const g = JSON.parse(raw) as Partial<RoomGeom>
    if ([g.x, g.y, g.w, g.h].some((n) => !Number.isFinite(n))) return null
    return { x: g.x!, y: g.y!, w: Math.max(MIN_W, g.w!), h: Math.max(MIN_H, g.h!) }
  } catch {
    return null // 隐私模式/配额/坏 JSON：几何不持久，会话内仍可拖
  }
}

export function useRoomPanelGeom() {
  const [geom, setGeom] = useState<RoomGeom | null>(loadStored)
  const placed = useRef(geom !== null)

  // 几何变化即落盘；null 不写（尚未定位时没有「用户的摆法」可记）。
  useEffect(() => {
    if (geom === null) return
    try {
      window.localStorage.setItem(STORE_KEY, JSON.stringify(geom))
    } catch {
      // 同 loadStored：持久化失败无害，会话内照常用
    }
  }, [geom])

  // ensurePlaced 首次打开浮窗时按当下视口定位。与 useHomeDock 同款承重：
  // 必须在打开那一刻算，不能在挂载时算（首帧 innerWidth 不可信）。
  const ensurePlaced = useCallback(() => {
    if (placed.current) return
    const vw = window.innerWidth || document.documentElement.clientWidth
    const vh = window.innerHeight || document.documentElement.clientHeight
    if (vw <= 0 || vh <= 0) return // 视口未定稿：下次打开再摆
    placed.current = true
    setGeom(defaultRoomGeom(vw, vh))
  }, [])

  // onGeom 拖动/缩放的增量入口；用户亲手摆过即视为已定位。
  const onGeom = useCallback((g: Partial<RoomGeom>) => {
    placed.current = true
    setGeom((prev) => {
      const next = { ...(prev ?? { x: 0, y: 0, w: 360, h: 520 }), ...g }
      next.w = Math.max(MIN_W, next.w)
      next.h = Math.max(MIN_H, next.h)
      next.x = Math.max(MARGIN, next.x)
      next.y = Math.max(topInset() + MARGIN, next.y)
      return next
    })
  }, [])

  return { geom, ensurePlaced, onGeom }
}
```

   `web/src/app/rooms/RoomPanel.tsx` 改动：

   a) react import 行补类型：
```tsx
import { useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react'
```
   并新增 `import { useRoomPanelGeom } from './useRoomPanelGeom'`。

   b) 组件体内（`markedReads` ref 之后）：
```tsx
  // 浮窗几何：仅非持久形态消费；geom===null 时浮窗不渲染（首次打开即定位）。
  const { geom: panelGeom, ensurePlaced, onGeom: applyGeom } = useRoomPanelGeom()
  const geomRef = useRef(panelGeom)
  geomRef.current = panelGeom
  const floating = !persistent && !collapsed
  useEffect(() => {
    if (floating) ensurePlaced()
  }, [floating, ensurePlaced])
```

   c) 拖拽会话（`send` 函数之前放）：
```tsx
  // grab 照抄 HomeWindow：按下记起点、document 收 move、抬起一次性解绑；
  // 指针拖出窗口也收得到 move，窗口不会卡在半路。
  const grab = (event: ReactPointerEvent, apply: (dx: number, dy: number) => void) => {
    event.preventDefault()
    const sx = event.clientX
    const sy = event.clientY
    const move = (e: PointerEvent) => apply(e.clientX - sx, e.clientY - sy)
    const up = () => {
      document.removeEventListener('pointermove', move)
      const g = geomRef.current
      if (g) logRoom('debug', 'room_geom_committed', { x: g.x, y: g.y, w: g.w, h: g.h })
    }
    document.addEventListener('pointermove', move)
    document.addEventListener('pointerup', up, { once: true })
    document.addEventListener('pointercancel', up, { once: true })
  }
  const onTitleDown = (event: ReactPointerEvent) => {
    const from = panelGeom
    if (persistent || !from) return
    grab(event, (dx, dy) => applyGeom({ x: from.x + dx, y: from.y + dy }))
  }
  const onCornerDown = (event: ReactPointerEvent) => {
    const from = panelGeom
    if (persistent || !from) return
    grab(event, (dx, dy) => applyGeom({ w: from.w + dx, h: from.h + dy }))
  }
```

   d) `PanelHeader`（`:100`）接管拖动 + testid，收起按钮拦 `stopPropagation`
   （不拦的话点收起会顺手把浮窗拖走，HomeWindow 同款坑）：
```tsx
function PanelHeader({ title, onCollapse, onDragDown }: { title: string; onCollapse: () => void; onDragDown: (event: ReactPointerEvent) => void }) {
  return (
    <header data-testid="room-panel-title" onPointerDown={onDragDown} className="flex shrink-0 cursor-move select-none items-center justify-between border-b px-3 py-2.5">
      <span className="text-sm font-semibold">{title}</span>
      <button type="button" aria-label="收起房间面板" onPointerDown={(event) => event.stopPropagation()} onClick={onCollapse} className="rounded-md px-2 py-1 text-xs hover:bg-accent">×</button>
    </header>
  )
}
```
   调用处（`:286`）传 `onDragDown={onTitleDown}`。room 视图 header（`:315`）与
   detail 视图 header（`:335`）：加 `onPointerDown={onTitleDown}`、className 补
   `cursor-move select-none`；这两个 header 里的 ‹ / ••• 按钮各加
   `onPointerDown={(event) => event.stopPropagation()}`。
   （PanelHeader 的 ReactPointerEvent 类型：函数组件文件顶部已有 react import，
   用 `import type { PointerEvent as ReactPointerEvent } from 'react'` 补类型即可。）

   e) 渲染尾部（`:374-375`）收起球保持 Task 4 的类；浮窗 aside 改为 geom 驱动
   （持久分支逐字保留原 className）：
```tsx
      {(!persistent || collapsed) && <button type="button" aria-label="打开房间面板" title="打开房间面板" onClick={() => setCollapsed((current) => !current)} className="fixed bottom-[104px] right-5 z-40 flex size-11 items-center justify-center rounded-full bg-slate-900 text-white shadow-lg">◌</button>}
      {!collapsed && (persistent
        ? <aside data-testid="room-panel" className="flex h-full w-[360px] shrink-0 flex-col border-l bg-background">{content}</aside>
        : panelGeom !== null && <aside data-testid="room-panel" className="fixed z-40 flex flex-col overflow-hidden rounded-2xl border bg-background shadow-xl" style={{ left: panelGeom.x, top: panelGeom.y, width: panelGeom.w, height: panelGeom.h }}>{content}<span data-testid="room-panel-corner" onPointerDown={onCornerDown} aria-hidden="true" className="absolute bottom-0 right-0 size-[15px] cursor-nwse-resize" style={{ background: 'linear-gradient(135deg, transparent 50%, #71717a 50%)' }} /></aside>)}
```
   （原 `fixed bottom-20 right-5 … h-[520px] w-[360px]` 的定位职责整体移交 geom；
   `panelGeom===null` 时浮窗不渲染一帧，避免闪现在 (0,0)。）
   跑 `cd web && npx vitest run src/app/rooms` → 全绿。
4. 提交 `fix(B287): 会话浮窗支持拖动/缩放，几何本机持久化`。

---

## Task 6 编辑表单对齐原型（spec ⑤，接缝 6）

**Consumes**：`prototypes/base/pages/settings.html:265-303`（载体 dialog 480px、
小队 dialog 440px 的 label/选项/hint 原文）。
**Produces**：无接口；SchedulingPage 弹窗渲染变更（aria-label 全部保持现值，
既有用例的 `getByLabelText('载体名')` 等不受影响）。

1. 判据基线跑：`cd web && npx vitest run src/app/settings` → 全绿。
2. **红**：`web/src/app/settings/SchedulingPage.test.tsx` 追加：

```tsx
  it('编辑弹窗对齐原型：label 语义后缀、角色中文选项、政策位与 role hint（B287）', async () => {
    const user = userEvent.setup()
    vi.mocked(getSquads).mockResolvedValue({
      carriers: [{ name: 'mbp', machine: 'local', cli: 'opencode', home_dir: '/h', credential: 'standalone', healthy: true, version: 3 }],
      squads: [{ name: 'coord', role: 'coordinator', members: [], version: 1 }],
    })
    render(<SchedulingPage />)
    await user.click(await screen.findByRole('button', { name: '编辑' }))
    expect(screen.getByText('小队名（唯一）')).toBeVisible()
    expect(screen.getByText('角色（不混编）')).toBeVisible()
    expect(screen.getByRole('option', { name: '执行者队' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '协调者队' })).toBeInTheDocument()
    expect(screen.getByText('成员载体（按勾选顺序解析：第一个健康且有空的载体领活）')).toBeVisible()
    expect(screen.getByText('并发上限（政策位；0 / 留空 = 不限）')).toBeVisible()
    expect(screen.getByText(/协调者队成员必须落在协调机；执行者队成员可以是任何执行机。/)).toBeVisible()
    expect(screen.getByRole('dialog').querySelector('form')?.className).toContain('max-w-[440px]')
    await user.click(screen.getByRole('button', { name: '取消' }))
    await user.click(await screen.findByRole('button', { name: '编辑 mbp' }))
    expect(screen.getByText('载体名（唯一，登记后不可改）')).toBeVisible()
    expect(screen.getByText('模型（留空 = CLI 默认）')).toBeVisible()
    expect(screen.getByRole('dialog').querySelector('form')?.className).toContain('max-w-[480px]')
  })
```

   跑 → **红**。
3. **绿**：`web/src/app/settings/SchedulingPage.tsx`：

   a) 弹窗 form（`:195`）宽度对齐原型（载体 480 / 小队 440，替换 `max-w-xl`）：

```tsx
        <form className={`w-full space-y-4 rounded-lg border bg-background p-5 shadow-lg ${dialog.kind === 'carrier' ? 'max-w-[480px]' : 'max-w-[440px]'}`} onSubmit={(event) => { event.preventDefault(); void save() }}>
```

   b) 载体两处 label（`:198`、`:201`；aria-label 不动）：
   `载体名` → `载体名（唯一，登记后不可改）`；`模型` → `模型（留空 = CLI 默认）`。

   c) 小队块（`:206-211`）由单列 `space-y-3` 改为两栏栅格并对齐原型文案：
   外层 `<div className="space-y-3">` → `<div className="grid gap-3 sm:grid-cols-2">`；
   `小队名` → `小队名（唯一）`；`角色` → `角色（不混编）`，两个 option 显示中文
   `<option value="executor">执行者队</option><option value="coordinator">协调者队</option>`
   （value/保存逻辑不动，wire 不变）；fieldset 加 `className="sm:col-span-2"`，
   legend `成员载体（按勾选顺序写入）` → `成员载体（按勾选顺序解析：第一个健康且
   有空的载体领活）`；并发上限 label → `并发上限（政策位；0 / 留空 = 不限）`；
   栅格末尾补 role hint（原型 `sfRoleHint` 原文，跨两栏）：

```tsx
            <p className="text-[11px] leading-5 text-muted-foreground sm:col-span-2">协调者队成员必须落在协调机；执行者队成员可以是任何执行机。</p>
```

   跑 `cd web && npx vitest run src/app/settings` → 全绿（既有用例不受影响：
   aria-label 全保留）。
4. 提交 `fix(B287): 编辑小队/载体表单对齐 settings.html 原型（label/角色中文/栅格/宽度）`。

---

## §自审（五项检查 + 三查）

**1. 缺陷族对抗（按族设问的结论）**
- 状态残留族：①的消费失败不回滚 → 靠「下次回复重试 + 幂等 Consume」收敛，
  测试锁住清零路径；角落情形（消费落账失败）有 Warn 日志可归因。
- 边界/几何族：浮窗 y 下界含 `topInset()`（桌面薄壳拖动区）、x/y/w/h 全钳制、
  坏 localStorage JSON 与隐私模式静默降级（不持久、不崩）。
- 序列化族：唯一的序列化边界 = geom 的 localStorage JSON roundtrip，Task 5 第二条
  用例（写入→重挂载恢复）穿过真实序列化边界。
- 回归族：既有 `CardItem.children.test`（nodeTag 用例仍过——文案位置变了但文本
  唯一性保持）、`SchedulingPage.test`（aria-label 全保留）、`RoomPanel.test`
  （ aside 类名变更影响面=该文件的布局断言，红即说明，逐条更新而不是放宽）。
- 并发/泄漏族：grab 的 pointerup/pointercancel 均 `{ once: true }` 且 move 在 up 里
  移除，无监听器泄漏。

**2. 序列化边界设问**：见上，一条边界一支 roundtrip 测试，无遗漏。

**3. 上下文预算**：T1 [collab/service.go(+test)]；T2 [usePoll.ts, RoomPanel.tsx(+test)]；
T3 [CardItem.tsx, CardDrawer.tsx(+tests)]；T4 [RoomPanel.tsx(+test)]；T5
[useRoomPanelGeom.ts(新), RoomPanel.tsx(+test)]；T6 [SchedulingPage.tsx(+test)]。
全部有界。

**4. 类型标注**：无边界型子系统；真机清单见 §验收。

**5. 接缝覆盖（双向）**：
- 测试→缝：T1 用例入口 `Service.Send`（缝1）；T2 用例入口 RoomPanel send 路径 +
  usePoll.refresh（缝2）；T3 CardItem/CardDrawer 渲染（缝3/4）；T4/T5 RoomPanel
  geom 与球（缝5）；T6 SchedulingPage 渲染（缝6）。无内部锁。
- 缝→测试：spec §6 六条缝逐条有至少一支缝级断言（缝1=T1；缝2=T2；缝3=T3 两条；
  缝4=T3 抽屉断言组；缝5=T4 一条 + T5 两条；缝6=T6 一条）。双向闭合。

**占位符扫描**：全文无 TBD/「适当处理」/「同 Task N」。例外声明（正出口）：T3 第 4
步抽屉断言组与 T2 既有夹具复用——断言已逐条列全（每条可判 pass/fail），照抄
harness 已指认（`CardDrawer.test.tsx` 本文件 fixture / `RoomPanel.test.tsx` setup）；
其余测试均为完整代码。**内部锁声明：零**——所有测试入口都在缝上，无「从缝构造不出
的断言」；步骤正文无条件退路。

**spec 故事归属**：故事1→T1+T2；故事2→T3；故事3→T4；故事4→T5；故事5→T6。全覆盖，
无落空。跨卡审计不适用（单卡无扇出）。

---

## §验收（acceptance 节点用，节点中立）

自动化：`go test ./internal/collab/ ./internal/agentd/ -count=1` 全绿；
`cd web && npx vitest run` 全绿；`cd web && npx tsc -b` 零错误。

真机清单（六项逐条，每条给可观察判据）：
1. 会话列表里对一条带 @我 的会话回复后，该行「待回复」底色与前缀立即消失
   （裁决/工单类待回复不受影响——另造一条 open 裁决确认角标仍在）。
2. 看板卡片右上角状态为一枚深色 chip、下方标签行无状态；抽屉头部只有一枚状态
   chip；多节点列的卡显示节点标签。对照截图 docs/lab/screenshots/b287/issue3*。
3. 收起会话后，悬浮球与「+」球之间有肉眼可见空隙、无重叠。对照 issue4。
4. 会话浮窗可拖动（标题栏）、可缩放（右下角）、缩不小于 320×360、刷新页面后摆法
   保持。
5. 编辑小队/编辑载体弹窗逐字段对照 prototypes/base/pages/settings.html:265-303
   （label 文案、角色选项中文、role hint、宽度 480/440）。对照 issue6a/6b。
