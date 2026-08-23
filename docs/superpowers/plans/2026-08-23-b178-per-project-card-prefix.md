# Plan（回填）：B178 卡号带项目前缀

> **本文档是回填的，实现先于计划。** 它不是指导实现的计划，是对一份**已经完成并经
> 协调者独立复核**的实现的如实记录，用途有二：过 implement 列的 `require_attachment: plan`
> 门，以及给 review/acceptance 节点一份可对账的清单。
>
> 成因：B178 的 plan 轮（task `70379862`，linux-01 + codex）无视纪律块声明的产出物定义，
> 直接把实现干完了——执行者原话「我先核对仓库现状、规格文档与基线测试，再按当前分支
> **完成卡 B178 的实现**并验证」。它给自己判了 pass，移进 implement 时被 gate 拒（缺 plan
> 附件），卡停在 plan 列。**是门兜住的，不是纪律兜住的。**这是已知缺陷 B182 的又一次现形
> （模板把整卡验收判据灌给了所有节点）。处置按 B175/B176 先例：协调者逐行审 + 人工裁决 +
> 回填实况 plan 补门，用户 2026-08-23 拍板接受。
>
> spec：`docs/superpowers/specs/2026-08-23-per-project-card-prefix.md`
> 实现分支：`cards/B178-charter`，commit `260364f2`（父提交 `615532e2`）

## 1. 实现落成了什么

17 个文件，822 插入 / 40 删除（其中 3 个 spec 文件是父提交带入的，非本次实现）。

| 文件 | 改动 | 对应 spec 章节 |
|---|---|---|
| `internal/ledger/card_prefixes.go` | 新增 132 行：`card_prefixes` 表的事务内分配（`cardPrefixTx`）、首字母派生（`firstASCIILetter`）、显式设置（`SetCardPrefix`） | §3.1 §3.3 |
| `internal/ledger/cards.go` | `nextTopID` 增加 `prefix` 参数，按前缀过滤取 max；`topIDPat` / `importIDPat` 放宽 | §3.2 §3.4 |
| `internal/ledger/store.go` | `card_prefixes` 表的 schema 与 seed | §3.1 |
| `cmd/card_prefix.go` | 新增 `handoff card prefix <project> <prefix>` 子命令 | §3.3 |
| `cmd/card.go` / `card_import.go` / `card_minb.go` | 取号调用点适配新签名 | — |
| `internal/ledger/events.go` | 取号调用点适配 | — |
| 各 `_test.go` | 新增 5 个用例 + 既有用例适配 | §6 |

## 2. spec §6 判据表逐条对账（协调者复核，非照抄执行者 ledger）

| spec 判据 | 承接它的用例 | 结论 |
|---|---|---|
| handoff 建卡仍走 B 号，续在当前水位之后 | `TestCreateCardAllocatesBNumbers`；`TestCreateCardAllocatesIndependentProjectPrefixes` 中 `min_b=173 → B174` | ✅ |
| 新项目首建卡自动派生首字母前缀并落库 | 同上（`charter → C1`、`sq → S1`） | ✅ |
| 前缀撞车时**建卡被拒**且错误文案含冲突方与解法 | `TestCreateCardRejectsPrefixCollision` | ✅ |
| 项目名无 ASCII 字母时建卡被拒 | `TestCreateCardRejectsProjectWithoutASCIIPrefix` | ✅ |
| **号段按前缀独立：新前缀从 1 起步，不受 min_b=173 影响** | `TestCreateCardAllocatesIndependentProjectPrefixes` | ✅ 实现为 `if prefix == "B"` 才读 `min_b` |
| 子卡继承父卡前缀（C1 → C1.1） | 同上用例末段 | ✅ `nextChildID` 未改动，只追加点号位，天然继承 |
| `card prefix` 在该项目已有卡时拒绝修改 | `TestSetCardPrefixValidationAndExistingCards` | ✅ |
| import 接受非 B 前缀的显式号 | `cards_import_test.go` 的 `ImportCard("C1", ...)` | ✅ |

## 3. 验收证据（协调者在本地独立复跑，2026-08-23）

在 `cards/B178-charter` 上：

- `go test ./...` —— **44 个包全 ok，零失败**
- `gofmt -l internal/ cmd/` —— 无输出
- `go build ./...` —— 通过

**变异复验两条（spec §6 原文要求，协调者亲手做）：**

| 变异 | 预期 | 实测 |
|---|---|---|
| 把 `if prefix == "B"` 改成 `if true`（所有前缀都读全局 `min_b`） | 「新前缀从 1 起步」转红 | ✅ `TestCreateCardAllocatesIndependentProjectPrefixes` FAIL |
| 把撞车分支改成 `return "B", nil`（静默回退 B 前缀） | 「建卡被拒」转红 | ✅ `TestCreateCardRejectsPrefixCollision` FAIL |

两条变异都当场击穿、恢复后全绿——**门是真的，不是摆设。**

## 4. 与 spec 的偏离

复核未发现实现偏离 spec 的地方。spec §3.5 明写的遗留（`B191` 属 charter、`B187` 属
benchmarking 会永久保持 B 号，同一项目下两套号）在实现中未被"顺手修正"，符合零迁移决定。

## 5. 剩余未验

- **真机走查未做**：给一个真实的新项目建第一张卡、验证 `handoff card prefix` 的拒绝路径
  文案在终端里读起来对。留给 acceptance 节点。
- 本实现改了取号这条**所有建卡路径都要过**的核心，全量绿是必要不充分条件；
  acceptance 时应在真实账本副本上建一张卡确认号没跳。

## 6. review 节点该看什么

按 B183（charter 流的 review 轮不派发），review 由协调者人工做。重点三处：

1. `cardPrefixTx` 必须在 `mutate` 的写事务内被调用——前缀占用与卡行插入同进同退，
   否则并发首建同项目两张卡会分出两个前缀（实现的注释声称做到了，review 时核对调用点）。
2. `topIDPat` 放宽后，`^([A-Z]{1,4})(\d+)$` 会不会把某个既有的非卡号字符串误判成卡号
   （尤其 import 路径与事件 payload 里的字符串）。
3. `firstASCIILetter` 对 `handoff-server` 派生出 `H`——与 `handoff` 的 `B` 不撞，
   但两个 h 开头的新项目之间会撞。撞车路径已有用例，确认文案能指导用户自解。
