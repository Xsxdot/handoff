# B464 实现计划：账号产品化（发现 → 登录 → 载体选登录，凭据永不复制）

- 状态：**已拍板（2026-10-22）**（r0 由 plan subagent 出稿；主会话裁决见「待拍板」节首的拍板记录：Q1–Q23 全按荐采纳，Q5–Q8 按 R0 真机结论分支执行）。
- 输入：`docs/superpowers/specs/b464.md` **r1**（2026-10-22 用户批准）。L3，形态甲（本地盯：本会话 subagent 实现、独立审查、主会话真机验收）。
- 基线：分支 `cards/B464-charter-1` @ `5b8eab24`（含 main/B453 合入 `6849dcf2`）。
- 原型（形态基准，gitignore）：`prototypes/b464-accounts/`——`pages/accounts.html`、`pages/settings.html?section=automation`、`pages/mobile-settings.html`、`shared/accounts.js`（数据模型注释 1–20 行）。`prototypes/base/README.md:28` 记「确认中」。
- 前作：B453（`docs/superpowers/plans/b453-plan.md` 已拍板 + §8 r1–r4；复用其探测器、统一准入单点、泄密测试表、底栏）。
- 台账：`docs/superpowers/specs/b464-ledger.md`「plan 出稿」节是本稿全部读数（文件:行）与放弃方案的原始记录；§13 只列索引。
- 读者：零上下文执行者。故事编号沿用 spec S1–S9。标「**未验证，需真机**」的结论不得写成测试夹具，汇总见 §8.3。

---

## 待拍板（出稿者推荐，主会话裁决）

> **拍板记录（主会话，2026-10-22）**：Q1–Q23 全部按「荐」采纳，SC1–SC5 采纳为 spec 事实更正（spec r1.1）。附加裁定：
> - Q5、Q6、Q7、Q8 是「按 R0 真机结论走」的条件裁决：R0 结论由主会话落台账后，按本条所列分支执行；结论落在所列分支之外（例如 Q8 发现 agy 换目录项会作废原件）时，停下升级主会话，不自选。
> - Q6 分支 b 成立时（同一 mac 的 claude 钥匙串跨 HOME 共用），按荐执行（darwin 上 claude 只认主 HOME），并由主会话告知用户、在 spec 记更正；多机归并（S7 验证主线）不受影响。
> - Q22：测试库 `handoff_b464` 由主会话用脚本建（凭据只经脚本 → 配置，不打印）；mbp 隔离实例只做发现/归并，不承担协调；需要用户本人完成的 OAuth 登录（R3、R6）集中到一次请求，由主会话届时发起。
> - Q13：形态验收时原型 `dispatchHint` 的「未选登录：不进派发」以 spec 为准，不回改原型。

标「**需用户**」的是我认为必须回人的；其余主会话可定。每条：**荐** + 理由（+ 被否方案）。

**数据模型**

1. **Q1 登录的存储形态。荐：新 registry kind `account_login`，一条登录一行；不改表、不升 `ledgerSchemaVersion`。**
   - 事实：registry 是通用 KV，账本不解释 body（`internal/ledger/registry.go:1-6`、`store.go:464-468`）；版本闸只管 DDL 与闸内迁移（`schema_version.go:19-21`）。
   - 行内字段分属两类写者：所在机发现（账号归属、来源、指纹、状态、到期、变更时刻）与人（代理）；两者都走「读 → 合并本写者字段 → CAS，冲突重读」（复用 `accounts.mergeReading` 同款，`accounts.go:282-314`）。发现**只在内容变化时写**（「上次发现」时刻另记 `discovery_run` 行，见 Q4），人改代理时几乎不撞版本。
   - 被否：登录数组嵌在账号行（多机发现 + 人改名/阈值同写一行，重演 B453 Q1 的 CAS 噪声）；改表（升版本、动共享 DDL，收益为零）。

2. **Q2 账号 id 与去重键。荐：账号 id 由「kind（+ provider）+ 身份指纹」确定性导出，registry 主键即去重键；名字是可改字段。**
   - id 形如 `opencode-go:3f9a1b2cc21e`、`codex:…`、`apikey:deepseek:9c41…`（12 位 = 身份短指纹 sha256 前 12 hex，与 B453 指纹同取材 `quota.Fingerprint`）。两台机器同时发现同一身份 = 对同一 id 的 `expect=0` 竞争，一方 409 后重读即归并；不需要「先扫后写」查重。
   - 身份材料按 kind（跨机必须一致，§11 冻结）：opencode-go OAuth = `账号 id/org id`（`opencodego.go:168`）；opencode-go API key = key；第三方 key = key（id 里带 provider）；codex = `tokens.account_id`；grok = `user_id`，缺则 `email`；claude、agy 见 Q6/Q7。opencode-go 两种来源身份材料不同 → 天然不互认（spec S1）。
   - 名字：全局唯一（独立命名空间，沿用 B453 Q11）；**禁止含 `:` 与 `/`**（与 id 形态区分，API 路径可同时接受 id 或名字，见 Q15）。
   - 自动起名落点：账号领域 `scheduling/accounts`（发现建账号时）。规则：有 label（邮箱）用邮箱；否则 `<kind>-<指纹前 4 hex>`；第三方 key 为 `<provider>-key-<4>`；撞名加 `-2`、`-3`…。并发两机同时建两个**不同**身份却撞同名的窗口接受（极低频，改名可修），§10 门禁族记。
   - 被否：随机 id + 查重（并发发现会建出两个同身份账号，且无测试会红）。

3. **Q3 载体对登录的引用与 HOME。荐：载体 body 新键 `login`（登录 id）与 `no_account`（true = 「无账号 · 主 HOME」）；`home_dir` 保留但只由服务端按所选登录写入（镜像）；删 `credential`、`account`、`cred_fingerprint`。**
   - 理由：`carrier.HomeDir` 有 20+ 处消费者——`IdentityOf`（`receiver.go:147-150`）、`bindingFor`（`scheduling.go:1268-1279`）、`AdmitFrozen` 物理身份比对（`:801-815`）、scheddrain/协调者 SessionSpec（`scheddrain.go:506-507`）、attach 定位（`coordinator_sessionref.go:64-66`）等。登录的 HOME 不可变（登录 = 机器 × HOME × 来源的一个槽位），镜像不会漂移；改成读时解析要给纯函数接账本，换不来任何行为差异。spec「HOME 一律取 载体 → 登录 → HOME」由「镜像只能由 PutCarrier/迁移从登录写入、请求体不再有 home_dir」保证。
   - 规则：选了登录 → 主 HOME 登录写 `""`（保持「空 = 主 HOME」——codex `CODEX_HOME` 例外 `codex/proc.go:211-235`、grok 权威副本 `grok/authsync.go:141-156`、`manager.go:1052-1060` 都依赖它）；其余写登录的 `home`（`~/.handoff/home/<dir>`）。**登录不变时 `home_dir` 串原样保留**（存量载体可能存了绝对路径，改串会让 `PutCarrier` 判 HOME 变 → pending、并让排队中的冻结绑定物理比对失败）。`no_account` → `""`。
   - 新建/编辑载体必须二选一（`login` 或 `no_account`），否则 400；两者都缺只出现在存量未迁移行（读作「未选账号登录」，照常可派发，spec S6）。

4. **Q4 「上次发现」与各机发现结果。荐：registry kind `discovery_run`，id = 机器名，body `{at, homes, logins, errors[]}`，每轮由该机写一次。** `GET /api/accounts` 带回每机一行（含 `self` 标记，顺带解决控制台读不到本机名的问题——roadmap `:1009`）。被否：每条登录写 `seen_at`（每轮全量写，顶高登录行版本）。

**承重未知的替代判据（需真机）**

5. **Q5 「已过期」判据。荐（C）证据型：登录「已过期」当且仅当 ① 经这条登录的探测或载体检测得到凭据失效（`credential_expired`/`unauthorized`/检测 `need_login`），且其后凭据文件未变；或 ② 离线可读的到期时刻已过且凭据里没有 refresh token。凭据文件变化（到期时刻后移、mtime 晚于失效标记）即回「正常」。API key 不过期（`opencode-go` key 的服务端过期只经 ① 得知）。**
   - 理由：codex（refresh_token，`codex.go:71-77`）、opencode OAuth（`account.refresh_token`）、grok、claude 都是 CLI 运行时自刷新；按「离线到期已过即过期」（A），闲置 HOME 会长期标红，提示不可行动，正是 spec「且 CLI 未自行刷新」要排除的情况。证据型下红字只在真的需要人重新登录时出现。
   - 被否：(A) 原始离线到期（原型 `LOGIN_STATE.expired.hint` 的「可能自行刷新」就是在给它打补丁）；(B) 按 kind 写死宽限期（数字无出处）。
   - **未验证，需真机**（R0-7）：各 kind 闲置 HOME 的到期时刻是否确实常年已过；若 R0 显示到期很长（如 codex 10 天、刷新随用随刷），(A) 噪声可接受，主会话可改选 (A)。

6. **Q6 claude 的身份来源与 macOS 钥匙串（**需用户**知悉分支 b）。** 荐按 R0-1/R0-2 真机结论走：
   - 身份：`<HOME>/.claude.json › oauthAccount.accountUuid`（label 取 `emailAddress`），**未验证，需真机**（R0-1）。登录判据：linux `<HOME>/.claude/.credentials.json` 存在；macOS 钥匙串条目存在（`security find-generic-password -s <服务名>` 只读属性，不取密码）。到期：linux 文件 `expiresAt`；macOS 只能经 ① 证据（不读钥匙串密文）。
   - 分支 a（钥匙串服务名随 HOME/配置目录变）：多 HOME 各自独立，S7 原样交付。
   - 分支 b（服务名固定 `Claude Code-credentials`，同一 mac 所有 HOME 共用一份）：同机第二个 HOME 里 `/login` 会**改写主 HOME 的登录**。此时荐：darwin 上 claude 登录只认主 HOME（非主 HOME 不发现为 claude 登录，新增登录对话框在 darwin 选 claude 时只允许主 HOME 并说明原因）；同 mac 多 claude 账号（经 `CLAUDE_CONFIG_DIR`）落 roadmap。S7 验证「mac-mini 与 macbook-pro 主 HOME 归并」不受影响。
   - 被否：读钥匙串密文取身份（要交互授权、密钥离开钥匙串，违反「只读位置与短指纹」）。

