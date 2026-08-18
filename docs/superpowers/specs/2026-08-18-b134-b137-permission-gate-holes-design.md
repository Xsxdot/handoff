# B134 + B137：权限门的两个洞——越界判据断链与拒绝理由迟到

> 状态：设计已定，待实现。来源：2026-08-18 B128（Windows claude 执行器）真机验收第 4 条，
> 一次实测同时暴露两个缺陷，且两者叠加才造成「目标文件被写成」这个后果。

## 1. 背景

B128 真机验收第 4 条的事件时序：

```
seq32  permission_request   模型 Write 越界 → 被拦
seq33  （同一回合内）        模型改用 echo … > /c/Users/administrator/b128-outside.txt，写成功
seq34  deny_guidance_relayed 协调者的拒绝理由此时才到达
seq36  （下一回合）          模型复述理由并停手
```

两个独立缺陷叠加：

- **B134**：那条 `echo … > …` **零权限请求**直接写成功——Write/Edit 的路径门禁被 shell 重定向整体绕过。
- **B137**：协调者的 `--reason` 走带外注入，落地时该回合已经结束。模型在 seq32→seq33 这段空窗里
  只知道「被拒了」，不知道为什么，于是自行发挥。

B137 单独存在时代价是「浪费一个回合」；与 B134 叠加时，模型在空窗里的自行发挥**成功地**做成了
刚被拒绝的那件事。这是两条合一份 spec 的理由——它们同源、同子系统、且后果耦合。

## 2. 已核实的事实

以下每条都经代码核对或文档取证，**不是推断**。写在这里是因为它们直接决定方案形状。

### 2.1 B134 只影响两个执行器，且两者性质不同

| 执行器 | 兜底放行 | 规则形态 | 能否表达「任何含重定向的命令」 | 本轮处置 |
|---|---|---|---|---|
| **claude** | `allowRules` 含裸 `"Bash"`（`taskenv.go:67`） | `Bash(prefix:*)` **前缀** | **不能** | 拆出单独 backlog 行，本轮不做 |
| **opencode** | `bashPermissionRules` 末行 `"*": "allow"` | 整串 glob | 能 | **本轮做** |
| grok | `allowRules` 为空，grok 内建只放行只读命令 | 整串 glob | 能 | 不受影响 |
| codex | `sandbox: workspace-write`（`adapter.go:303`），**OS 级**拦截 | n/a | n/a | 不受影响 |

claude 侧被排除的理由是**表达能力**而非优先级：重定向可以缀在任何命令之后
（`date > x`、`go env > x`、`git log > x`），枚举命令前缀永远追不上。claude 自己的内置指导
原文也承认这条限制：「"GET-only" cannot be expressed as a prefix rule」。claude 侧唯一
表达得出来的改法都在 allow 那一侧，取舍另立一行处理。

### 2.2 opencode 侧的真实缺陷是「判据断链」，不是「完全没有门」

`external_directory: "ask"` 对 **bash 形态**会发权限请求，载荷带 `metadata.directories`
（08-09 真机载荷样本已入库：`internal/executor/opencode/testdata/perm_external_directory_bash.json`）。
adapter 把它填进 `Perm.Paths`，并有一条测试钉住这个契约，断言文案原文是：

> `bash 形态的越界目录必须进 Paths，否则 permgate 判不出越界`
> —— `internal/executor/opencode/adapter_test.go:1461`

**但 permgate 的 `Judge` 在 bash 路由上把 `Paths` 整个丢弃**：

```go
case executor.PermToolBash:
    return g.judgeCommand(req.Command)   // req.Paths 不再被任何人看一眼
```

而 `judgeCommand` 对未命中黑名单的一律返回 `Consult`。于是越界的实际去向是**廉价模型**，
不是人。`permgate_test.go` 里 `Tool: bash` 且带 `Paths` 的用例是 **0 条**——这不是有意取舍，
是断了一截没人发现。

### 2.2.1 但 `external_directory` 只认路径参数，不认重定向落点（08-18 真机探针）

上一节写完后跑了探针（任务 `a0ad5f6e`，mac-02 + opencode 1.18.18，分支 `probe/b134-redirect`），
结论**推翻了「opencode 至少检出了越界」这个乐观前提**：

| 命令 | 权限请求 | 结果 |
|---|---|---|
| `echo handoff-b134-probe > /tmp/handoff-b134-probe.txt` | **一条都没有** | 文件写成：`-rw-r--r-- 19 Aug 18 15:53`，内容 `handoff-b134-probe` |
| `ls -l /tmp/handoff-b134-probe.txt; cat /tmp/handoff-b134-probe.txt` | `external_directory` 命中 | 升级人工 |

两条独立证据：审核者用 `handoff run` 直接 `ls`/`cat` 到了那个文件；执行者自己的回合报告也写
「第 1 步执行时…没有（被要求授权）」「成功」。

