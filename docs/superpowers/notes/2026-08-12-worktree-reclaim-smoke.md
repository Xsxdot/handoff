# 2026-08-12 worktree 回收真机烟测记录（B77 / Plan B77）

> 记录人：1aaacf5f 任务执行者。本机为 macOS（darwin）。**铁律遵守**：全程未触碰
> 生产 agentd（127.0.0.1:7777，pid 22072 前后一致）与 `~/.handoff`；隔离实例独立
> 端口 7897 + 独立 DataDir，devbox 不可达，原计划「对账 devbox 那 15 个 failed」改为
> 在隔离实例上复刻等价场景对账（见 §5.2 的说明）。

## 0. 结论速览

- `handoff reclaim` 无参列表与 `git worktree list --porcelain` **逐条对账一致**：3 个
  残留（脏×2 + prunable×1）全部入表，2 个「failed/completed 但 managed 字段未回写、
  实际无残留」的任务与 1 个非终态任务全部不入表——**顺带实证了「worktree_managed=true
  不代表有残留」**（B77 spec §1.3 的推断，隔离实例上 T2/T4 两例实锤）。
- 回收动作五条路径全部实测通过：脏树拒绝 409+清单+树保留、`--force` 强删+丢弃清单、
  幂等 `already_absent` 退 0、prunable 直接 remove 成功、非终态 409 且树保留。
- 分支保留纪律实测：回收后 `git branch --list handoff/*` 六个任务分支全部仍在；
  且在非 bare 远端上复刻了 2c58bbb7 原症状（`branch is currently checked out` 拒绝），
  残留被移除后 `push --delete` **放行**。
- **真机烟测照出一个真实代码缺陷并已修复**：`client.Reclaim` 把预编码的
  `bytes.NewReader(body)` 传给 `c.do`，而 `c.do` 会对 body 再 `json.Marshal` 一次——
  `bytes.Reader` 无导出字段，序列化成 `{}`，force 悄悄变 false，**CLI 的 `--force`
  永远不生效**（curl 直打 force=true 却有效，把缺陷锁定在客户端）。已改为传
  `map[string]bool{"force": force}`（与 Reply 一致），补回归用例
  `TestReclaimForceCarriesIntoRequestBody`，提交 `98e685d`。
- 变异检验 6 条，其中**第 3 条预期 FAIL 未出现**（计划指定的
  `TestReclaimRefusesWhenRepoUnreachable` 删的是仓库、命中 `repoWorktrees` 失败路径，
  到不了 `WorktreeUnknown` 分支）——按 B72 变异 4 先例当场补用例
  `TestReclaimRefusesWhenWorktreeUnreadable`（弄坏工作树 gitdir 的 index 逼出
  Unknown 态）后确认 FAIL，提交 `eaaef38`。

## 1. 隔离实例参数（不占生产）

| 项 | 值 |
|---|---|
| 独立二进制 | `go build -o /tmp/opencode/handoff-b77 ./`（feat/b77-worktree-reclaim 工作树，含 Task 1-9 全部提交） |
| 独立端口 | `127.0.0.1:7897`（生产 7777 的 agentd pid 22072 前后一致，未动） |
| 独立 DataDir | `/tmp/opencode/handoff-b77-data`（tasks/ 独立于 `~/.handoff`） |
| 独立仓库 | `/tmp/opencode/handoff-b77-repo`（git init + 1 commit + 假 origin）；另备非 bare 克隆 `/tmp/opencode/handoff-b77-devbox` 复刻 devbox 侧症状 |
| 配置 | `/tmp/opencode/handoff-b77-config.yaml`（listen 7897 / token / datadir / `executor.default: fake`） |
| 实例生命周期 | 建任务→stop→停机补 seeded 状态→重启→回收，全程 `pkill -f "handoff-b77 agentd --config …"` 精确匹配隔离二进制+配置，不可能误伤生产 |
| 执行者 | `fake`（不拉真模型进程；fake 无脚本时任务停在 running，`done` 需 waiting_review 故 completed 任务用停机窗口直接落库，见 §5.1） |