7. **Q7 agy 的身份来源。** 荐按 R0-3 结论三选一，优先级：
   - (a) 离线文件里有身份（如 `~/.gemini/google_accounts.json` 的 active 邮箱、或 token 文件 id_token 的 email claim）→ 用它；
   - (b) 无离线身份 → 发现时对该 HOME 跑一次 `agy -p /usage`（复用 `quota.Agy`，版本门 ≥1.1.11、进程组超时，`agy.go:50-103`）取信封身份，**按 token 文件 mtime 缓存**，只在文件变化后重跑；
   - (c) (b) 也取不到 → 每个 agy HOME 各成一个账号（身份 = `机器 + HOME`，不跨机归并），账号卡注明「agy 未提供身份，无法跨机归并」。
   - (b) 的代价：发现节拍上多一个子进程（文件不变时零次）；`/usage` 在 ≥1.1.11 上不耗额度（B453 R2 已证）。

8. **Q8 agy 任务 HOME 的 token（spec 现状读数漏列，SC1）。荐：源从「进程主 HOME」改为「载体登录 HOME」，任务 HOME 里放单文件软链（不再拷贝）；`.orig-google-oauth` 同理。**
   - 事实：`internal/executor/agy/taskenv.go:100-128` 每次任务从 `os.UserHomeDir()` 拷 token 进 `<taskDir>/agyhome`——与载体 HOME 无关；B305（`specs/b305.md:36`）当年禁止的是「软链整份 antigravity-cli 目录」，不是单文件。
   - 软链下 agy 若原地写 token = CLI 刷新它自己的登录（spec 允许）；若以换目录项方式写，任务侧变成普通文件、登录 HOME 不变，任务结束即弃（Google refresh token 不轮换，副本不会作废原件）。**未验证，需真机**（R0-9 被动观察 + R13）。若真机显示 agy 换目录项且会作废原件，退回 grok 式「发现副本即收编」（`grok/authsync.go`），升级主会话。
   - 被否：保持拷贝、只改源头（仍是复制，违反 spec 字面）。

9. **Q9 协调者供给在「登录 HOME = 主 HOME」时的写入面（SC2）。荐：整段跳过——`.handoff/config.yaml` 投影、opencode provider 配置投影、规则/skills 写入全部不做，只做展开与校验。**
   - 事实：`coordinator_home.go:87-91` 把投影后的活配置 `config.Save` 到 `<target>/.handoff/config.yaml`；target 是主 HOME 时这就是**改写 agentd 自己的活配置文件**。spec S8 只点名规则/skills，同一条「同源同目标、不自我覆盖」理由同样覆盖 config 与 provider 投影。
   - 判据「是主 HOME」：展开后 `filepath.EvalSymlinks` 比较（`/Users/x` 与经软链的同路径算同一个）。

**行为与形态细节**

10. **Q10 额度探测选哪条登录。荐：「可探登录」= 状态正常 且 所在机最近一次发现在 3 个发现周期内（`discovery_run.at`）；按登录 id 排序取第一条；只有该登录所在机的探测器探它。** 这样「那条登录失效时换下一条」（spec S5）与「所在机离线」都自然换人，且全队对「谁来探」看到同一结论。读数行记 `login`（本次用的登录）。无可探登录 → 状态「探测失败 · 没有可用登录」（读时推导，不写读数行）。

11. **Q11 claude 与第三方 key 的账号状态。荐：F6 状态词表增 `na`（不支持额度探测），`Blocks()=false`，不参与底栏与使用量弹层。** 原型 `accounts.js` STATUS 表就是 `na`。被否：另加布尔键（消费方要两处判断）。阈值与间隔对 `na` 账号缺席。

12. **Q12 第三方 key 的 kind 名。荐 `apikey` + 独立键 `provider`**（与原型一致）。执行器兼容：`opencode → {opencode-go, apikey}`，其余一一对应。范围只认 opencode `auth.json` 里 `type=api` 且条目名 ≠ `opencode-go` 的条目；`type=oauth` 的第三方条目（如 anthropic、github-copilot）不认、记 Debug（落 roadmap）。

13. **Q13 存量「未选账号登录」载体能否派发（SC3）。荐按 spec：照常可派发，载体行提示去选。** 原型 `accounts.js` `dispatchHint` 写的是「未选登录：不进派发」，与 spec S6 冲突；spec 是后批准的文字定义，浏览器形态验收时此处以 spec 为准。

14. **Q14 迁移时同机同 HOME 有多条兼容登录。荐：按载体模型前缀消歧**——`opencode-go/…` → opencode-go 登录；`<provider>/…` → 该 provider 的 apikey 登录；`opencode/…-free` 类或仍不唯一 → 不选，标「未选账号登录」。

15. **Q15 账号 API 的路径参数。荐：`{ref}` 接受账号 id 或名字**（名字不含 `:`，无歧义）；CLI `account probe <名或 id>` 直接透传。

16. **Q16 登录代理的写法。荐：专用端点 `PUT /api/logins/{id}/proxy`，服务端读-合并-CAS（不要求客户端带 `expect`）**；打码回填保留原值（沿用 `isMaskedEcho`，`delete.go:95-97`）。被否：整行 CAS（会与发现写入互顶）。

17. **Q17 删除账号的范围。荐：删账号行 + 其全部登录行 + 读数行；任一登录被载体引用 → 409 并列载体。** 下次发现会以同一 id 重建、名字回到自动名（删除对话框写明）。

18. **Q18 新增登录的 HOME 名与登录命令。荐：缺省目录名由控制台生成（`<缩写>-<4 位随机>`，缩写表 oc/cx/gk/agy/cl 与原型一致），服务端（目标机）校验字符集 `[a-z0-9-]`、只在 `~/.handoff/home/` 下 `mkdir 0700`、已存在拒绝；登录命令表只在 Go 一处（§7 段 3），随建 HOME 响应与 LoginView 的 `relogin` 下发。ssh 一行由入口机按设备表目标 `User@host` 换算（换算逻辑从 `cmd/attach.go:74 sshHostFromTarget` 下沉为共用函数）；relay 形态目标与本机不给 ssh 行。**

19. **Q19 codex 只有 `OPENAI_API_KEY` 的 HOME。荐：不认作 codex 登录（无订阅身份），记 Info；落 roadmap。** 该 HOME 上的存量 codex 载体迁移为「未选」（照常可派发）。

20. **Q20 B453 手填账号行（只存在于测试账本）。荐：新代码读到无 `fingerprint` 的旧形 `account` 行一律不展示、记一次 Warn「B453 手填登记已退役，可删」，`DELETE` 照常可删；不做写迁移。** 生产账本无账号（spec 备注）。

21. **Q21 发现器的落位（架构法第二、三条）。荐：把探测器与新发现器竖切进子包 `internal/agentd/accountwork`。** 事实：`internal/agentd` 单包源文件远超 40 且无子包（存量，不溯及既往），本卡会让 `account_` 前缀家族到 5 个文件（`account_api`/`account_carrier`/`account_prober` + 发现 + 登录 API），触发第三条必答题。探测器/发现器只依赖 `AccountsClient`、`quota`、`IsSelf`、`ExpandHome`，不依赖 `*Server`，切出零成本；HTTP 面仍留 `agentd`（要 `forwardJSON`、`pool`、鉴权）。有界文件集答：`internal/agentd/accountwork/**` + `internal/agentd/account_*.go` + `login_api.go`。被否：继续往 agentd 根目录加第 N 个前缀文件。

22. **Q22 真机环境（**需告知用户**）。荐：** 新测试库 `handoff_b464`（linux-01 PG，按全局 CLAUDE.md §4 建、登记 `credentials.md`「已分配」）；三台隔离实例 `t464-linux`（7795）/`t464-mac`（7796）/`t464-mbp`（7797），**先用 main 版二进制起、建存量形态载体，再换成分支二进制**（同时验迁移与混版本）。隔离实例的发现会**只读扫描真实主 HOME 与 `~/.handoff/home/*`**（读身份、不改任何文件）；测试中新建的 `~/.handoff/home/t464-*` 目录全部记账、验后删除。mbp 起隔离实例需告知用户（用户 10-19 说过 mbp 不做协调；本卡只用它做 claude/grok 发现与归并，不派协调任务）。S2/S5 的 OAuth 登录**需用户本人**在终端完成（§8.3 R3/R6）。

23. **Q23 撞额度重跑排除集与任务行账号快照。荐：一律改存账号 id**（`exclude_accounts`、任务库 `account` 列、`MarkQuota` 入参）；无账号载体仍按载体名计（`rerunExclusion` 现状）。混版本期间旧机存的名字与新机 id 对不上 → 最多多换回同一账号一次，可接受（§11 W3）。

---

## spec 更正（交主会话，plan 不改 spec）

| 编号 | spec 原文位置 | 事实 | 影响 |
|---|---|---|---|
| SC1 | 问题陈述「凭据被复制」与现状读数只列 probe.go、coordinator_home.go 两处 | agy 任务环境每次从进程主 HOME 拷 OAuth token（`executor/agy/taskenv.go:100-128`），是第三处复制，且无视载体 HOME | Q8；S6「载体运行 HOME = 登录 HOME」对 agy 需要它才成立 |
| SC2 | S8「规则文件与 skills…登录 HOME 就是主 HOME 时跳过写入」 | 同一函数还把投影配置写进 `<HOME>/.handoff/config.yaml`（`coordinator_home.go:87-91`），HOME 为主 HOME 时改写 agentd 活配置 | Q9 扩到整段跳过 |
| SC3 | S6「其余对不上的载体标『未选账号登录』，照常可派发」 | 原型 `accounts.js dispatchHint` 写「未选登录：不进派发」 | Q13 以 spec 为准；形态验收时注明 |
| SC4 | S7「claude…多机多登录…全有」 | macOS 上 claude 凭据在钥匙串（`toolchain/detect.go:79-83`），HOME 覆盖下是否分条目未知 | Q6 分支 b 时同一台 mac 只能有主 HOME 一条 claude 登录 |
| SC5 | S3「已过期 = 凭据到期时间已过且 CLI 未自行刷新」 | 自刷新只在 CLI 运行时发生，离线读不到「会不会刷新」 | Q5 证据型判据 |