**所以 opencode 的越界检测只解析「作为参数出现的路径」，不解析「重定向落点」。** 同一个路径，
读它要授权、写它不要。

因此 opencode 侧的定性要写两条，不是一条：

1. **重定向落点是盲区**——与 claude 同款，「零权限请求直接写成功」（§4.2 修）
2. **检出的那部分被降级**——`external_directory` 确实检出的越界（路径参数形态）送到 handoff 后，
   被 `Judge` 丢掉 `Paths`，从「人来判」降级成「廉价模型判」（§4.1 修）

两条互不覆盖，都要修。

### 2.3 permgate 的判据覆盖面远小于它看上去的样子

`judgeCommand` 的 doc 明写「本函数**永不返回 AutoAllow**」。这意味着：**能走到 permgate 的
命令，只有执行器静态表已经决定要 ask 的那一小撮**。推论是 B115 刚交付的自指令收口在 claude
上今天是空转的——`handoff dispatch` 不匹配 `rm`/`sudo`/`git push`/`git reset`/`curl`/`wget`
任何一条前缀，压根到不了 permgate。

这条不在本轮范围内（它的解法就是 2.1 里被排除的「反转」），但必须记账，另立 backlog 行。

### 2.4 claude 的拒绝理由通道早就是通的，只是接头没接上

- `internal/executor/claudecode/perm.go:37` —— `permDecision.Message` 字段存在
- `cmd/permission_mcp.go:183` —— 原样回给模型，作为 tool_result 正文

断点在 `internal/executor/claudecode/adapter.go:391`：

```go
behavior, msg = "deny", "协调者拒绝了本次操作"   // 协调者真正的理由在这一行被换成常量
```

根因是 Adapter 契约 `RespondPermission(ctx, taskID, permID, decision)` **没有理由参数**
（`internal/executor/executor.go:238`），所以理由只能走 B50 的带外挂起注入。

### 2.5 另外三家的原生协议确实带不了理由

