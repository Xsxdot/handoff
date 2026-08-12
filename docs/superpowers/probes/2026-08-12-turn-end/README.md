# 回合终结信号探针（2026-08-12）

依据 spec `docs/superpowers/specs/2026-08-12-false-completion-and-cursor-durability-design.md` §5。
**只回答一个问题：S3（截断）在事件层有没有可判别于 S1 的信号。**

## 前置

- **本机** agentd 以 `HANDOFF_RAW_TAP_DIR=~/handoff-probe-raw` 启动（旁路见 `internal/executor/rawtap`），且二进制含 `feat/probe-rawtap`
- 沙箱仓库 `probe-sandbox` 已在本机登记
- `~/handoff-probe-raw/archived/` 已建好（改名归档的落点）
- **串行**：任何时刻只有一个任务在跑

## 每次派发的动作

`handoff dispatch` 用 `os.ReadFile` 按**当前目录**读 plan 文件（`cmd/dispatch.go:141`），
而派发必须在沙箱仓库里执行（project_id 按 origin 算）——两者不在同一个仓库，
所以 plan 路径**必须写绝对路径**。先固定一个变量，后面所有派发都用它：

```bash
export PROBE_DIR="$(cd <handoff 仓库工作树> && pwd)/docs/superpowers/probes/2026-08-12-turn-end"
ls "$PROBE_DIR"/S1-natural-finish.md   # 确认路径对，再往下发
```

```bash
cd ~/workspace/handoff-probe-sandbox && handoff dispatch "$PROBE_DIR/<Sn>.md" --project probe-sandbox --executor <x> --new-branch probe-<Sn>-<x> --new-worktree --name "probe <Sn> <x>"
```

派发前查本机进程余量（不足则停）：

```bash
echo "maxprocperuid=$(sysctl -n kern.maxprocperuid) 已用=$(ps -u $(id -u) | wc -l)"
```

派发后：

```bash
handoff show <task-id>
```

**每次派发结束后必须立刻把样本改名归档，再发下一次**：

```bash
mv ~/handoff-probe-raw/<executor>-*.jsonl ~/handoff-probe-raw/archived/<Sn>-<executor>.jsonl
```

**为什么这一步不能省**：`rawtap` 以 `O_APPEND` 打开文件，而 opencode / grok / codex 三家的 taskID 传的是空串（`Dial` / `streamOnce` 不持有任务标识，D1 实现时按 plan 允许的退化处理），文件名因此是 `opencode-.jsonl` 这种不带任务区分的形式。opencode 一家要跑 S1/S2/S3/S4 四个场景——不改名就是四个场景全部追加进同一个文件，**样本混在一起，分不出哪段属于哪个场景，整轮探针作废**。claudecode 传了真 taskID（文件名带任务 ID）不会混，但改名步骤对四家一视同仁执行，不要按 executor 区别对待。

## 结果表（15 行，实测于 2026-08-12 22:09–22:40）

| # | 场景 | executor | handoff 判成 | 任务落到 | 事件层信号（原始样本里看到什么） | 样本文件 |
|---|------|----------|-------------|---------|--------------------------------|---------|
| 1 | S1 | opencode | result OK（trailer 缺失，按 git 新提交判定） | waiting_review | `step-finish` 三次，`reason` 依次 `tool-calls`/`tool-calls`/`stop`；write 与 bash 两个 tool part 均走完 `pending→running→completed`；收尾 `session.status:busy→idle` → `session.idle` → `session.diff` | `S1-opencode.jsonl` |
| 2 | S1 | claudecode | **failed**：`claude 回合异常结束（subtype=success）` | waiting_review | **模型从未运行**：`assistant` 帧 model=`<synthetic>`、`stop_reason=stop_sequence`、正文 `Failed to authenticate: OAuth session expired and could not be refreshed`；末帧 `is_error:true`、`terminal_reason:"api_error"`、`num_turns:1`、输出 token 全 0 | `S1-claudecode-authfail.jsonl`（重试：`S1-claudecode-authfail-retry.jsonl`） |
| 3 | S1 | grok | result OK（「模型未输出收尾协议，按 git 新提交判定完成」） | waiting_review | `turn_completed` 带 `stop_reason:"end_turn"`；`_x.ai/session/prompt_complete` 带 `stopReason:"end_turn"`；JSON-RPC `id:3` 的 `result.stopReason="end_turn"`；无 `error` 帧 | `S1-grok.jsonl` |
| 4 | S1 | codex | result OK（同上 fallback 文案） | waiting_review | `item/completed` 的 `agentMessage` 带 `phase:"final_answer"` → `thread/status/changed status:idle` → `turn/completed` 带 `status:"completed"`、`error:null` | `S1-codex.jsonl` |
| 5 | S2 | opencode | **question**（把「描述打算」合成成了 ask） | waiting_answer | 与 S1 逐帧同形：`step-finish` = `tool-calls`/`tool-calls`/`stop`，收尾 `session.idle` + `session.diff:[]`。事件层与 S1 无任何差别 | `S2-opencode.jsonl` |
| 6 | S2 | claudecode | — 未跑（executor 鉴权失效，见第 2 行） | — | — | — |
| 7 | S2 | grok | **question** | waiting_answer | `stopReason:"end_turn"`（与 S1 完全同形）；`last_turn_summary` 正常发出；无 `error` 帧 | `S2-grok.jsonl` |
| 8 | S2 | codex | **question** | waiting_answer | `turn/completed` `status:"completed"`、`error:null`（与 S1 完全同形）；`thread/status:idle` 正常 | `S2-codex.jsonl` |
| 9 | S3 | opencode | question | waiting_answer | **复现，且信号明确**：多出一条 `step-finish` 且 `reason:"unknown"`（tokens 全 0、cost 0）——S1/S2 基线里只有 `tool-calls` 与 `stop`，`unknown` 从未出现；同时 `write` 那个 tool part 停在 `status:"pending"` 后，`tool` 字段被翻成 `"invalid"`，`state.input.error` = `Invalid input for tool write: JSON parsing failed: ... Unterminated string` | `S3-opencode.jsonl` |
| 10 | S3 | claudecode | — 未跑（executor 鉴权失效，见第 2 行） | — | — | — |
| 11 | S3 | grok | question | waiting_answer | **未复现（含 1 次重试）**：全程最大帧 101 KB，且那是 `available_commands_update` 样板帧，从未发出超长 write；收尾 `stopReason:"end_turn"`，与 S1 同形。重试次数 1，第二次改用脚本生成内容（场景明令禁止）被 `--deny` 驳回，回合以 `stopReason:"cancelled"` 结束 | `S3-grok.jsonl`（重试：`S3-grok-retry.jsonl`） |
| 12 | S3 | codex | question | waiting_answer | **未复现——因为根本没被截断**：单帧 700 KB 的 `item/started+completed` `fileChange:add`，20000 行 / 1.88 MB 一次调用完整落盘；`turn/completed` 仍是 `status:"completed"`、`error:null`，与 S1 同形 | `S3-codex.jsonl` |
| 13 | S4 | opencode | question（**走原生通道**） | waiting_answer | `question.asked` 事件，带原生 `que_…` id 与结构化 `options`；对应 tool part `tool:"question"` 走 `pending→running`。ticket_id 形如 `<task>:que_…`，与合成 ask 的裸 UUID ticket 明显不同 | `S4-opencode.jsonl` |
| 14 | S4 | grok | question（**走原生通道**） | waiting_answer | JSON-RPC 请求 `_x.ai/ask_user_question`（带 `toolCallId` 与 `options`）+ `session_notification` 的 `pending_interaction` 且 `kind:"question"`（同一 tool_call_id 上先 `kind:"permission"` 后 `kind:"question"`） | `S4-grok.jsonl` |
| 15 | S4 | codex | question（**合成**，非原生） | waiting_answer | 样本里**没有任何**提问类方法帧（无 `ask`/`question`/`request_permission`）；模型自述「本环境无原生提问通道」，收尾仍是 `turn/completed` `completed`/`error:null` | `S4-codex.jsonl` |

