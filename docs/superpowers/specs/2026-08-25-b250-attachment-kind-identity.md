# B250：附件身份是 (kind, path)，不是 path

状态：**已批准**（用户 2026-08-25「继续吧」；方向裁决同日：改代码，幂等按 `(kind, path)`，detach 双形态与出声提示一并获批）
级别：**L1**（单子系统、不动跨子系统契约；plan 增量为零、验收一眼可核）
卡：B250

## 问题陈述

`card update --attach` 的幂等判据只比 `path`、不比 `kind`（`internal/ledger/cards.go:463`
`Store.AttachFile`）：

    for _, attachment := range card.Attachments {
        if attachment.Path == path { return nil } // 幂等
    }

于是同一份文件挂第二个 kind 时，命令**返回成功、附件列表纹丝不动、没有任何提示**。

这与 product-backlog skill 写死的 L1 快道直接冲突：L1 的 spec 一页纸、末尾三行就是
plan，**同一份文件要挂 `spec:` 与 `plan:` 双 kind** 才过得了 implement 列的门
（`~/.claude/skills/product-backlog/SKILL.md:114`、`:175`）。今天这条路走不通。

失败还**不指向真因**：门只说「进 implement 需要 plan 或 breakdown 附件之一（当前都
没有）」，操作者会去查门，而真因是上一条 attach 被静默吞了。2026-08-25 推 B248 时
实撞，绕行方式是 detach 掉 spec 后以 plan kind 重挂同一文件——绕行不是修复。

**现状读数**（本轮工作树，供 review 复核）：

- `internal/ledger/cards.go:463` `AttachFile`：幂等只比 path；函数注释写的也是「同 path 幂等」——实现与注释自洽，冲突的是设计意图，不是笔误。
- `internal/ledger/cards.go:488` `DetachFile`：按 path 匹配，摘掉全部同 path 项，无返回条数。
- `internal/ledger/move.go:236` `cardHasAttachmentKind`：门只看 kind，不看 path——**双 kind 一旦挂得上，门这侧零改动即可工作**。
- `cmd/card.go:147`：`--attach` 以首个 `:` 切成 kind/path；`--detach` 整串当 path。
- `internal/ledgerstep/dispatch.go:399`：派发时把附件按 `kind: path` 逐行注入 prompt。

## 方案

**选定：附件的身份是 `(kind, path)` 二元组。**幂等判据改为二元组相等，同一文件因此
可以同时以多个 kind 挂在卡上。

理由：`kind` 不是装饰，它决定这条附件满足哪一个门（`cardHasAttachmentKind` 只看
kind）。只按 path 判重等于把有语义的那一维丢掉——**同一份文件既是 spec 又是 plan，
在 L1 快道里是事实描述，不是重复登记**。

弃选一：**L1 只挂 `plan:` 单 kind**。零代码改动，但卡上从此看不出这张卡有 spec，
审计与取证少一条线索；且 `spec` 这个 kind 在 L1 卡上永久缺席，看板与派发注入都会
少一行事实。

弃选二：**L1 另写一份三行 plan 文件**。同样零代码改动，但与「一页纸末尾三行就是
plan」的 L1 初衷相悖，每张 L1 卡多一份注定只有三行的文件。

两个弃选都是拿流程去迁就一个判据写窄了的实现。

## 用户故事

1. 作为推 L1 卡的协调者，我把同一份 spec 文件先后以 `spec:` 与 `plan:` 挂上卡，两条都在，`move implement` 一次过门。
2. 作为协调者，我重复挂完全相同的 `(kind, path)` 时，命令告诉我「已存在，跳过」，而不是让我从 JSON 里自己找。
3. 作为协调者，我要摘掉某一个 kind（比如定级从 L1 改成 L2、要撤掉 `plan:`），能只摘那一条，另一条留在卡上。
4. 作为协调者，我沿用老写法 `--detach <path>` 时，同 path 的附件全被摘掉，命令告诉我摘了几条——不静默。

## 实现决定

1. **幂等判据改二元组**：`AttachFile` 命中判据由 `attachment.Path == path` 改为
   `attachment.Kind == kind && attachment.Path == path`。命中即跳过（保持幂等），
   但要有一行可见提示（见 3）。
2. **`--detach` 认两种形态，判定顺序写死**：先按 `kind:path` 解析，若卡上存在
   `(kind, path)` 精确匹配则**只摘这一条**；否则把整串当 path，摘掉全部同 path 项。
   这条顺序保证老写法（含带冒号的怪路径）行为不变——只有真的存在同名 kind 前缀时
   才走精确分支。
3. **两条动作都要出声**：attach 命中幂等打印「附件已存在，跳过：kind:path」；
   detach 打印摘掉的条数与清单。落点是 stderr（stdout 仍是卡的 JSON，不破坏管道）。
4. **派发注入不去重**：同一路径出现 `spec:` 与 `plan:` 两行是 L1 的真实语义
   （这份文件既是 spec 也是 plan），执行者需要知道这件事。

用户可见的名字只有一个新增形态：`--detach kind:path`；`--attach` 的形态不变。

## 测试决定（接缝清单）

一条主缝，两条从缝，全部是存量导出符号、调用方可 grep：

| 缝 | 调用方 | 断言 |
|---|---|---|
| `internal/ledger#Store.AttachFile` | `cmd/card.go` 的 `card update --attach`（`cmd/card.go:152`） | 同 path 两个 kind 挂完各自都在、顺序稳定；完全相同的 `(kind,path)` 二次挂不新增条目 |
| `internal/ledger#Store.DetachFile` | `cmd/card.go` 的 `card update --detach`（`cmd/card.go:157`） | `kind:path` 只摘一条、另一条仍在；裸 path 摘光同 path 全部；两种形态各自返回的条数正确 |
| `internal/ledger#cardHasAttachmentKind` | `internal/ledger/move.go` 的单值门（`move.go:82`）与择一门（`move.go:90`） | **端到端**：spec+plan 双挂后 `move` 到 implement 列通过（这一条证明修复真的解开了 B248 撞的那道门，不是只让数据结构好看） |

第三条是**回归网的牙齿**所在：只断言 `Attachments` 长度变成 2，改坏门也照样绿。

## Out of Scope

**永不做**：

- 附件去重展示（派发注入把同路径两个 kind 合成一行）——见实现决定 4，那会抹掉语义。
- 附件内容校验（文件存不存在、是不是 markdown）。附件是路径登记，不是内容托管；
  「附件必须在卡基线上」是派发纪律的事，不是 attach 的事。
- 让 `--attach` 支持一条命令挂多个 kind（`--attach spec:x --attach plan:x`）。
  cobra 的 `StringVar` 改 `StringArrayVar` 会顺手改掉一个稳定的 CLI 形态，
  收益只是省一条命令。

**本期不做、后续要做**：

- 门的报错文案指向真因（今天只说「当前都没有」，不提示「最近是否有 attach 被跳过」）。
  本卡修完之后这条静默路径已消失，剩下的是一般性的门诊断质量问题，落 roadmap。

## 备注

- 绕行处置留痕在 B248 卡上（detach spec 后以 plan kind 重挂同一文件）。绕行不算修复。
- 本卡 spec 阶段的方向裁决由用户于 2026-08-25 作出：改代码，不改流程。
  product-backlog skill 的 L1 段（`:114`、`:175`）因此**不需要改**——修完之后
  文档描述的流程第一次真的走得通。
- B250 卡上 2026-08-25 16:13–16:17 有 5 条标着「【实测·请忽略】」的 comment，
  是测 `card wait` 唤醒延迟时借这张卡打的事件，与本 spec 无关。