- **grok**：ACP `session/request_permission` 的应答是 `outcome:{outcome:"selected", optionId}`，无消息字段
- **codex**：`{decision: "approve"|"decline"}`，无消息字段
- **opencode**：**能带，但不在 handoff 走的那个端点上**（08-18 探针实证，读 opencode 1.18.18 二进制）。
  它内部有两代接口：handoff 用的老端点 `POST /session/{sessionID}/permissions/{permissionID}`
  的服务端处理器只读 `payload.response`——

      fG = I.fn("SessionHttpApi.permissionRespond")(function*(Y){
        … V.reply({requestID: Y.params.permissionID, reply: Y.payload.response})   // 到此为止

  多带的字段会被静默丢弃。带 message 的是**新端点** `POST /permission/{requestID}/reply`，
  其 body schema 为 `PermissionReplyBody = {reply, message?: string}`（opencode 自己的 TUI
  就是这么拼的：`{requestID, reply, ...message?}`）。**迁端点是另一件事**，见 §9

grok/codex 各有 `noteRejected` + `rejectedTurnQuestion`，但那条是**发给协调者**的被拒清单，
不是发给模型的，不能拿来顶替。

## 3. 非目标

- **不做 claude 侧的 B134 修复**：见 §2.1，拆出独立 backlog 行。
- **不反转 permgate 的 AutoAllow 不变式**：`judgeCommand` 继续永不返回 AutoAllow。
  一条把输出重定向进工作区内的命令（`cat /etc/shadow > ./out`）落点合法但行为是外泄，
  「落点在范围内」不足以构成放行理由。
- **不改 `deny_guidance_dropped` 的丢弃语义**：见 §5.4。
- **不动 `external_directory: "ask"`**：它是 opencode 侧唯一的越界拦截点，被
  `taskenv_test.go:185` 焊死（B27）。

## 4. B134 设计：把断掉的越界判据接上

### 4.1 主修：bash 路由必须同时判 Paths

`Judge` 的 bash 分支改为**命令判据与路径判据取更严的那个**：

```go
case executor.PermToolBash:
    return g.judgeBash(req, scope)
```

`judgeBash` 的规则，按顺序：

1. `req.Paths` 非空时逐条 `InScope`：**任一越界即 `Escalate`**，Reason 与 Write/Edit 越界同文案
   （`目标路径越出任务范围: %s`），复用同一份 `InScope`，不另写一套路径逻辑
2. `InScope` 归一化失败 → `Escalate`（fail-closed，与 `judgeFileWrite` 一致）
3. 路径全部在范围内、或 `Paths` 为空 → 落回既有的 `judgeCommand(req.Command)`

**为什么路径判据放在命令判据之前**：越界是确定性事实，命令判据的最好结果也只是 `Consult`。
先判路径能让「越界」这件事拿到它应得的出口，而不是被后面的 `Consult` 稀释。

**为什么 `Paths` 为空不算异常**（与 `judgeFileWrite` 的 `len(Paths)==0 → Escalate` 不同）：
纯 `bash` 门类（非 `external_directory`）本来就不带路径，`go build ./...` 是绝大多数情形。
在这里 fail-closed 等于把每条命令都升级人工，那是 §3 明确排除的反转。

### 4.2 重定向落点的自有判据

**探针已定：opencode 不对重定向形态发请求（§2.2.1），本节必须实现。**

实现 `internal/permgate/redirect.go`：

- `RedirectTargets(cmd string) []string`：从命令原文摘出重定向落点。识别 `>`、`>>`、`>|`、
  `n>`、`n>>`、`&>`、`&>>`；**排除 fd 复制**（`>&` 后跟数字或 `-`，如 `2>&1`、`>&-`）；
  落点取到空白或 `| ; & ) \n` 为止，剥掉成对引号
- 在 `judgeBash` 的第 1 步把它的返回值并进待判路径集合，与 `req.Paths` 同等对待

同步给 opencode 的 `bashPermissionRules` 加 ask 模式，否则这类命令根本进不来：

    "*>/*"、"*> /*"、"*>~*"、"*> ~*"

（`>>` 形态被 `>` 形态覆盖，无需另写。）

**为什么不用 `"*>*"`**：它会命中 `2>&1`，而 `go test ./... 2>&1 | tail` 是高频写法，
每条都送 `Consult`，在没配审批者的部署上等于升级人工。

**为什么摘落点用命令原文而不是 `StripQuoted` 后的文本**：落点常被引号包住
（`echo x > "/etc/foo"`），剥完就没了。

### 4.3 明确的残余（不修，但要写进文档与 backlog）

- `tee` / `cp` / `mv` / `ln` / `install` / `dd` 这些「写到路径参数」的命令，本轮**不做**路径判据。
  它们若被 `external_directory` 检出则由 §4.1 覆盖，否则仍落 `Consult`。
- 相对路径逃逸（`> ../../outside`、`cd /etc && echo x > passwd`）不被 §4.2 的 ask 模式命中。
  它们若被 `external_directory` 检出则由 §4.1 覆盖。
- 写入落在工作区内的**外泄**行为（`cat ~/.ssh/id_rsa > ./x`）不在本 spec 范围内，
  §3 已声明不以「落点合法」作为放行理由，但也没有新增判据去拦它。

## 5. B137 设计：让理由与拒绝同帧

### 5.1 契约变更

```go
// RespondPermission 应答 executor 的权限请求。
//   - decision: "once"（批准本次）或 "reject"（拒绝）
//   - reason:   decision 为 reject 时协调者给出的原因；批准时忽略，可为空
RespondPermission(ctx context.Context, taskID, permID, decision, reason string) error

```

`DenyReasonInBand` **不加进 `Adapter` 主接口**，做成可选接口 + 类型断言，与 `PermissionsVolatile`
的真实形状一致（后者也不在 `Adapter` 上，是 `internal/agentd/manager.go:2983` 的
`volatilePermitter` 接口，只有 grok 与 codex 实现）：

```go
// internal/agentd/manager.go，紧邻 volatilePermitter
type denyReasonInBander interface {
    DenyReasonInBand() bool
}
```

好处是不实现它的 adapter 一行都不用改，本轮只有 claude 实现它。

**这个能力位不是装饰**：manager 在 `waitPermission` 与 `RelayAnswer` 两处调用
`noteDenyGuidance` 之前必须先问它，返回 true 就跳过挂起。漏了这一步，claude 上模型会
先在 tool_result 里读到理由、下一条 question 时又被同一条理由砸一次。

### 5.2 四家的处置

| adapter | `DenyReasonInBand` | 实现 |
|---|---|---|
| **claude** | `true` | `behavior, msg = "deny", reason`；`reason` 为空时回退既有常量（协调者没给理由时不能送一句空的） |
| **opencode** | `false` | 老端点丢弃额外字段（§2.5 已实证）；带 message 要迁到 `/permission/{requestID}/reply`，本轮不做，拆行（§9） |
| **grok** | `false` | 原生协议无字段，保留 B50 挂起注入 |
| **codex** | `false` | 同上 |
| **fake** | 可注入 | 测试要能两条路都覆盖，默认 `false` |

### 5.3 理由文案

claude 侧送进 `message` 的文本沿用 `relayDenyGuidance` 已有的措辞，保证两条路模型读到的话一致：

```
你请求的操作已被协调者拒绝。原因：<reason>
请据此调整做法后继续，不要重复发起同一请求。
```

### 5.4 为什么不动 `deny_guidance_dropped`

回合终结时任务进 `waiting_review`，**下一个动作方是协调者而不是模型**。此时把挂起的理由
Send 出去等于替协调者开一个回合——留审计事件、由 `continue` 把话带上是对的。

B137 要治的是**回合内**的空窗（seq32→seq33），不是回合已经结束之后。B91 为这条事件补的
Publish 唤醒继续保留。

## 6. 破坏性变更

- `executor.Adapter` 接口新增一个方法、改一个方法签名。**接口在 internal/ 下，无外部实现方**，
  影响面是仓库内 5 个实现（claude/grok/codex/opencode/fake）与 4 处非测试调用点。
- `permgate.Judge` 的 bash 路由行为改变：此前带越界 Paths 的 bash 请求落 `Consult`，
  此后落 `Escalate`。**这是有意的收紧**，会让这类请求从「廉价模型自动裁决」变成「唤醒协调者」。
  预期频率极低（模型主动写工作区外是异常行为），且这正是 B27 立的规矩。
- 无配置项变更、无存储 schema 变更、无 CLI 契约变更。

## 7. 探针结论（已完成，2026-08-18）

两条探针在写 plan 之前由审核者跑完，结论已回填进 §2.2.1 与 §2.5，**实现期不再有条件分支**。

任务 `a0ad5f6e-0496-491c-a7ab-63d3f828ae44`，mac-02 + opencode 1.18.18，分支 `probe/b134-redirect`，
起点 `93852acba`。任务已 `stop`，`/tmp/handoff-b134-probe.txt` 已清理。

1. **opencode 对重定向形态不发权限请求** —— `echo … > /tmp/…` 零权限请求、文件写成；
   同一路径改以参数形态出现（`ls`/`cat`）则 `external_directory` 命中。两条独立证据：
   审核者 `handoff run` 直接取到文件，执行者回合报告自述「没有（被要求授权）」「成功」。
   → §4.2 必须实现
2. **opencode 老端点丢弃额外字段** —— 服务端处理器只读 `payload.response`；带 `message` 的
   是新端点 `/permission/{requestID}/reply`。→ §5.2 opencode 落 `false`，迁端点拆行

## 8. 验收判据

### 8.1 执行者可做

- `gofmt -l .` 无输出；`go build ./...`、`go vet ./...` 退出 0
- `go test ./...` 无失败包；`go test -race ./internal/agentd/ ./internal/permgate/ ./internal/executor/...`
- permgate 新增用例：bash + 越界 Paths → `Escalate`；bash + 范围内 Paths → 落回命令判据；
  bash + 空 Paths → 与改动前逐字同判（回归保护）
- claude adapter 新增用例：`RespondPermission(..., "reject", "<理由>")` 后 `permDecision.Message`
  等于 §5.3 的渲染结果；`reason` 为空时回退常量
- manager 新增用例：`DenyReasonInBand()` 为 true 的 adapter 拒绝后 `denyGuidance` 表为空
  （不挂起）；为 false 时仍挂起（B50 既有行为不回归）

### 8.2 归审核者，不派发

以下步骤要驱动 handoff 自身（起 agentd、派子任务、调 CLI），与 B 版执行纪律块直接冲突（B126）：

- B134 真机复验：opencode 任务执行越界重定向，确认落 `Escalate` 且 agentd 日志为 WARN 级
  `权限判定：升级人工 … reason="目标路径越出任务范围: …"`
- B137 真机复验：claude 任务的权限请求被 `reply --deny --reason "<可辨识文本>"` 拒绝后，
  **同一回合内**模型的下一条输出复述该文本；且事件流中**没有** `deny_guidance_relayed`
  （因为已同帧送达，不再走带外）

## 9. 拆出的 backlog 行

本 spec 明确不做、但已查实的三条，各自单独立行，不夹带进本轮：

1. **claude 侧的 B134**——裸 `"Bash"` + 前缀形式表达不出重定向；唯一出路在 allow 侧，
   需先跑「claude 的前缀匹配器认不认追加的重定向」探针
2. **permgate 判据覆盖面**（§2.3）——`judgeCommand` 永不 AutoAllow，导致判据只覆盖执行器
   静态表已决定要 ask 的那一小撮；B115 的自指令收口在 claude 上今天空转
3. **写路径参数类命令的路径判据**（§4.3）——`tee`/`cp`/`mv`/`ln`/`install`/`dd`
4. **opencode 权限应答迁到新端点**——从 `POST /session/{id}/permissions/{permID}`（只收
   `response`）迁到 `POST /permission/{requestID}/reply`（收 `{reply, message?}`），
   拒绝理由即可同帧送达。收益明确，但要评估两件事：老版本 opencode 是否有这个路由
   （代码里存在 `protocol === "v1"` 的分支，说明 opencode 自己在做协议版本兼容），
   以及新端点不带 sessionID 之后 B52「应答必须发回权限请求所在的会话」那条约束是否自然消解