**claudecode 无 S4**：`internal/executor/claudecode/` 下无原生提问通道翻译（grep 无 `askedViaTool`）。合计 15 次，不是 16。

**本轮实跑 15 次，其中 claudecode 的 3 格（#2/#6/#10）因 executor 鉴权失效未能取得有效样本**（`Failed to authenticate: OAuth session expired and could not be refreshed`，重试 1 次结果相同，两份样本均已归档为证据）。这是环境阻塞，不是「模型行为未复现」，两者不可混记。另有 2 次重试派发（grok S3、claudecode S1），实际派发 17 次。

### 关于 S4 判据的一处更正

plan Task 6 Step 2 建议用 `grep -c "本回合已通过 question 工具提问" ~/.handoff/agentd.log` 作为「原生通道真的被走了」的判据。**这条判据不可用**：该字符串出现在 `internal/executor/opencode/adapter.go:1658`，是一条 `a.log.Debug`，且只在「因原生提问已发生而抑制 trailer 提问工单」这一条分支上打。agentd 跑在 INFO 级别，它永远不会出现。本轮 S4 前后该计数都是 0，而 opencode 的原生 question 工具**确实被走了**。

因此 spec §2.3 里「devbox agentd.log 里该串出现 0 次 ⇒ 原生 question 工具历史上从未被用过」的推论**不成立**——0 次是日志级别造成的，不是行为证据。可用的判据是原始样本里的 `question.asked` 事件（opencode）/ `_x.ai/ask_user_question` 请求（grok）。

## 结论（按 spec §3.5 的规则套用，逐 executor）

| executor | S3 是否复现 | S3 信号能否与 S1 区分 | 处置 |
|---|---|---|---|
| opencode | **复现**（一次调用即被传输层截断，JSON 解析失败） | **能**：`step-finish reason:"unknown"` + tool 翻成 `"invalid"` 且带 `JSON parsing failed / Unterminated string`，两者在 S1/S2 基线中均为零出现 | **加证据层** |
| claudecode | 未测（executor 鉴权失效，无有效样本） | 未测 | 不加（本轮无结论，需补测） |
| grok | **未复现**（2 次尝试均未发出超长调用；第 2 次改用脚本生成被驳回） | 不适用 | **不加** |
| codex | **未复现**（1.88 MB 单次调用完整送达，未发生截断） | 不适用 | **不加** |

### 一句话结论

**只有 opencode 一家在事件层留下了可判别于 S1 的截断信号**，且信号很硬（`reason:"unknown"` 与 `tool:"invalid"` 都是基线里零出现的取值）。grok 会主动绕开超长调用因而诱不出截断；codex 的传输层能扛住 1.88 MB 的单次工具调用，压根不截断。claudecode 本轮因鉴权失效未测。

顺带测出的、与 S3 无关但更普遍的一条：**S2（不改不提交）在四家上全部被判成 `question`**，而其事件层与 S1 逐帧同形——判定差异完全来自 git 状态而非事件流。这说明 trailer 缺失时的分类目前只能靠仓库副作用兜底，事件层给不出任何帮助。