---

## 0. 交付目标

| 故事 | 本轮做到 |
|---|---|
| S1 发现 | 各机发现器（启动一次 + 随探测节拍）扫主 HOME 与 `~/.handoff/home/*` 一层；五种 CLI + 第三方 key；同身份归并（Q2）；自动起名；扫不到 → 已失效；`discovery_run` 与「上次发现 · 立即发现」（全队扇出）；密钥不离机 |
| S2 新增登录 | 目标机建 HOME（Q18）、登录命令（真机核实后的命令表）、单 HOME 检查（轮询 5s + 立即检查）、识别结果（挂到已有账号 / 新账号）；关掉对话框由定时发现兜底 |
| S3 登录状态 | 正常/已过期（Q5）/已失效；LoginView 含机器、HOME、来源、短指纹、状态、到期或变更时刻、使用载体；已过期/已失效给「复制登录命令」 |
| S4 代理挂登录 | 登录代理端点（Q16）；任务回合、检测回合、协调者回合、额度探测都走所用登录的代理 |
| S5 账号管理与探测 | 改名（唯一）、阈值/间隔、删除（Q17）；探测按 Q10 选登录与换登录；B453 窗口/阈值/底栏/准入/撞额度规则照旧，引用改账号 id |
| S6 载体选登录 | 载体 `login`/`no_account`（Q3）；兼容 kind 过滤；共用提示；无登录时链到新增登录；「登录已失效」进统一准入；存量迁移（§5） |
| S7 claude 与第三方 key | claude 第五种 kind（Q6）、`apikey` kind（Q12）、`na` 状态（Q11）；载体可选；免费模型「无账号 · 主 HOME」 |
| S8 协调者 HOME | 不复制凭据；主 HOME 时整段跳过写入（Q9） |
| S9 CLI | `account list` 带登录；`account probe` 照旧；新增 `account discover [--machine]`；`squad list` 载体行「账号 · 机器 HOME」 |

**留待后续（落 roadmap「B464 残余」）**：spec OOS 三条；Q6 分支 b 的同 mac 多 claude 账号；Q12 `type=oauth` 第三方条目；Q19 codex API-key-only HOME；Q7(c) agy 无身份时的跨机归并。

## 1. 触及子系统与类型

| 子系统 / 领域 | 类型 | 本轮角色 |
|---|---|---|
| 编制域·账号领域 `internal/scheduling/accounts` | 逻辑型 | 账号（新 id）、登录、读数、发现结果的规则：身份归并、自动起名、改名唯一、登录状态迁移、合并写、`na`、可探登录选择、删除连带 |
| 编制域 `internal/scheduling` | 逻辑型 | 载体 `login`/`no_account`、HOME 镜像、兼容 kind 校验、统一准入加「登录已失效」、迁移规则、删凭据模式与指纹 |
| 额度适配 `internal/quota` | **边界型**（对面是各 CLI 的凭据文件/钥匙串/子进程） | 新增「本 HOME 有哪些登录」的离线读取（身份指纹 + label + 来源 + 到期 + 有无 refresh）；claude/agy 新取材；原始身份不出包 |
| agentd 网关 `internal/agentd` + 新子包 `accountwork`（Q21） | 逻辑型；对外 HTTP 面 | 发现器（与探测器同节拍、单飞）、账号/登录/发现 API 与扇出转发、新增登录两端点、`/api/squads` 投影、检测链传 `login`、派发查登录代理、迁移触发 |
| 主机能力 `internal/hostapi` | 逻辑型 | `WakeHome` 删凭据模式与复制；删 `ProbeHome`；删指纹取材注入 |
| 编排 `internal/orchestration` | 逻辑型 | 协调者供给删复制、主 HOME 整段跳过；任务行账号快照改 id |
| 执行器 agy `internal/executor/agy` | 边界型（对面是 agy CLI） | 任务 token 源改登录 HOME + 软链（Q8） |
| wire `internal/proto` + 契约 fixture | 逻辑型（契约面） | §11 全部条目 |
| 客户端与 CLI `internal/client`、`cmd/` | 逻辑型 | `account list/probe/discover`、`squad list` |
| Web `web/src` | 逻辑型 + 形态验收走浏览器熟练工 | 账号页（分组、登录行、新增登录、改名、代理、删除、立即发现）、载体对话框、底栏过滤、移动设置 |

不扇出（理由：账号/登录模型是其余各段的契约硬依赖，且各段共享同一批文件——`scheduling/accounts`、`agentd/account_*`、`web/src/app/accounts`；扇出只制造施工冲突）。派卡资格四条在此不适用；上表用于 review 架构轴核对。

## 2. 故事映射与行为闭环

### 2.1 故事 → 实现责任 → 真实消费者 → 集成/回归负责人

| 故事 | 实现责任 | 真实消费者 | 集成 / 回归 |
|---|---|---|---|
| S1 | quota 取材；accounts 归并/起名/状态；accountwork 发现器；agentd 发现 API 与扇出 | 账号页、CLI、载体对话框、迁移 | 执行者 V1–V4、V12；主会话 R1、R2 |
| S2 | agentd 建 HOME/检查 HOME；登录命令表；web 三步对话框 | 控制台用户 | 执行者 V13、W2；主会话 R3（需用户） |
| S3 | accounts 状态迁移（Q5）；投影 | 账号页、载体行、准入 | 执行者 V5、V6；主会话 R4 |
| S4 | 登录代理端点；派发/检测/协调者/探测四处读登录代理 | 执行器子进程、探测出网 | 执行者 V9；主会话 R5 |
| S5 | accounts 改名/删除；探测选登录（Q10） | 账号页、底栏、CLI、准入 | 执行者 V7、V8；主会话 R6 |
| S6 | scheduling 引用/镜像/准入/迁移；投影；web 载体对话框 | 派发入口、`squad list`、控制台 | 执行者 V10、V11、V14、W3；主会话 R1、R7 |
| S7 | quota claude/apikey；`na` | 账号页、载体对话框 | 执行者 V2、V3；主会话 R8 |
| S8 | orchestration 供给 | 协调者回合 | 执行者 V15；主会话 R9 |
| S9 | cmd | 主会话/协调者终端、skill | 执行者 V16；主会话 R10 |

### 2.2 行为闭环（触发者 → 权威事实/载体 → 消费者 → 可观察结果 → 实现责任）

| 触发者 | 权威事实 / 载体 | 消费者 | 可观察结果 | 实现责任 |
|---|---|---|---|---|
| 各机发现节拍（启动一次 + 周期） | 本机扫 HOME → quota 离线取材 → `account/<id>`（缺则建、自动名）、`account_login/<id>`（合并写）、`discovery_run/<机器>` | 各机 `GET /api/accounts`、CLI | 两台机器同身份的登录出现在同一账号下；`account list` 与账号页一致 | quota + accountwork + accounts |
| 人点「立即发现」 | 入口机并发 `POST /api/accounts/discover?machine=<各机>`（一跳），各机本机发现写账本 | 控制台 | 「上次发现 刚刚」；离线机那一行给可行动失败（转发失败/版本过旧），其余照常 | agentd API + web |
| 人在新增登录第 1 步确认 | 入口机转发 `POST /api/logins/home?machine=X`，X 建 `~/.handoff/home/<dir>` | 对话框 | 显示该机登录命令（远端附 ssh 行）；目录已存在 → 409 文案 | agentd + web |
| 人登录后对话框每 5 秒检查 | `POST /api/logins/check?machine=X`：X 对该 HOME 跑单 HOME 发现并写账本 | 对话框 | 「已识别 <label> → 已挂到账号 <名>（已有 n 条登录）」或「→ 新账号 <名>」 | agentd + accounts + web |
| 人关掉对话框后才登录 | 下一轮定时发现 | 账号页 | 新登录照样出现 | accountwork |
| 某 HOME 的凭据被删 | 下次发现：该登录 `state=gone` | 统一准入、载体行、账号页 | 引用它的载体被跳过，文案「载体 X 的登录已失效：<机器> <HOME>」；账号页给「复制登录命令」 | accounts + scheduling + web |
| 探测/检测经某登录得到凭据失效 | 登录行写失效证据（Q5 ①） | 账号页、载体行 | 登录「已过期」红字，派发不拦；凭据文件更新后下轮发现回「正常」 | accountwork + scheduling detect |
| 人给登录填代理 | `PUT /api/logins/{id}/proxy` 合并写 | 派发落点、检测目标机、协调者 runner、探测器 | 任务环境六个代理键 = 登录代理、`NO_PROXY` 含 localhost；检测回合与探测同走它 | agentd + orchestration（B453 env 覆盖原样复用） |
| 探测到期 | 可探登录（Q10）所在机探测 → `account_reading/<id>`（含 `login`） | 底栏、准入、CLI | 读数照 B453；改名后读数/载体/底栏同步新名（引用是 id） | accountwork + accounts |
| 可探登录失效或其机器离线 | 全队按 Q10 重算出下一条 | 下一条所在机探测器 | 下一轮探测在另一台机器成功，读数行 `login` 变 | accounts + accountwork |
| 人在载体对话框选登录保存 | `carrier/<名>` body `login`（CAS），服务端写 `home_dir` 镜像 | 派发准入、任务启动、检测、协调者 | 任务/检测/协调者都在登录 HOME 跑，不写任何凭据文件；同登录多载体显示「与 X 共用」 | scheduling + agentd 投影 + web |
| 升级后载体所在机首轮发现结束 | 迁移：只填空选择（§5） | `/api/squads`、准入 | 主 HOME 存量载体对上主 HOME 登录或「无账号 · 主 HOME」；其余「未选账号登录」照常派发 | scheduling + accountwork |
| 协调者载体拉起 | 供给在登录 HOME：不复制；登录 HOME = 主 HOME 时整段跳过 | keystone | 日志无凭据复制；凭据与主 HOME 规则/配置文件 mtime 不变 | orchestration |
| agy 任务启动 | 任务 HOME 的 token = 指向登录 HOME token 的软链 | agy 子进程 | 任务以所选登录身份运行；登录 HOME 内 token 不被复制 | executor/agy |
| 人删账号 | 被引用 → 409 列载体；否则删账号/登录/读数行 | 账号页 | 删除后下轮发现以同一 id、自动名重新出现（对话框已写明） | accounts + web |

