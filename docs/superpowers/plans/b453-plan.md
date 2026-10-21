# B453 实现计划：账号与额度（手工登记、所在机定时探测、派发看账号额度）

- 状态：**已拍板（2026-10-22，主会话协调者；用户外出，按 2026-10-22 自主授权裁决）**。裁决见下方「拍板」节；与下方各 Q 的「荐」不一致处以「拍板」节为准。 **§8 修订 r1**（2026-10-22，审查第 1 轮 I1 回写）。
- 输入：`docs/superpowers/specs/b453.md` **r1.1**（r1 2026-10-22 用户批准；r1.1 同日增第四种 kind `agy`，提交 `9043ba71`）。L3，形态甲（本地盯），**不扇出**：一张卡、单分支 `cards/B453-charter-1`、按段串行。
- 事实：`b453-facts-code.md`（基线 fad1aadf，本稿复核过的行号见 §10）、`b453-facts-orca.md`（三家 HTTP + agy CLI 的外部形状）、`b453-facts-providers.md`。台账 `docs/superpowers/specs/b453-ledger.md`「plan」节是本稿全部读数、命令与判断的原始记录。
- 基线：分支 `cards/B453-charter-1` @ `9043ba71`（已含 main/B460；代码与 `a4e206d8` 相同，之后只有文档提交）。
- 原型（形态基准，gitignore 不入库）：`prototypes/b453-accounts/`（README 2026-10-22 00:56 版：四种 kind、底栏两模式、hover 二级面板、移动设置页）。
- 读者：零上下文执行者。故事编号沿用 spec：S1–S12。标「**未验证，需真机**」的结论不得按推测写成测试夹具，见 §5.3 汇总。

---

## 拍板（2026-10-22 主会话）

- **Q1 采纳**：`account`（人写，CAS）与 `account_reading`（机写，读-合并-CAS 重试）两 kind 同 id；不改表、不升 `ledgerSchemaVersion`。
- **Q2 采纳 (a)/(b) 路径；`opencode.db › account` 备选不做，留给用户**。理由：spec 只授权 `auth.json › opencode-go` 条目，读 opencode 的 SQLite 取 OAuth token 是新的凭据位置，按自主授权边界（凭据类）须用户点头。R1 先在 linux-01 root HOME 跑；(a) 成则首故事换机到 linux-01（机器替换、不改语义，批）；(b) 则首故事改 codex@linux-01，opencode-go 记欠交。mini 上的 opencode-go 账号给明确失败文案。卡 note 记待问。
- **Q3 采纳**。
- **Q4 采纳**（撞额度标记 > 探测失败 > 未探测 > 读数推导；已过重置时刻的窗口按 0% 计）。
- **Q5 采纳**；`config.proxy` 注释的故障域声明同步改（不再只影响升级）。
- **Q6 采纳**；任务库为各机本地 SQLite，加列走既有逐列 ALTER，不触共享账本 DDL。R4c 必须真机验 `NO_PROXY` 补 localhost 后 opencode serve 可达。
- **Q7 采纳 `notice` kind**：对 B460 F2 是增量（新 kind 值），不改既有字段；机队三台同批升级后才在载体上绑定账号，避免旧 CLI 误读。CHANGELOG 记一条。
- **Q8 采纳窄改**：`waiting_review` 且有待答工单时先交回工单（退出 5）。B460 F4 冻结条目同步修订并在 CHANGELOG 记。
- **Q9 采纳**；Q9 残余（崩溃窗口重跑任务无回链）落 roadmap。
- **Q10 采纳**：429 限速不算额度耗尽。
- **Q11、Q12、Q13 采纳**。
- **Q14 采纳纳入**（roadmap 975 随本卡关闭）：S6/S7 在役载体全是 opencode，没有失败文本就无从分类，属交付已批故事的必要前置，不算范围扩张。
- **Q15 采纳**：新测试库 `handoff_b453`；三台隔离实例含 `t453-mbp`（只用于读 grok 额度，不派协调任务，验完即停即删），卡 note 告知用户。在役 7777 全程不动。
- **Q16 采纳**（真错误优先，注入只在隔离实例）。
- **Q17、Q18、Q19 采纳**。
- **SC1–SC7 认可**，spec 不升版：SC1 由 Q2 路径覆盖；SC2 由 Q8 覆盖；SC3 由 Q10 覆盖；SC4 由 Q14 覆盖；SC5/SC6 由 Q15 覆盖；SC7 冒烟按「各已登记真实账号」计、欠交 kind 不算通过而是列欠交。

## 待拍板（出稿者推荐，协调者裁决）

标「**需用户**」的是我认为必须回人的；其余协调者可定。每条：**荐** + 备选 + 理由。

1. **Q1 账号定义与探测读数的存储落位（spec「目标架构」交 plan 定）。荐：两个 registry kind、同 id。**
   - `account`（id = 账号名）：人写的定义，CAS 语义与载体完全相同（GET 取版 → PUT `?expect=`）。只有控制台/API 写。
   - `account_reading`（id = 账号名）：最新探测读数 + 撞额度标记 + 失败状态。写者两类：所在机探测器（写读数/失败）、撞额度标记者（检测链或任务收尾所在机，写标记）。所有写都走「读 → 合并本写者负责的字段 → CAS，冲突重读重试（上限沿用 `maxCASAttempts=8`）」。
   - 理由：`RegistryPut` 是整行 CAS（`ledger/registry.go:29-77`）。同行分字段时，探测每 5–15 分钟写一次就会顶高 version，人开着编辑框保存必撞「已被修改」——这是把机器写入的噪声推给人。分两行后人写与机写**物理上不同行**，互不顶版本；读数行的两类机器写者都是可重试的合并写，不丢写。
   - 连带规则（钉进验收 V3/V5）：探测成功**只在探测开始时刻晚于标记时刻时**清除撞额度标记（防「探测发出 → 任务撞额度标记 → 旧探测返回把新标记抹掉」）；删账号时同时删读数行（读数行无定义行时一律视为孤儿、不展示、探测器跳过）。
   - 纯新增 kind，registry 表结构不动，**不升 `ledgerSchemaVersion`**（`ledger/schema_version.go:19-22`）。
   - 备选（否）：单行分字段 + 人写时服务端「读最新版本合并定义字段」——等于让 PUT 忽略 `?expect=`，破坏既有 CAS 语义与载体一致性。

2. **Q2 opencode-go 的凭据来源（**需用户**，首故事承重）。**
   - 事实（台账 plan 节，只查存在性、未读任何值）：mac-mini（载体 mini 的 HOME = 主 HOME）**没有** `~/.local/share/opencode/auth.json`；opencode 1.19.3 把登录态放在 `opencode.db`，其 `credential` 表 0 行，`account` 表 1 行（列含 `access_token/refresh_token/token_expiry/url`）——即 mini 的 opencode-go 是 **opencode 账号 OAuth 登录**，不是 spec S1 缺省条目 `auth.json › opencode-go` 里的 API key。linux-01 root 有 `auth.json`（条目名未读）。
   - 后果：spec 首故事「mini@mac-mini 的 HOME，`auth.json › opencode-go`」在 mini 上按现状必然读不到 key；orca 的 `zen/go/v1/usage` 收不收 OAuth access_token **未验证，需真机**。
   - **荐**：段 1 只实现 spec 写明的条目形态（`auth.json › opencode-go` API key）。真机 R1 先在 linux-01 root HOME 跑：
     - (a) linux-01 `auth.json` 里有 `opencode-go` 条目且 usage 接口通 → 首故事在 linux-01 完成（**偏离 spec「mini@mac-mini」**，属机器替换、不改语义，协调者可批）。mini 上登记的同类账号得到明确失败「凭据条目 opencode-go 不存在（该机 opencode 为账号登录，见 Q2）」，记欠交。
     - (b) linux-01 无该条目或接口不通 → 按 spec「证伪时首故事改用 codex 账号」：首故事改 codex@linux-01 走同一竖切，opencode-go 记欠交待用户裁决。
   - 备选（**需用户点头才做**）：新增条目形态 `opencode.db › account`（读 active account 的 access_token 当 Bearer）。它不是 spec 否掉的 cookie 路径，但 spec 未列、接口是否接受未知，且要在 Go 里只读打开 opencode 的 SQLite（与 opencode 进程并发读，需只读 + busy 超时）。协调者拍板时请决定：是否在汇报里把它作为「opencode-go 欠交」的解法问用户。

3. **Q3 探测执行与读数写入的位置。荐：所在机 agentd 自己探测、自己直接写共享账本；「立即探测」= 入口机把同一端点转发到所在机。**
   - 事实：每台挂了 `ledger.dsn` 的 agentd 都打开同一个共享 PG 账本并在组装点构造自己的编制服务（`cmd/agentd.go:659-700,734-760`）；三台在役机都在设备表自登记（台账）。所以所在机写读数**不需要**经协调机。
   - 与载体检测（`schedapi.go:237-330`，入口机写状态）不同的理由：spec 定「读数由所在机写」；让周期探测与立即探测共用所在机上同一个探测器实例，才能做到同一账号不并发探测（单飞）、立即探测后顺延下一次周期探测、退避状态只有一份。
   - 所在机离线 = 转发失败，入口机把 502 原因交回控制台（卡片上显示本次失败原因，不落账本，状态停在原值——spec S1「关键失败」）。
   - 所在机未挂共享账本（无 DSN）时探测器不启动；登记校验要求机器在设备表里（只有挂共享账本的机器在设备表），R0 复核三台都挂（行为事实，**未验证，需真机**：看启动日志 `dsn_configured=true`）。

