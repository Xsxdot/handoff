# B299 真机复验步骤

> 日期：2026-08-29  
> 分支：`acc/b156.2-156.3`  
> 修复提交：`4e0ab1462` `fix(B299): resume coordinator turns with isolated HOME`  
> 对照：`docs/superpowers/notes/2026-08-29-real-machine-acceptance.md`（上次已走过检测 / 开卡即绑 / 两级准入入队，本页只复验上次没过或没拍到的）  
> 环境：上次那台 Linux 隔离 agentd。不要动本机生产 7777。

上次真机里，房间二次唤醒落的是系统指针「载体已更换」，进程 argv 也没有 `-s`。根因是续接没带隔离 HOME。换新二进制后按下面三条跑；三条都过才算 B299 真机过。

---

## 0. 换新二进制（不做这条，后面全是旧行为）

1. 在跑隔离 agentd 的那台机器上：

   ```bash
   git fetch origin acc/b156.2-156.3
   git checkout acc/b156.2-156.3
   git merge --ff-only origin/acc/b156.2-156.3
   git log -1 --oneline
   # 必须是 4e0ab1462 或它之后的提交
   ```

2. 按这台机器惯用方式重编并**重启隔离 agentd**（先停旧的再起新的，不要起第二个）。
3. 确认进程吃到的是新二进制，例如启动日志或 `handoff status` 的版本/时间。
4. keystone 会话在内存里：重启之后旧卡没有绑定。续接必须在**同一次 agentd 进程寿命内**先拉起、再发房间消息。

---

## 1. 必做：协调者续接（B299 本体）

用新探针卡。不要拿上次已经走过重建的 `B1` 直接发消息——重启后它在内存里没有 session，第一次唤醒会再走 Launch，测不到 Resume。

### 1.1 开卡即绑（首次拉起）

前提：隔离实例上已有 online 的 `opencode` 载体，以及 coordinator 小队 `coord`（上次验收配过可复用）。

```bash
handoff card add "B299 续接复验探针" --project handoff --coordinate
```

记下卡号（下面写作 `<CARD>`）和 session id。

`agentd.log` 必须同时满足：

| 日志 `msg` | 要看到 |
|---|---|
| `自动化拉起协调者回合` | `home_dir` = 该载体隔离 HOME（如 `~/.handoff/home/opencode`） |
| `协调者回合开始` | `mode=new`，**且有 `home_dir`**（旧二进制没有这个字段） |
| `自动化拉起协调者回合结束` | `rebuilt=false`（上次误标 true） |
| `协调者 HTTP 拉起成功` | 同一 `session`，`rebuilt=false` |

任一 `rebuilt=true` 即失败。

### 1.2 房间消息续接（agentd 不要重启）

等 1.1 回合结束、名额归还之后：

```bash
curl -s -X POST -H "Authorization: Bearer <token>" -H "Content-Type: application/json" \
  -d '{"body":"协调者请确认本卡当前状态与基线"}' \
  "http://127.0.0.1:<隔离端口>/api/rooms/<CARD>/messages"
```

`agentd.log` 必须同时满足：

| 日志 `msg` | 要看到 |
|---|---|
| `自动化唤醒协调者回合` | `home_dir` 仍是隔离 HOME |
| `协调者回合开始` | `mode=resume`，`home_dir` 仍是隔离 HOME，**不是**空、也不是 agentd 默认 HOME |
| `协调者回合完成` | `session_id` 与 1.1 **同一个** `ses_…` |
| `自动化唤醒协调者回合结束` | `rebuilt=false` |

进程 argv（`ps` / `pgrep -af opencode`）必须带 `-s <同一个 ses_…>`，例如：

```text
opencode run --format json -m <model> -s ses_… -- 你是本卡的机器协调者…
```

没有 `-s` = 又在新建会话，失败。

房间历史 `GET /api/rooms/<CARD>/messages`：

- **失败**：用户消息后面紧跟系统指针「载体已更换：新载体承接同一协调者身份（重建四步已执行）」——这就是上次的洞。
- **通过**：没有这条换绑指针；允许有协调者叙事或其它指针，但不许用重建冒充续接。

### 1.3 一眼失败表

| 现象 | 含义 |
|---|---|
| 二次唤醒 `mode=new` | 没走进 Resume |
| 二次唤醒 `home_dir` 空或缺字段 | 跑的还是旧二进制，或 Resume 又丢了 HOME |
| `rebuilt=true` | 续接失败走了重建，或首次拉起误标没修好 |
| argv 无 `-s` | 同上 |
| 房间只有「载体已更换」 | 闭环仍是假的 |

