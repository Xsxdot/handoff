# B94 控制台走查第三轮：四条交互缺陷

**日期**：2026-08-15
**分支基线**：`w4-delivery` @ `4f86c41ff`（= `integration/w4-final`，六条 W4 分支并线后的交付线）
**来源**：08-15 用户在并线后的构建上走查，口头提出三条；第四条由我从用户截图里的报错反查出来。

---

## 0. 这份 spec 的边界

四条都是**已并线代码里的缺陷**，不是新功能。每一条都已经在代码里定位到确切的根因坐标，不存在「先查一查」的阶段。

**不在本 spec 内**（见 §5）：项目位置的「编辑」能力（后端无接口，08-15 用户裁决单开 backlog）、⌘T 快捷键的真实实现、浮窗多开。

---

## 1. 四条缺陷的证据与根因

### 1.1 悬浮入口要点两次才开得出终端

**用户原话**：「悬浮框不应该点击后弹出一层，然后再点击才打开。应该是第一次点击后直接打开一个空白标签页，如果有未关的，直接打开对应的。」

**现状链路**：`HomeDock.tsx:36` 持有本地 `panelOpen`；`:115` FAB 的 `onClick` 只是 `setPanelOpen(true)`。要真的拿到终端，还得在面板上再点一次「新终端」（`:103` → `dock.newTerminal()`）或点一条清单项（`:89` → `dock.activate(id)`）。

**根因不是「多了一次点击」，是这张面板本身是重复的第二套清单**。浮窗 `HomeWindow` 自己就有 tab 条（`HomeWindow.tsx:102` 每个 tab 一个按钮）和 `+`（`:128`）。同一批终端在两个地方各列一遍，而其中一个还挡在另一个前面。

附带发现：面板底部那个 `⌘T`（`HomeDock.tsx:108`）**没有任何键盘实现**——全仓库 `web/src/` 内没有一处 `metaKey` 或 `keydown` 绑到新建终端上。它是印在界面上的一句假话。

### 1.2 注销按钮压在计数上

**用户原话**：「项目的删除按钮还是和状态重叠在一起了，放到右键点击中处理吧，右键点击，弹出菜单，可以删除、编辑。」

**现状**：`ProjectTree.tsx:343-352` 的注销按钮是 `absolute right-2 top-1/2`，而同一行右端 `:341` 渲染 `RowCounts`（三段「目录/运行中/待处理」计数）。两者都要右端，按钮直接盖在计数上。

**上一轮只修了一半**：08-14 修的是「`top-1/2` 以整棵 578px 子树为基准，把按钮甩到列表正中间」，做法是把定位上下文收进机器行（`:319` 的 `group relative`，注释写在 `:316-318`）。**垂直修了，水平没修**——收进机器行之后按钮正确地落在了机器行的垂直中点，然后就和那一行右端的计数正面撞上。

### 1.3 有别的标签页时点不进新标签页

**用户原话**：「中间区域的tab页有bug，如果有别的标签页在，无法跳转到新标签页，点击新标签页就会跳到最近的一个有内容的标签页。」

**根因（已确定，三段链路）**：

1. `tabs.ts:203-210` 的 `setTabContent` 在非去重分支末尾无条件执行 `next.groups[gi].activeId = tabId` 与 `next.active = gi`。**它把「写内容」和「导航过去」焊死成了一件事。**
2. `Shell.tsx:294-302` 把 `FileTab` 的 `onDraftChange` 接成 `wb.setContent(group, tabId, {...})`。
3. `FileTab.tsx:136-138` 在**卸载时**回调 `onDraftChange`——`useEffect(() => () => notifyRef.current?.(draftRef.current), [])`。

于是：用户点空白 tab → `WorkbenchPage` 只渲染 activeTab，`FileTab` 被卸载 → 卸载 cleanup 回写内容 → `setTabContent` 把 `activeId` 拽回那个刚被离开的文件 tab。**用户看到的就是「点了新标签页，又弹回 go.mod」。**

两个加重情节：

