# B397 OpenCode V2 支持：monitor 插件 v1/v2 分发 + opencode2 执行器 — 设计

- 日期：2026-09-22
- 卡：B397（高，charter · 待办）
- 状态：spike 已完成，设计已按实测修订，待用户过目
- spike 报告：`docs/superpowers/specs/b397-spike-v2-serve.md`（136 条路由表 `b397-spike-v2-serve-routes.md`；OpenAPI 全文在 spike 目录归档）

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
- 实现前置：spike 真 `opencode2 serve` 协议——**已完成**（§4，报告 `b397-spike-v2-serve.md`）。
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
- 加载落点：V2 全局插件发现 **`~/.config/opencode/plugins/`（spike 实测不变）**；现有 V1 插件在 V2 下 `state.status:"failed"`（导出不符 `Plugin must export a default definition with an id and an effect or setup function`）。
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
- 凭证：spike 实测 V2 仍用 `~/.local/share/opencode/auth.json`，首版直接沿用 `opencode` 的判据。
- `credRelPath` Windows 过滤逻辑对 `opencode2` 同样适用（凭证路径相同）。

### 3.2 fail-closed（V1 adapter）

`internal/executor/opencode` 的 `Start`（或 `serveSpec` 组参前）：对将要 exec 的二进制做一次 version 判定（缓存可接受，进程级一次即可）：

- major ≥ 2 → 返回明确错误：`本机 opencode 是 v2，请改用 --executor opencode2`（或等价中文），任务 `failed`，不静默换协议。
- 判定成本/时机以实现最小侵入为准；**禁止**在 V1 包内路由到 V2 协议。

`opencode2` adapter 反向不强制（若用户拿 `opencode2` 名字却只有 v1 二进制，启动自然失败并报真因）。

### 3.3 新包 MVP（`internal/executor/opencode2`）

实现 `executor.Adapter` 五动作的最小闭环（HTTP/SSE 契约按 spike §4.2，OpenAPI 归档为实现依据）：

| 动作 | MVP 行为 |
|---|---|
| Start | 经 prochost 起 `opencode2 serve --port <随机> --hostname 127.0.0.1`，`OPENCODE_SERVER_PASSWORD` 注入（与 V1 同款）→ 就绪探测（HTTP 层可应答即算，密码校验属后续请求）→ `POST /api/session` → 渲染 prompt（`turn.RenderPrompt`，Discipline 必传）→ `POST /api/session/{sid}/prompt` → 订阅 `GET /api/event` SSE |
| Events | 从 SSE `type` 映射：`permission.asked`→permission（`PermissionID`=data.id，`Perm` 由 action/resources 归一）、`session.execution.succeeded`→result OK、失败终态→result Fail（形状实现期补验）、text/tool 进度→progress；提问若无原生通道，按 MVP 降级见降级清单 |
| Send | `POST /api/session/{sid}/prompt`（delivery steer/queue 按续接语义） |
| RespondPermission | `POST /api/session/{sid}/permission/{pid}/reply` `{"decision":"once\|reject"}` → 204；reject 的 reason 回流实现期补验（spike 只验 once），做不到就走带外注入降级 |
| Stop | kill serve + 关 SSE；会话保留（不 DELETE，供 continue） |

**降级清单机制**：包内 `var Degraded = []string{...}`（或等价常量），首版至少声明：

- 断线冷恢复 / resume 对账 / `Last-Event-ID` SSE 重放（spike 未验且不在首版）：**不支持**——serve 死亡直接终态失败，提示重新 dispatch 或人工处置（不假装能 resume）。
- Spend / Timing / Usage 细粒度：不上报（事件不带这些字段），UI 显示空 ≠ 报 0。
- reconcile / 未知终态兜底：若 MVP 未实现，遇未知帧 fail-closed 收束为 turn_failed 或 failed 并留 serve.log 尾部。
- deny 同帧送达：spike 只验 `once`；reject 的 reason 回流实现期补验，做不到就走既有带外注入路径。

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

## 4. Spike（已完成，2026-09-22，v2.0.12 真机）

### 4.1 实测确认「不变」的假设

| 假设 | 实测 |
|---|---|
| `OPENCODE_SERVER_PASSWORD` 生效 | 生效；另有别名 `OPENCODE_PASSWORD`；都不设则 serve 随机生成并打 stdout。401 空 body，用户固定 `opencode` |
| `OPENCODE_CONFIG` 生效 | 生效；坏路径/非法 json **静默容忍**（不报错） |
| 全局插件发现 `~/.config/opencode/plugins/` | 不变；`opencode2 plugin list` 正常列出本地 `.ts` |
| 凭证 `~/.local/share/opencode/auth.json` | 不变（`~/.config/opencode/auth.json` 已废弃） |
| 真实 prompt / 权限闭环可跑 | 已跑通（默认 provider），生命周期与 `permission.asked/replied` 事件抓到原文 |

