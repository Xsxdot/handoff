# 工作流定义文件

这里放**待应用**的工作流定义。文件不是运行时读的——账本里的定义才是权威，
这些文件只是「打算改成什么样」的可评审载体。应用一次就产生一个新版本。

## charter-v4.json

配合 B183 / B182 的代码改动使用。相对账本里的 charter v3 只有两处改动：

| 节点 | 改动 | 为什么 |
|---|---|---|
| `review` | `override.purpose = "review"` | 让审阅轮走审阅专用路径：基线取卡的工作分支、开一次性 `cards/<卡>-review-N` 分支、不被 `WorkBranch` 当成卡的工作分支。v3 里 review 节点引的是 `charter-default`（purpose=charter），审阅轮因此从卡基线开新分支，工作树里根本没有待审的代码（B183 真机实测：执行者在空分支上把实现重写了一遍） |
| `contract` / `breakdown` / `plan` | `omit_acceptance: true` | 这三个节点的法定产出是文档/骨架，而卡的验收判据通常是实现级的（测试全绿、真机跑通）。两者同时在场时，「pass 的依据是你真实跑到的结果」在这些节点上无解，执行者化解矛盾的方式是直接把实现做掉（B182 真机实测一次；对照组是判据字段为空的卡，同一执行者没越轨） |
| `contract` / `breakdown` / `plan` | `produces.kind/path` | pass 后分别自动挂载 `contract`/`doc`/`plan` 到约定的 specs/plans 路径；四道 gate 保持不变 |

B201 使产文档节点在 pass 后由协调者按约定路径校验本轮 diff 并自动挂卡：
`contract` 产出 `contract` 到 `docs/superpowers/specs/{{CARD_LOWER}}-contract.md`，
`breakdown` 产出 `doc` 到 `docs/superpowers/specs/{{CARD_LOWER}}-breakdown.md`，
`plan` 产出 `plan` 到 `docs/superpowers/plans/{{CARD_LOWER}}-plan.md`。
路径在派发 prompt 中固定告知执行者；未声明 `produces` 的旧工作流行为不变。
本次四道 gate 一律不变：contract=require_attachment: spec、
breakdown=require_attachment: contract、plan=require_attachment: spec、
implement=require_attachment: plan。

### 应用顺序（**先部署二进制，后 put**）

```bash
# 1. 先让 agentd 与 CLI 都换成含 B183/B182 改动的二进制
# 2. 再写入新版本（不改旧版，v3 上的存量卡不受影响）
handoff workflow put charter --file deploy/workflows/charter-v4.json
# 3. 只有需要让某张在飞的卡用上新流程时，才显式迁
handoff workflow migrate <卡号> --workflow charter --column <当前列> --yes
```

顺序反了会造出「配置已新、二进制还旧」的窗口：旧二进制的 JSON 解码会**静默忽略**
它不认识的 `purpose` / `omit_acceptance` 两个字段，于是 v4 表现得和 v3 一模一样，
而看板上写着已经改了。

### 本次刻意**没有**改的两个节点

- **`图对账`**：它要核对的是「分支改动 vs 视图 diff」，因此必须看得见工作分支——
  但它的需求是**分支接续**，不是审阅语义（它可能要提交回灌产物，一次性只读分支
  会把提交丢掉）。正解在 B192（charter 流各节点分支不接续），不在本次这两个开关。
- **`integrate`**：同上，它要合分支、跑全量、修接缝，同样卡在 B192 那条上。

把这两个节点也标成 `purpose=review` 能让它们「看见代码」，但会顺带把它们变成
只读一次性分支——那是用一个错换另一个错。