4. **Q4 状态推导的优先级与「已过重置时刻」的窗口。荐：**
   1. 读数行有撞额度标记 → **无额度**（spec S3：持续到下一次成功探测；探测失败不清除）；
   2. 最近一次尝试失败（晚于最近一次成功）→ **探测失败**（旁注沿用旧读数；S5 视为可用）；
   3. 从无成功读数且从无尝试 → **未探测**；
   4. 按最近成功读数：任一窗口已用 ≥ 100 或接口明示不可用（codex `allowed=false`/`limit_reached=true`、opencode-go 403 EntitlementError 归此）→ **无额度**；任一窗口 ≥ 低额度线 → **低额度**；否则 **正常**。
   - 补一条（荐）：`resets_at` 已过当前时刻的窗口按 0% 计（重置已发生，读数只是还没刷新）。否则一个 5h 窗口 100% 的账号会在重置后最多再被挡一个探测间隔（缺省 15 分钟）。备选：不补，严格按读数——简单但会多挡。
   - 「已过期」（最近成功读数 > 30 分钟）只是展示标记，不参与五档、不影响准入。
   - 注意第 2 条的后果（spec 原文如此，提请知悉）：一个读数显示 100% 的账号，若之后探测连续失败，状态变「探测失败」→ 可派发。荐照 spec 执行，不另加规则。

5. **Q5 账号未填代理时探测用什么出网。荐：`proxycfg.Transport(cfg.Proxy)`**——配置 `proxy:` 非空用它，空则沿用进程环境 `HTTPS_PROXY/HTTP_PROXY/NO_PROXY`。
   - 理由：spec「沿用该机 handoff 进程的出网代理环境」；mac-mini 配置了 `proxy: socks5://127.0.0.1:7897`（`squad list` 日志），launchd 环境里有没有 HTTPS_PROXY **未验证**——只读进程环境很可能在 mac-mini 上直连 opencode.ai/chatgpt.com 失败。
   - 代价：`config.go:83-100` 注释写明 `proxy:` 只作用于升级与 git，本条扩大其作用范围到「额度探测」，注释同步改；故障域声明改为「代理挂了影响升级与额度探测，不影响任务执行」。
   - 备选：只读进程环境（字面最窄）。R2 在 mac-mini 实测两种，若进程环境本身可通，协调者可改选。
   - agy 是子进程：同一个代理以 `HTTPS_PROXY/HTTP_PROXY/ALL_PROXY`（大小写各一）注入子进程环境，`NO_PROXY` 沿用进程环境。

6. **Q6 账号代理注入任务环境的落点。荐：落点机由编制域查「载体 → 所选账号」，快照进任务行；三处 env 组装与检测回合统一覆盖代理键，`NO_PROXY` 不覆盖。**
   - 查询发生在**落点机** agentd 的派发处理里（它挂着同一份共享账本，`AdmitFrozen`/`AdmitCarrier` 已读载体）：把 `account`、`account_kind`、`account_proxy` 放进 orchestration 的 Go 级 `DispatchReq`（不是机间 wire），Manager 不调编制域（守 `receiver_bind.go:4` 边界）。
   - 任务库（各机本地 SQLite）加三列 `account`、`account_kind`、`account_proxy`（`store.go:297-324` 同款逐列 ALTER）。续接/恢复（`manager.go:1539`、`:3840`）从任务行取，**快照语义**：运行中/续接的任务不随账号代理后改而变（与 `home_dir` 一致）。
   - 覆盖位置：三处 `withCarrierHome` 之后一个新帮手「删除全部代理键再追加」（`HTTPS_PROXY/HTTP_PROXY/ALL_PROXY` 大小写各一，共六键）；检测回合在 `wakeProxyEnv`（`probe.go:373-426`）结果之上同样覆盖。
   - **`NO_PROXY` 不覆盖**的理由：opencode 执行器经 localhost 与自己起的 `opencode serve` 通信（`executor/opencode/api.go`），socks 代理吃掉 localhost 会让任务整段失联。账号代理生效时若下层没有 `NO_PROXY`，补 `localhost,127.0.0.1,::1`。**行为事实未验证，需真机**（R4c）。
   - 备选（否）：代理随冻结身份走机间 dispatch wire——代理串可能带 `user:pass@`，没必要过网，也多一个跨版本字段。

7. **Q7 S7 分支任务撞额度的工单用什么 kind。荐：新 kind `notice`（系统告知，人答「已知悉」即关单，不回传执行器）。**
   - 事实：今天只有 `gate`/`ask`（`proto.go:398`）。`ask` 被答复时 `RelayAnswer`（`manager.go:1937-`）会把答案送进执行器会话——对一个额度耗尽、回合已结束的任务，这等于「人一答就在原会话续跑」，那是 B459 接管的范围，不是本卡。
   - 新 kind 必须过的既有白名单（执行者 grep 补全、逐处加断言）：`cmd/skilled.go:389-432 writePendingTickets`（今天非 ask 一律打成 permission）、`manager.go:1913 RelayAnswer`（未知 kind 报错 → 改为 notice 只记 delivered）、`agentd/roomsapi.go:734`、`web/src/app/task/TicketsPanel.tsx:87`、`web/src/app/task/review.ts:28,52`、`cmd` reply 的参数校验、账本镜像的开放工单投影（若按 kind 区分）。
   - 退出码 5 的输出块：`kind: notice`，全文取请求体 `notice`，答复行一条 `reply: handoff reply <任务> --ticket <工单> --answer "已知悉"`。这是对 B460 **已冻结 F2** 的增量 → 见 §8 F-B460-1，重走冻结。
   - 备选：复用 `ask`（不动 F2）——被否，理由见上。

8. **Q8 `WaitFinal` 判定次序（B460 F4 已冻结「回合已结束 > 有待答工单」）。荐：窄改——任务处于 `waiting_review` 且快照有待答工单时，先交回工单（退出 5）。**
   - 事实：回合失败路径先作废全部挂起工单再迁 `waiting_review`（`manager.go:3526-3548`），所以今天 `waiting_review` 下不会有待答工单；本卡的 `notice` 是唯一会出现在 `waiting_review` 下的待答工单。终态 `completed/failed` 不变（仍 0/3 优先）。
   - 答「已知悉」后再 `wait --final` → 回到「回合已结束」→ `turn_failed` → 退出 3。
   - 这是改 B460 F4，§8 F-B460-2 重走冻结。备选（否）：分支任务撞额度时不迁 `waiting_review` 而停在 `waiting_answer`——答复后 `ResumeIfIdle`（`facade.go`）会把它迁回 `running`，而执行器早已不在，看门狗再判死，状态机乱。

9. **Q9 只读任务重跑链的编排落点与原子性。荐：Manager 分类后**同步**调用 agentd 侧回调完成「标账号 + 选人 + 派新任务」，拿到新任务引用后，Manager 以一次状态迁移把原任务写成 `failed` 并带上 `rerun_by` 链接。**
   - 回调接口定义在 orchestration（使用方，架构法第九条），实现在 agentd `Server`（与 `TerminalNotifier`/`Server.TaskTerminal` 同款接线，`readonly.go:31`、`server.go:542`）。选人复用 `routeOrdinaryDispatch`/`SelectWhere`（`dispatchroute.go:63,100`）+ 「排除已用账号/原载体」谓词；落点他机时走既有 `forwardDispatch`（冻结身份转发）。
   - 先派后落终态的理由：`--wait` 的客户端只要看见原任务 `failed` 而没有链接，就会以退出码 3 结束——终态与链接必须一起可见（架构法第十二条：要等结果 → 同步调用）。
   - 「同一份输入」：原机在派发时把原始派发请求（计划原文**不含**只读追加的「## 产物目录」小节、mode、squad、项目、base、名称、纪律、亲和参数）落在任务目录一个私有文件里；重跑读它。文件名与形状执行者定（任务私有，不是契约）。
   - 重跑链只用账号一次：链上已用账号集合随重跑请求传递（`dispatchRequest` 新可选键，见 §8 F14），选人谓词据此排除；未选账号的载体以「载体名」计入排除集（防同一载体反复撞）。
   - 原任务的名额：回调先派新任务（新任务在别的成员上占名额），原任务迁 `failed` 后走既有 `TaskTerminal` 归还原载体名额。
   - 已知残余（写进 §7 生命周期族）：回调已派出新任务、原任务尚未写终态时 agentd 崩溃 → 重启后原任务被对账判死，无链接，`--wait` 退出 3，而新任务在跑。荐：派出后**立即**先写链接字段，再迁终态，把窗口缩到两次相邻写库；不另造补偿机制，落 roadmap。

10. **Q10 撞额度分类收口。荐：新包 `internal/quota`（边界型）提供唯一判据 `ClassifyTurnFailure(cli, accountKind, text)`，返回三类：额度耗尽 / 限速 / 其他。**
    - 检测链：`hostapi.classifyTurnError`（`probe.go:430-444`）的 quota 分支改为委托它——额度耗尽 → `WakeQuota`（wire 值保留，见 Q19）；**限速（429/too many requests/rate limit）→ 视为可达（ready + 说明）**，不再当 quota；登录类分支不动。
    - 任务链：Manager 在回合失败（`Result.OK=false`）时对 `FailReason` 调用；只有「额度耗尽」触发 S6/S7，限速与其他按普通失败。
    - 签名表（初版，R6a 用真样本校正）：opencode-go `EntitlementError`/`ENTITLEMENT_ERROR`/`403` + 额度字样、`usage limit`；codex `insufficient_quota`、`usage_limit_reached`、`You've hit your usage limit`；grok 额度/credits 耗尽类；agy 额度耗尽类（样本未知）。**全部未验证，需真机**——表驱动测试的样本必须来自 R6a 真样本（脱敏后入 testdata），合成样本只能作补充、且注明来源。
    - 按「载体执行器 + 账号 kind」判：执行器决定错误文本的外壳（opencode 执行器跑 codex 额度时，外壳是 opencode、内容是 codex 的签名），账号 kind 缩小签名集合；无账号时用该执行器可承载的全部签名。

