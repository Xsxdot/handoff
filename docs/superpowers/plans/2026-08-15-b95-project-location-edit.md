# 执行纪律（先读这段，再读 plan）

你收到的是一份完整实现计划。用你自己的 subagent 机制按以下纪律执行，不要单上下文从头写到尾：

1. 逐 task 派全新 subagent 实现。每个 subagent 只给三样东西：该 task 的完整需求原文（含精确值、签名、测试用例）、它要接触的接口、全局约束。不要把会话历史或前序 task 总结灌进去。
2. 实现 subagent 不并行（避免改动冲突）。
3. 每个 task 完成后，派一个独立审查 subagent 做双裁决：spec 符合性（要求全实现、没有多做）+ 代码质量。输入是该 task 的需求原文 + 完整 diff。缺任一裁决不算过。
4. 审查不过进修复回路：一轮 = 一次修复 + 一次只看修复 diff 的复审，最多 5 轮。前 3 轮回原实现者，4-5 轮换全新实现者接手。5 轮后仍有未决项：非承重的记账搁置；承重的（后续 task 依赖它、或暴露 plan 缺陷）停下上报 BLOCKED。
5. 进度落盘到 ledger 文件：每 task 完成、每轮修复各追加一行，含 commit 范围。恢复现场以 ledger + git log 为准，不信记忆。
6. Minor 发现记账不进回路，留给终审统一 triage。
7. 全部 task 完成后做一次整分支终审（相对分支起点的完整 diff）。有发现项就一次性派一个修复 subagent 全量修，再做一次范围复审；不搞逐项派发，也没有第二轮修复波。
8. 协调上下文保持干净：你自己不亲自改代码，所有改动经 subagent 产出且经审查。
9. 每个 task 完成即 commit，提交信息说清做了什么。
10. 不停下来问「要不要继续」。只在 BLOCKED、真歧义、全部完成三种情况停；需求取舍拿不准就发工单问，等审核者裁决。

---

# B95 实现计划：项目位置的「编辑」

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给项目位置加一个 `PATCH /api/projects/{name}` 端点与配套前端，能改**引用名**与 **path**，本机与远程开发机都能改。

**Architecture:** 不动数据模型。`project_locations` 的主键 `project_id` 由 origin URL 算出、改名改路径都不变；`name` 只是带 UNIQUE 约束的引用名。改 path 时必须校验新目录算出**同一个 project_id**，否则拒绝。远程位置靠把 PATCH 打到那台机器的 agentd 实现，复用现有扇出通道。

**Tech Stack:** Go 1.26 + SQLite；前端 `web/`（Vite + TS + vitest）。

**设计依据：** `docs/superpowers/specs/2026-08-15-b95-project-location-edit-design.md`
（**开工前完整读一遍**，尤其 §1 的身份模型纠正与 §3.2 的承重不变量）。

**形态基准：** `prototypes/b95-project-edit/`（若该目录不在你的工作树里——它是 gitignore 的——就按 spec §3.6 的文字实现，不要自己另发明形态）。

## Global Constraints

- **不改数据模型、不加新表、不动 `project_id` 的算法。**
- **不回溯改写已有任务/工作树记录的路径**（spec §3.4）。
- **不做「换机器」**：机器维度不可编辑。
- 名字合法性校验**必须复用登记时那一套**，不得另写一份。
- 冲突（撞名）必须复用 `store.ErrProjectDuplicate` 哨兵与它现有的 409 映射。
- **两边已有的日志调用一条都不能丢**；新增分支按 `instrumenting-code` 补日志（进入端点带 name、改动字段；每个错误分支带上下文与 cause；成功路径打一条含新旧值的 Info——**但 path 之外的敏感值不打**）。
- 提交信息前缀 `feat(b95):` / `fix(b95):`。
- **不合并进任何长期分支，不 `git push` 到 `w4-delivery`/`main`**，只交你自己的任务分支。
- **不动 `~/.handoff`**（正在服役的数据目录）。冒烟一律用临时 datadir + 临时端口，用完删掉。
- 本次不做 B96/B100/B101/B105。

---

### Task 1: store 层的 `UpdateProjectLocation`

**Files:**
- Modify: `internal/store/projects.go`
- Test: `internal/store/projects_test.go`

**Interfaces:**
- Produces:
  ```go
  // UpdateProjectLocation 改一条位置登记的引用名与/或路径。
  //
  // 参数：
  //   - name: 当前引用名（定位用）
  //   - newName: 新引用名；空串表示不改
  //   - newPath: 新路径；空串表示不改
  //
  // 返回：更新后的记录；
  //   - ErrNotFound：name 不存在
  //   - ErrProjectDuplicate：新名字或新路径已被占用
  func (s *Store) UpdateProjectLocation(name, newName, newPath string) (proto.ProjectLocation, error)
  ```

