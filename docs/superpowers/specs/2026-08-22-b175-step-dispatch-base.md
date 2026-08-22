# B175：卡派发的空基线缺省解析——不再退回执行机陈旧 HEAD

> 2026-08-22。来源：B174 试点（B171 plan 轮 task 0a9e6f16）实测，取证在 B175 卡。

## 问题陈述

卡派发（`card dispatch`，含 `--step` 环节派发）的任务起点在「卡链上没有显式
基线分支」时退回执行机仓库当前 HEAD。实测 B171 plan 轮基点 7dec3118 落后
origin/main 571 个提交，执行者看不到卡附件引用的 spec，自己重写了一份（deny
拦下）。

根因是一条**有文档、无人履行**的契约：

- `internal/ledger/relations.go#EffectiveBaseBranch` 注释明写「全空返回 ""
  （= 项目默认主线，**由调用方在派发时解析为具体分支名**——库不猜
  main/master）」（现状读数 relations.go:194-196）。
- 但调用方 `internal/ledgerstep/dispatch.go#ViaTemplate` 把空串原样透传给
  `DispatchOpts.Base`（现状读数 dispatch.go:141-144），没有任何解析。
- agentd 侧 `internal/agentd/workspace.go#ResolveBaseline` 对「Base 空 +
  BaseCommit 空」的文档化语义正是「起点退回任务仓库 HEAD」——那是给 CLI
  直派发 `--no-sync-check` 留的刻意行为，卡派发误落进了同一条缺省路。

**缺口比卡上初记的窄**：显式设了基线分支名的卡不受此害——agentd 的 D2 分支
补拉（`internal/agentd/workspace.go#resolveDispatchBase`，部署版本已含
6944be152）会 fetch 远端并解析到尖端。只有「全链空基线」这条缺省路是坏的。

## 级别与档位

**L2**（账本派发域：`ledgerstep` 与 agentd 的卡派发入口；不动 CLI 直派发的
基线协议 B4/B35/B76/D2）。

## 方案

空基线时解析**项目仓库的默认分支名**（如 origin/HEAD 指向的分支），把它作为
`Base` 交给既有 D2 补拉决议为远端尖端——复用 D2，不新造 fetch 逻辑。

- 落点约束：`ledgerstep` 的边界是「不做网络、不知道 repoPath」（包头注释），
  所以默认分支解析天然落在**知道仓库路径的一侧**（agentd 的卡派发入口 /
  Transport 实现），或经注入缝进 `ViaTemplate`。具体落点 plan 定。
- 必须**区分来源**：普通 CLI 直派发的「Base 空 + BaseCommit 空」（
  `--no-sync-check`）退回 HEAD 是既有承诺，不许被本修复改掉。收口范围只是
  卡派发（CLI `card dispatch` 与 agentd StepRunner 两条入口都要覆盖——它们
  最终共用 `ViaTemplate`）。
- 默认分支解析失败（如 origin/HEAD 未设置）时**拒发并说清**，不许静默退回
  执行机 HEAD——静默退回正是本卡要消灭的行为。
- 弃选：①让 `ResolveBaseline` 空 sha 时自动 fetch——改掉 `--no-sync-check`
  的文档化语义，伤及既有承诺；②要求所有卡必须显式设基线——把工程缺省的活
  推给每张卡的操作者，防漏靠记忆。

## 测试决定（接缝清单）

最高可测缝：**卡派发的 Base 决议**。

1. `ledgerstep`/派发入口单测：全链空基线的卡派发，Transport 收到的 `Base`
   为解析出的默认分支名（非空）；显式基线的卡行为不变；解析失败拒发带真因。
2. agentd 集成测试（两仓夹具：bare origin 领先本地 clone）：空基线卡的
   `--step` 派发建出的 worktree 基点 = origin 默认分支尖端，不是本地陈旧 HEAD。
3. 既有 CLI 直派发测试不许转红（`--no-sync-check` 语义回归网）。

## Out of Scope

- CLI 直派发的基线协议（B4/B35/B76/D2）任何改动；
- 历史已派任务的补救；已合并的 B171 分支不受影响；
- B176（verdict 块提取）另卡处理。

## 备注

- 真机验收依赖 linux-01 部署新 agentd，部署前记「未验」，账挂本卡。
- 图覆盖债：`ViaTemplate`/`ResolveBaseline` 图查询未做，符号锚以本文件
  `文件#符号` 为准。