## 3. 数据模型（回答「存储、id、引用迁移、去重键、起名、schema 版本」）

| registry kind | id | 写者 | body 要点（字段形状见 §11） |
|---|---|---|---|
| `account` | Q2 确定性 id | 发现建（`expect=0`）；人改 `name`/阈值/间隔（CAS） | kind、provider?、name、label?、fingerprint、interval/warn/low（`na` kind 缺席）、created_by、created_at |
| `account_login` | 登录槽位的确定性 id（机器 × HOME × kind × provider × 来源），URL 安全 `[a-z0-9-]`，重启后不变 | 所在机发现（归属/来源/指纹/状态/到期/变更时刻）；探测器与检测（失效证据）；人（代理）——全部合并写 | account、machine、home（`~` 或 `~/.handoff/home/<dir>`）、home_abs、kind、provider?、source、fingerprint、state、expires_at?、changed_at、auth_failed_at?、proxy? |
| `account_reading` | 账号 id | 探测器（读-合并-CAS，B453 同款） | B453 形状 + `login` |
| `discovery_run` | 机器名 | 该机发现器 | at、homes、logins、errors[] |
| `carrier`（已有） | 载体名 | 人（CAS）；迁移（所在机，只填空选择） | 增 `login`、`no_account`；`home_dir` 改为服务端镜像；删 `credential`、`account`、`cred_fingerprint` |

- 去重键 = 账号 id（Q2）；登录槽位换了身份（同 HOME 重新登录另一个账号）= 同一登录行 `account` 字段改变（记 Info「登录改挂账号」），载体引用的是槽位，所以载体跟着 HOME 里真实在用的身份走。
- `ledgerSchemaVersion` **不升**：只新增 registry kind 与 body 键，无 DDL、无闸内迁移。
- 账号/登录/读数之间的关系、归并、起名、状态迁移、可探登录选择全在 `scheduling/accounts` 收口（spec 目标架构），agentd 只调导出符号；原始身份材料只在 `quota` 内存里，出包只有指纹与 label。

## 4. 删除清单（逐项：生产调用方 → 替代 → 行为闭环）

| 删除对象 | 生产调用方（基线行号） | 替代 | 行为闭环（删后谁保证什么） |
|---|---|---|---|
| 手填账号登记（创建路径） | `PUT /api/accounts/{name}?expect=0`（`account_api.go:91-129`）；web `AccountDialog` 登记态（`AccountsPage.tsx:66-133`、「＋ 登记账号」`:327`） | 账号只由发现建；PUT 只改名/阈值/间隔，`expect=0` → 400「账号由发现产生」 | 发现节拍 → 账号行出现 → 账号页可见（R1） |
| 凭据条目字段 `entry` 及 `DefaultEntry`、`sameCredential` 查重 | `types.go:41-50`、`accounts.go:60-122`、`accountFromInput`（`account_api.go:162-185`）、web 条目输入 `:129-133` | 来源由登录行 `source` 表达，探测按来源映射到 quota 条目 | 探测器按登录 `source` 取凭据（V8） |
| 账号上的 `machine`/`home_dir`/`proxy` | `types.go:53-63`；探测器 `IsSelf(v.Account.Machine)`、`ExpandHome(a.HomeDir)`、`a.Proxy`（`account_prober.go:149,244,303,296`）；`hostWakeProxy`/`dispatchAccount`/`coordinatorAccountProxy`（`account_carrier.go:76-160`） | 移到登录行；四处改读「所用登录」 | 探测/检测/任务/协调者代理都取登录代理（V9、R5） |
| 载体 HOME 输入与凭据模式 | `CarrierInput.home_dir/credential`（`proto/scheduling.go:71-81`）；`handleCarrierPut`（`schedapi.go:128-132`）；`PutCarrier` 凭据词表（`scheduling.go:241-247`）；`CredentialSource`（`:46-52`）；web 凭据选择 `SchedulingPage.tsx:37,55` 与 HOME 输入 `:489`、HOME 实时探测 `:247-265` | 载体选 `login`/`no_account`；`home_dir` 服务端镜像（Q3） | 载体对话框只读显示 HOME/来源/代理（W3、R7） |
| main_home_sync 凭据复制 ①：检测 | `WakeHome` 空目标 + main_home_sync → `copyMainCredential`（`probe.go:136-142,319-357`）；`applyMainHomeSync`、`mainHomeHasCredential`（`:252-315`）；`WakeRequest/ProbeRequest.Credential`；`HomeWakeReq.credential` | 检测在登录 HOME 原样跑；登录 HOME 必须已存在（不存在 = 登录已失效，检测给可行动文案，不 mkdir 造空 HOME）；「无账号 · 主 HOME」沿用主 HOME | 检测前后凭据文件 mtime 不变（V15、R7） |
| main_home_sync 凭据复制 ②：协调者 | `copyMissingCoordinatorCredential`（`coordinator_home.go:94,342-389`）；白名单里的三条凭据路径（`:194-226` 中 `auth.json` 项）；`ProfileReq.Credential`/`executor.CredentialPolicy`（`profile.go:14-20,35`，零读取） | 不复制；Q9 主 HOME 整段跳过 | 协调者回合凭据 mtime 不变、主 HOME 规则/配置不改（V15、R9） |
| 复制 ③：agy 任务 token | `agy/taskenv.go:100-128` | Q8 登录 HOME + 软链 | 任务以所选登录运行（R13） |
| hostapi 凭据路径解析器 | `NewWithCredentialPathFor`（`hostapi.go:63-67`，组装 `cmd/agentd.go:795`）只服务 ProbeHome/WakeHome 判态与复制 | 删；`toolchain.credRelPath` 表保留（`toolchain.Detect` 与 quota 仍消费） | — |
| `/api/host/probe` 与 `ProbeHome` | 唯一消费者 = web 载体对话框 HOME 实时提示（`scheduling.ts:174-178`、`SchedulingPage.tsx:247`） | 删端点、`HomeProbeReq/Resp`、web `probeHome`；HOME 检查改由 `POST /api/logins/check` 承担（语义不同：识别登录） | 新增登录第 2 步（V13） |
| 载体指纹比对 | `Carrier.CredFingerprint`（`scheduling.go:71,254,282-287`）；`DetectEvidence.Fingerprint`、`ApplyDetect` 写回（`status.go:93-103,162`）；`accountFingerprints`/`accountMismatch`（`account_carrier.go:46-72`）；`CarrierView.account_mismatch`；`hostapi.credentialFingerprint`/`SetCredentialFingerprint`（`probe.go:164-176`、`hostapi.go:50-53`、组装 `cmd/agentd.go:799`）；`HomeWakeReq.fingerprint_kind/entry`、`HomeWakeResp.fingerprint`；web `MISMATCH_TEXT`（`SchedulingPage.tsx:61-84`）；CLI「凭据指纹不符」（`cmd/squad.go:232-241`） | 无（载体就跑在登录 HOME，不再可能绑错）。`quota.Identifier` 保留给发现取材 | — |
| 载体 `account`（账号名引用） | `checkCarrierAccount`（`scheduling.go:322-346`）、`CarriersUsingAccount`（`admission.go:141-155`）、`usability`（`:86-112`）、`markDetectQuota`（`status.go:183-`） | 改为 载体 → 登录 → 账号 id | 准入/拒删/撞额度标记同语义（V10、V11） |

## 5. 存量载体迁移（spec S6）与升级时机

- **执行者**：载体所在机的 agentd，在**本机一轮发现成功写回之后**，对本机载体执行；幂等；只处理 `login` 与 `no_account` 都缺席的行；CAS 冲突（人恰在编辑）→ 本轮放弃、下轮再试；从不覆盖人的选择。只有所在机能展开 `~`、只有它刚刚完整扫过本机 HOME，所以放在这里（被否：读时推导，见 Q3/台账）。
- **规则**（按序，命中即停）：
  1. 载体 HOME 在所在机展开（`""` → 主 HOME 绝对路径）后，与本机 `state≠gone` 的登录 `home_abs` 相同、且 kind 与载体 CLI 兼容的登录恰一条 → 写 `login`（`home_dir` 串不动，Q3）。
  2. 多条兼容 → Q14 按模型前缀消歧；仍不唯一 → 不写。
  3. 零条且载体 HOME 是主 HOME → 写 `no_account=true`。
  4. 其余 → 不写；读面标「未选账号登录」，照常可派发，载体行提示去选。
- 今天的 `main_home_sync` 载体：其 HOME 里的副本凭据被发现为同账号的一条独立登录，按规则 1 对上；此后再无任何代码覆盖它。
- 载体 body 里遗留的 `credential`/`account`/`cred_fingerprint` 键：新代码解码时忽略；下次任何写入自然去掉（不做专门写迁移）。
- **升级时机**：三台机器**同批升级**（finish 节点）。旧版 agentd 解码再编码载体 body 会丢 `login`/`no_account`（与 B453 F19 同类跨版本风险），所以混版本期间不得在控制台给载体选登录；迁移在每台机器升级后自己的首轮发现结束时完成，未升级或离线机器的载体保持「未选」（今天的行为）。finish 冒烟按机器核对生产 7 个载体的迁移结果表。

## 6. 改动边界