- [ ] **Step 1: 写失败的测试**

沿用该文件既有的建库/建表桩写法（照抄邻近用例，不要新造框架）。四条：

```go
// TestUpdateProjectLocationRenames 改名成功，且 project_id 不变——
// 身份由 origin 算出，改名不许动它，否则任务与工作树会与项目失联。
func TestUpdateProjectLocationRenames(t *testing.T) { /* … */ }

// TestUpdateProjectLocationChangesPath 改路径成功，project_id 同样不变。
func TestUpdateProjectLocationChangesPath(t *testing.T) { /* … */ }

// TestUpdateProjectLocationRejectsDuplicateName 新名字已被别的位置占用 →
// ErrProjectDuplicate（上层映射 409）。
func TestUpdateProjectLocationRejectsDuplicateName(t *testing.T) { /* … */ }

// TestUpdateProjectLocationNotFound 不存在的名字 → ErrNotFound。
func TestUpdateProjectLocationNotFound(t *testing.T) { /* … */ }
```

每条都要断言 `project_id` 前后一致。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -count=1 -run TestUpdateProjectLocation ./internal/store/`
Expected: 编译失败（方法不存在）。

- [ ] **Step 3: 实现**

要点：
- 两个字段都为空 → 直接返回当前记录，不发 SQL（这是合法的空操作，不是错误）；
- UPDATE 语句只更新非空字段；
- UNIQUE / PRIMARY KEY 冲突翻成 `ErrProjectDuplicate`，**复用文件里已有的那段
  `strings.Contains(err.Error(), "UNIQUE constraint failed")` 判断**，不要另写；
- 影响行数为 0 → `ErrNotFound`；
- 按本文件的叶子层纪律：**return 前不打日志**，由调用方带上下文记录。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -count=1 ./internal/store/`
Expected: 全 PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(b95): store 层加 UpdateProjectLocation，改名改路径不动 project_id"
```

---

### Task 2: `PATCH /api/projects/{name}` 端点与承重不变量

**Files:**
- Modify: `internal/agentd/server.go`（注册路由）、`internal/agentd/projectadmin.go`（handler）
- Test: `internal/agentd/projectadmin_test.go`

**Interfaces:**
- Consumes: `store.UpdateProjectLocation`（Task 1）
- Produces: `PATCH /api/projects/{name}`，请求体 `{"new_name":"…","path":"…"}`（均可选），响应 `proto.ProjectLocation`

- [ ] **Step 1: 写失败的测试**

```go
// TestProjectPatchRenames 改名走通，响应里 project_id 不变。
// TestProjectPatchRejectsDifferentOrigin 是本 task 的正身：
//   把 path 改到一个 origin 不同的仓库 → 400，且报文说明「那是另一个项目」。
//   没有这条校验，「编辑 path」就成了一条不声不响把登记指向另一个仓库的路径：
//   project_id 还是旧的，磁盘上却是别的项目——比不给编辑危险得多。
// TestProjectPatchDuplicateName 撞名 → 409。
// TestProjectPatchEmptyBody 两个字段都空 → 400。
// TestProjectPatchNotFound 不存在的名字 → 404。
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -count=1 -run TestProjectPatch ./internal/agentd/`
Expected: FAIL（路由不存在，404）。

- [ ] **Step 3: 实现 handler**

顺序（**不许调换**）：

1. 解析 body；两个字段都空 → 400；
2. 按 name 取当前记录；不存在 → 404；
3. 若要改 name：用**登记时那套**名字校验（在 `projectadmin.go` 里找现成的，
   复用它）；不合法 → 400；
4. 若要改 path：对新目录做与登记同款的 `EnsureRepoUsable`，取 origin，
   算 `projectid.FromOrigin(origin)`；
   - 与当前 `project_id` **不同** → **400**，报文：
     `该目录是另一个项目（origin 不同），请注销后重新添加`；
   - 相同 → 继续；
5. 调 `store.UpdateProjectLocation`；`ErrProjectDuplicate` → 409，`ErrNotFound` → 404；
6. 返回更新后的记录。

日志：进入时 Info（name + 要改哪些字段）；每个拒绝分支 Warn/Error 带原因；
成功 Info 带新旧 name 与新旧 path。

- [ ] **Step 4: 注册路由**

在 `server.go` 已有的 projects 路由旁边加：

```go
	mux.HandleFunc("PATCH /api/projects/{name}", s.handleProjectPatch)
