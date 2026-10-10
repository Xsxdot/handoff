# B431 台账

阶段状态：**代码与自动化通过**。分支 `cards/B431-charter`，被测源码见下。

## 2026-10-10 实现节点（实现者亲跑）

### 基线

- `git status` → 干净；`git branch --show-current` → `cards/B431-charter`；HEAD `8d4223ef docs(B431): L1 spec+plan`。
- `web/` 无 node_modules；`npm ci` 装依赖成功。
- 基线测试：`npx vitest run src/app/tree/ProjectTree.test.tsx` → `109 passed (109)`。

### 根因定位（spec 声明路径 + codegraph/grep）

- 图命中：`ContextMenuItem` → `web/src/app/shared/ContextMenu.tsx:15`（domain d_web_shell）；`ContextMenu` func 同文件。
- `locationMenus` 为 React 本地 state，图未命中（**图覆盖债**：组件内部局部符号不在图中），按规则回落 grep：`ProjectTree.tsx:403` 声明、`:728` `locationsVisible = compact || locationMenus.has(pKey) || searching`、`:1549` 管理工作树只 `Set.add` 无对称收起。终端菜单机器行（`:1535-1546`）无 draggable/onDragStart；侧栏机器行（`:1216-1222`）已有 `DRAG_DIR_MIME`+`DRAG_BASE_MIME` 载荷（主工作区 base）。

### TDD 首红（断言红，3 条新用例）

`ProjectTree.test.tsx` 新增 describe「B431 管理工作树 toggle 与终端菜单机器行拖拽」。首跑：

- toggle 用例：`AssertionError: expected <div data-testid="directory-group">… to be null`——二次点「管理工作树…」目录组仍在（只进不出）。
- MIME 用例：`toHaveAttribute("draggable","true")` → Received `null`。
- 离线用例：`toHaveAttribute("draggable","false")` → Received `null`。
- 汇总：`Tests 3 failed | 109 passed (112)`。三条均为功能缺失断言红，非 typo。

### 实现

- `ContextMenu.tsx`：`ContextMenuItem` 增 `draggable?` / `onDragStart?`；按钮挂 `draggable`/`onDragStart`，`onDragEnd` 关菜单（dragstart 不关——关了会卸掉 drag source）。
- `ProjectTree.tsx`：「管理工作树…」改 toggle（有 key 删、无 key 加，`desktopDisclosure` 仍置 true 保持展开行为）；终端菜单机器行挂 `draggable: base !== null && problem === ''` 与同侧栏的 MIME 载荷，`console.debug` 增 `open` 字段。
- 首次 typecheck 报 `ProjectTree.tsx(1542,77): error TS7006: Parameter 'e' implicitly has an 'any' type`（map 返回值无上下文类型）——为 onDragStart 参数显式标注 `DragEvent<HTMLButtonElement>`（react import 增 `type DragEvent`）后消除。

### 绿灯与集成

- `npx vitest run src/app/tree/ProjectTree.test.tsx src/app/shared/ContextMenu.test.tsx` → `122 passed (122)`。
- `npx vitest run src/app/shell/Shell.test.tsx` → `131 passed (131)`。
- `npm run typecheck`（tsc -b）→ 通过，无输出错误。
- 集成全量 `npm test` → `Test Files 144 passed (144)`、`Tests 1849 passed (1849)`。
- `npx eslint`（三个触及文件）→ `0 errors, 7 warnings`（均为 ProjectTree.tsx 既有的 react-refresh only-export-components 警告，与本卡无关）。

### 变异自验（四发全被杀；每发先断言锚点唯一，编译过再数红）

| 变异 | 锚点计数 | 编译 | 结果 |
|---|---|---|---|
| M1 toggle 取反（open 删/关 加） | 1 | 通过 | `Tests 28 failed | 84 passed`——toggle 有牙 |
| M2 onDragStart 守卫取反（`!==`→`===`） | 1 | tsc -b 通过 | `Tests 2 failed`（MIME + 离线用例）——守卫有牙 |
| M3 draggable 条件取反 | 1 | 通过 | `Tests 2 failed`（MIME + 离线用例）——draggable 有牙 |
| M4 ContextMenu dragend 不关菜单 | 1 | 通过 | `Tests 1 failed`（dragend 关菜单断言）——有牙 |

变异后均从 `$TMPDIR` 备份恢复并复跑：`122 passed`、typecheck 通过。

### 备注

- 未改 `paneDrop` 消费端；未碰 B430 / 契约面；本机无浏览器真机点验，右缘分屏的端到端停靠由 Shell/WorkbenchPage 既有 paneDrop 测试覆盖（`DRAG_DIR_MIME`+`DRAG_BASE_MIME` → 右缘 drop 开右栏），菜单行与侧栏行同载荷。
- 未 push、未操作 handoff CLI、未起子任务。