**可改（有界文件集）**：
- 新增：`internal/agentd/accountwork/**`（Q21：探测器迁入 + 发现器 + 迁移触发）、`internal/agentd/login_api.go`、`internal/scheduling/accounts/{login,discover,naming,…}.go`、`internal/quota/{claude,logins,…}.go`、`web/src/app/accounts/` 新组件（新增登录对话框、登录行、改名/代理对话框）、对应测试与 `internal/quota/testdata/`（脱敏样本）。
- 修改：`internal/scheduling/accounts/{types,accounts,delete}.go`；`internal/scheduling/{scheduling,admission,status,registry_read}.go`；`internal/agentd/{account_api,account_carrier,schedapi,hostprobe,server,dispatchroute,quota_rerun}.go`（`account_prober.go` 迁入子包）；`internal/hostapi/{probe,hostapi}.go`；`internal/orchestration/{coordinator_home,manager}.go`；`internal/executor/agy/taskenv.go`；`internal/executor/profile.go`（删 `Credential`）；`internal/proto/scheduling.go` + `contract_fixture_test.go` + `web/src/api/testdata/*.json`；`internal/client/*`（账号方法）；`cmd/{account,squad,attach,agentd}.go`；`web/src/api/{accounts,scheduling}.ts`、`web/src/app/accounts/**`、`web/src/app/settings/SchedulingPage.tsx`；文档（README 中英、CHANGELOG、roadmap、`~/.claude/skills/handoff/SKILL.md` 账号段）。
- **须遵守**：组装点唯一（发现器、quota 注册表、迁移触发只在 `cmd/agentd.go` 组装）；Manager 不 import 编制域（`receiver_bind.go:4`），登录代理照 B453 经 `DispatchReq` 快照进任务行；`AccountsClient`/`SchedulingClient` 新增方法逐个列消费点（「多一个即越界」）；凭据值与原始身份只在 quota 内存里——任何 error、日志、事件、API、账本 body 只出现位置、来源名与短指纹；周期任务 ticker + ctx，`Stop()` 等 goroutine 退出。
- **不许动**：`ledger` 表结构与 `ledgerSchemaVersion`；任何凭据文件（只读，绝不刷新、改写、复制）；在役 agentd 与生产账本（真机只用隔离实例 + `handoff_b464`）；B453/B460 其他冻结条目（§11 未列的）。

## 7. 工作顺序

一个工作单元、单分支、**按段串行**，每段末提交。段 1 = spec 首故事。

段间依赖：段 1 → 段 2/3/4 是**契约/数据硬依赖**（账号/登录模型、载体引用、发现器是一切前提）；段 0 → 段 2 的 claude/agy 取材与 Q5 判据、→ 段 3 的登录命令表是**事实硬依赖**；段 4 与段 2/3 是**施工排序**（同批文件，单人串行，可局部对调）；段 5 硬依赖 §11 冻结形状（不依赖段 4）；段 6 依赖全部。

**段 0 真机事实（主会话执行，不派发；与段 1 并行）**——R0-1…R0-9（§8.3）。产出：Q5/Q6/Q7/Q8 的分支结论、五种 CLI + opencode key 的登录命令表、各 kind 到期与 refresh 字段的真实位置。结论回写本稿「待拍板」对应条与台账。**段 2 的 claude/agy 取材、段 3 的命令表在 R0 结论出来前不得按推测写进夹具。**

**段 1 首故事：mac-mini 主 HOME 的 opencode-go 登录被发现 → 归为账号 → 新建 opencode 载体选它 → 派任务在主 HOME 跑通**（承重未知：发现的扫描与去重落位、HOME 供给三处、存量载体自动对上）
- 做：accounts 新模型（Q1–Q4：确定性 id、`account_login`、`discovery_run`、归并、自动起名、`ok/gone` 两态）；quota 的 opencode 离线取材（`auth.json › opencode-go` API key 与 `opencode.db` OAuth 两来源，原始身份不出包）；`accountwork` 发现器（启动一次 + 随节拍；扫描范围只有主 HOME 与 `~/.handoff/home/*` 一层）与探测器迁入、探测改走「可探登录」（Q10 最小形：第一条正常登录）；`GET /api/accounts` 带 `logins` 与 `discovery`；载体 `login`/`no_account` + HOME 镜像 + 兼容 kind 校验，删 `credential`；统一准入改读 载体 → 登录 → 账号；**HOME 供给三处去复制**（检测 `WakeHome` 删 main_home_sync 分支；协调者删 `copyMissingCoordinatorCredential`；任务启动经镜像不变——加断言锁住）；§5 迁移；`account list` 打印登录。因段 1 改载体模型而失去编译基础的指纹比对代码可在本段一并删。
- 红→绿入口：`go test ./internal/scheduling/accounts/ -run 'Reconcile|Dedupe|AutoName'`（两机同身份并发 `expect=0` 归并为一个账号两条登录；撞名加后缀）；`go test ./internal/quota/ -run 'Logins.*Opencode'`（夹具 HOME：auth.json 只有 key / 只有 opencode.db / 两者都有 → 两条登录、身份不互认）；`go test ./internal/scheduling/ -run 'CarrierLogin|Mirror|Migrate'`；`go test ./internal/agentd/... -run 'Discovery.*Vertical'`（**生产组装**：夹具 HOME + 测试账本 → 发现 → 载体选登录 → 派发 → 任务行 HomeDir 与 env `HOME`；断言三处供给零凭据写入）。
- 可检查结果：R1（主会话）。**R1 不过不进段 2。**

**段 2 其余 kind、登录状态、代理、探测换登录、账号管理（S1 完整、S3/S4/S5/S7 后端）**
- 做：codex、grok、`apikey`（Q12）、claude（Q6 分支）、agy（Q7 分支）取材；label/起名；登录状态 `expired`（Q5）与证据写入（探测失败、检测 `need_login`）；`na`（Q11）；登录代理端点（Q16）及四处消费（派发快照、检测 `HomeWakeReq.login`、协调者 runner、探测器）；Q10 完整（发现新鲜度、换登录）；统一准入加「登录已失效」；改名（唯一、禁 `:`/`/`）、阈值/间隔、删除（Q17）；撞额度标记与重跑排除集改 id（Q23）；Q20 旧行处理。
- 红→绿入口：`go test ./internal/quota/... -run 'Logins'`（五 kind + apikey 夹具表，样本只来自 R0 真机读数的脱敏形状）；`go test ./internal/scheduling/accounts/ -run 'LoginState|ProbeLogin|Rename|Delete'`；`go test ./internal/scheduling/ -run 'Admit.*Login'`；`go test ./internal/agentd/... -run 'LoginProxy'`；泄密表（V4）。
- 可检查结果：R2、R4、R5、R6、R8。

**段 3 新增登录、立即发现、CLI（S2、S1 立即发现、S9）**
- 做：`POST /api/logins/home`、`POST /api/logins/check`（`?machine=` 一跳转发，`forwardIfRequested`）；`POST /api/accounts/discover` 扇出；登录命令表（段 0 结论）与 ssh 行（Q18）；LoginView `relogin`；`handoff account discover [--machine]`；`squad list` 账号格「账号名 · 机器 HOME」。
- 红→绿入口：`go test ./internal/agentd/... -run 'LoginHome|LoginCheck|DiscoverFanout'`（他机转发、目录已存在 409、非法目录名 400、离线机器在扇出结果里单独报错而整体 200）；`go test ./cmd/ -run 'Account(Discover|List)|SquadList'`。
- 可检查结果：R3（需用户）、R10。

**段 4 收口删除与 S8（删除清单剩余项）**
- 做：Q9 协调者主 HOME 整段跳过；删 `/api/host/probe`/`ProbeHome`/凭据路径解析器/`ProfileReq.Credential`；剩余指纹比对与凭据模式残留；Q8 agy token 软链；契约 fixture 刷新。
- 红→绿入口：`go test ./internal/orchestration/ -run 'CoordinatorHome'`（目标 = 主 HOME → 零写入；目标 = 登录 HOME → 无凭据文件被创建/改写，规则照写）；`go test ./internal/executor/agy/ -run 'TaskEnv'`（任务 HOME token 是指向登录 HOME 的软链；登录 HOME 无 token → 可行动错误）；`go test ./internal/hostapi/ -run Wake`；`go test ./internal/proto/ -run TestContractFixtures`（先红再 `-update`）。
- 可检查结果：R9、R13。

**段 5 前端（S1–S7 形态）**
- 做：`api/accounts.ts`/`scheduling.ts` 类型镜像 fixture；账号页按六组（OpenCode Go / Codex / Grok / Antigravity / Claude / 第三方 API key）、账号卡登录行（状态、来源、指纹、到期/变更、使用载体、编辑代理、复制登录命令）、「＋ 新增登录」三步对话框（5s 轮询 + 立即检查 + 关闭兜底说明）、「在其他机器登录」预选、改名/阈值对话框、删除对话框（写明会重新出现）、顶部「上次发现 x 前 · 立即发现」；底栏与使用量弹层只列支持探测的 kind；载体对话框 机器 → 执行器 → 账号登录（「账号 · HOME」逐条、末尾「无账号 · 主 HOME」、空态链到账号页预选机器与 agent）、只读 HOME/来源/代理/共用；删 HOME 输入、凭据模式、指纹红字；移动设置节同步。
- 红→绿入口：`cd web && npm test -- accounts SchedulingPage contract && npm run typecheck && npm run lint`。
- 可检查结果：R11（浏览器熟练工对照原型）。

**段 6 收口**：文档（README 中英命令表与 `--help` 一致、CHANGELOG、roadmap「B453 残余」`:1000` 改写为永不做、`:1022` 作废、新增「B464 残余」、handoff skill 账号段）；全量测试（implement 三段律）；R12 密钥审计；finish 节点 R14。

## 8. 验收

### 8.1 机内断言（执行者）