```

并把文件顶部那段端点清单注释一并补上一行（那份清单是给人看的目录，漏了就过时了）。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test -count=1 ./internal/agentd/`
Expected: 全 PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/
git commit -m "feat(b95): PATCH /api/projects/{name}，改 path 必须算出同一个 project_id"
```

---

### Task 3: CLI 侧 `handoff project edit`

**Files:**
- Modify: `internal/client/client.go`（加 `PatchProject`）、`cmd/project.go`
- Test: `cmd/project_test.go`（沿用该包既有写法）

**Interfaces:**
- Consumes: `PATCH /api/projects/{name}`（Task 2）
- Produces: `func (c *Client) PatchProject(ctx context.Context, name, newName, path string) (proto.ProjectLocation, error)`

**为什么要做这条**：控制台能做而 CLI 做不到的事，会让「一切经 CLI」这条铁律
出现例外——那条铁律是 handoff 的地基，不能为了省事在它上面开洞。

- [ ] **Step 1: 写失败的测试**

断言：`handoff project edit <name> --name <new> --path <p> [--target <machine>]`
把请求打到正确的 URL 与 body；缺两个可选参数时报错并提示至少给一个。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -count=1 -run TestProjectEdit ./cmd/`

- [ ] **Step 3: 实现**

`--target` 的解析**复用该文件里既有的 target 解析**，不要另写。

- [ ] **Step 4: 跑测试确认通过 + 全包回归**

Run: `go test -count=1 ./cmd/ ./internal/client/`

- [ ] **Step 5: Commit**

```bash
git add cmd/ internal/client/
git commit -m "feat(b95): handoff project edit 子命令"
```

---

### Task 4: 前端 API 与编辑弹层

**Files:**
- Modify: `web/src/api/client.ts`（加 `patchProject`）
- Create: 编辑弹层组件（放在项目树相邻目录，命名随该目录既有风格）
- Modify: 项目树组件（右键菜单加「编辑」）
- Test: 对应的 `*.test.tsx`

**Interfaces:**
- Consumes: `PATCH /api/projects/{name}`
- Produces: `patchProject(name, body: {new_name?: string; path?: string}, machine?: string)`

**形态按 spec §3.6**：右键菜单「编辑 / 注销」；弹层复用「添加项目」第二步的词汇
（location tabs、本机访达选择器、远程只能粘贴 path）；机器维度不可编辑并给出理由；
底部「本次改动」只列真的变了的字段、每条带后果说明；无改动时保存禁用。

- [ ] **Step 1: 写失败的测试**

```
- 右键项目行 → 菜单出现且含「编辑」「注销」两项
- 点「编辑」→ 弹层出现，显示名与各 location 的 path 预填当前值
- 改一个字段 → 「本次改动」列出 1 项；改回原值 → 回到 0 项且保存禁用
- 保存 → 对**每个有改动的 location** 各发一次 patchProject，本机不带 machine、
  远程带 machine
- 某台失败 → 弹层逐条列出每台结果，成功的那台**不回滚**（spec §3.5）
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run <新测试文件>`

- [ ] **Step 3: 实现**

- [ ] **Step 4: 三件套**

Run: `cd web && npx vitest run && npx tsc -b && npx vite build`
Expected: 0 error。

- [ ] **Step 5: Commit**

```bash
git add web/
git commit -m "feat(b95): 项目树右键编辑与编辑弹层，逐 location 提交并如实呈现部分成功"
```

---

### Task 5: 端到端冒烟与 ledger

**Files:**
- Create: `docs/superpowers/notes/2026-08-15-b95-ledger.md`

- [ ] **Step 1: 起临时 agentd 冒烟**

**临时 datadir + 临时端口，不要动 `~/.handoff`。** 步骤：
登记一个项目 → `PATCH` 改名 → `GET /api/projects` 确认新名字与 **project_id 未变**
→ `PATCH` 改回原名 → 确认与初始一致 → 停进程、删临时目录。

把每一步的请求与响应原文贴进 ledger。

- [ ] **Step 2: 全量回归**

Run: `go build ./... && go vet ./... && go test -count=1 ./...`
Expected: 0 FAIL。

- [ ] **Step 3: 在 ledger 里如实记下没做的部分**

至少要写清：跨机部分成功只做了「如实呈现」没有做事务（spec §3.5 的既定取舍）；
已有任务/工作树的路径没有回溯改写（§3.4）。

- [ ] **Step 4: Commit**

```bash
git add docs/superpowers/notes/
git commit -m "docs(b95): 端到端冒烟记录与 ledger"
```
