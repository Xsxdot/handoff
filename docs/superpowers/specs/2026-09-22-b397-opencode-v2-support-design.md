# B397 OpenCode V2 支持：monitor 插件 v1/v2 分发 + opencode2 执行器 — 设计

- 日期：2026-09-22
- 卡：B397（高，charter · 待办）
- 状态：brainstorm 定稿，待用户过目

## 背景

本机 opencode 升到 v2.0.12 后：

1. 现有 monitor 插件 `plugins/opencode-monitor/handoff-monitor.ts` 使用 V1 插件 API（`@opencode-ai/plugin`），V2 下无法加载。
2. 现有 executor `internal/executor/opencode` 以 `opencode serve` + V1 HTTP/SSE 契约驱动会话；V2 serve 是「v2 API」（官方 breaking change 之二），路径、事件形状、鉴权均未实测，不能当同一协议用。
3. V1/V2 可并存或覆盖安装：官方 v2 装出 `opencode2`（或覆盖 `opencode` 二进制为 v2），探测与派发必须能区分。

superpowers 插件已从本机 `~/.config/opencode/opencode.json` 摘除（用户配置，不进本仓）。

## 决策摘要（brainstorm 已定）

| 分叉 | 决策 |
|---|---|
| 执行器身份 | **A**：新执行器名 `opencode2`，与 `opencode` 并列 |
| 插件分发 | **A**：仓内 v1/v2 两份源码，按探测结果写同一落点；探测规则见下 |
| adapter 归属 | **B**：新包 `internal/executor/opencode2`，V1 协议零改动（V1 包仅加 fail-closed 闸）；只抽已稳定的共享层 |
| 首版深度 | **C**：MVP 通路 + 显式降级清单，不静默假成功 |
| 配置面 | **C**：`opencode2` 独立键，缺省回落 `opencode` |
| 版本冲突 | `opencode2` 或 `opencode --version` 报 v2 都算有 V2；`--executor opencode` 实测撞 v2 二进制 **fail-closed 拒发** |

## 1. 范围

### 范围内

- monitor 插件仓内 v1/v2 双源码，`handoff skill install` 按探测分发。
- 新执行器 `opencode2`：toolchain 探测、配置键（env/approver/纪律映射/默认执行者）、CLI 枚举、agentd 拉起。
- `internal/executor/opencode2` MVP adapter：起 serve → 建会话 → 灌 prompt → 权限/提问/终态 → reply/continue/done 闭环。
- 实现前置：spike 真 `opencode2 serve` 协议（路径、鉴权、SSE/事件形状、权限与提问如何冒出）。
- skill（`skills/handoff/SKILL.md`）与 README（中英）：`opencode2` 条目、monitor 能力按 V1/V2 分述、降级清单。
- fail-closed：`--executor opencode` + v2 二进制拒发。

### 范围外（首版不做，降级清单显式声明）

- V2 冷恢复 / 断线重连对账、Spend/Timing/Usage 细粒度、reconcile 归类、deny 同帧送达等 V1 已有能力的对齐——后续卡按 V2 API 稳定度补。
- V1 路径任何行为改动（除「撞 v2 拒发」这一道 fail-closed 闸）。
- superpowers（已处理，用户配置）。
- 会话历史 db 迁移（第三方工具，不进本卡）。

## 2. Monitor 插件

### 2.1 仓内形态

```
plugins/opencode-monitor/
  handoff-monitor.ts        # 现有 V1，原样保留（@opencode-ai/plugin + tool() + default plugin）
  handoff-monitor-v2.ts     # 新增 V2（@opencode/plugin + Plugin.define + ctx.tool.transform）
```

`main.go`：

```go
//go:embed plugins/opencode-monitor/handoff-monitor.ts
var pluginContent string

//go:embed plugins/opencode-monitor/handoff-monitor-v2.ts
var pluginContentV2 string
```

`cmd.SetPluginContent` 扩展为同时注入两份（或 `SetPluginContents(v1, v2)`）。

### 2.2 V2 插件 API 要点（实现时以真机 + 官方 build/plugins 文档为准）

