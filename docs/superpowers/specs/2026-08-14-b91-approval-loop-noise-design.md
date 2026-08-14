# B91 审批回路降噪设计：跨 gate 同命令去重 + deny 丢弃唤醒

两条缺陷都出自 08-14 审核 B89/B90 派发任务的实测，同属「审批回路的信噪比」，
合一个 spec。**都不碰状态机、不碰工单契约**。

---

## 1. 问题与实证

### 1.1 同一条命令被两种 gate 各拦一次

B89 任务 `4356d318`（mac-02 / opencode）事件 seq 630 与 631：

```
630  external_directory: rm node_modules && cd /Users/sycm/.handoff/worktrees/4356d318 && git worktree remove …
631  bash:               rm node_modules && cd /Users/sycm/.handoff/worktrees/4356d318 && git worktree remove …
```

**同一条命令**，先以 `external_directory` 形态升级人工问一次，审核者批完后又以
`bash` 形态再问一次。两张独立工单、两次唤醒、两次裁决。每张工单 = 审核者一个
完整回合（唤醒→show→reply），并行 subagent 场景下翻倍。

### 1.2 deny 原因被丢弃时无人知晓

`reply --deny --reason "..."` 返回 `{"ok":true}`，但若回合在拒绝原因下发前
终结（result/done/stop），那句话被静默丢弃。agentd 侧其实落了
`deny_guidance_dropped` 审计事件（B50 立的规矩：「审核者说的话去哪了」在任何
路径下都要有答案），但**只 AppendEvent 不 Publish**——`wait` 不会为它醒。
审核者拿到的是 `{"ok":true}`，除非事后主动 `show` 翻事件历史，否则永远不知道
自己的裁决空转了。B50 修了「有没有答案」，没修「谁会去看这个答案」。

---

## 2. 裁决史：推翻的是什么，没推翻什么

B57① 当年对 1.1 的定案是**不做**，理由：「B27 双层判据（路径走归属、命令走
黑名单）是有意设计，合并等于放宽安全语义」。

**本次推翻的只是结论，不是理由。** 当年拒绝的方案是「把两道门合成一道」；
本设计不动门本身——两道自动判据照跑，黑名单硬拒不受任何影响——只在
**升级到人**这一步复用既有裁决。会走到人面前的前提是两道门都没能自动放行，
此时同一任务里同一条命令已被人批过，再问一遍不产生任何新信息，是纯噪音。
（08-14 用户裁决重启此条。）

---

## 3. 跨 gate 同命令去重：指纹换键

### 3.1 根因

B57② 的复用指纹取权限描述**全文**的 sha256
（[manager.go:1802](../../internal/agentd/manager.go:1802) `permFingerprint`）。
双胞胎工单的文本前缀不同（`external_directory: <cmd>` vs `bash: <cmd>`），
指纹必然不同，复用永不命中。

而结构化载荷里两者的命令一字不差：opencode adapter 对 bash 形态的
`external_directory` 明确写着「同时带命令与越界目录，两个都要给 permgate」
（[adapter.go:1281](../../internal/executor/opencode/adapter.go:1281)），
`Perm.Command` 都取自同一个 `metadata.command`。

### 3.2 换键规则

指纹计算收敛为一个函数（形如 `permFingerprintFor`），规则：

- **`Perm.Command` 非空** → 指纹取 `sha256("cmd\x00" + command)`。
  `cmd\x00` 前缀做域隔离：命令域指纹与全文域指纹永不相撞，杜绝「某段权限
  描述全文恰好等于另一条命令文本」的伪命中。
- **`Perm.Command` 为空**（纯路径的 edit/write 类、提取不出结构的 fail-closed
  类）→ 维持现状，全文 sha256。这类没有跨 kind 双胞胎问题，不动。

### 3.3 三个触点同步换键