## 2. 六闸门实际输出（Task 10 Step 1）

```
$ go build ./...                      # 无输出，退出 0
$ go vet ./...                        # 无输出，退出 0
$ gofmt -l .                          # 无输出
$ go test ./... -count=1              # 27 个包全部 ok，无 FAIL
$ go test -race ./internal/agentd/ ./internal/client/ ./cmd/
  ok  github.com/xushixin/handoff/internal/agentd   43.7s
  ok  github.com/xushixin/handoff/internal/client   10.7s
  ok  github.com/xushixin/handoff/cmd                2.8s
$ GOOS=windows go build ./...         # 无输出，退出 0
```

修复 `98e685d` 后六闸门**全量重跑一遍**（含 `-race` 三包与交叉编译），依旧全绿。

## 3. 变异检验（Task 10 Step 2，六条）

每条：改码 → 跑指定用例确认 FAIL → `git checkout -- <file>` 还原 →
`git diff --exit-code` 干净。实际输出摘要：

| # | 变异 | 指定用例 | 结果 |
|---|---|---|---|
| 1 | `classifyWorktree` 脏判据过滤掉 `??` 行 | `TestClassifyUntrackedOnlyIsDirty` | FAIL ✓（`实得 clean`） |
| 2 | `Reclaim` 的 `if !force` 改成无条件放行 | `TestReclaimRefusesDirtyWithoutForce` | FAIL ✓（`实得 git worktree remove … contains modified or untracked files`） |
| 3 | `Reclaim` 的 `WorktreeUnknown` 分支改走 `already_absent` | `TestReclaimRefusesWhenRepoUnreachable` | **预期 FAIL 未出现（用例缺陷）**：该用例删的是仓库，在 `repoWorktrees` 就返回 `ErrReclaimRepoUnreachable`，到不了 Unknown 分支——这条变异原本没有用例盯着。当场补 `TestReclaimRefusesWhenWorktreeUnreadable`（把工作树 gitdir 里的 index 写坏 → git status 读不出 → Unknown），重跑变异 → FAIL ✓（`实得 <nil>`，即静默成功）。补丁提交 `eaaef38` |
| 4 | `ReclaimList` 的 `entries == nil` 分支改 `continue` | `TestReclaimListDegradesPerRepo` | FAIL ✓（`不可达仓库的行必须标 unknown 而不是消失`） |
| 5 | `canonPath` 删掉父目录退让段 | `TestCanonPathResolvesMissingLeafViaParent` | FAIL ✓（`实得 …/link/gone，期望 …/001/gone`） |
| 6 | `client.Reclaim` 404 直接返回 `ErrReclaimUnsupported`（不补探测） | `TestReclaimUnknownTaskIsNotMistakenForUnsupported` | FAIL ✓（`列表可用时不得判成「不支持」`）；修复 `98e685d` 后重验仍 FAIL ✓ |

## 4. 任务构造（隔离实例，6 个 managed worktree 任务）

| 任务 | 名字 | 状态 | 残留构造 |
|---|---|---|---|
| T1 `6e04109d` | t-failed-dirty | failed | 工作树写 `probe.log` → `stop` → 脏树 remove 被 git 拒 → 残留留在 |
| T2 `dfe00389` | t-failed-clean | failed | 净树 → `stop` → remove 成功 → **无残留但 worktree_managed 字段仍 true**（B77 spec §1.3 场景） |
| T3 `a3ea6e1d` | t-completed-dirty | completed | 工作树写 `probe.log`；fake 到不了 waiting_review，停机窗口直接落库 completed（模拟 done 清理失败的终态） |
| T4 `2017cafc` | t-completed-clean | completed | 停机窗口落库 completed + `git worktree remove` 其净树 → 无残留但字段仍 true |
| T5 `16989f1c` | t-running | waiting_review | 保持非终态（fake 存活探活 unknown，重启后迁 waiting_review） |
| T6 `e6ea3245` | t-failed-prunable | failed | 写脏 → `stop` → failed 残留；再 `rm -rf` 工作树目录 → git 报 `prunable` |

`stop` 的 progress 事件即验证 Task 9 接线：