- 导出形态：`export default Plugin.define({ id: "handoff-monitor", setup(ctx) { ... } })`。
- 工具注册：`ctx.tool.transform` → `editor.add({ name, description, input, execute })`；`monitor` 工具语义与 V1 对齐（起后台 `handoff wait --follow`，stdout 按行叫醒会话）。
- 加载落点：V2 仍发现全局 `~/.config/opencode/plugins/` 下的 `.ts`（官方文档）；**spike 时复核**，若实际发现路径变了，分发目标路径随之改，不硬编码假设。
- 依赖：`@opencode/plugin`（V2 包名）；V1 侧继续 `@opencode-ai/plugin`。全局 `package.json` 若只服务插件编译/类型，按需加依赖或两份各自可独立 typecheck。

### 2.3 分发与状态

`internal/skill/plugin.go`：

- `InstallPlugin(content, home)` 签名不变；由调用方（`cmd/skill.go`）按探测选 content。
- 探测规则（确定性、不联网）：
  1. `lookPath("opencode2")` 成功 → V2。
  2. 否则 `lookPath("opencode")` 成功且 `opencode --version` 输出含 `v2` → V2。
  3. 否则 → V1。
  4. 探测失败/超时 → 按 V1 写（保守：V1 是现网已验证形态）并 Note 说明。
- 落点仍为 `~/.config/opencode/plugins/handoff-monitor.ts`（同一文件名，内容二选一；V1/V2 同目录混装被排除）。
- `PluginStatus`：按**当前探测应写的那份**算哈希；不匹配报 `stale`。
- 空内容/无配置目录：沿用现有 skip 语义，不造目录。

## 3. 执行器 opencode2

### 3.1 探测（`internal/toolchain`）

- `order`：`opencode, opencode2, claude, grok, codex, agy`（`opencode2` 紧随 `opencode`）。
- `lookPath("opencode2")` + 跑 `opencode2 --version` 须含 `v2` 才算装了；`lookPath` 成功但 version 非 v2（或 version 跑不了）→ `StateMissing`（Result 无 Note 字段，中文态串足够 init 表达）。
- 凭证：首版沿用 `opencode` 的 `auth.json` 判据（与 V2 是否同路径由 spike 核实；若不同，表加独立项）。
- `credRelPath` Windows 过滤逻辑对 `opencode2` 同样适用（若凭证路径相同）。

### 3.2 fail-closed（V1 adapter）

`internal/executor/opencode` 的 `Start`（或 `serveSpec` 组参前）：对将要 exec 的二进制做一次 version 判定（缓存可接受，进程级一次即可）：

- major ≥ 2 → 返回明确错误：`本机 opencode 是 v2，请改用 --executor opencode2`（或等价中文），任务 `failed`，不静默换协议。
- 判定成本/时机以实现最小侵入为准；**禁止**在 V1 包内路由到 V2 协议。

`opencode2` adapter 反向不强制（若用户拿 `opencode2` 名字却只有 v1 二进制，启动自然失败并报真因）。

### 3.3 新包 MVP（`internal/executor/opencode2`）

实现 `executor.Adapter` 五动作的最小闭环：

| 动作 | MVP 行为 |
|---|---|
| Start | 经 prochost 起 `opencode2 serve`（或 spike 实锤的等价命令）→ 就绪探测 → 建会话 → 渲染 prompt（`turn.RenderPrompt`，Discipline 必传）→ 订阅事件流 |
| Events | 权限 / 提问 / 进度 / result 四类映射到 `AdapterEvent`；原生 id 有则填 `PermissionID`/`QuestionID` |
| Send | 同会话续接（continue/answer） |
| RespondPermission | once / reject；deny 理由若 V2 带外可注入则注入，否则如实走 manager 降级（`DenyReasonInBand` 可选接口按实测） |
| Stop | kill serve + 关事件流 |

**降级清单机制**：包内 `var Degraded = []string{...}`（或等价常量），首版至少声明：

- 断线冷恢复 / resume 对账：**不支持**——serve 死亡直接终态失败，提示重新 dispatch 或人工处置（不假装能 resume）。
- Spend / Timing / Usage 细粒度：不上报（事件不带这些字段），UI 显示空 ≠ 报 0。
- reconcile / 未知终态兜底：若 MVP 未实现，遇未知帧 fail-closed 收束为 turn_failed 或 failed 并留 serve.log 尾部。
- deny 同帧送达：视 spike；做不到就走既有带外注入路径。