11. **Q11 账号名命名空间。荐：独立命名空间**，不参与 `CrossKindConflict`（`receiver.go:139-145`）。理由：账号不是接收者，`--receiver` 只解析载体/小队；CLI 用独立子命令 `handoff account …`。

12. **Q12 「限额中」退役的存量处理。荐：读时归一，不做写迁移。** 编制域解码载体时 `status:"quota"` 一律读作 `online`（spec「存量处于限额中的载体读作已上线」）；`StatusQuota` 常量删除；前后端词表删 `quota`/限额中。滚动升级期间旧版 agentd 仍可能把 `quota` 写回共享账本，读时归一照样兜住。

13. **Q13 指纹的取材（语义：「是不是同一个账号」，不是「是不是同一个 token」）。荐：** 存 `sha256(身份值)` 前 12 个十六进制字符（形如 `sha256:3f9a1b2cc21e`），界面按原型显示首 4 + 尾 4。身份值按 kind 取**不随 token 刷新而变**的那一项：opencode-go 取 API key；codex 取 `tokens.account_id`（不取会刷新的 `access_token`）；grok 取条目 `user_id`（无则 `email`）；agy 取登录身份（邮箱/id，取不到留空、不比对）。取材细节执行者可调，拍板者只需认可「指纹不随 token 刷新抖动」这一语义。

14. **Q14 opencode 执行器的失败文本（roadmap 975）纳入本卡。荐 纳入**，作为段 4 的前置：opencode 把 403 ENTITLEMENT_ERROR 等错误写在 stdout JSON / 会话错误事件里，今天 `FailReason` 为空——不修，S6/S7 对全部在役载体（都是 opencode）不可能分类到。修法限定在 opencode 执行器把会话错误事件的消息带进 `FailReason`（尾部截断规则不变），不改其他执行器。**行为事实未验证，需真机**（R6a 先取样本，再定修改点）。

15. **Q15 真机环境（**需告知用户**）。荐：**
    - 新测试库 `handoff_b453`（linux-01 PG，按全局 CLAUDE.md §4 用管理员凭据建、登记到 `credentials.md`「已分配」表；凭据不进文档/提交/对话）。
    - 三个隔离实例（在役 7777 的进程、二进制、配置全程不动）：`t453-linux`（linux-01，端口 7795，datadir `/root/b453-iso`）、`t453-mac`（mac-mini，7796，datadir 在会话 scratchpad）、`t453-mbp`（macbook-pro，7797，datadir `~/b453-iso`）。**mbp 起隔离实例需告知用户**：用户 10-19 说过 mbp 不做协调；本卡只用它读 grok 额度（唯一有 `~/.grok/auth.json` 的机器），不派协调任务。
    - 真实账号落点（存在性检查见台账）：opencode-go → linux-01 root（Q2(a)）或欠交；codex → linux-01 root（已有在役载体 codex 同 HOME，可顺带做 S4 指纹）；grok → macbook-pro xushixin；agy → linux-01 root（agy 1.4.4 ≥ 1.1.11；登录态未判，R2 实测）。
    - 测试账本里自建两成员执行者小队（在役小队都只有一个成员，换人场景造不出来）。

16. **Q16 S6 撞额度的真机触发方式。荐：真错误优先，注入兜底。**
    - 真错误：opencode 载体用一个已知返回 403 ENTITLEMENT_ERROR 的模型（memory：`cline-pass/...` 曾返回此错；先 R6a 确认仍复现）。这是 opencode-go「无订阅」签名，按 Q4 归无额度。
    - 兜底注入：隔离实例配置 `path_dirs` 前置一个只在隔离实例可见的假 CLI 脚本（输出该执行器真样本里的额度错误后退出）。只用于 `t453-*`，不碰在役。
    - 两者都要求测试小队有第二个可用成员。

17. **Q17 前端数据源与刷新节奏。荐：** Shell 级唯一数据源（模块级 store + `usePoll` 30s，`app/data/usePoll.ts`），底栏、弹层、账号页、移动设置页、载体对话框共享；「立即探测」返回的新读数就地合入 store；页面隐藏停表沿用 `usePoll`。执行者可在 15–60s 内调整间隔。

18. **Q18 CLI `account list --json` 是否构成对外承诺。荐 是**，且与 `GET /api/accounts` 元素**同一形状**（一行一个账号对象）——主会话与协调者在终端/脚本里读它（spec S11），skill 会写它。冻结 API 形状即覆盖 CLI（§8 F5、F11）。

19. **Q19 `WakeQuota` 机间 wire 值是否保留。荐 保留。** `HomeWakeResp.outcome` 允许 `quota`（`proto/scheduling.go:166`）是机间契约，旧版机器会发它；新版协调侧收到 `quota` 映射为「可达 + 撞额度」：载体按已上线写，绑定了账号的标该账号无额度，没绑定的把原因写进 `last_error`（spec S4）。只删「载体状态」里的 quota，不删「检测结局」里的 quota。

---

## spec 更正（交协调者，plan 不改 spec）

| 编号 | spec 原文位置 | 事实 | 影响 |
|---|---|---|---|
| SC1 | S1 缺省条目「opencode-go = opencode 的 `auth.json › opencode-go`」；首故事「mini@mac-mini 的 HOME」 | mac-mini 无 `auth.json`；opencode 1.19.3 登录态在 `opencode.db` 的 `account` 表（OAuth），不是 API key | 首故事机器/条目要按 Q2 改；mini 的 opencode-go 账号很可能欠交 |
| SC2 | S7「`--wait`/`wait --final` 按 B460 规则以退出码 5 交回工单」 | B460 F4 冻结的是「回合已结束 > 有待答工单」，分支任务撞额度后处于 `waiting_review`，按现规则得 3 不得 5 | 需改 F4（Q8） |
| SC3 | 目标架构「撞额度分类与检测链已有的额度分类收口为同一处」 | 现有检测分类把 429 限速与额度耗尽混为 quota（`probe.go:437`），与「分类不确定的不算撞额度」冲突 | 收口时拆三类（Q10），检测链对限速的结论随之从「限额中」变为「已上线 + 说明」 |
| SC4 | S6「判据按载体执行器的失败输出分类」 | opencode 执行器的失败文本今天为空（roadmap 975） | Q14 纳入 |
| SC5 | S5/S6 验证「小队中一个成员…另一成员」 | 在役小队 pro/leader/runner 都只有一个成员 | 真机在测试账本自建两成员小队（Q15） |
| SC6 | 测试决定「codex/grok/agy 按实际登录所在机」 | grok 只在 macbook-pro 有凭据文件；agy 只装在 linux-01；codex 两机都有 | 真机需要 mbp 隔离实例（Q15） |
| SC7 | 测试决定「最后升级机队后生产冒烟（`account list` 读到三家读数）」 | r1.1 后是四种 kind | 冒烟按「各已登记真实账号」计，欠交的 kind 不算 |

---

## 0. 交付目标

| 故事 | 本轮做到 |
|---|---|
| S1 登记 | `PUT/DELETE /api/accounts/{name}`（CAS）+ 控制台登记/编辑/删除对话框；四种 kind；校验（阈值、HOME 形态、同机同 HOME 同条目查重、未知 kind 拒）；删除被引用拒删并列载体；保存后控制台调一次立即探测 |
| S2 探测 | 所在机周期探测器 + 四个适配（opencode-go/codex/grok HTTP，agy CLI 子进程）；读数/失败/保留旧读数/退避/过期；立即探测（机间转发）；只读凭据、密钥不离机 |
| S3 状态 | 五档推导（Q4）+ 撞额度标记直到成功探测；单一推导函数供准入、API、CLI 共用 |
| S4 载体 | 载体选同机账号；`GET /api/squads`、`squad list` 带账号名；「限额中」退役（Q12/Q19）；检测时算载体 HOME 指纹并比对标红；账号代理进任务与检测回合（Q6） |
| S5 准入 | 五个准入点经同一判据；全员被跳过 / 点名被拒的文案点名载体、账号、状态、预计恢复 |
| S6 只读重跑 | 三类分类（Q10）；标无额度；原任务以额度耗尽结束并链接新任务；`--wait`/`wait --final` 跟链；每账号一次；无可换成员时 S5 文案 |
| S7 分支停下 | 标无额度；任务停在 `waiting_review`，工作树保留；开 `notice` 工单（Q7）；`--wait` 退出 5（Q8） |
| S8 账号页 | dock「◔」+ `/accounts` 路由；四组分组卡片；色彩纪律 |
| S9 底栏 | 外壳底栏 + 使用量弹层（详细/精简、记住选择）+ hover 二级面板 + 溢出 `+N` |
| S10 移动 | 设置首页「账号与额度」一节（只读 + 立即探测） |
| S11 CLI | `handoff account list [--json]`、`handoff account probe <名称>` |
| S12 文档 | skill、README 中英、CHANGELOG、roadmap 323/590/600/975/986 等条目关闭或改写 |

**留待后续，本轮不做**（落 roadmap「B453 残余」）：spec Out of Scope 全部；Q2 备选（opencode.db 账号登录态，若用户未批）；Q9 崩溃窗口的补偿；agy 在 Windows 上的探测（非目标平台，报「本平台未支持」）。