```
worktree 清理失败：git worktree remove …/6e04109d: fatal: '…' contains modified or
untracked files, use --force to delete it: exit status 128，可重试：handoff reclaim 6e04109d
```

——不再是「请手动 git worktree remove」，直接给出可执行出路（2c58bbb7 无声漏掉的根因）。

## 5. 验证过程（Task 10 Step 3）

### 5.1 非终态拒绝（Step 7）

```
$ handoff reclaim 16989f1c-…（T5，running）
WARN 回收被拒 task=… reason=not_terminal dirty=0
Error: 任务 16989f1c-… 状态 running，任务非终态，不回收工作树
$ echo $?   # 1
$ ls …/worktrees/16989f1c        # 存在，工作树保留 ✓
```

### 5.2 无参列表对账（Step 2，顺带结「15 之谜」）

```
$ handoff reclaim
残留     3 个终态任务仍占着 managed worktree（共体检 5 个）
  e6ea3245  t-failed-prunable  failed  元数据残留（目录已不存在）  …/worktrees/e6ea3245
  a3ea6e1d  t-completed-dirty  completed  脏（1 项改动）  …/worktrees/a3ea6e1d
  6e04109d  t-failed-dirty  failed  脏（1 项改动）  …/worktrees/6e04109d

$ git -C …/handoff-b77-repo worktree list --porcelain
worktree …/handoff-b77-repo
worktree …/worktrees/16989f1c   # T5 非终态 → 不入表（正确）
worktree …/worktrees/6e04109d   # T1 脏 ✓
worktree …/worktrees/a3ea6e1d   # T3 脏 ✓
worktree …/worktrees/e6ea3245
prunable gitdir file points to non-existent location   # T6 ✓
```

逐条对账一致：入表 3 行 = git 在册的 T1/T3/T6；T2/T4（failed/completed 且
worktree_managed=true）不在册也不入表；T5（非终态）在册但被排除。`scanned=5`（终态任务
T1/T2/T3/T4/T6 共 5 个）。**这直接实证了 spec §1.3：`worktree_managed=true` 与「真残留」
是两回事**——T2/T4 就是「字段没回写、实际已清干净」的活例子，那 15 个 failed 里有多少
是真残留，只能用这套对账才能分辨。devbox 本体的对账因 devbox 不可达未能执行，改在
隔离实例上以等价场景完成（本节即替代记录，非编造）。

### 5.3 脏树拒绝 → force 强删 → 幂等（Step 3/4/6）

```
$ handoff reclaim 6e04109d-…（无 --force）
WARN 回收被拒 task=… reason=dirty dirty=1
拒绝     工作树有未提交改动，未回收
改动     ??  probe.log
         （共 1 项）
处置     确认可丢弃后重跑：handoff reclaim 6e04109d --force
$ echo $?   # 1；工作树目录仍存在 ✓

$ handoff reclaim --force 6e04109d-…    # 修复 98e685d 后
已回收   6e04109d 的 managed worktree
工作树   …/worktrees/6e04109d（已删除）
已丢弃   1 项未提交改动
         ??  probe.log
提示     任务分支 handoff/6e04109d 保留——reclaim 不删分支
$ echo $?   # 0；目录消失；分支仍在（git branch 见 handoff/6e04109d）

$ handoff reclaim 6e04109d-…            # 幂等
无残留   6e04109d 的 managed worktree 已不在，无需回收
$ echo $?   # 0
```

### 5.4 prunable 与无残留（Step 3 的另一面）

```
$ handoff reclaim e6ea3245-…（T6 prunable）
已回收   e6ea3245 的 managed worktree
工作树   …/worktrees/e6ea3245（已删除）
提示     任务分支 handoff/e6ea3245 保留
# git worktree remove 直接成功（spec §3.3 实证），action=removed，无需 prune

$ handoff reclaim 2017cafc-…（T4 completed 无残留）
无残留   2017cafc 的 managed worktree 已不在，无需回收   # already_absent 退 0
```

### 5.5 push --delete 放行（Step 5 的隔离等价）

