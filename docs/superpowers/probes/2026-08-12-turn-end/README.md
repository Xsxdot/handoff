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

## 结果表（15 行，逐次填）

| # | 场景 | executor | handoff 判成 | 任务落到 | 事件层信号（原始样本里看到什么） | 样本文件 |
|---|------|----------|-------------|---------|--------------------------------|---------|
| 1 | S1 | opencode | | | | |
| 2 | S1 | claudecode | | | | |
| 3 | S1 | grok | | | | |
| 4 | S1 | codex | | | | |
| 5 | S2 | opencode | | | | |
| 6 | S2 | claudecode | | | | |
| 7 | S2 | grok | | | | |
| 8 | S2 | codex | | | | |
| 9 | S3 | opencode | | | | |
| 10 | S3 | claudecode | | | | |
| 11 | S3 | grok | | | | |
| 12 | S3 | codex | | | | |
| 13 | S4 | opencode | | | | |
| 14 | S4 | grok | | | | |
| 15 | S4 | codex | | | | |

**claudecode 无 S4**：`internal/executor/claudecode/` 下无原生提问通道翻译（grep 无 `askedViaTool`）。合计 15 次，不是 16。

## 结论（按 spec §3.5 的规则套用，逐 executor）

| executor | S3 是否复现 | S3 信号能否与 S1 区分 | 处置 |
|---|---|---|---|
| opencode | | | |
| claudecode | | | |
| grok | | | |
| codex | | | |