- **干净文件照样抢**。`draftRef.current` 为 `null`（不脏）时 `FileTab.tsx:137` 仍然回调，`d` 为 `null` → 仍然写一次 content → 仍然抢焦点。用户截图里的 `go.mod` 未编辑过，照样把焦点拽回去。
- **终端 tab 同一条路**。`Shell.tsx:279` 的 `onSession` 也走 `wb.setContent`。会话建成的那一刻若用户已经切走，焦点会被拽回终端 tab。这条更隐蔽（只在建会话的那一次触发），但根因同一处。

### 1.4 关 home 终端报「机器 [object Object] 未在本机配置的 targets 中定义」

**这条用户没提，是我从他截图里那句红字反查出来的。**

`HomeWindow.tsx:128` 写的是 `onClick={onNew}`。`onNew` 一路传下来是 `dock.newTerminal`（`HomeDock.tsx:59` → `useHomeDock.ts:52` `newTerminal(machine?: string)`）。React 把 `MouseEvent` 当第一个实参传进去，于是 `machine` 是一个合成事件对象，`HomeTab.machine` 存的就是它。

**为什么建会话时看不出来**：`Shell.tsx:335` 渲染 tab 用的是 `base={HOME_BASE}`（machine 恒为 `''`），**根本不读 `t.machine`**。会话在本机正常建起来。

**为什么关的时候炸**：`Shell.tsx:186` 的 `killHomeSession` 把 `tab.machine` 塞进 `closingHome`，`:168` 再 `killPtySession(sessionId, closingHome.machine || undefined)` → `deletePtySession` 拼出 `?machine=[object Object]` → 服务端答「机器 [object Object] 未在本机配置的 targets 中定义」。

**为什么 TypeScript 没拦住**：`HomeWindowProps.onNew` 声明为 `() => void`（`HomeWindow.tsx:26`），而 `(machine?: string) => void` 对 `() => void` 是**合法赋值**（TS 允许把参数更多的函数赋给参数更少的签名，只要多出的参数可选）。类型系统在这里结构上就看不见。

---

## 2. 设计

### 2.1 悬浮入口：删掉中间层

**删掉整张入口面板**，`HomeDock` 收缩成「FAB + 浮窗」两件东西，`panelOpen` 这个 state 消失。

FAB 的 `onClick` 按三条分支：

| 条件 | 行为 | 理由 |
|---|---|---|
| `dock.windowOpen` 为真 | `dock.collapse()` | 浮窗就在眼前时，点悬浮球最可能的意图是收起它。这也给了「收起」第二个入口 |
| 浮窗收起且 `dock.tabs.length === 0` | `dock.newTerminal()` | 用户原话「第一次点击后直接打开一个空白标签页」 |
| 浮窗收起且有 tab | `dock.activate(dock.activeId ?? dock.tabs[dock.tabs.length - 1].id)` | 用户原话「如果有未关的，直接打开对应的」 |

**为什么第三条用 `activeId` 而不是永远取最后一个**：收起浮窗之前你正看着哪个，重开就该是哪个——`collapse()` 刻意不动 `activeId`（`useHomeDock.ts:69-71`）。`?? 最后一个` 只是兜底：`activeId` 唯一可能为 `null` 的情形是 tabs 为空，而那条分支已经在上一行被吃掉了，所以这个兜底实际不会命中，写它是为了不留一个 `null` 进 `activate`。

**角标不变**：FAB 上的存活数角标（`HomeDock.tsx:119-128`）原样保留，它是「收起不杀」这条口径在界面上唯一的证据。

**丢掉了什么，为什么可以丢**：面板提供的是「不打开浮窗就能看见有哪几个终端」。删掉之后这个能力由角标（有几个）+ 浮窗 tab 条（分别是哪几个）承担。第一次点击就能看到 tab 条，比先看一张清单再点一次快。

### 2.2 项目位置操作改右键菜单

**删掉** `ProjectTree.tsx:343-352` 那个 `absolute` 注销按钮。行右端从此只有 `RowCounts`，不再有任何东西和它抢位置。

**机器行加 `onContextMenu`**，弹一个轻量菜单，本期只有一项「注销」，点它进入既有的 `setUnregisterTarget` 确认弹层（`:347` 的既有行为，一字不改）。

**编辑本期不做**（08-15 用户裁决）：后端只有 `POST /api/projects`、`GET /api/projects`、`DELETE /api/projects/{name}`（`server.go:222-224`、`:195`），没有任何改登记的端点。加它是新功能不是修 bug，单开一条 backlog。**菜单里也不放灰掉的「编辑」占位**——一个永远点不动的菜单项只会让人反复去点。

