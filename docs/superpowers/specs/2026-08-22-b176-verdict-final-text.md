# B176：裁决块到不了解析器——回合末正文要完整送达 StepRunner

> 2026-08-22。来源：B174 试点，取证坐实假设①（render.log 实录见 B176 卡）。

## 问题陈述

charter 流 Verdict 环节「裁决解析失败→转人工」在试点三个派发环节全部复现。
取证（implement 轮 task e4faed94 的 render.log）：codex 按契约输出了完整的
```` ```handoff-verdict ```` fenced block，但账本记录的「最终报文」只剩收尾
行 trailer 的 summary 字段——块在 adapter 侧就丢了。

链路四环，丢在第一环，且**四家 adapter 系统性同病，不止 codex**：

1. adapter 收尾只发 trailer 摘要，回合正文 `text` 被丢弃——
   `internal/executor/codex/adapter.go#finishTurn`（:804 `Summary: tr.Summary`）、
   grok :567、opencode :1756、claudecode :777 同形态；
2. agentd `internal/agentd/manager.go` :2968 落 `completedPayload{Summary}`；
3. `internal/ledgerstep/wire.go#finalMessageFromEvents` 只读 `summary`；
4. `internal/ledgerstep/verdict.go#ParseVerdict` 全文扫块——但它拿到的
   「全文」只是 trailer.summary。

契约与线格式互相矛盾：`internal/ledger/templates.go#implVerdictContract`
明写「裁决块放在收尾行之前的同一条报文里（ParseVerdict 全文扫）」——承诺了
全文，线上只送摘要。`reviewVerdictContract` 的变通（把块原文塞进收尾行
summary 的 JSON 字符串）模型守不稳：codex 实测把块写在了 trailer **之后**的
正文里。

## 级别与档位

**L2**（执行器域 Result 契约 + agentd completed payload + ledgerstep 读取；
payload 变更**只允许增量可选字段**，旧消费者忽略未知字段不受影响。实现中若
发现必须做非增量 proto 变更，升级跨流迁移，不静默扩权——护栏同 B171）。

## 方案

让回合末正文完整（或尾部截断保全）到达裁决解析器，四家一致：

- `executor.Result` 增可选字段（如 `FinalText`）：回合末正文，**尾部截断**
  （如保留末 16KB——verdict 块按契约在报文尾部，截断从头部丢，块必须保全）；
- 四家 adapter 的 finish 路径把 `text` 带上（trailer 解析不变，Summary 语义
  不变）；
- `completedPayload` 增对应可选字段（additive）；
- `finalMessageFromEvents` 优先读新字段，缺失回落 `summary`（兼容旧 agentd
  产生的历史事件与滚动升级窗口）；
- 块位置宽容：`ParseVerdict` 的正则本就全文扫、取最后一个块，正文完整送达后
  trailer 前后都认，天然满足取证发现的「codex 把块放在 trailer 之后」形态；
- 模板措辞同步：`reviewVerdictContract` 不再要求把块塞进收尾行 summary 的
  JSON 字符串（正文写块 + 收尾行只带简短 summary 即可）；改契约 = 出新模板
  版本（templates.go 注释的既有纪律）。
- 弃选：①只修 codex——其余三家同病，实现类裁决节点在任何执行器上都到不了
  解析器；②继续教模型把块塞 trailer.summary——已实测守不稳，而且把解析
  健壮性押在模型纪律上正是本缺陷的成因。

## 测试决定（接缝清单）

最高可测缝：**`finalMessageFromEvents` 的线格式解析**（wire 层纯函数）。

1. wire 层表驱动：新字段存在→优先取用；缺失→回落 summary；两者皆空→报错
   形态不变。夹具用真实线格式 JSON（既有 wire_test 模式）。
2. 各 adapter finish 用例：Result 新字段含回合正文里的 verdict 块；超长正文
   尾部截断后块仍保全（块在末尾的用例）。
3. `node_test` 端到端：块在 trailer 之后的报文（codex 实测形态）能解析出
   pass/fail 并正确路由。
4. 模板测试：审阅/实现契约文案更新后 `templates_test` 的断言同步。

## Out of Scope

- 提问工单通道发裁决（模板已明令禁止，不改）；
- 历史任务事件的回填修复；
- turn_failed/failed 路径的 fail_reason 语义（不动）；
- 真机验收依赖 linux-01 部署新 agentd + 新执行一轮 Verdict 环节，部署前记
  「未验」，账挂本卡。

## 备注

- 现状读数行号为 2026-08-22 main（a8141186f）；实现时以符号锚复核。
- 图覆盖债：`finishTurn`/`finalMessageFromEvents` 图查询未做。