## 1. 触及子系统与类型

| 子系统 / 领域 | 类型 | 本轮角色 |
|---|---|---|
| 编制域 `internal/scheduling`（新子包 `scheduling/accounts`，见注） | 逻辑型 | 账号定义/读数两 kind、校验、五档推导、撞额度标记合并写；载体加 `account` 引用与 `cred_fingerprint`；统一准入判据；「限额中」退役 |
| 额度适配 `internal/quota`（新） | **边界型** | 四种 kind 的探测适配（HTTP ×3、CLI 子进程 ×1）、响应解析、指纹、撞额度三类分类。对面是外部现实：机内只验解析与契约形状，行为走真机清单 |
| agentd 网关 `internal/agentd` | 逻辑型（接缝对面是自有代码）；对外 HTTP 面 | 账号 API 与机间转发、周期探测器、`/api/squads` 投影、检测链指纹与撞额度标记、派发落点查账号代理、重跑回调实现 |
| 主机能力 `internal/hostapi` | 逻辑型 | `classifyTurnError` 委托 quota；检测回合算指纹、注入账号代理 |
| 编排 `internal/orchestration` + 任务库 `internal/store` | 逻辑型 | 回合失败分类；只读重跑回调与链接；分支任务 `notice` 工单；任务行三列账号快照；env 覆盖代理键 |
| 执行器 opencode `internal/executor/opencode` | 边界型（对面是 opencode CLI） | Q14：会话错误进 `FailReason` |
| wire `internal/proto` | 逻辑型（契约面） | `CarrierView/CarrierInput` 新键、账号 View/Input、`HomeWakeReq/Resp` 指纹键、任务快照链接键、工单 kind `notice` |
| 客户端与 CLI `internal/client`、`cmd/` | 逻辑型 | `WaitFinal` 跟链与次序（Q8）；`account list/probe`；`squad list` 账号列；退出 5 的 notice 块 |
| Web 控制台 `web/src` | 逻辑型（机内单测）+ 形态验收走浏览器熟练工 | 账号页、底栏/弹层/二级面板、载体对话框、移动设置节、词表删 quota |
| 文档 | — | S12 |

注（架构法第二、三条）：`internal/scheduling` 今 18 个文件、无子包；账号会带来 ≥3 个同前缀源文件（定义、读数、推导、合并写），在层内自我分解 → **竖切成子包** `internal/scheduling/accounts`，编制服务以导出符号调用它（同一子系统内的领域，第 1 档）。`internal/quota` 是新的边界型子系统：契约面 = `Probe`/`Fingerprint`/`ClassifyTurnFailure` 三个导出入口 + 读数值类型，消费者 = agentd 探测器、hostapi、orchestration；依赖方向 quota → 无业务包（读数值类型定义在 quota，`scheduling/accounts` 以导出符号引用或各自定义个位数字段的值对象——执行者择一，不得出现环）。

不扇出（spec 定）。派卡资格四条在此不适用；以上划分用于 review 的架构轴核对。

## 2. 故事映射与行为闭环

### 2.1 故事 → 实现责任 → 真实消费者 → 集成/回归负责人

| 故事 | 实现责任 | 真实消费者 | 集成 / 回归 |
|---|---|---|---|
| S1 | accounts 校验与 CAS；agentd 账号 API；web 对话框 | 控制台用户 | 执行者 V1、V2、W1；协调者 R1、R2 |
| S2 | quota 四适配；agentd 探测器与立即探测转发；读数合并写 | 账号页/底栏/CLI；调度准入 | 执行者 V3、V4、V5；协调者 R1、R2 |
| S3 | accounts 推导 | 准入、API、CLI、前端 | 执行者 V6；协调者 R3 |
| S4 | scheduling 载体引用与退役；hostapi 指纹与代理；orchestration env；投影 | 载体对话框、`squad list`、任务进程环境 | 执行者 V7、V8、V9、W2；协调者 R4 |
| S5 | scheduling 统一准入与文案 | 五类派发入口 | 执行者 V10；协调者 R5 |
| S6 | quota 分类；orchestration 分类与回调；agentd 重跑；client 跟链 | 主会话的 `dispatch --template … --wait` | 执行者 V11、V12、V13；协调者 R6 |
| S7 | orchestration notice 工单；client 次序；cmd 输出；web 工单面板 | 主会话 `--wait` | 执行者 V14；协调者 R7 |
| S8–S10 | web | 控制台用户 | 执行者 W3–W6；浏览器熟练工 R8 |
| S11 | cmd | 主会话/协调者终端 | 执行者 V15；协调者 R1、R9 |
| S12 | 文档 | 读 skill 的主会话 | 协调者复核 |

### 2.2 行为闭环（触发者 → 权威事实/载体 → 消费者 → 可观察结果 → 实现责任）

| 触发者 | 权威事实 / 载体 | 消费者 | 可观察结果 | 实现责任 |
|---|---|---|---|---|
| 人在控制台保存一个新账号 | 共享账本 `account/<名>` v1；随后控制台 `POST /api/accounts/<名>/probe` | 入口机账号 API → 转发到所在机 → 所在机探测器 | 账号出现在列表与底栏「未探测」；数秒后变为读数或「探测失败 · 原因」 | agentd API + web |
| 所在机探测器到期 | 所在机读本机凭据 → 调外部额度接口 → `account_reading/<名>` CAS 合并写 | 各机控制台、CLI、调度准入 | `account list` 在任一台机器上看到同一份窗口读数与 `probed_at` | quota + agentd 探测器 + accounts |
| 外部接口失败（断网/错代理/401） | `account_reading` 写 `last_error`、`last_attempt_at`、退避，**保留上次成功读数** | 同上 | 状态「探测失败 · △ 原因」，旧窗口弱色；下次探测按 30s×2ⁿ 封顶间隔 | quota + 探测器 |
| 人点「立即探测」（账号在他机） | 入口机转发 `POST /api/accounts/<名>/probe` 到所在机；所在机单飞执行并写读数 | 发起的控制台/CLI | 返回新读数；所在机离线则 502「转发到 X 失败」、读数不变 | agentd |
| 人在载体对话框选账号并保存 | `carrier/<名>` body 新键 `account`（CAS） | `GET /api/squads`、`squad list`、派发准入 | 载体行显示账号名与其状态 | scheduling + 投影 + web |
| 人点载体「检测」 | 入口机经 `/api/host/wake` 让载体所在机按所选账号 kind/条目读载体 HOME 算指纹；`carrier.cred_fingerprint` 写回 | `/api/squads` 投影比对账号读数指纹 | 不一致 → 载体行与对话框红字；可保存、不拦派发 | hostapi + schedapi + 投影 + web |
| 检测回合撞额度（真额度错误） | 载体写 `online`；绑定账号的 `account_reading` 写撞额度标记；未绑定写 `last_error` | 准入、账号页 | 载体「已上线」；账号「无额度」直到下一次成功探测 | quota 分类 + schedapi + accounts |
| 派发挑选载体（任一入口） | 准入读 `carrier` + `account` + `account_reading`，按统一判据 | 派发请求方 | 低额度/无额度账号的载体被跳过；全员被跳过时错误文案点名「mini：账号 og-mini 无额度，预计 2h41m 后恢复」 | scheduling |
| 账号设了代理，任务在该账号的载体上跑 | 落点机查账号 → 任务行 `account_proxy` → 三处 env 覆盖代理键 | 执行器子进程 | 任务进程环境 `HTTPS_PROXY` = 账号代理；`NO_PROXY` 含 localhost | agentd 派发 + orchestration |
| 只读任务回合失败且分类为额度耗尽 | 原机 Manager → 回调 agentd：标账号、选人（排除链上账号）、派新任务 → Manager 原任务写 `failed` + `rerun_by{task,machine}` + 事件「因额度耗尽改由 X 重跑」 | 主会话 `dispatch --wait`（`WaitFinal`） | 客户端换到新任务继续等；最终退出码与 `artifacts:` 取自新任务；无人可换时原任务 failed、stdout/stderr 是 S5 文案、退出 3 | quota + orchestration + agentd + client |
| 分支任务回合失败且分类为额度耗尽 | 标账号；任务 `waiting_review`、工作树保留；`notice` 工单（载体、账号、窗口、预计恢复、分支、工作树、「接管见 B459」） | 主会话 `--wait` | 退出 5，stdout 打 `kind: notice` 块与「已知悉」答复行；答复后续等得 3 | orchestration + client + cmd |
| 人在 375 宽视口打开设置 | 同一 `GET /api/accounts` | 移动设置页 | 「账号与额度」一节，分组两行，点开看重置时刻，可立即探测 | web |

## 3. 改动边界