- **V1 归并（承重）**：两个「机器」并发对同一身份各跑一轮 reconcile（内存 registry，N=50 轮交错）→ 恰一个账号、两条登录；不同身份同 label → 两个账号、名字后缀；同 HOME 两来源（opencode key + OAuth）→ 两个账号。
- **V2 取材**：每 kind 一张夹具表（存在/缺失/形状不对/到期字段有无/refresh 有无），断言产出的来源名、指纹、label、到期；claude/agy 表只能用 R0 读到的**键形状**造（值为哨兵）。
- **V3 扫描范围**：主 HOME + `~/.handoff/home/*` 一层；更深层、其他路径、软链出界的目录不扫（夹具含 `~/.handoff/home/a/b`、指向 `/etc` 的软链）。
- **V4 密钥不离机（承重安全属性）**：B453 泄密表扩到发现路径——含哨兵串的假凭据走成功/缺失/坏形状/超时路径，断言 error、日志捕获、登录行/账号行 body、API 响应、`discovery_run` 中零出现哨兵与原始身份材料；account id 与指纹不是凭据任何子串。
- **V5 登录状态迁移**：表驱动 ok→gone（扫不到）、gone→ok（再现，含换身份改挂账号）、ok→expired（Q5 两种证据）、expired→ok（文件变化）；gone 不删行。
- **V6 并发写不丢**：发现器合并写 × 人改代理 × 探测器写失效证据并发 N=50，三类字段各自保留最后一次写。
- **V7 账号管理**：改名唯一与字符规则；改名后载体/读数/准入不断（引用 id）；删除被引用 409 列载体；删除连带删登录与读数行；`expect=0` 的 PUT 400。
- **V8 探测登录选择**：可探登录 = 正常 + 机器发现新鲜，按 id 序；首选失效/机器过期 → 换下一条；无可探 → 读面「探测失败 · 没有可用登录」；探测请求按登录 `source` 映射 quota 条目；探测用登录代理。
- **V9 代理四处**：派发快照（任务 env 六键 + `NO_PROXY` 补 localhost，B453 V9 回归锁原样保留）、检测（目标机按 `login` 查本机登录代理，他机登录不套用）、协调者回合、探测；未设代理时逐字节同改动前。
- **V10 载体引用**：选他机登录 / 不兼容 kind / 不存在登录 → 400；二选一缺失 → 400；HOME 镜像：主 HOME 登录 → `""`，其他 → 登录 home；登录不变时 `home_dir` 原串保留；PUT 请求体带 `home_dir`/`credential` 被忽略（或 400，执行者定并在交回物说明）。
- **V11 统一准入**：五个入口 + `CheckUsable` 各一条「登录已失效被跳过」与一条「已过期放行」；文案「载体 X 的登录已失效：<机器> <HOME>」；`errors.Is(err, ErrNoHealthy)` 仍真；B453 结构断言（准入只经 `usability`）保留。
- **V12 迁移**：§5 四条规则各一例 + Q14 模型前缀消歧 + 已有选择不动 + CAS 冲突本轮放弃。
- **V13 新增登录端点**：建 HOME（字符集、已存在 409、只在 `~/.handoff/home/`）、检查（未登录 → 空数组；登录后 → 新账号/已有账号两种结果）、转发一跳（转发到达非目标机 409）。
- **V14 投影穿链（序列化边界）**：登录字段从 registry body → accounts View → `proto.AccountView/LoginView` → JSON → TS fixture 一条链；区分「代理未设（键缺席）」与「代理为空串」、`expires_at` 缺席与零值、`no_account` 只在 true 时出现。
- **V15 凭据不再复制（承重）**：检测、协调者供给、任务启动三处跑完后，夹具 HOME 内凭据文件集合与 mtime 不变；协调者目标 = 主 HOME 时 `.handoff/config.yaml`、`opencode.jsonc`、规则、skills 零写入（Q9）。
- **V16 CLI**：`account list --json` 每行可解进 `proto.AccountView` 且字段全等；`account discover` 退出码（全成功 0、任一机失败 1）；`squad list` 账号格。
- **W1–W4 前端**：W1 账号页分组与 `na` 账号无窗口条、不进底栏；W2 新增登录三步（轮询每 5s、立即检查、关闭后不再轮询、两种识别结果文案）；W3 载体对话框只列所选机器 × 执行器兼容登录、「账号 · HOME」、末尾「无账号 · 主 HOME」、空态链接带预选参数、共用提示；W4 已过期红字不拦、已失效暂停文案。

### 8.2 运行入口

- 后端：`go test ./internal/quota/... ./internal/scheduling/... ./internal/agentd/... ./internal/hostapi/... ./internal/orchestration/... ./internal/executor/... ./internal/proto/... ./internal/client/... ./cmd/...`；全量按 implement 三段律（memory：两支 Go 测试在 `CLAUDE_CODE_SESSION_ID`/嵌套 worktree 环境下会红，与本卡无关）。
- 契约：`go test ./internal/proto/ -run TestContractFixtures [-update]`；`cd web && npm test -- contract`。
- 前端：`cd web && npm test && npm run typecheck && npm run lint`。

### 8.3 真机清单（**本节由主会话执行，不派发**；隔离 agentd + 测试账本，在役 7777 全程不动）

**前置 P0**：分支与 main 各构建三份二进制（darwin arm64 ×2 机、linux amd64），草稿路径，记 sha256；建 `handoff_b464`；三台隔离实例（Q22），设备表互登，所有 CLI 带 `--config <草稿配置>`。开始与结束各记一次：三机在役 agentd pid/启动时刻；`~/.handoff/config.yaml` 与主 HOME 规则文件（`~/.config/opencode/AGENTS.md` 等）的 sha256 + mtime；主 HOME 各凭据文件的 mtime（只 `stat`，不读内容）。**凭据值、钥匙串密文一律不打印**；下列命令只输出键名、存在性或时间戳。

**R0 事实核查（段 0，与段 1 并行；用机器上现有 CLI，不依赖分支）**
- **R0-1 claude 身份**（mac-mini、macbook-pro；linux-01 若装有 claude）：`claude --version`；`python3 -c 'import json,os;d=json.load(open(os.path.expanduser("~/.claude.json")));o=d.get("oauthAccount");print("oauthAccount" in d, sorted(o.keys()) if isinstance(o,dict) else None)'`；linux：`python3` 打印 `~/.claude/.credentials.json` 顶层与 `claudeAiOauth` 的键名；mac：`security find-generic-password -s "Claude Code-credentials" 2>&1 | /usr/bin/grep -E '"(svce|acct)"|keychain:'`（不带 `-w`），以及 `security dump-keychain 2>/dev/null | /usr/bin/grep -o '"svce"<blob>="Claude Code[^"]*"' | sort -u`（列服务名变体）。判据：`oauthAccount` 含 `accountUuid`/`emailAddress` → Q6 身份可离线取。
- **R0-2 claude HOME 隔离（mac）**：`T=$(mktemp -d); HOME=$T claude auth status 2>&1 | head -5`（无此子命令则 `HOME=$T claude -p ping 2>&1 | head -3`）；随后 `ls -a $T` 与 `$T/.claude.json` 里有无 `oauthAccount`；`rm -rf $T`。判据：空 HOME 下已登录 → 钥匙串跨 HOME 共用 → Q6 分支 b。
- **R0-3 agy 身份**（linux-01；mbp 若有）：`agy --version`；`ls -la ~/.gemini ~/.gemini/antigravity-cli`；`python3` 打印 token 文件（若为 JSON）键名、`~/.gemini/google_accounts.json` 键名及 `active` 是否非空；token 若含 `id_token`，只打印 JWT payload 的 claim 键名。判据 → Q7 (a)/(b)/(c)。
- **R0-4 opencode**（三台）：`opencode --version`；`opencode --help`、`opencode auth --help`、`opencode auth login --help`（找 console 账号 OAuth 登录命令与 API key 登录命令）；`sqlite3 -readonly ~/.local/share/opencode/opencode.db '.schema account' '.schema account_state'`（只看 schema：有无 email 列）；`python3` 打印 `auth.json` 每条目的 `type`（不打印 key）。
- **R0-5 codex**（linux-01、mac-mini）：`codex --version`；`codex login --help`（无浏览器机器的登录方式，如 device auth）；`python3` 打印 `~/.codex/auth.json` 顶层键与 `tokens` 键名、`id_token` payload claim 键名（有无 `email`）、`access_token` 的 `exp`（只打印时间）与 `last_refresh`。
- **R0-6 grok**（macbook-pro）：`grok --version`；`grok --help` 里的登录子命令；`~/.grok/auth.json` 条目键名与 `expires_at`（时间）。
- **R0-7 到期噪声**：汇总 R0-1/4/5/6 各 kind 闲置 HOME 的到期时刻相对当前（已过多久/还剩多久）与 refresh token 有无 → Q5 (A)/(C) 依据。
- **R0-8 登录命令表**：按 R0-4/5/6/1/3 的 `--help` 定 opencode（OAuth 与 API key 两条）、codex、grok、agy、claude 的首次登录命令与无浏览器机器上的做法；写回台账，段 3 照抄。
- **R0-9 agy token 写法（被动）**：`stat -c '%i %Y' ~/.gemini/antigravity-cli/antigravity-oauth-token`（linux）记一次，下一次在役 agy 任务自然跑完后再记一次：inode 变 = 换目录项写入，mtime 变 inode 不变 = 原地写 → Q8 依据。