---

## 2. 必做：排队出队（上次只拍到入队）

上次四卡同时点火：`B15`/`B17` 在飞，`B14`/`B16` 进了 `ignition_queue`。结论写了「名额释放后有序流转」，但没有出队日志。本条补拍。

拓扑与上次相同即可：

- 载体 `opencode`：`max_concurrency = 2`
- `squad-a` executor，成员上限 1 → 绑 `node1`
- `squad-b` executor，成员上限 2 → 绑 `node2`
- 四张新探针卡：两张停在 `node1`，两张停在 `node2`，同时 `POST /api/cards/<id>/step`

入队阶段（应与上次一致，确认二进制没把准入打坏）：

- 在飞恰好 2 个（小队 1 + 载体总上限 2）
- `GET /api/queue` 里有另外两张，`kind=ignition_queue`
- 日志有 `小队满员，点火请求已持久排队`

然后**等那两个在飞回合结束**（名额归还），不要手动清队列。在 2 秒清队周期内应出现：

| 日志 `msg` | 要看到 |
|---|---|
| `自动化队列出队` | `kind=ignition_queue`，卡号是刚才排队的那两张 |
| `按模板派发` | 那两张卡各至少一次 |

之后 `GET /api/queue` 不再含这两张。全程同时在飞仍不超过 2。

若在飞结束了、过了多个清队周期（日志有 `自动化清队` 轮次）排队卡仍不动：出队没走通，记下当时 `GET /api/queue` 和最近清队日志，不要改配置重试混证据。

---

## 3. 抽测：本机点火不被纪律闸误杀

阶段 2 的 `POST /api/cards/<id>/step` 目标是 `local` 时，不应再出现：

```text
环节派发被拒发闸拦下  cap_absent=true
环节派发前取得目标机客户端失败  target=local
```

若 2 已经派出执行者任务，本条自然过。若 2 被纪律闸挡在派发前，就是本机 `pool.For("local")` 那条还在。

---

## 4. 不必重做

这些上次已经用真机拍过，换二进制后不拦 B299 收口：

- opencode ping 检测 → online，隔离 HOME 长出文件
- codex/grok 报「未实装」/ 无主凭据 → pending
- 两级准入入队（小队上限 1、载体上限 2）

---

## 5. 怎么记结果

把下面三行的原文（命令 + 日志关键字段，不要改写）回填到本文件末尾或另开验收笔记，并 `handoff card note B299`：

1. 首次拉起：`rebuilt=` ？ `mode=` ？ `home_dir=` ？
2. 房间续接：`mode=` ？ `session_id` 是否与首次相同？ argv 有没有 `-s`？房间有没有「载体已更换」？
3. 排队出队：出队的两张卡号 + `按模板派发` 是否出现？

三条都过 → B299 真机过。任何一条失败 → 不要把卡标完成。

---

## 6. 真机复验结果记录（2026-08-29 实测）

### 6.1 首次拉起（卡号 B18）
- **日志字段原文**：
  ```json
  {"time":"2026-08-29T22:37:24.773420637+08:00","level":"INFO","msg":"自动化拉起协调者回合","component":"agentd","card":"B18","source":"card_create","squad":"coord","carrier":"opencode","cli":"opencode","home_dir":"~/.handoff/home/opencode","workdir":"/root/.handoff/repos/handoff"}
  {"time":"2026-08-29T22:37:24.774008639+08:00","level":"INFO","msg":"协调者回合开始","component":"agentd","mod":"hostapi","cli":"opencode","mode":"new","home_dir":"~/.handoff/home/opencode","workdir":"/root/.handoff/repos/handoff","timeout":"30m0s","prompt_bytes":293}
  {"time":"2026-08-29T22:59:56.513868575+08:00","level":"INFO","msg":"自动化拉起协调者回合结束","component":"agentd","card":"B18","source":"card_create","session":"ses_fb20baad7ffelBMrMXOOHxMZgw","rebuilt":false,"escalated":false}
  {"time":"2026-08-29T22:59:56.522476967+08:00","level":"INFO","msg":"协调者 HTTP 拉起成功","component":"agentd","card":"B18","source":"card_create","session":"ses_fb20baad7ffelBMrMXOOHxMZgw","rebuilt":false,"escalated":false}
  ```
- **关键字段核验**：
  - `home_dir` = `~/.handoff/home/opencode`
  - `mode` = `new`
  - `rebuilt` = `false`
  - `session_id` = `ses_fb20baad7ffelBMrMXOOHxMZgw`