**可改（有界文件集）**：
- 新增：`internal/scheduling/accounts/**`、`internal/quota/**`（含 `testdata/` 脱敏真样本）、`internal/agentd/account*.go`、`cmd/account*.go`、`web/src/api/accounts.ts`、`web/src/app/accounts/**`（账号页、底栏、弹层、二级面板、数据源）、对应测试。
- 修改：`internal/scheduling/{scheduling,status,registry_read}.go`（载体新键、统一准入、quota 退役、读时归一）；`internal/agentd/{schedapi,hostprobe,handlers,dispatchroute,receiver_bind,server,scheduling_client}.go`（API 注册、投影、检测指纹与标记、派发查账号、重跑回调）；`internal/hostapi/{probe,driver}.go`（分类委托、指纹、代理覆盖）；`internal/orchestration/{manager,facade,readonly,contracts}.go` 及新文件（分类、回调接口、notice、env 覆盖、原始输入持久）；`internal/store/store.go`（三列 + 链接列）；`internal/proto/{scheduling,proto}.go` + `contract_fixture_test.go` + `web/src/api/testdata/*.json`；`internal/client/skilled.go`、`cmd/{skilled,squad,wait,dispatch}.go`；`internal/executor/opencode/*`（仅 Q14）；`internal/config/config.go`（仅 Q5 注释与作用范围）；`cmd/agentd.go`（组装：探测器、回调、quota 注入）；`web/src/api/scheduling.ts`、`web/src/app/settings/{SchedulingPage,SettingsHub,SettingsPage}.tsx`、`web/src/app/shell/Shell.tsx`、`web/src/app/tree/ProjectTree.tsx`、`web/src/app/task/{TicketsPanel.tsx,review.ts}`；文档。

**须遵守**：
- 组装点唯一（架构法第八条）：探测器、quota 适配注册表、重跑回调只在 `cmd/agentd.go` 组装；`scheduling.New` 不出 cmd。
- Manager 不 import 编制域（`receiver_bind.go:4`）；账号信息经 Go 级 `DispatchReq` 与回调进入。
- 编制服务对 gateway 的出站接口 `SchedulingClient`（`scheduling_client.go`）新增方法要逐个列出并说明消费点（「多一个即越界」）。
- 凭据值只在 quota 包内存在于内存；任何 error、日志、事件、API 响应、账本 body 不得含凭据值或 HTTP 请求头。
- 周期任务形态沿用 ticker + ctx（facts-code §5），停止 = ctx 取消 + `Stop()` 等 goroutine 退出。

**不许动**：`ledger` 表结构与 `ledgerSchemaVersion`；既有 `gate`/`ask` 工单语义；B460 其他冻结条目（F1、F3、F5–F11）；任何在役 agentd 与生产账本（真机只用隔离实例与测试库）；凭据文件（只读、绝不刷新改写）。

## 4. 工作顺序

一个工作单元、单分支，**按段串行**；每段末提交。段 1 = 首故事。每段给「可检查结果」与「红→绿入口」。

段间依赖的性质：段 1 → 段 2/3 是**契约/数据硬依赖**（账号两 kind、读数形状、探测器与 API 是后续一切的前提；Q2 结论决定段 2 是否含 opencode-go 以外的首故事替代）；段 3 → 段 4 是硬依赖（撞额度标记写入与准入判据、任务行账号快照）；R6a 样本 → 段 4 分类表是**事实硬依赖**；段 5 只依赖 §8 冻结形状，与段 4 是**施工排序**（单分支单人串行，可对调）；段 6 依赖全部。

**段 1 首故事（S2+S1 最薄后端竖切，opencode-go）**——承重未知：opencode-go 接口与凭据（Q2）、读数跨机写回与人编辑并发（Q1/Q3）。
- 做：`scheduling/accounts`（定义/读数两 kind、最小校验、最小推导：未探测/探测失败/正常/无额度）；`quota` 的 opencode-go 适配（只 `auth.json › opencode-go`）与解析；agentd 探测器（周期 + 单飞立即探测，本机账号）；`GET/PUT /api/accounts`、`POST /api/accounts/{name}/probe`（含转发）；`handoff account list [--json]`、`account probe`。
- 红→绿入口：`go test ./internal/quota/ -run OpencodeGo`（解析表：200 三窗口、401、403 EntitlementError、字段缺失、秒/毫秒时间戳）；`go test ./internal/scheduling/accounts/ -run 'Merge|Concurrent'`（人写定义与机写读数并发 N 轮，两侧字段都不丢；旧探测不抹新标记）；`go test ./internal/agentd/ -run AccountProbeForward`（他机账号的立即探测转发到所在机、所在机写读数、入口机不写）。
- 可检查结果：R1（协调者）——隔离实例上登记真实 opencode-go 账号，`account list` 看到窗口读数；或按 Q2 降级路径的明确结论。**R1 不过不进段 2**（按 Q2 走 (a)/(b)，结论回主会话）。

**段 2 其余适配与探测纪律（S2 完整、S1 校验完整）**
- 做：codex、grok HTTP 适配；agy CLI 适配（版本门、子进程超时与进程组清理、`disabled` bucket 不计、每模型组 5h/周窗口）；代理（账号代理 > Q5 回落）；退避 30s×2ⁿ 封顶间隔；过期 30 分钟；指纹（Q13）；token 过期文案；S1 全部校验、删除拒删列引用、删除连带删读数行。
- 红→绿入口：`go test ./internal/quota/...`（四家解析表 + agy 版本门：`< 1.1.11` 不执行 `/usage` 的断言用假可执行文件计数调用次数为 0 + 超时杀进程组：假 CLI 睡眠超时后无残留子进程）；密钥不外泄测试（V5）。
- 可检查结果：R2。

**段 3 状态、载体、准入（S3、S4 后端、S5）**
- 做：五档推导（Q4）；载体 `account` 引用（同机校验、可空）与 `cred_fingerprint`；`StatusQuota` 退役 + 读时归一（Q12）；检测链：指纹（`HomeWakeReq/Resp` 新键）、限速/额度三类（Q10/Q19）、撞额度标记；`/api/squads` 与 `squad list` 投影；派发落点查账号 → 任务行三列 → env 覆盖（Q6）；统一准入判据替换五处 `Status != StatusOnline`，S5 文案。
- 红→绿入口：`go test ./internal/scheduling/... -run 'Admit|Select|Derive|Legacy'`；`go test ./internal/proto/ -run TestContractFixtures`（先红再 `-update`）；`go test ./internal/orchestration/ -run ProxyEnv`；`go test ./internal/hostapi/ -run 'Classify|Fingerprint'`。
- 可检查结果：R3、R4、R5。

**段 4 撞额度（S6、S7）**——先做 Q14 的样本采集（R6a，协调者），样本入 testdata 后执行者才写分类表与 opencode 执行器修改。
- 做：opencode `FailReason`（Q14）；`quota.ClassifyTurnFailure`；Manager 分类与回调接口；agentd 重跑实现（原始输入持久、排除集、链接、事件）；`notice` 工单全链（Q7 白名单清单逐处）；`WaitFinal` 跟链与次序（Q8）；退出 5 的 notice 块。
- 红→绿入口：`go test ./internal/quota/ -run Classify`（真样本表）；`go test ./internal/orchestration/ -run 'Quota|Rerun|Notice'`（伪执行器 `internal/executor/fake` 产出额度失败）；`go test ./internal/client/ -run 'WaitFinal.*(Rerun|Notice)'`；`go test ./cmd/ -run 'PendingTicket.*Notice'`。
- 可检查结果：R6、R7。

**段 5 前端（S1/S4 对话框、S8、S9、S10）**
- 做：`api/accounts.ts`（类型镜像 fixture）、Shell 级数据源（Q17）、dock「◔」与 `/accounts` 路由（`ProjectTree.tsx:1467-1536`、`Shell.tsx:1287-1300,1579-1594,922-923`）、账号页、外壳底栏（Shell 今无 footer，新增于主区底部，紧凑档不渲染）、弹层两模式（偏好走 `webPrefs`/`useWebPrefs`）、hover/聚焦/点击二级面板（无 Popover 组件，手写，参考 `app/lib/IconMenu.tsx` 的 portal 做法）、载体对话框账号下拉与红字、`SettingsHub` 新节、词表删 quota。
- 红→绿入口：`cd web && npm test -- accounts`、`npm run typecheck`；`contract.test.ts` 新增用例。
- 可检查结果：R8（浏览器熟练工对照原型）。

**段 6 收口（S11 打磨、S12、机队）**
- 做：文档；roadmap 条目；全量测试（implement 三段律）；协调者 R9 生产冒烟与 R10 密钥审计。

## 5. 验收方式

### 5.1 机内断言（执行者）

