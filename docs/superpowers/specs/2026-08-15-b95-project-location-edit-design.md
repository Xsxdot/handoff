# B95 设计：项目位置的「编辑」——改引用名与目录

**日期**：2026-08-15
**基线**：`w4-delivery`
**形态原型**：`prototypes/b95-project-edit/`（fork 自 `desktop-console`，未入库）

---

## 1. 先纠正一个前提：身份与显示名**本来就是分开的**

我原先在 backlog 里写「改名会让引用旧名的脚本失效，因为 name 是登记主键」，
并把「显示名与主键拆成两个字段」列为一个待决选项。**查代码之后这个前提是错的**：

`internal/store/store.go:132-134`

```
-- project_id 做主键：ADR-0008 的「一台机器上一个项目最多一个位置」由它保证
project_id TEXT PRIMARY KEY,
```

`project_locations` 的列是 `project_id, name, path, origin_url, created_at`，
其中 **`project_id` 由 `projectid.FromOrigin(originURL)` 算出**（`internal/projectid/projectid.go:107`），
**`name` 只是一个带 UNIQUE 约束的引用名**。

所以：**改名不动身份**。任务、工作树、扇出全部按 `project_id` 关联，改 `name`
不会让它们失联。用户裁决的「拆成两个字段」这件事，现有数据模型已经做到了——
本次不需要动数据模型，只需要加一个改写端点。

唯一真实存在的代价是：`DELETE /api/projects/{name}` 这类**按名字寻址的 URL**
在改名后会指向不存在的名字。这是引用名本身的性质，不是缺陷。

## 2. 范围

用户 08-15 定的范围：可改**引用名**与 **path**，**本机与远程开发机都要能改**。
机器维度不可改（换机器 = 另一份代码副本 = 注销后重新添加）。

## 3. 方案

### 3.1 新增 `PATCH /api/projects/{name}`

请求体（两个字段都可选，但不能都为空）：

```json
{ "new_name": "handoff-lab", "path": "/Users/me/workspace/handoff" }
```

响应：更新后的 `proto.ProjectLocation`。

现有端点一个不动（`POST` 登记 / `GET` 列表 / `GET /tree` / `DELETE /{name}` 注销）。

### 3.2 承重不变量：**新 path 必须算出同一个 `project_id`**

改 path 时，服务端要对新目录做与登记时同样的检查（`EnsureRepoUsable`），
取出它的 origin，算 `projectid.FromOrigin`，然后：

- **算出来与当前 `project_id` 相同** → 放行（这是「同一个项目换了个目录」）；
- **不同** → **400 拒绝**，报文写明「该目录是另一个项目（origin 不同），
  请注销后重新添加」。

**为什么这条是承重的**：没有它，「编辑 path」就变成了一条不声不响把一条登记
指向另一个仓库的路径——`project_id` 还是旧的，而磁盘上是另一个项目。那比不给编辑
危险得多。有了它，编辑 path 的语义被收紧成「同一个项目搬了家」，与身份模型自洽。

### 3.3 改名的冲突处理

`name` 有 UNIQUE 约束。新名字已被占用 → **409**，复用现有的 `ErrProjectDuplicate`
哨兵与它的映射。新名字与旧名字相同 → 当作没改这个字段，不报错。

名字的合法性校验**复用登记时那一套**（`agentd/projectadmin.go` 的名字派生/校验），
不要另写一份——两处规则不一致，迟早出现「能建不能改」的名字。

### 3.4 已有任务与工作树：只影响将来，不回溯

改 path 之后，**已登记的任务/工作树仍然记着旧路径，本次不动它们**。
理由：那些是历史事实（任务确实是在那个目录里跑的），回溯改写等于篡改审计。
新的派发用新路径。

**这条必须在 UI 上说出来**（原型已经做了：改动摘要里写「该位置已登记 N 个工作树，
它们仍指向旧目录，需要在保存后逐个确认」）。

### 3.5 远程位置怎么改

`project_locations` 是**每台机器各自一张表**。改远程机器上的位置，就是把
`PATCH` 打到**那台机器的 agentd**——与项目树扇出（`projectfanout.go`）同一条路，
复用它的 target 解析与鉴权，不要新造通道。

前端在编辑弹层里按 location 分栏，保存时**逐个 location 各发一次 PATCH**：
本机的打本机，远程的打远程。**部分成功要如实呈现**——某一台失败时，
成功的那台不回滚（跨机事务不存在），弹层里逐条列出每台的结果。

### 3.6 前端形态

按 `prototypes/b95-project-edit/` 已确认的形态实现：

- 入口：**项目行右键** → 菜单「编辑 / 注销」（注销沿用 B94 已有实现）；
- 弹层复用「添加项目」第二步的词汇：location tabs、本机用访达选择器、
  远程只能粘贴 path；
- 机器维度不可编辑，并给出理由文案；
- 底部「本次改动」只列真的变了的字段，每条带后果说明；无改动时保存按钮禁用。

## 4. 影响面

| 文件 | 改动 |
|---|---|
| `internal/store/projects.go` | 新增 `UpdateProjectLocation`（改 name / path），冲突翻 `ErrProjectDuplicate` |
| `internal/agentd/server.go` | 注册 `PATCH /api/projects/{name}` |
| `internal/agentd/projectadmin.go` | handler：校验、同 project_id 不变量、复用名字校验 |
| `internal/client/client.go` + `cmd/` | CLI 侧（`handoff project edit`，可选但建议一并做，否则控制台能做的事 CLI 做不了） |
| `web/src/api/client.ts` | `patchProject(name, body, machine?)` |
| `web/src/app/shell/`（项目树右键菜单）+ 新建编辑弹层组件 | 按原型实现 |

## 5. 风险

**跨机部分成功**是本设计唯一无法消除的粗糙面（§3.5）。不要用「先试再全提交」
去伪装成事务——两台机器上没有共享事务，装出来的原子性比诚实的逐条结果更危险。

**`EnsureRepoUsable` 在远程是有代价的**（可能触发 fetch）。PATCH 的超时要与
登记端点一致，不要另设一个更短的。

## 6. 验收

1. `go build/vet/test ./...` 全绿；`web/` 三件套全绿。
2. **服务端单测**：改名成功 / 改名撞名 409 / 改 path 成功 / **改 path 指向
   另一个 origin 被 400 拒绝**（这条是承重不变量）/ 两个字段都空 400 /
   不存在的名字 404。
3. **`project_id` 在改名与改 path 后都不变**——单独一条用例断言它。
4. **前端**：右键出菜单、编辑弹层按原型呈现、改动摘要只列变化项、
   无改动时保存禁用、部分成功时逐条列出每台结果。
5. 端到端冒烟（临时 datadir + 临时端口）：`PATCH` 一次改名再改回来，
   `GET /api/projects` 前后一致。**不要动 `~/.handoff`。**

## 7. 明确不做

- 不改数据模型、不加新表、不动 `project_id` 的算法。
- 不做「换机器」——那是注销后重新添加。
- 不回溯改写已有任务/工作树记录的路径（§3.4）。
- 不做批量编辑、不做撤销。