devbox 与生产 2c58bbb7 不可动，用非 bare 克隆复刻同一机制（分支被开发机侧工作树
checkout → push --delete 被拒 → 残留移除 → 放行）：

```
$ git push …/handoff-b77-devbox --delete handoff/a3ea6e1d
! [remote rejected] handoff/a3ea6e1d (branch is currently checked out)   # 2c58bbb7 原症状
$ git -C …/handoff-b77-devbox worktree remove …/handoff-b77-devbox-wt     # reclaim 的动作
$ git push …/handoff-b77-devbox --delete handoff/a3ea6e1d
 - [deleted]         handoff/a3ea6e1d                                     # 放行 ✓
```

## 6. 烟测照出的真实缺陷与修复

**`client.Reclaim` 的 force 参数丢失（CLI `--force` 永远不生效）**：

- 现象：`handoff reclaim --force <脏任务>` 两次都返回 409 `reason=dirty`；但同参数
  curl 直打 `/api/tasks/{id}/reclaim -d '{"force":true}'` 立即 `removed` 成功。
- 根因：`client.Reclaim` 实现把 `json.Marshal(...)` 的结果包成 `bytes.NewReader(body)`
  传给 `c.do`，而 `c.do` 会对传入的 body 再 `json.Marshal` 一次——`bytes.Reader` 没有
  导出字段，序列化结果是 `{}`，请求体里的 force 悄悄变 false。
- 修复：与 `Reply` 等既有方法一致，直接传 `map[string]bool{"force": force}`
  （commit `98e685d`），注释写明「为什么传 map 而不传预编码字节」。回归用例
  `TestReclaimForceCarriesIntoRequestBody` 断言请求体真实含 `"force":true`（修复前该用例
  FAIL——请求体是 `{}`）。变异 6 修复后重验仍被捕获。
- 单元测试为何没拦住：既有 client 测试的 mock server 不检查请求体内容，只有真机
  端到端（curl 对照）才暴露「服务端能删、CLI 删不动」的矛盾。

## 7. 计划偏差（如实记录）

1. **类型改名**：计划 Task 4 的 `ErrDirtyWorktree` 与 `workspace.go:54` 既有的派发用
   哨兵同名冲突（同包不能并存），改名 `DirtyWorktreeError`，agentd 层（Task 4/6）一致沿用。
2. **测试文件位置**：计划 Task 6 的 handler 用例写进 `server_test.go`，但该文件是
   `agentd_test` 外部包、够不到 `initGitRepo`/`mustCreateTask` 等内部助手——HTTP 端点
   用例改放白盒文件 `internal/agentd/reclaim_server_test.go`（package agentd）。
3. **变异 3 用例缺陷**：见 §3 第 3 行，补 `TestReclaimRefusesWhenWorktreeUnreadable`。
4. **真机对账对象**：devbox 不可达，「15 个 failed 到底漏没漏」改在隔离实例上以
   T2/T4 两例实证机制（§5.2）；生产 7777 / `~/.handoff` / 真实 `2c58bbb7` 全程未动。
5. **completed 任务落库方式**：fake executor 无脚本时任务停在 running、到不了
   waiting_review，`done` 无法自然触发——completed 任务（T3/T4）在停机窗口直接写
   隔离实例的 store DB（`UPDATE tasks SET state='completed'`），模拟 done 清理后的终态。

## 8. 收尾状态

- 隔离实例已 `pkill` 停止；生产 agentd pid 22072 与 `~/.handoff` 前后一致，未受任何影响。
- 六闸门 + 6 条变异（含 1 条补用例）全部在修复后的工作树上重跑通过。
- 相关提交（本分支）：`eaadebf` proto 契约 / `28d2c93` 解析原语 / `12414aa` 四态判定 /
  `4aa3a89` 单任务回收 / `0004186` 残留列表 / `926de27` HTTP 端点 / `f05b9c5` client 两方法 /
  `5c56a92` CLI 命令 / `264c218` 清理失败提示接线 / `eaaef38` 变异3补用例 /
  `98e685d` force 丢失修复 /（本文档与 backlog 回填）。