- **V1 S1 校验**：表驱动——阈值（预警 < 低额度 ≤ 100）、HOME（绝对或 `~` 开头）、未知 kind、名称为空、同机同 HOME 同条目重复（不同名）→ `ErrInvalid` 且文案点名冲突账号；改名（PUT 到另一个名字带旧 version）不可能（名字即 id）；编辑不提供名字字段。
- **V2 S1 删除**：有载体引用 → 409，响应体列出引用载体名；无引用 → 删定义行与读数行；读数行删除失败只 Warn（孤儿读数行不展示，V6 有断言）。
- **V3 S2 并发不丢写（Q1 承重）**：在内存 registry 上并发跑 N=50 轮「人 PUT 定义（各自持正确版本、冲突即重读）」×「探测器写读数」×「标记者写撞额度标记」，结束后：定义字段 = 人最后一次写；读数 = 探测器最后一次写；标记按时间序规则存在/清除。另一条：探测开始于标记之前、返回于标记之后 → 标记保留（反例锁 Q1 连带规则）。
- **V4 S2 解析**：四种 kind 各一张表，样本来自 orca 字段说明 + R1/R2 真样本（脱敏后入 `internal/quota/testdata/`，脱敏规则：email/id/金额替换为占位，结构不变）；含 401/403/429、字段缺失、秒与毫秒时间戳、codex `limit_window_seconds` 归类、grok `/billing?format=credits` 失败回退 `/billing`、agy `disabled` bucket 与 `remaining_fraction` 换算。
- **V5 密钥不外泄（承重安全属性）**：用含哨兵串的假凭据跑四个适配的成功、401、网络错、超时、响应体回显 token 的路径，断言 error 文本、日志捕获、返回读数、序列化后的 registry body、API 响应体中**零出现**哨兵串与 `Authorization` 头值；指纹不等于凭据任何子串。
- **V6 S3 推导**：五档表 + 优先级（Q4）+ 过期标记 + 已过重置时刻窗口（若 Q4 补条获批）+ 孤儿读数行。
- **V7 S4 载体引用**：选他机账号 → 拒；选不存在账号 → 拒；不选 → 合法；存量 `status:"quota"` 的载体 body 读出为 `online`（读时归一）；PutCarrier 保留未改字段的 `account`。
- **V8 S4 指纹与投影**：`/api/squads` 载体行 `account` 与 `account_mismatch` 的穿链测试（编制 → 投影 → JSON → TS fixture，区分键缺席与零值）；指纹相同不标、不同标、任一侧空不标。
- **V9 S4 代理进环境**：账号代理覆盖 env 文件与进程环境的六个代理键；`NO_PROXY` 保留并在缺失时补 localhost；未选账号/账号无代理时 env 与改动前逐字节一致（回归锁）；续接路径同样生效。
- **V10 S5 准入**：五个准入入口（`AdmitCarrier`、`SelectWhere`、`AdmitFrozen`、`AdmitSeatCarrier`、`admitInto`）各至少一条「账号低额度/无额度被拒」与一条「未探测/探测失败放行」；全员被跳过的错误 `errors.Is(err, ErrNoHealthy)` 仍为真（调用方 HTTP 映射不变），文案逐个点名载体、账号、状态、预计恢复（无读数时「恢复时刻未知」）。再加一条结构断言：编制包内不再有直接比较 `Status != StatusOnline` 的准入代码（grep 型测试或 review 清单二选一，执行者定）。
- **V11 S6 分类**：`ClassifyTurnFailure` 真样本表：额度耗尽 / 限速 / 其他 三类各有正例；限速样本**不得**判额度；空文本、截断文本判其他。
- **V12 S6 重跑链（伪执行器集成）**：两成员小队，成员 A 的伪执行器首回合输出额度错误 → 原任务 `failed` 且快照带 `rerun_by`、事件「因额度耗尽改由 B 重跑」；新任务 `rerun_of` 指回；账号 A 标无额度；新任务输入（计划原文）与原任务一致且**不含两份**「## 产物目录」。反例：B 也撞额度 → 链终止，原/新任务 failed、文案同 S5；同一账号不出现两次；无账号载体按载体名排除。
- **V13 S6 客户端跟链**：`WaitFinal` 遇 `failed + rerun_by` 切换任务（含跨机 `machine`），最终退出码与 `artifacts:` 取自新任务；链长 2 时跟两跳；`rerun_by` 缺席时行为与今天一致（3）。
- **V14 S7**：分支任务额度失败 → `waiting_review`、工作树仍在、`notice` 工单一张（字段齐全，含「接管见 B459」）；`WaitFinal` 先返回 Pending（5）；stdout 块形状按 F-B460-1；答复「已知悉」后不调用执行器 `Send`/`RespondPermission`（伪执行器计数为 0），再等得 3；`gate`/`ask` 工单在 `waiting_review` 下的旧行为不变（今天不存在该组合，加一条构造用例锁 Q8 的窄改范围）。
- **V15 S11**：`account list` 表格列与 `--json` 每行可被 `json.Unmarshal` 进 API 的 View 类型且字段全等（同形承诺 F11）。
- **W1–W6 前端**：W1 登记对话框缺省值随 kind 预填（agy 条目为空且只读说明）；W2 载体对话框只列同机账号、红字条件；W3 账号页分组顺序与卡片状态短语；W4 底栏排序（无额度 > 低额度 > 探测失败 > 未探测 > 正常）、两模式切换记忆、精简模式问题账号显示触发窗口；W5 色彩纪律——渲染快照里彩色类只出现在越线数字/状态短语节点上；W6 移动设置节。

### 5.2 运行入口

- 后端：`go test ./internal/quota/... ./internal/scheduling/... ./internal/agentd/... ./internal/hostapi/... ./internal/orchestration/... ./internal/store/... ./internal/client/... ./internal/proto/... ./cmd/...`；全量按 implement 三段律。注意 memory：两支 Go 测试在 `CLAUDE_CODE_SESSION_ID`/嵌套 worktree 环境下会红，与本卡无关（`handoff-env-sensitive-tests`）。
- 契约：`go test ./internal/proto/ -run TestContractFixtures [-update]` 与 `cd web && npm test -- contract`。
- 前端：`cd web && npm test && npm run typecheck && npm run lint`。

### 5.3 真机清单（**本节由协调者执行，不派发**）

前置（Q15）：
- 分支构建三份二进制（darwin arm64 ×2 机、linux amd64），草稿路径，记 sha256。
- 建测试库 `handoff_b453`；三个隔离实例 `t453-linux`/`t453-mac`/`t453-mbp`（端口与 datadir 见 Q15），设备表互登。**在役 7777 与生产账本全程不动**；开始与结束各记一次三机在役进程 pid/启动时刻。所有 CLI 带 `--config <草稿配置>`。
- **R0**：三个隔离实例启动日志 `dsn_configured=true`、设备表三行 `updated_at` 新鲜（Q3 的前提）。

步骤：
- **R1 首故事**（段 1 后）：`t453-linux` 上 `PUT /api/accounts/og-linux`（opencode-go，HOME `/root`，条目缺省）→ 自动立即探测 → `handoff account list` 在 `t453-mac` 上读到三窗口读数（跨机可见）。同时在 `t453-mac` 登记 `og-mini`（HOME 主 HOME）→ 预期明确失败文案（SC1）。结论按 Q2 回主会话。另：控制台连续保存定义 5 次期间探测器以 1 分钟间隔写读数，人侧零次 CAS 冲突（Q1 行为证据）。
- **R2 其余 kind**（段 2 后）：codex@`t453-linux`（`/root`）、grok@`t453-mbp`（`/Users/xushixin`）、agy@`t453-linux`（`/root`，先记 `agy --version`）各登记一个；每个跑满一轮周期探测（间隔临时调 5 分钟）+ 一次跨机立即探测；断网/错代理（账号代理填一个不存在端口）→「探测失败」且旧读数保留；改坏凭据——**不得改动真实凭据文件**：改用另建的隔离 HOME 放一份内容为哨兵的假 `auth.json` 登记成另一个账号 → 401/解析失败文案。agy：确认 `/usage` 未消耗额度（探测前后各读一次，模型组窗口不因探测上升）；若 agy 未登录，记欠交。Q5：mac-mini 上分别以「config proxy」与「仅进程环境」各探一次 opencode-go/codex（若有账号），记结果。
- **R3 S3**：把某真实账号低额度线临时调到当前已用以下 → 「低额度」，调回复原。
- **R4 S4**：(a) 测试账本建载体 `t453-codex`（linux，codex，HOME `/root`）选账号 codex 账号 → 检测 → 指纹一致；(b) 绑同 kind 另一个账号（R2 的哨兵 HOME 账号）→ 检测后红字，可保存，派发不拦；(c) 给账号填 linux-01 本机代理 `http://127.0.0.1:1080`（trojan，memory/machines.md）→ 派一个只读任务让执行者 `env | grep -i proxy` 写进产物 → 读到账号代理、`NO_PROXY` 含 localhost，且 opencode 载体任务能正常完成（Q6 行为事实）；(d) `handoff squad list` 与 `GET /api/squads` 载体行带账号名；(e) 存量 `quota` 载体：测试账本手写一条 `status:"quota"` 的载体 body → 读出「已上线」。
- **R5 S5**：两成员测试小队（各绑一个真实账号）；把成员 A 账号低额度线调低 → 连续 3 次派发都落 B；两者都调低 → 点名文案；`--receiver <A>` 点名派发被拒、同文案。
- **R6 S6**：(a) **先取样本**（段 4 前）：opencode 载体用会返回 ENTITLEMENT 的模型派一个只读任务，取 `FailReason`/事件原文与 opencode 原始输出（脱敏）交执行者入 testdata；codex/grok/agy 的额度错误样本有就取、没有就记「无真样本，合成并注明」；(b) 段 4 后：两成员测试小队，成员 A 用 ENTITLEMENT 模型（或 Q16 注入）、B 正常；主会话侧后台 `dispatch --template code-investigate --receiver <测试小队> --wait` → 看到原任务 failed + 链接、B 上新任务完成，`--wait` 退出 0 且 `artifacts:` 是新任务的；`wait <原任务> --final` 同样跟到新任务；(c) 两成员都撞 → 退出 3 + S5 文案。
- **R7 S7**：同 R6 注入方式派分支任务 → `notice` 工单、退出 5、工作树仍在；答「已知悉」→ `wait --final` 退出 3。
- **R8 前端**：交 `browser-worker`，URL 为隔离实例控制台，对照 `prototypes/b453-accounts/` 的 `pages/accounts.html`、`index.html`/`pages/board.html` 底栏与弹层（两模式、hover 面板、`+N`）、`pages/settings.html?section=automation`、`pages/mobile-settings.html`（375 宽）；判据 = spec S8–S10 条目 + 色彩纪律；裁决归主会话。
- **R9 机队升级与生产冒烟**（全部 R 通过、合并后，按 finish 节点）：**先升级全部三台再登记账号/给载体选账号**（§7 跨版本族：旧版 agentd 重写载体 body 会丢 `account` 键）；`handoff account list` 读到各已登记真实账号读数 + 一次正常熟练工派发。
- **R10 密钥审计**：在测试库 `registry` 表、三个隔离实例日志、任务事件里搜各真实账号凭据的前 8 字符（审计脚本由代码读凭据并只输出命中计数，**不打印凭据**），零命中。
- **收尾**：停隔离实例；删 datadir；测试库按 credentials.md 注记处理；台账记全部证据与在役进程前后对照。

