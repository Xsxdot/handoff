# B252：codex 权限请求的沙箱档位只读呈现——盲批变知情批

状态：**已批准**（用户 2026-08-25 问答裁决三选一，选定「最小档：只读呈现」）
级别：**L1**（单子系统 codex executor adapter，不动跨子系统契约；plan 增量为零、
验收一眼可核——spec 末尾三行即 plan，同一文件挂 spec:/plan: 双 kind）
卡：B252

## 问题陈述

codex 的每条权限请求可能带不同的沙箱档位诉求（枚举 use_default /
with_additional_permissions / require_escalated，其中 require_escalated 是**无沙箱
执行**；被沙箱拒后执行者申请的正是它）。但 handoff 侧读不到这个维度：协调者与
廉价模型审批者做的每一次 allow，都不知道自己批出去的是不是无沙箱执行
（2026-08-25 两次人工批准 git commit --amend 即属此类）。

**现状读数**（本轮工作树，供 review 复核）：

- `internal/executor/codex/perm.go:57` `commandApproval` 字段全集：ItemID / ThreadID /
  TurnID / Command / Cwd / CommandActions——无任何 sandbox / escalation 字段，
  `parseCommandApproval` 也不解析。
- 同文件 `:43` `decisionFor`：once→accept、其余一律 decline；`:294`
  `RespondPermission` 回发报文只有 `{"decision": d}` 一个键。
- **其余三个执行器无此维度**（本轮已核）：claudecode / grok / opencode 的 perm
  协议文件 grep sandbox|escalat 零命中——档位是 codex 专属概念。

## 方案

**选定：最小档，只读呈现。**解析档位进报文视图，权限工单描述与账本事件里如实附注
「本次请求要求脱离沙箱执行」。审批从盲批变知情批，回发能力不变。

弃选：

- **完整档（选择性回发 with_additional_permissions，即裁决「只批沙箱内执行」）**：
  需开 codex 的 exec_permission_approvals 特性；且 B227 修好后 git 类提权实例大减，
  剩余需求量未知，性价比存疑。落 roadmap，等呈现层跑出真实数据再定。
- **继续搁置等 B227**：B227 消除的是提权**实例**，不是能力缺口——非 git 类提权照旧
  盲批，且本档改动很小，不值得等。

## 用户故事

1. 协调者收到 codex 权限工单时，描述里直接写明本次是否要求脱离沙箱，据此裁决。
2. 廉价模型审批者的判据输入里含同一附注，危险档请求不再与沙箱内请求同权重。
3. 事后审计从账本事件即可分辨历史上哪些 allow 放出过无沙箱执行。

## 实现决定

- 档位作为 **codex 附注**呈现（拼进该 adapter 已有的请求描述/事件文本），**不新增
  通用权限契约字段**——已核实仅 codex 有此维度，泄漏成通用概念是过度设计。
- 枚举值取自 B227 对 codex 二进制的取证；**线上报文的字段名与形态以真实/录制夹具
  为准**（implement 的 TDD 第一步）。无档位字段的报文行为一律不变。
- **风险与退路**：若真实报文根本不携带档位（取证枚举只存在于 codex 内部、不上线），
  最小档不可行，卡退回 spec 并记明——不硬造。

## 测试决定（接缝清单）

一条缝：**codex 权限报文解析入口**（边界型例外：对端载荷的解析入口即缝上符号）——
`internal/executor/codex/perm.go#parseCommandApproval`，调用方是同 adapter 的权限
请求处理链。缝级断言：require_escalated 夹具 → 描述/事件含脱沙箱附注；无档位字段
夹具 → 输出与现状逐字节一致。

## Out of Scope

- 选择性回发/降档裁决（完整档，roadmap）。
- 其他执行器的档位维度（已核实不存在，永不做，除非对端协议先长出来）。
- permgate 判据规则按档位加权（先呈现，规则层是否消费该附注归 B249 族后续）。

## Plan（L1 三行，与 spec 同文件双 kind）

1. 改 `internal/executor/codex/perm.go`：`commandApproval` 增档位字段并解析，
   require_escalated 时在 desc 与账本事件文本附注「要求脱离沙箱执行」。
2. 夹具先行：先以真实/录制 requestApproval 报文确认字段名，写红测试再实现。
3. 验收：单测断言两支夹具（带档位/不带档位）的输出；`go test ./internal/executor/codex/...` 全绿。