| 触点 | 现状 | 改后 |
|---|---|---|
| 升级人工建单 [manager.go:1415](../../internal/agentd/manager.go:1415) | `permFingerprint(ev.Text)` | `permFingerprintFor(ev)` |
| 审批者/复用自动放行建单 [manager.go:1721](../../internal/agentd/manager.go:1721)（`approvePermission`） | `permFingerprint(permission)` | 同键。该函数现在只收文本串，需把命令（或算好的指纹）随调用传入——实现时取调用点在手的 `ev` 统一算，不在函数内重新猜 |
| 复用查询 [manager.go:1817](../../internal/agentd/manager.go:1817)（`reuseDecision`） | `permFingerprint(ev.Text)` | `permFingerprintFor(ev)` |

写入与查询必须同一版规则，否则复用静默失效——这正是本缺陷的形态，不能在修它
的过程中再造一个。

### 3.4 换键的一个未言明后果：跨子 agent 也会复用（08-14 派发中发现，补记）

opencode adapter 给子 agent 的权限描述加 `[子 agent: <标题>] ` 前缀
（[adapter.go:1248](../../internal/executor/opencode/adapter.go:1248)），
**前缀只加在 `text` 上，`Perm.Command` 不带它**。所以命令域指纹天然忽略前缀：
主 agent 批过的命令，子 agent 再发同一条时也会命中复用。

这是 §3.2 的直接推论，但原稿没写出来，补记并明确认可：

- 前缀的用途是**让审核者读懂这条请求的来源**（adapter.go:1234 的原话：
  「子 agent 的越权与主 agent 的越权含义不同」）——那是**首次裁决时**的判断
  依据。首次裁决已经发生过了。
- 复用的前提仍是同任务、同一条命令一字不差、已被 allow 且已送达。同一个任务
  里同一条命令，换个子 agent 执行不产生新的安全信息。
- 反过来若要按前缀区分，等于「同一条命令在一个任务里要被批 N 次，N = 派出的
  子 agent 数」——而本项目的执行纪律恰恰强制大量使用子 agent，那正是 B91
  要消灭的噪音的最大来源。

### 3.5 未覆盖：纯路径请求仍按子 agent 重复问（08-14 实测，待裁决）

**实测**：B91 自身的派发任务 `d912b23a`，seq 678 与 679 相隔 28 秒：

```
678  external_directory: /var/folders/xc/…/T/opencode/*
679  [子 agent: Task 1 审查（双裁决） (@general subagent)] external_directory: /var/folders/xc/…/T/opencode/*
```

同一个目录模式，因为前缀不同而重复问。**§3.2 的换键治不了它**：这类请求
`Perm.Command` 为空（描述里是路径不是命令），落回全文域，而全文含前缀。

按当前纪律，每派一个新子 agent 就会再问一次同一个 temp 目录。

**未决**：是否再加一个路径域——`Perm.Command` 为空且 `Perm.Paths` 非空时，
指纹取 `sha256("paths\x00" + 排序后的 Paths 拼接)`。与命令域同构、同样只复用
allow 同任务，能一并治掉「同目录跨 kind」与「同目录跨子 agent」。

风险面比命令域**大**：路径域丢掉的不只是前缀，还有 `Tool`——`edit` 与
`external_directory` 对同一路径的含义不同（一个是写文件，一个是越界目录授权），
合并等于把两种授权当成一件事。若要做，指纹应带上 `Tool`
（`sha256("paths\x00" + tool + "\x00" + paths)`）而不是裸路径。

本轮 plan（`d912b23a`）**不含**本节，等裁决后决定是 `continue` 补做还是另开条目。

### 3.6 不变的部分（照抄 B57 既有裁决，不重新辩论）

- **只复用 allow，不复用 deny**（B57 §3.3）：错误复用 allow 是安全事故，
  错误少复用只是多问一次；deny 本来就少，收益不成立。
- **只在同任务内复用**（B57 §3.4）。
- **查询失败 fail-closed 到照常问人**（`reuseDecision` 现有行为）。
- **并发窗口不加锁**（B57 §3.6）：两张双胞胎单同时挂起时互不自动解决，
  先批的答完后、后到的才命中复用。实测 630→631 是先批后到的时序，能治。
- **旧库无需迁移**：旧工单存的是全文指纹，新键查不到就是不复用，沿 B57
  「空指纹永不参与复用」的同一姿势降级安全。

---

## 4. `deny_guidance_dropped` 补 Publish