**未验证，需真机**（汇总）：
- U1 opencode-go usage 接口是否接受 `auth.json` 里的 API key；linux-01 `auth.json` 是否有 `opencode-go` 条目（Q2，R1）。
- U2 OAuth access_token 是否可用（Q2 备选，仅在用户批准后验）。
- U3 codex `wham/usage`、grok `billing` 的真实响应字段与 orca 描述是否一致（R2）。
- U4 agy 1.4.4 的 `/usage` JSON 信封形状与「不消耗额度」（R2）；linux-01 agy 是否已登录。
- U5 无账号代理时 mac-mini 的可达性（Q5，R2）。
- U6 账号代理注入后 opencode 执行器 localhost 通信是否正常（Q6，R4c）。
- U7 各执行器额度错误的真实文本（Q10/Q14，R6a）；opencode 执行器失败文本是否为空（roadmap 975）。
- U8 三台隔离实例都挂共享测试账本（Q3 前提，R0）。

## 6. 决策权限

**执行者可自行决定**（交回物「偏离与自主决定」逐条对照）：
- 新包内部文件拆分、私有函数与类型命名；`scheduling/accounts` 与 `quota` 之间值类型放哪一侧（不得成环）。
- 探测器内部调度结构（每账号 timer 还是统一 ticker 扫描）、扫描间隔（≤ 60s，用于发现新账号/改间隔）、并发上限（≥ 2）。
- HTTP 超时（10–30s 区间，参考 orca）、agy 子进程超时（≥ print-timeout 20s + 余量）、读数行字段命名（§8 冻结的除外）。
- 原始派发输入文件的名称与形状（任务私有）。
- 前端组件拆分、轮询间隔（15–60s）、弹层/二级面板实现方式、偏好键名。
- 日志措辞与级别。必须有的观测点：探测成功一条 Info（账号、kind、机器、窗口数、耗时，**不含**凭据与头）；探测失败一条 Warn（账号、失败类别、连续失败数、下次时刻）；准入因账号跳过一条 Info（载体、账号、状态）；撞额度标记一条 Info（账号、来源 task/carrier、分类类别）；重跑一条 Info（原任务、新任务、机器、链上已用账号数）；notice 工单一条 Info。

**必须升级回主会话**：
- §8 任一冻结条目的改动（端点路径、JSON 键名与取值、状态词、kind 名、退出码与输出块形状、机间 wire 键）。
- 「待拍板」清单任一裁决的偏离；Q2 降级路径的选择（R1 结论由协调者定）。
- 需要改 `ledger` 表结构或 `ledgerSchemaVersion`。
- 改写既有测试断言。预计允许的只有：(1) 断言 `StatusQuota`/限额中 存在的测试（`status_test.go`、`scheduling.fetch.test.ts:146` 等，随退役删改）；(2) 断言检测把 rate limit 判 quota 的测试（随 Q10 改）；(3) `WaitFinal` 中锁 F4 旧次序的测试（随 Q8 窄改）；(4) 契约 fixture 刷新。改前列清单报协调者。
- 发现任何读凭据的路径需要写/刷新凭据文件，或某 kind 只能通过刷新 token 才能探测。
- 真样本与假设签名不符导致分类判据要放宽到「不确定也算额度」。
- 段 1 R1 结论出来前开始段 2 的 opencode-go 以外工作——允许（施工排序不受影响），但不得为 opencode-go 引入 Q2 备选形态。

## 7. 缺陷族（`defect-families` 逐族设问 → 验收责任）

| 族 | 设问与结论 | 验收 |
|---|---|---|
| 生命周期/状态机中断 | 探测中 agentd 重启：内存退避状态丢失 → 重启后从读数行的 `last_attempt_at`/连续失败数重建，不突发全量探测。agy 子进程在超时/停机时：独立进程组，超时与 ctx 取消都杀整组，停机 `Stop()` 等探测 goroutine 退出。重跑回调中途崩溃：Q9 残余窗口（先写链接再迁终态），落 roadmap。分支任务停下后：工作树保留、无自动清理，由 B459 接管。 | V5（超时杀组）、段 2 agy 测试；R2 观察无残留 `agy` 进程；Q9 残余入 roadmap |
| 静默失败/误导报错 | 探测失败必须落账本（不是只打日志）；所在机离线时立即探测返回可行动 502；分类不确定按普通失败而非静默重跑；全员被跳过文案点名；opencode 空失败文本（Q14）；`notice` 被旧 CLI 当 permission 打印（跨版本，见下）。存在「报成功但没做」吗：立即探测转发成功但所在机探测器未挂载 → 所在机必须返回 503「探测器未运行」而非 200 空读数。 | V2、V10、V11、V14；R1/R2 失败路径 |
| 跨平台假设 | `~` 展开用 `hostapi.ExpandHomePath`；进程组 Setpgid 仅 unix，Windows 上 agy 探测报「本平台未支持」；opencode 凭据位置随版本变（SC1）；mac/linux HOME 路径差异。 | 段 2 单测；R2 三机 |
| 假红/假绿 | 夹具里的接口形状来自 orca 转述 → 必须由 R1/R2 真样本替换（U3/U4）；额度错误签名只能来自真样本（U7）；并发不丢写用负载测试（V3 N=50）；准入「统一判据」用结构断言防有人在某个入口另写一份；前端色彩纪律断言锁行为（越线才着色）不锁类名实现。 | V3、V4、V10、V11、W5 |
| 门禁绕过 | 新写路径 `PUT/DELETE /api/accounts`、`POST …/probe` 与 `/api/squads` 写端点同一鉴权门；机间转发带目标 token（`forwardJSON`）；转发来的探测请求在所在机核「账号确属本机」否则 409；TOCTOU：查重登记与删除拒删都是「先扫后写」，两人并发登记同机同 HOME 可能双成功——接受（人工低频，第二个可删），删除与载体选该账号并发时，载体侧引用不存在账号按「未选账号」处理并 Warn。 | V1、V2、V7；review 核鉴权中间件挂载 |
| 序列化边界 | 新字段的每一处手写投影：registry body（定义/读数/载体 `account`/`cred_fingerprint`）、`/api/accounts` View、`CarrierView`、`HomeWakeReq/Resp`、任务快照 `rerun_of/rerun_by`、`dispatchRequest` 排除集与 `rerun_of`、任务库三列 + 链接列、CLI `--json`、TS 类型。每条至少一支穿过真实转换链的测试，用可空类型区分缺席与零值（`warn_percent: 0` 非法但必须能被识别为「给了 0」而不是缺省）。 | V3、V8、V12、V13、V15、契约 fixture |
| 枚举新值过白名单 | 新值：五个状态键、四个 kind、工单 kind `notice`、事件类型（额度耗尽/重跑）；删值：载体状态 `quota`。逐处：`writePendingTickets`、`RelayAnswer`、`roomsapi.go:734`、`TicketsPanel.tsx`、`review.ts`、账本镜像开放工单、`CARRIER_STATUS_LABEL`、`carrierStatus()` 回落、`detectEvidence`、事件可交付策略 `client.WaitDeliveryPolicy`（新事件类型是否唤醒等待）。 | V7、V14 + 执行者交回物列出 grep 结果 |
| 跨版本（本项目追加） | 滚动升级期间：旧版 agentd 解码再编码载体 body 会丢 `account` 键 → R9 规定先全队升级再选账号；旧版写回 `status:"quota"` → 读时归一；旧 CLI 读新 agentd 的 `notice` 工单会打成 permission → 全队同升，文档注明；旧机收到 `HomeWakeReq` 新键忽略 → 无指纹、不标红（降级可接受）。 | R9 顺序；V7 |
| 承重安全属性 | 「密钥不离机」：V5 + R10 两层锁；「凭据只读」：适配器打开凭据文件只用只读标志，测试断言文件 mtime 不变。 | V5、R10 |

## 8. 需冻结的外部承诺

**入口判据成立**：新增跨进程 wire（混版本 agentd 互调：立即探测转发、检测指纹、派发 `rerun_of`、任务快照链接）、共享账本 body（所有版本的 agentd 读同一行）、对外 CLI 输出（主会话/skill 依赖），以及对 B460 已冻结 F2/F4 的变更。单工作单元内可逆的内部方法不入。复用不重述：B452 F19–F23（`--wait` 完成输出与 0/3/4/124）、B460 F1、F3、F5–F11。

**账号 HTTP API（producer agentd，consumer 控制台 / CLI / 他机 agentd）**
- **F1**：`GET /api/accounts` → `200 {"accounts":[AccountView…]}`；空库为 `[]` 不是 null。
- **F2**：`PUT /api/accounts/{name}?expect=<v>` 请求体 AccountInput；`expect=0` 新建；版本冲突 409；校验失败 400 且文案可行动。
- **F3**：`DELETE /api/accounts/{name}?expect=<v>`；被载体引用时 409，体含 `"carriers":[…]`。
- **F4**：`POST /api/accounts/{name}/probe` → `200 AccountView`（含本次读数或失败）；所在机非本机时由入口机转发同路径；所在机不可达 502；转发到达的机器不是账号所在机 409；所在机探测器未运行 503。
- **F5**：AccountView 键：`name, kind, machine, home_dir, entry, proxy, interval_minutes, warn_percent, low_percent, version, state, stale, reading, last_attempt_at, last_error, carriers`，其中可缺席的：`entry`（agy）、`proxy`、`reading`（从无成功）、`last_attempt_at`、`last_error`。
- **F6**：`state` ∈ `ok | low | out | fail | none`（正常/低额度/无额度/探测失败/未探测）。
- **F7**：`kind` ∈ `opencode-go | codex | grok | agy`。
- **F8**：`reading` 键：`windows, plan_type?, extra_usage?, fingerprint?, probed_at`；`windows[]` 元素键：`key`（`session|weekly|monthly`）、`group?`（agy 模型组名）、`used_percent`（0–100 数值）、`resets_at`（RFC3339，可缺席）。
- **F9**：AccountInput 键：`kind, machine, home_dir, entry?, proxy?, interval_minutes, warn_percent, low_percent`；不含 `state/reading/version`（与 `CarrierInput` 不含 status 同理）。
- **F10**：账本 body 与 API 响应中任何位置不出现凭据值（V5/R10 锁）。