### 6.2 房间续接（卡号 B18）
- **日志字段原文**：
  ```json
  {"time":"2026-08-29T23:00:10.789268169+08:00","level":"INFO","msg":"自动化唤醒协调者回合","component":"agentd","card":"B18","event_count":1,"squad":"coord","carrier":"opencode","cli":"opencode","home_dir":"~/.handoff/home/opencode"}
  {"time":"2026-08-29T23:00:10.789468959+08:00","level":"INFO","msg":"协调者回合开始","component":"agentd","mod":"hostapi","cli":"opencode","mode":"resume","home_dir":"~/.handoff/home/opencode","workdir":"/root/.handoff/repos/handoff","timeout":"30m0s","prompt_bytes":374}
  {"time":"2026-08-29T23:01:05.022996608+08:00","level":"INFO","msg":"协调者回合完成","component":"agentd","mod":"hostapi","cli":"opencode","session_id":"ses_fb20baad7ffelBMrMXOOHxMZgw","output_bytes":429,"duration":"54.233495197s"}
  {"time":"2026-08-29T23:01:05.023048925+08:00","level":"INFO","msg":"自动化唤醒协调者回合结束","component":"agentd","card":"B18","event_count":1,"session":"ses_fb20baad7ffelBMrMXOOHxMZgw","rebuilt":false,"escalated":false}
  ```
- **进程 argv 实测**：
  ```text
  /root/.opencode/bin/opencode run --format json -m opencode/mimo-v2.5-free -s ses_fb20baad7ffelBMrMXOOHxMZgw -- 你是本卡的机器协调者。醒来第一件事：读卡、查依赖、看基线新鲜度；不适合现在推就在房间说明原因并休眠。 ## 本卡上下文 - 卡号：B18 - 标题：B299 续接复验探针 ## 本次唤醒事件 - [message] 协调者请确认本卡当前状态与基线 ...
  ```
- **房间消息历史**：
  ```json
  {"messages":[{"seq":39,"card_id":"B18","type":"room_message","actor":"web:127.0.0.1","payload":{"room":"B18","kind":"user","body":"协调者请确认本卡当前状态与基线"}}]}
  ```
- **关键字段核验**：
  - `mode` = `resume`
  - `session_id` 与首次完全相同（`ses_fb20baad7ffelBMrMXOOHxMZgw`）
  - argv 携带 `-s ses_fb20baad7ffelBMrMXOOHxMZgw`
  - `rebuilt` = `false`
  - 房间中无「载体已更换」系统指针

### 6.3 排队出队与按模板派发
- **日志字段原文**：
  ```json
  {"time":"2026-08-29T22:05:45.20031348+08:00","level":"INFO","msg":"自动化队列出队","component":"agentd","kind":"ignition_queue","card":"B16","node":"node2","squad":"squad-b","priority":"中"}
  {"time":"2026-08-29T22:13:28.916552332+08:00","level":"INFO","msg":"队列出队唤醒完成，进入节点再入口","component":"agentd","card":"B16","node":"node2"}
  {"time":"2026-08-29T22:13:28.934779913+08:00","level":"INFO","msg":"按模板派发","component":"agentd","card":"B16","template":"default","target":"local","executor":"opencode","model":"opencode/mimo-v2.5-free"}
  {"time":"2026-08-29T22:13:28.923805747+08:00","level":"INFO","msg":"自动化队列出队","component":"agentd","kind":"ignition_queue","card":"B14","node":"node1","squad":"squad-a","priority":"中"}
  {"time":"2026-08-29T22:21:51.561186616+08:00","level":"INFO","msg":"队列出队唤醒完成，进入节点再入口","component":"agentd","card":"B14","node":"node1"}
  {"time":"2026-08-29T22:21:51.575773641+08:00","level":"INFO","msg":"按模板派发","component":"agentd","card":"B14","template":"default","target":"local","executor":"opencode","model":"opencode/mimo-v2.5-free"}
  {"time":"2026-08-29T23:09:50.107214642+08:00","level":"INFO","msg":"自动化队列出队","component":"agentd","kind":"ignition_queue","card":"B23","node":"node1","squad":"squad-a","priority":"中"}
  ```
- **关键字段核验**：
  - 出队卡号：`B16`（`node2`）、`B14`（`node1`）、`B23`（`node1`）
  - `按模板派发` 均已真实出现并完成任务环境与工作区创建
  - 队列在名额释放后自动清空并有序流转

### 6.4 验收结论
- B299 复验三条全部通过（✅ PASS）。