**菜单组件要手写**：`web/src/components/ui/` 只有 `badge / button / card`，没有 dropdown 或 context-menu，全仓库零处 `onContextMenu`。所以本 spec 顺带产出一个最小的 `ContextMenu`：

- `fixed` 定位到 `event.clientX/clientY`，越出视口右/下边界时向内翻转（菜单贴着窗口右缘弹出时会被裁掉一半）
- 关闭条件三个：点菜单项、点菜单外任意处（`pointerdown` 捕获阶段）、按 `Esc`
- 同时只允许存在一个菜单实例（状态挂在 `ProjectTree` 上，不是每行一份）
- `role="menu"` / `role="menuitem"`，打开时焦点落到第一项，`↑↓` 在项间移动

**键盘可达性天然满足**：浏览器把 `Shift+F10` 与 `ContextMenu` 键都派发成 `contextmenu` 事件，所以 `onContextMenu` 一处就同时覆盖鼠标右键和键盘。不需要额外绑快捷键。

**只有机器行加菜单**。项目行与目录行当前没有任何可做的操作，给它们弹一个空菜单等于教用户「这里右键没用」。

### 2.3 内容回写不得抢焦点

**唯一的改动点**：`tabs.ts` 的 `setTabContent`，非去重分支去掉

```
next.groups[gi].activeId = tabId
next.active = gi
```

只留原地替换 `next.groups[gi].tabs[idx] = { id: tabId, content }`。

**语义上这才是对的**：`setTabContent` 是「把这个 tab 的内容换掉」，不是「切到这个 tab」。切 tab 有专门的 `activateTab`。两件事焊在一起，就意味着任何一次后台内容回写都变成一次导航。

**去重分支必须保留 `activateTab`**（`tabs.ts:192-202`）：那一支处理的是「用户在空白 tab 里选的目标已经在别处开着了」——用户刚做了一次选择动作，跳过去是他要的。这一支是导航，不是回写。

**三个调用方逐一核对，确认去掉之后没有一处会退化**：

| 调用方 | 当前是否依赖 `setTabContent` 顺带激活 | 结论 |
|---|---|---|
| `WorkbenchPage.tsx:69` `pick`（空白 tab 选了「终端」） | 否——被操作的就是 `activeTab`，`activeId` 已经等于它 | 安全 |
| `Shell.tsx:279` `onSession`（会话 id 回写） | 否——它本就不该抢焦点，这是 §1.3 的加重情节之一 | 修好了 |
| `Shell.tsx:295` `onDraftChange`（草稿回写） | 否——同上，这正是主症状 | 修好了 |

**不动 `FileTab` 的卸载回写**。看上去「干净文件就别回调了」是个便宜的减噪，但它会引进新 bug：用户把草稿改回原样（`dirty` 变假）后切走，不回调就会把 tab content 里那份**旧草稿**留着，切回来草稿死灰复燃。回写本身是对的，错的是回写顺带改了导航。修根因，不在调用方打补丁。

### 2.4 `onNew` 吞掉 MouseEvent

**改调用点**：`HomeWindow.tsx:128` 的 `onClick={onNew}` 改成 `onClick={() => onNew()}`。

**再堵一层**：`HomeDock` 往下传时写 `onNew={() => dock.newTerminal()}`，让「这里刻意不传 machine」变成显式的一行，而不是靠引用传递碰巧对。

**不在 `useHomeDock.newTerminal` 里加 `typeof machine === 'string'` 兜底**。那是拿运行时判断去掩盖调用方的错，下一个把事件传进来的调用点会被静默吞掉，症状变成「机器参数莫名其妙丢了」，比现在这个当场炸的报错更难查。

**靠测试钉死**：加一条断言「点浮窗 `+` 之后，新建的 `HomeTab.machine` 必须是 `''`」。类型系统在这个形状上结构性地看不见（见 §1.4 末段），只有测试能守住。

---

## 3. 文件影响面