清单出处：包级 doc comment + README 表 + SKILL.md 各 executor 须知一节，三处一致。

### 3.4 共享层（克制）

只抽**已实测重复且 V2 beta 期形状稳定**的，例如：

- prochost 启动封装 / 存活锁（若 V1 的 `StartServe` 能参数化 argv 而不绑协议，则优先参数化后两边共用；否则 V2 自写薄封装）。
- serve.log 尾部有界读取 + 密码脱敏。
- 端口/密码生成、`proc.json` 形状（若 V2 复用同恢复凭据布局）。

**不抽**：api.go 的 SSE 解析、会话/reconcile/question 归类——V1 专属协议，等 V2 稳定后再谈合并。

共享落点候选：独立小包（如 `internal/executor/ocshared`，名字实现期定，避免 `opencode`/`opencode2` 循环 import）或既有 prochost 包扩展。

### 3.5 配置面（独立键 + 回落）

| 面 | 规则 |
|---|---|
| `executor.default` / `--executor` / agentd `--executor=` | 枚举加 `opencode2` |
| `env:` 段 | `opencode2:` 优先；未配回落 `opencode:` 的文件名 |
| approver executor 名 | 同 env：`opencode2` 优先，回落 `opencode` |
| 纪律块映射（B129） | 缺省映射 subagent 版（与 opencode 同）；映射表允许显式 `opencode2` 键覆盖，不配不报错 |
| toolchain `order` | 见 3.1 |

回落发生在**读取配置的组装点**，不在 store 层造第二份配置语义；日志打实际用的键与回落来源。

## 4. Spike（实现前置，只读协议）

拿真 `opencode2 serve`（本机已有 v2.0.12）在隔离目录验证并记录：

1. 启动 argv / 端口 / 密码 env 是否仍适用（`OPENCODE_SERVER_PASSWORD`、`OPENCODE_CONFIG` 是否仍被认）。
2. 建会话、发 prompt 的 HTTP 路径与请求体。
3. 事件流：SSE 还是轮询；权限/提问/完成事件的 JSON 形状与 id 字段。
4. 权限应答端点与 deny reason 是否可带。
5. 全局插件发现路径是否仍为 `~/.config/opencode/plugins/`。
6. 凭证文件路径是否仍为 `~/.local/share/opencode/auth.json`。

产出：`docs/superpowers/specs/b397-spike-v2-serve.md`（或并入 plan 前置任务）。spike 改变上文假设时，**先改本设计再实现**。

## 5. 文档

- `README.md` / `README.zh-CN.md`「各 executor 须知」：加 `opencode2`（就绪判据、MVP 降级清单、与 `opencode` 的 fail-closed 关系）。
- `skills/handoff/SKILL.md`：执行器选型列表提 `opencode2`；monitor 能力差异按 V1/V2 宿主分述（装了哪份插件才有 `monitor` 工具）。
- 降级清单三处（包 doc、README、SKILL）逐条一致。

## 6. 验收锚点（整卡怎么算过）

1. 纯 V1 机器：`skill install` 写 V1 插件，`PluginStatus` in_sync；有 V2（`opencode2` 或 `opencode` 报 v2）写 V2 插件，哈希 in_sync。
2. V1 adapter 在 v2 二进制上 Start 返回拒发文案，任务 failed，不发任何 V1 协议请求。
3. `--executor opencode2` 在本机跑通 MVP：权限 approve/deny、question 回答、continue、done 全链路至少各一次真机。
4. 配置回落：只配 `env.opencode` 时 `opencode2` 任务注入同一 env；只配 `env.opencode2` 时覆盖生效。
5. `go test ./...` 绿；V1 包测试除 fail-closed 新增用例外无行为漂移。
6. README/SKILL 降级清单与 `Degraded` 声明一致（审查项）。

## 7. 风险

| 风险 | 缓解 |
|---|---|
| V2 beta API 继续变 | MVP+降级清单；spike 先行；假设被证伪先改设计 |
| 插件发现路径/env 变了 | spike 第 5/6 条；分发与探测参数化，不散落硬编码 |
| 共享层抽错形状 | 只抽稳定重复；宁可短期两份 |
| `opencode` 名字被 v2 覆盖的存量机器 | fail-closed 文案指路 `opencode2`；toolchain 探测把 v2 装机报成「有 opencode2 能力」 |