**R1 首故事**（段 1 后）：隔离实例先以 main 二进制起，在测试账本建三个存量形态载体（mac-mini opencode、HOME 空、standalone；linux-01 某 `~/.handoff/home/<x>`、main_home_sync；一个对不上任何登录的 HOME）；换分支二进制重启隔离实例。判据：① `t464-mac` 首轮发现后 `account list --json` 有 opencode-go 账号，登录（`mac-mini`/`~`/`opencode.db`）；② 三个存量载体按 §5 分别得到 `login` / 规则 1 / 「未选账号登录」（squads 读面）；③ API 新建载体 `t464-oc`（opencode、选该登录、模型 `opencode-go/…`）→ `home_dir` 为 `""`；④ 派只读任务（让执行者把 `echo $HOME` 写进产物）→ completed，HOME = 主 HOME；⑤ P0 记录的凭据 mtime 前后一致。
- **R2 跨机归并**（段 2 后）：在两台机器上同身份的登录（候选：claude mac-mini + mbp 主 HOME；opencode 视 R0 而定）归为一个账号两条登录；`account list` 在三台隔离实例上一致。若无现成同身份，并入 R3 由用户在第二台机器登录同一账号。
- **R3 新增登录（需用户本人登录）**：控制台在 linux-01 新增 codex 登录走完三步（建 `~/.handoff/home/t464-cx-xxxx` → 用户按命令登录 → 对话框识别并挂上）；再建一个 HOME 后关掉对话框、用户登录，等定时发现挂上。
- **R4 已失效**：在 `t464-*` 测试 HOME 放一份**哨兵内容**的第三方 key `auth.json`（provider `t464prov`，不是任何真实凭据）→ 发现为 `apikey` 账号；建 opencode 载体选它；删掉该文件 → 下轮发现登录「已失效」、载体行标红、点名派发被拒文案「载体 … 的登录已失效：…」。
- **R5 代理**：给 linux-01 一条登录填本机代理 `http://127.0.0.1:1080`（machines.md 的 trojan）→ 派只读任务写 `env | grep -i proxy` → 读到该代理、`NO_PROXY` 含 localhost、任务正常完成；检测与探测日志显示经该代理（打码）。
- **R6 账号管理与换登录**：改名 → 载体、底栏、`account list` 同步；有两条登录的账号（来自 R2/R3）：停掉正在探测那台的隔离实例 → 3 个发现周期后另一台探测成功、读数行 `login` 变；用户删掉 R3 测试 HOME 的登录（用户同意后）→ 同样换登录。
- **R7 载体**：mac-mini 主 HOME 登录上建两个不同模型的 opencode 载体 → 两者都显示共用、都派发成功；检测前后凭据 mtime 不变。
- **R8 claude 与第三方 key**：claude 账号（R2）建 claude 载体（mac-mini）派任务成功；R4 的 apikey 账号可被 opencode 载体选；免费模型载体选「无账号 · 主 HOME」派任务成功；claude/apikey 账号显示「不支持额度探测」、不进底栏。
- **R9 协调者（S8）**：(a) 隔离实例协调者载体选一条 `~/.handoff/home/t464-*` 登录，拉起一轮 → 日志无凭据复制，该 HOME 凭据 mtime 不变、规则照写；(b) 另起一个**进程 HOME 为沙盒目录**的隔离实例（避免触碰真实主 HOME），协调者载体选「无账号 · 主 HOME」+ 免费模型，拉起一轮 → 沙盒内 `.handoff/config.yaml`、规则、skills 的 sha256/mtime 不变。
- **R10 CLI**：`handoff account list/probe/discover --help` 与 README 命令表一致（finish-check ④）；`squad list` 载体行「账号 · 机器 HOME」。
- **R11 前端**：交 `browser-worker`，URL 为隔离实例控制台，对照 `prototypes/b464-accounts/pages/accounts.html`（分组、登录行、新增登录三步、在其他机器登录、立即发现）、`pages/settings.html?section=automation`（载体弹窗）、`pages/mobile-settings.html`（375 宽）；判据 = spec S1–S7 + Q13（未选账号照常派发以 spec 为准）；裁决归主会话。
- **R12 密钥审计**：测试库 `registry` 表、三台隔离实例日志、任务事件里按各真实凭据前 8 字符搜（审计脚本由代码读凭据、只输出命中计数，不打印凭据），零命中；account id/指纹与凭据无子串关系。
- **R13 agy 任务 token**（段 4 后）：linux-01 agy 载体选其登录，派任务 → 任务 HOME 的 token 是指向登录 HOME 的软链；任务后登录 HOME token 的 inode/mtime 与 R0-9 结论一致。
- **R14 机队升级与生产冒烟**（finish）：三台同批升级；各机首轮发现后核对生产 7 个载体的迁移结果表（`login`/无账号/未选），`account list` 读到各真实账号与登录，一次正常熟练工派发。
- **收尾**：停隔离实例；删 datadir 与全部 `~/.handoff/home/t464-*`；测试库按 credentials.md 注记处理；台账记证据与 P0 前后对照。

**未验证，需真机（汇总）**：
- U1 claude 离线身份字段（R0-1）。
- U2 macOS 钥匙串条目是否随 HOME 变（R0-2）→ Q6 分支。
- U3 agy 离线身份来源（R0-3）→ Q7 分支。
- U4 五种 CLI + opencode key 的登录命令与无浏览器做法（R0-4/5/6/8）。
- U5 各 kind 到期字段与 refresh token 位置、闲置 HOME 的过期噪声（R0-7）→ Q5。
- U6 opencode.db 是否有可作 label 的邮箱列（R0-4）。
- U7 agy 刷新 token 时原地写还是换目录项（R0-9、R13）→ Q8。
- U8 三台隔离实例都挂共享测试账本、设备表互见（P0，`dsn_configured=true`）。
- U9 存量载体在真实 HOME 布局下按 §5 能否对上（R1、R14）。

## 9. 决策权限

**执行者可自行决定**（交回物「偏离与自主决定」逐条对照）：
- 新文件拆分、私有函数与类型命名；`accounts` 与 `quota` 之间值类型放哪侧（不得成环，quota 不 import 业务包）。
- 登录 id 的具体派生（满足：确定性、重启不变、URL 安全 `[a-z0-9-]`）；`discovery_run` 与登录行非冻结字段的命名。
- 发现节拍（30s–5min；启动一次与「立即发现」必须有）、Q10 的新鲜度窗口（2–5 个发现周期）、扇出并发与超时（≤30s/机）。
- 新 HOME 缺省名的随机源；登录命令表的措辞（命令本身以 R0 结论为准）。
- V10 中请求体残留 `home_dir`/`credential` 是忽略还是 400（交回物说明）。
- 前端组件拆分、轮询实现（间隔固定 5s）、空态/提示措辞（以原型为准）。
- 日志措辞与级别。**必须有的观测点**：每轮发现一条 Info（机器、扫描 HOME 数、登录数、新增/变化/失效数、耗时，不含身份材料）；登录改挂账号一条 Info（登录 id、旧/新账号 id）；登录失效一条 Warn（登录 id、机器、HOME）；迁移每个载体一条 Info（载体、规则号、结果）；扇出每机一条（成功/失败原因）；建 HOME 一条 Info；协调者主 HOME 跳过写入一条 Info；agy 软链建立一条 Info。

**必须升级回主会话**：
- §11 任一冻结条目的改动；「待拍板」任一裁决的偏离；R0 结论与 Q5–Q8 推荐不符时的分支选择。
- 需要改 `ledger` 表结构或 `ledgerSchemaVersion`。
- 任何需要读钥匙串密文、写/刷新/复制凭据文件、或扫描 spec 范围外路径的实现。
- 改写既有测试断言。预计允许的只有：(1) 断言 `main_home_sync`/`Credential*`/凭据复制存在的测试（36 个文件，台账有清单，随删除改写）；(2) 断言载体指纹比对、`account_mismatch`、`ProbeHome`/`/api/host/probe` 的测试；(3) B453 账号登记（手填、`entry`、`machine/home_dir` 字段、按名引用）的测试；(4) 契约 fixture 刷新。改前列清单报主会话。
- 迁移规则（§5）对生产 7 个载体的预期结果与 R14 实测不符。
- agy 软链方案（Q8）在 R13 证伪。

## 10. 缺陷族（`defect-families` 逐族设问 → 验收责任）

| 族 | 设问与结论 | 验收 |
|---|---|---|
| 生命周期/状态机中断 | 发现中 agentd 重启：发现是幂等全量重扫，重启后启动轮即重建，不留半写（每条登录单行合并写）；扇出中入口机重启：各机已写的结果在账本里，控制台重试即可；建 HOME 后用户没登录：空目录留在 `~/.handoff/home/`，不产生登录行（清理归 OOS「登录 HOME 清理」）；迁移中途：逐载体 CAS，未完成的下轮继续；agy 软链任务结束随任务目录回收。 | V1、V12、V13；R1 重启一次隔离实例复核 |
| 静默失败/误导报错 | 「立即发现」不得在某机失败时整体报成功——逐机结果；检查 HOME 时目标机离线 → 502 可行动文案而非「尚未识别」；登录 HOME 被删时检测不得 mkdir 出空 HOME 再报「需要登录」（那会掩盖「已失效」）；无可探登录不得显示旧读数为正常（读面「探测失败 · 没有可用登录」）；B453 手填旧行不静默消失（Warn 一次，Q20）。 | V8、V13；R3、R4 |
| 跨平台假设 | 主 HOME 判定用展开 + EvalSymlinks（mac `/Users` 与 linux `/root`）；opencode data 目录在 Windows 无已知落点（沿用 `unsupported_platform`）；claude 凭据 mac 钥匙串 vs linux 文件（Q6）；`security` 命令只在 darwin 调；agy 进程组只 unix；软链在 Windows 不做（agy 非 Windows 目标，报 unsupported）。 | V2、V3；R0 三机 |
| 假红/假绿 | claude/agy 夹具若按推测造会编码不存在的世界 → 只许用 R0 读到的键形状（V2 条款）；并发归并与并发合并写必须用负载测试（V1/V6 N=50），单线程下两种存储方案都全绿；「凭据不再复制」用 mtime + 文件集合断言（V15），不只断言「没调用复制函数」；迁移测试锁行为（四条规则的结果）不锁内部遍历顺序。 | V1、V2、V6、V12、V15 |
| 门禁绕过 | 新写路径 `PUT /api/accounts/{ref}`、`PUT /api/logins/{id}/proxy`、`POST /api/accounts/discover`、`POST /api/logins/home|check` 全挂同一 api mux 鉴权门（`server.go` auth 包整个 mux）；转发带目标 token（`forwardJSON`）且一跳封顶；建 HOME 只在 `~/.handoff/home/` 下、目录名字符集白名单、拒 `..` 与已存在（防路径穿越与覆盖他人 HOME）；检查 HOME 只接受 `~` 或 `~/.handoff/home/<dir>`（防借发现读任意路径）；TOCTOU：两机同时建同名账号（不同身份）→ 撞名窗口接受（Q2）；人删账号与载体选其登录并发 → 载体引用不存在登录按「未选」放行并 Warn（B453 同款）。 | V3、V7、V13；review 核鉴权挂载 |
| 序列化边界 | 手写投影逐处：registry body（account/login/reading/discovery_run/carrier 新键）→ accounts View → `proto.AccountView/LoginView/CarrierView` → JSON → TS 类型 → CLI `--json`/表格；`HomeWakeReq.login`；任务行 `account` 列语义改 id。每条至少一支穿真实转换链的测试，可空类型区分缺席与零值（代理、到期、`no_account`）。 | V14、V16、契约 fixture |
| 枚举新值过白名单 | 新值：kind `claude`、`apikey`；状态 `na`；登录状态 `ok/expired/gone`；registry kind `account_login`、`discovery_run`；删值：载体凭据来源两值。逐处：`accounts.validate` kind 词表（`accounts.go:142`）、quota `Registry` 注册表（未注册 kind 现报 `unsupported_kind`——claude/apikey 是「不支持额度探测」而非错误，探测器必须在调 quota 前就按 `na` 跳过）、`State.Label`/`Blocks`、web `kindMeta`/STATUS 词表/底栏过滤、CLI 表格状态列、`CARRIER` 相关词表删 credential。 | V2、W1 + 执行者交回物列 grep 结果 |
| 跨版本（本项目追加） | 旧 agentd 重写载体 body 丢 `login`/`no_account` → 同批升级、混版本期不选登录（§5）；新目标机收旧检测请求（带 `credential`/`fingerprint_*`/`account`）→ 忽略，无登录代理（降级可接受）；旧目标机收 `login` → 忽略；扇出到旧机 → 404 → 结果行「该机 agentd 版本过旧」；`exclude_accounts` 名/id 混用 → 最多多换一次（Q23）。 | §5；V13；R14 |
| 承重安全属性 | 「密钥不离机」：V4 + R12；「凭据只读、永不复制」：V15 + R1/R7/R9 mtime；「扫描范围只有两类 HOME」：V3。 | V3、V4、V15；R12 |