**额外实锤**：本机现存 V1 monitor 插件在 V2 下加载失败：`Plugin must export a default definition with an id and an effect or setup function.` —— 双源码分发的必要性直接证实。

### 4.2 实测推翻 / 修订的假设（adapter 按此实现）

| v1 习惯 | v2 实测 |
|---|---|
| 根路径 API（`/session`、`/event`…） | 全部加 **`/api` 前缀**；根路径落 SPA HTML 兜底 |
| SSE 端点 | **`GET /api/event`**；只有 `data:` 行、事件名在 JSON `type`，`: heartbeat` 注释帧 |
| `POST /session/<id>/message` | 取消；发消息 = **`POST /api/session/{sid}/prompt`**（`delivery: steer\|queue`），无写入型 `/message` |
| `POST /permission/<id>` | **`POST /api/session/{sid}/permission/{pid}/reply`**，body `{"decision":"once\|always\|reject"}` → **204** |
| `/doc` 是 OpenAPI | `/doc` 是 HTML；唯一 OpenAPI = **`GET /openapi.json`**（3.1，113 paths / 245 schemas） |
| 权限事件形状 | `permission.asked`：`{id(per_…), sessionID, action(bash/shell/…), resources[], save[], source{type,messageID,id}}`；待决列表 `GET /api/permission/request` |
| 终态与回合事件 | 成功终态 `session.execution.succeeded`（失败形态待补验）；回合内 `session.step.started/streamed/ended`、`session.text.*`、`session.tool.*`、`session.usage.updated`；等待可用 `POST /api/experimental/session/{sid}/wait` |
| 错误体 | 400/401/404/500 JSON 带 `_tag`（未知路由 404 **空 body**，不要假设 JSON） |

事件 type 全集、SSE 原文样例、错误体形状在 spike 报告 §三/§七；实现以 `openapi.json` 归档为准。`Last-Event-ID` 断线重放**未验证**——归入降级清单。

### 4.3 实现期补验项（spike 未覆盖）

- 失败/异常回合的终态事件形状（succeeded 已见，failed/aborted 未见）——首只真任务时补验。
- deny（reject）后模型侧收到什么（理由如何回流）——spike 只验了 once；实现 RespondPermission 时补验。

## 5. 文档

- `README.md` / `README.zh-CN.md`「各 executor 须知」：加 `opencode2`（就绪判据、MVP 降级清单、与 `opencode` 的 fail-closed 关系）。
- `skills/handoff/SKILL.md`：执行器选型列表提 `opencode2`；monitor 能力差异按 V1/V2 宿主分述（装了哪份插件才有 `monitor` 工具）。
- 降级清单三处（包 doc、README、SKILL）逐条一致。

## 6. 验收锚点（整卡怎么算过）

1. 纯 V1 机器：`skill install` 写 V1 插件，`PluginStatus` in_sync；有 V2（`opencode2` 或 `opencode` 报 v2）写 V2 插件，哈希 in_sync；装出的 V2 插件在 `opencode2 plugin list` / `GET /api/plugin` 里 `status != failed`。
2. V1 adapter 在 v2 二进制上 Start 返回拒发文案，任务 failed，不发任何 V1 协议请求。
3. `--executor opencode2` 在本机跑通 MVP：权限 approve/deny、question 回答、continue、done 全链路至少各一次真机。
4. 配置回落：只配 `env.opencode` 时 `opencode2` 任务注入同一 env；只配 `env.opencode2` 时覆盖生效。
5. `go test ./...` 绿；V1 包测试除 fail-closed 新增用例外无行为漂移。
6. README/SKILL 降级清单与 `Degraded` 声明一致（审查项）。

## 7. 风险

| 风险 | 缓解 |
|---|---|
| V2 beta API 继续变 | MVP+降级清单；spike 先行（已完成）；剩余未验项（4.3）实现期补验 |
| 插件发现路径/env 变了 | spike 已实测不变；分发与探测仍参数化，不散落硬编码 |
| 共享层抽错形状 | 只抽稳定重复；宁可短期两份 |
| `opencode` 名字被 v2 覆盖的存量机器 | fail-closed 文案指路 `opencode2`；toolchain 探测把 v2 装机报成「有 opencode2 能力」 |
