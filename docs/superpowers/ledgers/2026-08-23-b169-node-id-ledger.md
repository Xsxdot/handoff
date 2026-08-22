# B169 节点编辑器控件 id ledger

## 修改前回归基线

- `cd web && npm ci`：`added 290 packages, and audited 291 packages in 2s`；`found 0 vulnerabilities`。
- `cd web && npx vitest run src/app/flows/NodeEditor.labels.test.tsx` 失败，原始输出：

  ```text
   RUN  v4.1.10 /root/.handoff/worktrees/9cd4defc/web

   ❯ src/app/flows/NodeEditor.labels.test.tsx (1 test | 1 failed) 34ms
       × 中文列名节点的控件 id 不重复，点第二个聚合闸 label 只更新第二个节点 33ms

  ⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯ Failed Tests 1 ⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯

  FAIL  src/app/flows/NodeEditor.labels.test.tsx > NodeEditor 控件 id > 中文列名节点的控件 id 不重复，点第二个聚合闸 label 只更新第二个节点
  AssertionError: expected 5 to be 10 // Object.is equality

  - Expected
  + Received

  - 10
  + 5

   ❯ src/app/flows/NodeEditor.labels.test.tsx:36:39
       34|     const checkboxIds = [...container.querySelectorAll('input[type=che…
       35|       .map((input) => input.id)
       36|     expect(new Set(checkboxIds).size).toBe(checkboxIds.length)
         |                                       ^
       37|     
       38|     const childrenDoneLabels = [...container.querySelectorAll('label')]

  Test Files  1 failed (1)
       Tests  1 failed (1)
  [exit_code=1]
  ```

## Task 1：controlID 改用下标

- 双裁决第 1 轮：spec 符合，`NodeEditor` 显式接收 `index`，控件 id 统一使用 `flow-node-${index}-${suffix}`，`FlowsPage` 传入 map 下标，并加入两个中文列名节点的 id/label 回归用例；没有改布局、字段集合、保存逻辑或移动按钮。代码质量通过，注释说明了列名不能作为稳定键及 `checked` 与 label 关联的边界；无修复轮次。
- 验证：`cd web && npx vitest run src/app/flows/NodeEditor.labels.test.tsx`：1 个文件、1 个用例通过；`git diff --check` 无输出。
- 提交范围：`HEAD^..HEAD`（Task 1 提交）。

## Task 2：既有测试补 prop

- 双裁决第 1 轮：spec 符合，`NodeEditor.test.tsx` 的 4 个实例与 `NodeEditor.childrengate.test.tsx` 的 2 个实例均补 `index={0}`；`NodeEditor.purpose.test.tsx` 不在本分支，未新增或修改其它断言。代码质量通过，改动仅为所需 prop，无修复轮次。
- 验证：`cd web && npx vitest run src/app/flows/NodeEditor.test.tsx src/app/flows/NodeEditor.childrengate.test.tsx`：2 个文件、5 个用例通过；`git diff --check` 无输出。
- 提交范围：`HEAD^..HEAD`（Task 2 提交）。
