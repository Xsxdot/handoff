# C12 一次性扫描 plan：handoff 全量重扫（flows + channel）

本次任务参数：
- 项目：handoff（本仓）
- 形态：**全量重扫**，只更新 `codegraph/baseline.json`；`meta.branch`/`commit` 写当前分支与 HEAD；`meta.generator` 写 `codex-codegraph-c12-full-rescan`；`meta.scannedAt` 写扫描当日
- 配方真源：本工作树 `docs/codegraph-scan-recipe.md`（已含 C12 `flows`/`channel` 段）。**先通读配方再写 JSON**，配方与本页冲突时以本页「特别要求」为准，其余以配方为准。

## 本轮特别要求（高于配方冲突条款）

1. **必须产出 `flows`**。只给承重函数建 flow（入口、入口第一跳 handler、跨域入缝非兜底桶符号、跨域出边 ≥ 3 的编排单元）。禁止给全部节点建 flow。禁止把 BFS 邻居列表写进 `steps` 冒充流程图。`cond` 抄源码原文。接口调用标 `iface: true`，`to` 写接口节点，不复制实现清单。
2. **每个 entry 必须有 `channel`**：`cli|http|ws|web`。缺一个都不算完。
3. **入口不要停在一跳**。上轮 162 个入口里 137 个出边=1。本轮 entry 的 edges 与 flow 都要沿 handler 走进编排层。
4. **`packages` 节必须有**（上轮基线缺这段）。每个节点 `file` 所在目录一条，Go 转录包 doc，TS 空串。
5. **容器 kind 只用八值词表**。未知 kind 是错误，不许塞进「函数组」圆场。
6. **禁动清单**：`codegraph/target.json`、`codegraph/domains/*.json`、`codegraph/best.json`、任何源码。本轮 decls 已由协调者机械搬运，扫描者只读。
7. **入口容器拓扑不要改**。仍是 CLI/HTTP/WS 三容器，不要按服务领域拆容器（会打乱 best 归属）。分组靠 `channel`。
8. **C12 键自检必须跑**（配方文末那段 python）。`handoff graph validate` 在旧二进制上会忽略 `flows`/`channel` 并假绿，不能当完成判据。

## 完成定义

- 只改了 `codegraph/baseline.json`（加一份交付说明 markdown 可以，放 `docs/ledgers/` 或计划旁）
- python C12 键自检退出 0：`flows` 非空、entry 全有合法 channel、flows 键都是已定义节点
- `python3 -m json.tool codegraph/baseline.json` 能 parse
- `handoff graph validate --repo .` 零 issues（引用完整性）
- 交付说明里写清：flows 条数、承重函数怎么圈的、entry 各 channel 计数、packages 条目数 vs 目录数、仍然 unscanned 的入口清单、相对上轮基线节点/边增减

不要改测试、不要改 go.mod、不要重构扫描器。这是数据任务。