| 文件 | 改动 |
|---|---|
| `web/src/app/homedock/HomeDock.tsx` | 删 `panelOpen` 与整张面板；FAB `onClick` 改三分支；`onNew` 显式包一层 |
| `web/src/app/homedock/HomeWindow.tsx` | `onClick={onNew}` → `onClick={() => onNew()}` |
| `web/src/app/workbench/tabs.ts` | `setTabContent` 非去重分支不再改 `activeId` / `active` |
| `web/src/app/tree/ProjectTree.tsx` | 删 absolute 注销按钮；机器行加 `onContextMenu`；接菜单状态 |
| `web/src/app/tree/ContextMenu.tsx` | **新建**：轻量右键菜单 |
| 对应 `*.test.tsx` / `*.test.ts` | 见 §4 |

---

## 4. 验收判据

每条给一个可观测的判据，不接受「看起来对了」：

1. **悬浮入口**：零终端时点 FAB 一次 → 浮窗出现且已有一个终端 tab（不是先出一张面板）。收起后再点 FAB 一次 → 浮窗带着**收起前那个** tab 重新出现。浮窗开着时点 FAB → 收起。全仓库 `web/src/` 内检索不到 `panelOpen`。
2. **右键菜单**：机器行右端只剩 `RowCounts`，DOM 里该行不再有 `aria-label="注销"` 的常驻按钮。右键机器行 → 菜单出现且含「注销」；点它 → 进入既有确认弹层，文案不变。`Esc`、点菜单外均可关闭。`Shift+F10` 与右键走同一条路。
3. **tab 焦点**：开一个文件 tab（干净的，不编辑）→ 点 `+` 开空白 tab → **焦点留在空白 tab**。编辑过的脏文件重复一遍，焦点同样留住，且切回文件 tab 时草稿还在（回写没被砍掉，只是不再抢焦点）。
4. **[object Object]**：浮窗里点 `+` 新建终端 → 点该 tab 的 `×` → 确认关闭 → 会话真的被删、无报错。DevTools 网络面板里那条 `DELETE /api/pty/sessions/{id}` **不带** `machine` 查询参数。
5. **回归**：`npx vitest run` 全绿、`npm run typecheck` 无错、`npx eslint` 0 error（既有 warning 数量不变）、`vite build` 通过。
6. **变异测试**（沿 B47/B57/B91 先例，防「用例存在但其实不会红」）：把 §2.3 删掉的那两行加回去，第 3 条对应的用例必须 FAIL；把 `onClick={() => onNew()}` 改回 `onClick={onNew}`，第 4 条对应的用例必须 FAIL。

---

## 5. 明确不做

- **项目位置的「编辑」**：后端无接口，08-15 用户裁决单开 backlog。菜单里连占位都不放。
- **⌘T 的真实实现**：随面板一起消失的是那句假话，不是补上功能。要做另开一条。
- **浮窗多开 / 多窗堆叠**：B90 已定案「一个浮窗内含 tab 条」，本 spec 不翻案。
- **项目行、目录行的右键菜单**：无操作可放。
- **`FileTab` 卸载回写的时机优化**：见 §2.3 末段，会引进草稿复活的新 bug。
- **`Shell.tsx:155` 的 `wb.base?.machine`**：关中央终端 tab 时取的是**当前选中基准**的机器而不是**那个 tab 所属基准**的机器。这是同一族的错（拿错了 machine），但分屏 + 切目录才触发，与本轮四条不同源，单记一条 backlog，不塞进来。

---

## 6. 风险

**与 B90 已确认的原型基准冲突**。那张入口面板是 08-14 用户在 `prototypes/desktop-console/` 里当场点过确认的形态，本 spec 把它整个删掉——**这是用户 08-15 的明确翻案，不是实现偏离**。收尾时必须：把原型里的 HomeDock 面板同步删掉，并把 `prototypes/base/README.md` 里该行的确认状态退回「确认中」，等真实页面改完再重新对照确认。不做这一步，原型基准就会永久停在一个用户已经否掉的形态上。

**`setTabContent` 的改动面比看起来广**。它是所有「tab 内容变化」的唯一通道，去掉激活语义会影响到每一个调用方。§2.3 的表格逐个核对过三处调用，但既有测试里可能有断言直接依赖「setContent 之后 activeId 变了」——那类断言要判断是「测试写死了错误行为」还是「真有第四个调用方」，不能一律改绿。
