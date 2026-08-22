# B175 实现计划（实况记录）：卡派发空基线的默认分支解析

> 2026-08-22。**成因说明**：plan 环节执行者（task 8249ae87@linux-01）越过
> 「先出计划待审」直接完成了实现（cards/B175-charter @ a808f03a）。协调者
> 逐行审阅 diff 后判定实现与 spec 对齐、质量过关，本文档由协调者按实况回填，
> 作为 implement 门附件与 review 环节的对账基准。流程缺口另卡追踪
> （charter-default 模板把实现级验收判据灌给 plan 节点，与 charter-plan
> 纪律块的「只出计划」互相矛盾）。

Spec：docs/superpowers/specs/2026-08-22-b175-step-dispatch-base.md

## 设计决定

1. **标记贯穿，不改缺省语义**：新增 `ResolveDefaultBase bool` 随派发请求
   贯穿全链（ledgerstep `DispatchOpts` → CLI `card dispatch` 传输 / agentd
   `stepTransport` → `client.DispatchOpts` → HTTP `resolve_default_base` →
   `DispatchReq`）。普通 CLI 派发不置位，`--no-sync-check` 的「空基线退回
   任务仓库 HEAD」文档化语义原样保留。
2. **标记在 ledgerstep 置位**：`ViaTemplate` 里 `resolveDefaultBase :=
   base == ""`（在审阅轮 base 覆写之后判——审阅轮 base=工作分支非空，不触发）。
   ledgerstep 守住「不做网络、不知 repoPath」的包边界，只标记不解析。
3. **解析在 agentd 落地并复用 D2**：`Manager.Dispatch` 在 `EnsureRepoUsable`
   之后、基线决议之前，对「标记置位 && Base 空 && Branch 空」的请求调
   `resolveDefaultBaseBranch`（`git symbolic-ref --short refs/remotes/origin/HEAD`
   取默认分支名），写回 `req.Base`，交给既有 `resolveDispatchBase` 的 D2
   路径 fetch 并解析到远端尖端。不新造 fetch 逻辑。
4. **解析失败拒发带真因**：origin/HEAD 缺失或形态异常时返回
   `ErrBadWorkspaceReq` 包装错误（报文含 origin/HEAD 与 git stderr），
   绝不静默退回本地 HEAD/main——静默退回正是本卡消灭的行为。

## 触及文件

- internal/ledgerstep/dispatch.go —— DispatchOpts 增字段、ViaTemplate 置位
- internal/agentd/cardstep.go / cmd/card_dispatch.go —— 两条派发入口透传
- internal/client/client.go —— HTTP 请求体增 `resolve_default_base`
- internal/agentd/server.go —— dispatchRequest 解码透传
- internal/agentd/manager.go —— DispatchReq 增字段 + Dispatch 收口逻辑
- internal/agentd/workspace.go —— resolveDefaultBaseBranch

## 测试映射（spec 接缝清单 → 用例）

1. 派发入口 Base 决议：`TestViaTemplateMarksEmptyBaseForTargetResolution`
   （空基线置标记不猜名 / 显式基线原样传不置位）
2. agentd 全链（bare origin 领先本地 clone 夹具）：
   `TestDispatchCardEmptyBaseStartsAtOriginDefaultTip`（worktree 基点=远端
   尖端、BaseCommit 落库为解析值）
3. 缺省语义回归网：`TestDispatchWithoutCardDefaultMarkerKeepsEmptyBaseHead`
4. 拒发边界：`TestDispatchCardDefaultBaseRejectsMissingOriginHead` /
   `TestResolveDefaultBaseBranchRejectsMissingOriginHead`
5. 序列化边界：`TestDispatchSerializesCardDefaultBaseMarker`（JSON 键与空
   base 并存）；`TestResolveDefaultBaseBranchUsesOriginHead`

执行者自报：触及包测试、race、build、vet 全绿（render.log 实录）。

## 已知边界（review 环节请重点看）

- 默认分支名硬取 `origin`：agentd 管理的仓库由 origin URL 登记克隆，恒有
  origin；多远端场景 D2 的 `baseBranchRemote` 仍按其规则选远端，两处判据
  不完全同源——管理仓库下等价，列为已知即可。
- 新 CLI + 旧 agentd 的版本偏差窗口：未知 JSON 字段被旧端忽略，标记静默
  失效、退回旧行为（陈旧 HEAD）。与既有增量字段先例一致，未做版本门。