**CLI（producer `handoff`，consumer 主会话 / skill / 协调者）**
- **F11**：`handoff account list --json` 每行一个对象，形状 = F5 AccountView。
- **F12**：`handoff account probe <名称>` 成功退出 0，失败（含探测失败结论）退出 1，stdout 打新读数表格行。

**编制 wire（producer agentd，consumer 控制台 / `squad list`）**
- **F13**：`CarrierView` 新键 `account`（omitempty，所选账号名）、`account_mismatch`（omitempty，bool，仅 true 时出现）；`CarrierInput` 新键 `account`（omitempty，空 = 不选）。`status` 取值去掉 `quota`。

**机间 wire（producer/consumer 均为 agentd，混版本）**
- **F14**：`dispatchRequest` 新可选键 `rerun_of`（`{"task","machine"}`）与 `exclude_accounts`（字符串数组，含以载体名计的无账号成员）；旧版收件端忽略不影响派发本身。
- **F15**：`HomeWakeReq` 新可选键 `fingerprint_kind`、`fingerprint_entry`；`HomeWakeResp` 新可选键 `fingerprint`；缺席 = 不比对。
- **F16**：`HomeWakeResp.outcome` 仍允许 `quota`（Q19），语义改为「可达 + 撞额度」。
- **F17**：任务快照（`GET /api/tasks/{id}` 的 task 对象）新可选键 `rerun_of`、`rerun_by`（`{"task","machine"}`）。

**共享账本 body（producer/consumer 为各版本 agentd）**
- **F18**：registry kind 名 `account`、`account_reading`；id = 账号名。
- **F19**：`carrier` body 新键 `account`、`cred_fingerprint`（omitempty）；`status` 写入值不再有 `quota`，读到 `quota` 视为 `online`。

**对 B460 已冻结条目的变更（重走本节，需协调者审核）**
- **F-B460-1**（改 F2）：新增 `kind: notice` 块：全文取请求体 `notice`，答复行一条 `reply: handoff reply <任务ID> --ticket <工单ID> --answer "已知悉"`；`gate`/`ask` 块不变。工单 kind 取值新增 `notice`。
- **F-B460-2**（改 F4）：任务状态为 `waiting_review` 且快照 `pending_tickets` 非空时，「有待答工单（5）」先于「回合已结束（3）」；`completed`/`failed` 下次序不变。

**§8 修订 r1（2026-10-22，主会话据审查第 1 轮 I1 回写；实现中形成、经各段主会话裁决认可的冻结形状）**
- **F2 增补**：PUT 成功回 `200 AccountView`（F5 形状）；`expect` 必填，缺 → 400；重复新建 → 409。
- **F3 增补**：DELETE 成功回 `200 {"name","version"}`（沿用载体删除回包形状）；被引用 → `409 {"error","carriers":[…]}`；不存在 → 404；版本冲突 → 409；缺 `expect` → 400；删除定义行连带删读数行。
- **F4 增补**：不存在 → 404；所在机离线/未登记 → 502（「转发到 X 失败」，读数不变）；转发到达的不是所在机 → 409；所在机探测器未运行 → 503；写回失败 → 500。
- **F5 增补**：`last_error` 形状 `{category, message}`（category 取值见 `internal/quota` 失败类别：unauthorized、credential_missing、credential_expired、credential_invalid、no_subscription、limit_reached、proxy、network、timeout、rate_limited、upstream、parse、version_too_old、cli_missing、unsupported_kind、unsupported_platform）；`proxy` 含 userinfo 时密码段打码为 `scheme://user:***@host:port`，PUT 收到与打码形式相同的值时保留原存值；`carriers` 为引用该账号的载体名数组（无则 `[]`）。
- **F8 增补**：`extra_usage` 形状 `{unit?, balance?, used?, limit?, unlimited?}`（codex credits → `unit:"credits"` + balance；grok prepaidBalance/onDemandUsed/onDemandCap → balance/used/limit）；`windows[].group?` 为模型组名（agy）；`fingerprint` 形如 `sha256:` + 12 位十六进制。
- **F15 增补**（D2）：`HomeWakeReq` 另增可选键 `account`（所选账号名）——目标机凭它在本机账号域查代理（代理串不过机间 wire），只认所在机为本机的账号；旧版目标机忽略该键 = 检测回合不走账号代理（可接受降级）。
- **F-B460-2 实际范围（收窄）**：只有任务处于 `waiting_review` 且快照待答工单中**含 `kind: notice`** 时，「有待答工单（5）」先于「回合已结束（3）」；`gate`/`ask`（permission/question）工单在 `waiting_review` 下维持 B460 F4 原序（回合结论 0/3 优先）；`completed`/`failed` 下次序不变。原文「`pending_tickets` 非空」作废。B460 plan F4 已加交叉注。

**拍板记录**（难逆转、无上下文会惊讶、真取舍）：
- **定义与读数分两行**（F18，Q1）。被否方案：同行分字段。反过来写不会有任何单测变红（单线程下两种都对），只有 V3 并发负载与 R1 人侧编辑体验能看出差别。
- **读数只由所在机写，立即探测转发到所在机执行**（Q3）。被否方案：照 detect 由入口机写。反过来写功能测试全绿，但会出现两个写路径、两份退避状态、同账号并发探测。
- **限速不算额度**（Q10、F16）。被否方案：沿用 `quota|rate limit|too many requests` 一把抓。反过来写，所有 429 都会触发换人重跑与账号标无额度，持续到下次成功探测（最长一个间隔），且没有测试会红。

## 9. 读数行（`account_reading` body）字段草案

非冻结（F18 只冻 kind 与 id），执行者可调字段名，但语义要齐：最近成功读数（`windows`、`plan_type`、`extra_usage`、`fingerprint`、`probed_at`）；最近尝试（`last_attempt_at`、`last_attempt_started_at`、`last_error`{类别、短因、全文}、`failure_streak`）；撞额度标记（`quota_mark`{`at`、来源 `task`/`carrier`/`machine`、分类类别、短因}）；写者标识与时刻（审计用）。失败类别至少：`credential_missing`、`credential_expired`、`unauthorized`、`no_subscription`、`network`、`proxy`、`timeout`、`parse`、`version_too_old`（agy）、`unsupported_platform`。

## 10. 核实读数（出稿时在 `9043ba71` 上核对，详见台账 plan 节）

- 账本：`cmd/agentd.go:659-700 setupLedger`（有 DSN 即 OpenShared + SelfRegister）、`:734-760` 每机组装编制服务；`internal/agentd/server.go:1447,1507-1550` registry 适配。
- registry：`internal/ledger/registry.go:29-77 RegistryPut`（整行 CAS）、`:80-124` Get/List/Delete；`internal/schedclient/schedclient.go`（使用方接口）。
- 编制：`internal/scheduling/scheduling.go:27-33` kind 常量、`:55-65 Carrier`、`:228 PutCarrier`、准入 `:428/:618/:714/:838/:1080`、`:42 maxCASAttempts=8`；`status.go:22-30,106-138`。
- 检测：`internal/agentd/schedapi.go:37-46` 路由、`:237-330 handleCarrierDetect`、`:364-376 detectEvidence`；`internal/hostapi/probe.go:67 WakeQuota`、`:373-426 wakeProxyEnv`、`:430-444 classifyTurnError`。
- 转发：`internal/agentd/forward.go:54 forwardIfRequested`、`:116 forwardJSON`；`dispatchroute.go:63 routeOrdinaryDispatch`、`:100 SelectWhere`、`:154 forwardDispatch`。
- 编排：`manager.go:767/1539/3840` env 组装、`:1171 withCarrierHome`、`:1913-1960 RelayAnswer`、`:3463 handleResult`、`:3526-3548` 失败分支；`readonly.go:31 TerminalNotifier`；`server.go:542 TaskTerminal`；`facade.go:115 AnswerTicket`。
- 客户端：`internal/client/skilled.go:196-258 WaitFinal`；`cmd/skilled.go:389-432 writePendingTickets`；`cmd/exit.go` 退出码 1/3/4/5/124。
- 工单：`proto.go:398` kind 注释（gate/ask）；`store.go:297-324` 任务列迁移。
- 代理：`internal/proxycfg/proxycfg.go:43,89,144`；`internal/config/config.go:83-100`。
- 凭据路径表：`internal/toolchain/detect.go:84-88`（opencode/grok/codex 相对路径；claude/agy 不在表）。
- 前端：见台账 plan 节 Explore 交回条目（Shell、ProjectTree dock、SettingsHub、usePoll、契约 fixture、`quota` 引用点）。
- 真机存在性：见台账 plan 节「真机存在性检查」条目（未读取任何凭据值）。