## 11. 需冻结的外部承诺

**入口判据成立**：共享账本 body（所有版本 agentd 读写同一批行）、混版本 agentd 之间的转发与检测 wire、CLI `--json` 输出（主会话/skill 依赖），以及对 B453 已冻结 F1–F7、F9、F11、F13–F15、F18、F19 的变更。控制台 ↔ 本机 agentd 的 HTTP 面随同一二进制发布，但他机 agentd 会经转发调用其中的新增登录/发现/探测端点，故一并冻结。复用不重述：B453 F8（读数窗口形状）、F10（任何位置不出现凭据值）、F12（`account probe` 退出码）、F16（`HomeWakeResp.outcome` 含 `quota`）、F17；B460/B452 冻结条目全部不动。依赖的既成行为：registry CAS 版本从 1 起、`expect=0` = 必须不存在（`ledger/registry.go:29-77`）；转发一跳、`X-Handoff-Forwarded` 头、目标 token（`agentd/forward.go:54-78,116-`）。

**共享账本 body**
- **L1**：registry kind `account` 的 id = `<kind>:<hex12>`，第三方 key 为 `apikey:<provider>:<hex12>`；`hex12` = `sha256(身份材料)` 十六进制前 12 位。
- **L2**：身份材料按 kind：opencode-go OAuth = `<account_id>/<org_id>`；opencode-go API key 与 `apikey` = key 原文；codex = `tokens.account_id`；grok = `user_id`，缺则 `email`；claude、agy = 段 0 后按 Q6/Q7 裁决补入本条（补入即重走本节）。
- **L3**：`account` body 键：`kind`、`name`、`fingerprint`（`sha256:` + 同一 hex12）、`created_by`、`created_at`；可缺席：`provider`（仅 apikey）、`label`、`interval_minutes`/`warn_percent`/`low_percent`（`na` kind 缺席）。
- **L4**：`account` 的 `name` 全局唯一，不含 `:` 与 `/`。
- **L5**：registry kind `account_login` 的 id 确定性、重启不变、只含 `[a-z0-9-]`。
- **L6**：`account_login` body 键：`account`（账号 id）、`machine`、`home`（`~` 或 `~/.handoff/home/<dir>`）、`home_abs`、`kind`、`source`、`fingerprint`、`state`、`changed_at`；可缺席：`provider`、`expires_at`、`auth_failed_at`、`proxy`。
- **L7**：`account_login.state` ∈ `ok | expired | gone`。
- **L8**：`account_reading` id = 账号 id；body 增可缺席键 `login`（本次探测所用登录 id）。
- **L9**：registry kind `discovery_run`，id = 机器名；body 键 `at`、`homes`、`logins`；可缺席 `errors`。
- **L10**：`carrier` body 增可缺席键 `login`（登录 id）、`no_account`（仅 true 时出现）；`home_dir` 只由服务端写；读到 `credential`、`account`、`cred_fingerprint` 一律忽略，新版本不再写。

**账号/登录 HTTP API（producer agentd；consumer 控制台、CLI、他机 agentd）**
- **A1**：`GET /api/accounts` → `200 {"accounts":[AccountView…],"discovery":[DiscoveryView…]}`；空库两者为 `[]`。
- **A2**：AccountView 键：`id, name, kind, fingerprint, version, state, stale, logins, carriers`；可缺席：`provider, label, interval_minutes, warn_percent, low_percent, reading, last_attempt_at, last_error, recover_at, probe_login`。B453 F5 的 `machine, home_dir, entry, proxy` 删除。
- **A3**：`state` ∈ `ok | low | out | fail | none | na`（F6 增 `na`）；`kind` ∈ `opencode-go | codex | grok | agy | claude | apikey`（F7 增两值）。
- **A4**：LoginView 键：`id, machine, home, source, fingerprint, state, changed_at, carriers`；可缺席：`expires_at, proxy`（打码）、`relogin`（`{ssh?, run, note?}`）。`home` 为 `~` 时控制台显示「~（主 HOME）」。
- **A5**：DiscoveryView 键：`machine, self`；可缺席：`at`、`error`。
- **A6**：`PUT /api/accounts/{ref}?expect=<v>`，`{ref}` = 账号 id 或名字；体 `{name?, interval_minutes?, warn_percent?, low_percent?}`（F9 删 `kind/machine/home_dir/entry/proxy`）；`expect=0` → 400；名字冲突 → 409；成功 `200 AccountView`。
- **A7**：`DELETE /api/accounts/{ref}?expect=<v>`：成功 `200 {"name","version"}` 并连带删登录与读数行；任一登录被引用 → `409 {"error","carriers":[…]}`；其余码同 B453 F3 r1。
- **A8**：`POST /api/accounts/{ref}/probe`：转发到可探登录所在机（一跳）；无可探登录 → 409；`na` 账号 → 409；其余码同 B453 F4 r1。
- **A9**：`PUT /api/logins/{id}/proxy` 体 `{proxy}`（空串 = 清除）→ `200 LoginView`；服务端合并写，不要求 `expect`；打码回填保留原值；校验失败 400。
- **A10**：`POST /api/accounts/discover[?machine=<名>]`：无 `machine` = 入口机对全部已登记机器（含本机）扇出；有 = 只对该机；→ `200 {"machines":[{"machine","ok","error?","at?"}]}`；单机失败不改变整体 200。
- **A11**：`POST /api/logins/home?machine=<名>` 体 `{agent, dir}` → `200 {"home","command":{"ssh?","run","note?"}}`；`agent` ∈ `opencode | codex | grok | agy | claude`；目录名非法 400；已存在 409；目标机不可达 502。
- **A12**：`POST /api/logins/check?machine=<名>` 体 `{home}` → `200 {"logins":[{"login","account","account_name","new_account","account_logins"}]}`；尚未识别为 `[]`；`home` 只接受 `~` 或 `~/.handoff/home/<dir>`，否则 400。

**编制 wire（producer agentd；consumer 控制台、`squad list`）**
- **A13**：CarrierView 删 `credential`、`account`、`account_mismatch`；增可缺席 `login`、`no_account`（仅 true）、`account_id`、`account_name`、`login_state`（派生只读）；`home_dir` 保留（派生）。
- **A14**：CarrierInput 删 `home_dir`、`credential`、`account`；增 `login?`、`no_account?`；二者恰一个，否则 400。
- **A15**：删除 `POST /api/host/probe` 与 `HomeProbeReq/HomeProbeResp`。

**CLI（producer `handoff`；consumer 主会话/skill/协调者）**
- **C1**：`handoff account list --json` 每行一个对象，形状 = A2。
- **C2**：`handoff account discover [--machine <名>]`：全部机器成功退出 0，任一失败退出 1；stdout 每机一行（机器、结果、登录数或原因）。

**机间 wire（producer/consumer 均为 agentd，混版本）**
- **W1**：`HomeWakeReq` 删 `credential`、`fingerprint_kind`、`fingerprint_entry`、`account`，增可缺席 `login`（目标机按它在本机查登录代理与 kind，只认本机登录）；`HomeWakeResp` 删 `fingerprint`。
- **W2**：A10–A12 的他机调用一律带 `X-Handoff-Forwarded`，目标机不再外扇；目标机返回 404（旧版本）时入口机在结果里报「该机 agentd 版本过旧」。
- **W3**：`dispatchRequest.exclude_accounts` 元素语义改为账号 id（无账号成员仍为载体名）。

**拍板记录**（难逆转、无上下文会惊讶、真取舍）：
- **账号 id 由身份确定性导出**（L1/L2，Q2）。被否：随机 id + 查重。反过来写，单线程测试全绿，只有两机并发发现（V1 负载）才会出两个同身份账号。
- **载体 `home_dir` 保留为服务端镜像**（L10/A14，Q3）。被否：读时经登录解析。反过来写功能测试同样全绿，但要改 20+ 处消费者与冻结身份比对语义，且换不来行为差异。
- **迁移由载体所在机在本机发现后写一次**（§5）。被否：读时推导。反过来写，存量载体当下表现相同，但同 HOME 后来多出一条兼容登录时绑定会无声改变，没有测试会红。
- **「已过期」用证据型判据**（Q5，若拍板采纳）。被否：离线到期即过期。反过来写所有单测都绿，只有真机闲置 HOME 会长期误报红字。

## 12. 自审

- 故事覆盖：S1–S9 均在 §0、§2.1 有行，在 §7 有段落、§8 有断言与真机项。
- 待验证假设：U1–U9 各有 R 步骤与阻塞范围（段 0 阻塞段 2 的 claude/agy 与段 3 命令表；R1 阻塞段 2）。
- 决策权限 §9；待拍板在稿首；行为闭环每行五格；依赖已分硬依赖与施工排序；真机清单已汇总；冻结节有结论。

## 13. 核实读数索引

全部文件:行与判断见 `docs/superpowers/specs/b464-ledger.md`「plan 出稿」节：账本闸与 registry、账号域现状、载体与准入、检测链、hostapi 复制、协调者供给（含 SC2）、agy 任务 token（SC1）、grok 软链（不在删除清单的理由）、codex `CODEX_HOME` 例外、任务 HOME 与代理快照、探测器、账号 API、quota 身份取材、toolchain 凭据表、转发与 ssh 目标、CLI、web、roadmap 相关行、受影响测试面。
