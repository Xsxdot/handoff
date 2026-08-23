# B169 实现计划：节点编辑器的控件 id 不许由列名生成

> 2026-08-22。协调者写。卡：B169（中）。
> **你的工作分支从 main 起，看不到 B183/B182 那批改动，这是预期的**：卡的基线
> 建卡时定死、事后不可改。B183 那批（给 NodeEditor 加了「用途」下拉与「不注入
> 验收判据」复选框）只在协调者的集中分支上，合并冲突由协调者在合并侧收口。
>
> 对你的影响只有一条：**按你看到的文件改，不要去猜那两个控件的存在**。
> 下面提到的 `NodeEditor.purpose.test.tsx` 在你的分支上不存在，跳过它即可。

## 事实基线（协调者在 985f37135 上查证）

```tsx
function controlID(name: string, suffix: string): string {
  const safe = name.replace(/[^a-zA-Z0-9_-]+/g, '-') || 'node'
  return `flow-node-${safe}-${suffix}`
}
```

两个缺陷叠在一起：

1. **中文列名整段塌缩成一个 `-`**：`待办`/`集成`/`已完成` 都得到
   `flow-node---<suffix>`，同一页上多列的控件 id 完全相同。于是 label 的 `for`
   走 `getElementById` 只命中**第一个**——点第三列的开关，翻的是第一列的框。
   中文列名是常态（骨架锚点本身就是待办/进行中/待审阅/已完成），所以**基本每条
   工作流的每个复选框都错关联**。
2. **`|| 'node'` 兜底永不触发**：`"-"` 是真值不是空串。

调用点已经有现成的稳定键：`web/src/app/flows/FlowsPage.tsx:138`
`nodes.map((node, index) => ...)`，`index` 就在手边。

**排查时容易误判的一点（写进注释，别让下一个人再踩）**：直接读复选框的 `checked`
是**对的**（与保存的定义一致），坏的只有 label→input 关联。用 label 的 `for` 去
`getElementById` 只会命中第一个，据此会得出「开关没回显」的错误结论。

## 设计决定

1. **id 用节点下标当稳定键**（`flow-node-${index}-${suffix}`），不再依赖列名。
   列名是用户可改、可重名、可为任意语言的显示文本，本就不该承担唯一键的职责。
2. **`index` 由调用方显式传入**，不在组件内部凭列名推。组件不持有节点数组
   （文件头注释已声明这条边界），下标只能来自上层。
3. **保留空值兜底但修对判断**：slug 全是分隔符时也算空，回落 `node`。
   这条在 index 方案下已经用不上了，但 `controlID` 仍是导出面之外的公共小函数，
   留一个正确的兜底比留一个假的强。

## Task 1：controlID 改用下标

`web/src/app/flows/NodeEditor.tsx`：

```tsx
// 控件 id 用节点下标当稳定键，不用列名——列名是用户可改、可重名、可为任意
// 语言的显示文本。曾经用 name.replace(...) 造 id，中文列名整段塌缩成一个
// `-`，同一页上「待办/集成/已完成」三列拿到同一个 id，label 的 for 走
// getElementById 只命中第一个：点第三列的开关翻的是第一列的框（B169）。
//
// 注意别据此误判「开关没回显」：坏的只有 label→input 关联，复选框自己的
// checked 一直是对的。
function controlID(index: number, suffix: string): string {
  return `flow-node-${index}-${suffix}`
}
```

`NodeEditorProps` 增 `index: number`；组件内
`const id = (suffix: string) => controlID(index, suffix)`。

`web/src/app/flows/FlowsPage.tsx` 的 `<NodeEditor ... />` 增 `index={index}`
（`index` 已在 `nodes.map((node, index) =>` 里）。

## Task 2：既有测试补 prop

`NodeEditor.test.tsx`、`NodeEditor.childrengate.test.tsx`，以及 B183 那批新增的
`NodeEditor.purpose.test.tsx`（若已在分支上）都要补 `index={0}`。
**这些文件的断言一个字都不许改**——它们是本次改动的回归网。

## 测试映射

新增 `web/src/app/flows/NodeEditor.labels.test.tsx`：**同时渲染两个中文列名的
节点**（index 0 与 1，如「待办」与「集成」，两个都开 `dispatch`），断言：

1. 两个节点的同名控件 id 不相同（用 `container.querySelectorAll('input[type=checkbox]')`
   取 id 集合，断言无重复）。
2. 点**第二个**节点的「需全部子卡完结」label，只有第二个节点的 `onChange` 被调用
   ——这是本卡的核心判据，修前必红（会打到第一个）。

执行者必须先在**修改前**跑一次这条用例并把红色报文抄进 ledger。

## 测试范围

- `cd web && npx vitest run src/app/flows/`
- `npx tsc -b`、`npx eslint src/app/flows/NodeEditor.tsx src/app/flows/FlowsPage.tsx`
- **不跑 Go 侧**：本卡零 Go 改动。

## 不属于本次

- 不改节点编辑器的任何布局、样式或字段集合。
- 不动 `FlowsPage` 的保存逻辑与上移/下移按钮。
- jsdom 看不见布局，本卡也不涉及布局——不要顺手加视觉断言。
