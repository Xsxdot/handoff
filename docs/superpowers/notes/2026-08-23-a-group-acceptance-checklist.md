# A 组真机验收清单

> 2026-08-23 协调者整理。分支 `feat/a-group-fixes`。
> 每张卡的**代码级**判据都已由协调者在本机独立复跑 + 变异验证过（记录在各卡的
> timeline 与 `card accept` 的证据里）。这里只列**必须真机才能验**的那一半。
>
> 卡全部停在**待审阅**，没有归档——验收没过就 `done`，之后想 `continue` 会撞 409。

## 0. 部署顺序（顺序反了会造出「配置已新、二进制还旧」的窗口）

```bash
# 1) 先换二进制（协调机 + linux-01 都要）
# 2) 再同步 skill 落点（B184 改了 skills/handoff/SKILL.md 的恢复纪律）
handoff skill install
# 3) 最后才写工作流新版本
handoff workflow put charter --file deploy/workflows/charter-v4.json
```

旧二进制解码 JSON 时会**静默忽略** `purpose` / `omit_acceptance`，v4 会表现得和
v3 一模一样，而看板上写着已经改了。

## 1. B183（+B182）：用途归节点 / 判据按节点收放

1. `workflow put` 完 v4 之后，挑一张卡迁到 charter v4，跑到 review 列，
   `handoff card dispatch <卡> --step review`。
2. **判据**：派发 stderr 里 `按模板派发` 那行应出现
   `purpose=review purpose_overridden=true`，分支名是 `cards/<卡>-review-1`
   （不是 `-charter-N`），base 是卡的工作分支。
3. 审阅执行者的工作树里应当**看得见待审的实现**（v3 下它是空的，会重写一遍）。
4. **判据（B182）**：plan 列派发时该行应有 `omit_acceptance=true`，
   且执行者 prompt 里不出现整卡验收判据原文；plan 轮的产出应是计划文档而非实现。

## 2. B180：回合收口后的假失败

跑任意一次真派发，看事件流：`completed` 之后**不再**跟一条
`turn_failed: codex/grok 连接断开 … EOF`。agentd 日志里应有一行
`回合已收口后到达的失败结果，判为传输层假警报并丢弃`。

（本轮 A 组自己制造了 6 次现场实证：B183/B180/B186/B168/B184 每个任务收尾都补发了
这条假警报——修好之后它们应当消失。）

## 3. B184：无人值守 vs 卡驱动归属

`handoff status --target linux-01`，在有卡驱动但没挂订阅的任务行上应看到
`⚠ 无人订阅（卡 B1xx 驱动 <session>，心跳 <N>m 前）`；真孤儿（没挂卡或卡无驱动）
仍是 `⚠ 无人值守`。**注意**：心跳会在认领 5 分钟后必然过期（B189 记的另一个缺陷），
所以「心跳 N 分钟前」的数字会一直增长，这是已知的、不影响本卡判据。

## 4. B186：ptyhost 临时目录

`go test ./...` **全量并发**下跑几轮，看 `TestWindowsCrossCompiles` 是否还偶发红；
跑完 `git status --porcelain internal/ptyhost/` 应无输出。

## 5. B168：`workflow put` 接受 nodes-only 文件

`handoff workflow put charter --file deploy/workflows/charter-v4.json` 本身就是判据
——那个文件正是 nodes-only 形态，v3 的 CLI 会拒它。

## 5b. B159 / B162 / B169：三条偶发红与一个界面 bug

- **B159**：变异实验已把 CI 上那个偶发窗口变成确定性复现（`markDelivered` 插 200ms
  延迟，旧门稳定红在 `DeliveredAt:<nil>`、新门 `-count=10` 全绿）。真机侧要看的是
  **后续几次 CI**（ubuntu runner）上它是否还偶发——本卡判据是窗口被门罩住，不是
  「再也不红」。
- **B162**：新用例等的是诊断完成信号而不是 3 秒挂钟。判据现场感受得到：把诊断判定
  改恒假时，新用例 **0.03 秒**就红（旧写法要等满 3 秒）。真机侧看全量并发下是否
  还偶发。
- **B169**：浏览器里打开工作流编辑器，点**第三列**的复选框 label，翻的必须是第三列
  自己的框（v3 下翻的是第一列）。中文列名是常态，随便挑一条流都能验。

## 5c. 一次未识别的全量偶发红（B193）

集中分支上跑 `go test ./...` 时 `internal/agentd` 红过一次，之后单包绿、全量重跑
4 次连续绿，**具体用例没抓到**——当时我把输出过滤了。已建卡 B193 记下形态与复现
建议。你若在验收时撞见全量红，**请把完整输出留下**再判。

## 6. 本轮**没有**做完的（各有卡，别当遗漏）

| 卡 | 事 |
|---|---|
| **B192**（高） | charter 流各节点分支不接续：每个节点都从卡基线新开分支，plan 的产出不在 implement 的工作树里。B183 只修了审阅那一条 |
| **B189** | 驱动租约无人续期：`HeartbeatDriver` 零生产调用点，认领 5 分钟后可被抢占 |
| **B190** | 存量账本的 bug/feature 流是 States-only 老定义，卡驱动 `--step` 派不动 |
| **B191** | B170 的执行侧**已完成**（charter 仓 `docs/b170-view-plan` 分支）；剩下的是 charter 发版 + handoff 侧 go.mod 升版，属发版动作 |
| **B193** | 上面那次未识别的全量偶发红 |

## 7. 一条给下次的操作纪律

`card dispatch` 没有 `--executor`，执行者由模板定（`feature-impl`→opencode）。
本轮为了拿 codex 一律用了 `--template charter-default`，代价是没带
`carry_card_context`（卡上下文段缺席，判据靠模板的 `{{ACCEPT}}` 进去）。
要长期顺手，给 bug 流的「进行中」节点加一个 `override.executor=codex` 即可——
那是改工作流定义，属工作台管理动作，留给你定。