### 4.1 两个落点

丢弃事件有两个产生点，都只入库不广播：

1. [manager.go:1666](../../internal/agentd/manager.go:1666) `clearApproverState`
   内联 AppendEvent——回合终结（result→waiting_review / done→completed /
   stop→failed）时原因还挂着；
2. [manager.go:1946](../../internal/agentd/manager.go:1946)
   `appendGuidanceDropped` helper——下发时 `Send` 失败。

两处各补 `m.hub.Publish(evt)`（AppendEvent 成功才 Publish，与全文件其余
「落库→广播」点同序）。

### 4.2 语义升格

该事件从「审计留痕」升格为「可操作唤醒」：审核者被叫醒后的动作就一个——
**把那句 reason 自己写进 `continue`**。payload 已含 `reason` 与 `cause` 原文，
不需要再 `show`（但 skill 纪律照旧：醒来先 show）。

**为什么 done/stop 路径不抑制**：那两条是审核者自己刚动的手，多收一条
「你的 reason 白说了」只是确认，不值得为省它引入路径判断。progress /
approver_decision / permission_reuse 不唤醒的先例不适用于此——那三类没有
任何审核者动作可做，这条有。

### 4.3 配套

- handoff skill 的事件分诊表补一行（`deny_guidance_dropped` → 把 reason 写进
  continue）。skill 文件在 `~/.claude/skills/handoff/`，实现落地后顺带改，
  不算工程量。

---

## 5. 明确不做

- **deny 复用、跨任务复用**（§3.6，B57 既有裁决原样保留）
- **adapter 层合单**：在 opencode 侧按时间窗把两条 `permission.asked` 并成
  一张工单。上游两条请求 12–30s 才到齐，窗口注定竞态；且两条 perm id 都要
  应答，合单还得拆回去，复杂度全在刀刃外
- **reply 时扇出**：批一张时自动应答其它同命令挂起单。是并发窗口的补丁，
  B57 原 §3.6 已定不加锁，本次不重开
- **缺陷①（`failed` 后 `stop` 仍被接受 / 状态迁移疑似丢失，任务 `054ca06f`
  seq 595/644）**：需先复现定性（外部看不出是 stop 少校验还是 failed 迁移
  丢失），另案处理，不混进本条

---

## 6. 验收标准

1. 单测：同任务内，`external_directory` 形态命令 X 获人工 allow 且送达后，
   `bash` 形态同命令 X 到达 → 自动放行，落 `permission_reuse` 事件
   （`source=reuse` 日志可辨），审核者零唤醒
2. 单测：命令不同（哪怕只差一个字符）→ 不复用，照常升级
3. 单测：无命令的纯路径权限维持全文指纹行为（现有 B57 用例不红）
4. 单测：deny 过的同命令 → 不复用，照常升级
5. 单测：`clearApproverState` 与 `Send` 失败两条路径产生的
   `deny_guidance_dropped` 均被 Publish（hub 订阅者能收到）
6. 回归：`go build ./... && go vet ./... && gofmt -l .`（无输出）
   `&& go test -count=1 ./...` 全绿
7. 真机：复现双胞胎场景（同 B89 终审那类跨目录命令），第二张不再唤醒；
   `wait --follow` 挂着时 deny 一条注定送不达的 reason，能收到
   `deny_guidance_dropped` 事件

---

## 7. 已知风险

- **命令域指纹比全文指纹粗**：全文里除命令外还有 kind 与目录清单，换键后
  「同命令、不同越界目录」的两次请求会互相复用。评估：命令文本本身就含
  目录参数（cd、绝对路径都在命令里），同命令不同目录的场景要求 opencode 对
  同一条命令提取出不同的 directories——真机未观测到；且复用仍限同任务同
  allow，最坏情况等价于「审核者对同一条命令的批准在本任务内粘住」，这正是
  想要的语义。
- **`approvePermission` 签名要动**：调用点需把指纹（或 `ev`）传进去，属机械
  改动，但漏改任一调用点会让审批者自动批准的工单失去复用资格——测试项 1
  要求 reuse 命中的先例既可来自人工也可来自审批者，覆盖这条。
