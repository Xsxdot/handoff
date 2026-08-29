# B293 契约增量：隔离 HOME 生命周期 + 载体四态

**上游状态：已批准**（源 spec：`docs/superpowers/specs/2026-08-29-b293-isolated-home-carrier-status-design.md`，
头部状态行「已批准（用户 2026-08-29：非空无凭据选方案 1；检测按钮；四态含不可达与限额中；原型形态「可以」）」）
**级别：L3 ｜ 选档：轻档**（单条用户旅程，不值并行子卡。直通竖切归重档，本节点只落空壳与直通镜像。）
**冻结物**：本文档、`codegraph/target.json`、`codegraph/best.json`（补挂 `k_web_api_scheduling*`）、
`codegraph/diffs/cards-B293-charter-3.json`、本轮补冻视图
`codegraph/diffs/cards-B293-charter-5.json` 与本提交 Ticket 0 骨架，**随本提交冻结**。
**形态权威**：`prototypes/b293-carrier-home/pages/settings.html`。
**本节点**：charter / contract；交棒：breakdown。
**废止**：B156.3 契约 §4.1「健康位本期只有形状、登记缺省 Healthy=true」。

## 1. 现状查证

| 契约事实 | 代码出处 | 本卡关系 |
| --- | --- | --- |
| `PutCarrier` 把 `!Healthy` 翻 `true` | `internal/scheduling/scheduling.go:133`（图未覆盖该方法，见 §10） | 废止；新建/改 HOME 落未上线 |
| `admitInto` 跳过 `!Healthy`；全无健康 → `ErrNoHealthy` | `internal/scheduling/scheduling.go#admitInto` | 改判 `status==online`；哨兵名沿用 |
| `CarrierInput` 不含 healthy | `internal/proto/scheduling.go#CarrierInput` | 继续不含 status / last_error / healthy |
| `CarrierView.healthy` 手写投影 | `internal/agentd/schedapi.go#carrierView` | 加 `status`/`last_error`；implement 删 healthy |
| 登记 HTTP 不做跨机转发 | `internal/agentd/schedapi.go` 包注释 | 探测可转发；检测写状态不能整段转发 |
| `?machine=` 一跳搬运、防环头 | `internal/agentd/forward.go#Server.forwardIfRequested` | 探测/唤起走它 |
| `HomeDir` 非空覆写进程 `HOME` 且赢过 env 同名行 | `internal/hostapi/driver.go#buildEnv` | 保持；执行者任务必须同样消费载体 HomeDir |
| 凭据相对路径表 | `internal/toolchain/detect.go#credRelPath` / `credRelPathFor` | 探测「已登录」判据；见 §4 |
| claude 无文件判据 | 同文件 `Detect` 的 claude 分支 | 探测永不报 logged_in |
| Windows opencode 无文件判据 | `credRelPathFor` | 同左 |
| 默认数据目录 `~/.handoff` | `internal/config/config.go#defaultDataDir` | 默认 HOME **不**跟可改 DataDir |
| 项目仓库默认 `<DataDir>/repos` | `internal/config/config.go#Load` 补 RepoRoot | 「同款管理」只指落点形态，不是共用 DataDir |
| 协调者拉起已写 `carrier.HomeDir` | `internal/agentd/scheddrain.go#launchCoordinatorRound` | 执行者任务路径尚未消费 |
| codex 丢弃 `CODEX_HOME` 复用 `~/.codex` | `internal/executor/codex/taskenv.go#droppedEnvKeys` | 载体 HomeDir 非空时禁止再这么做 |
| grok 任务级 grokhome 软链主 HOME 凭据 | `internal/executor/grok/taskenv.go#EnsureAuthLink` | 沙箱仍合法；凭据不得指回主 HOME（除非本轮 main_home_sync 供给） |
| `hostapi.RunTurn` 缺省 30 分钟、会喂 prompt | `internal/hostapi/hostapi.go#TurnRequest` / `internal/hostapi/driver.go#DefaultTurnTimeout` | 检测**不是** RunTurn |
| 设置页仍画 health 点 | `web/src/app/settings/SchedulingPage.tsx` | 对照原型改四态药丸 + 检测/运行 |
| `WakeRequest` 的 Credential 字段 | `internal/hostapi/probe.go#WakeRequest` | 空值表示 standalone；供 `main_home_sync` 供给语义使用 |
| `HomeWakeReq` 的 credential JSON 字段 | `internal/proto/scheduling.go#HomeWakeReq` | `credential` 使用 `omitempty`，空值缺席 |
| `handleHomeWake` 将 Credential 透传至 `WakeHome` | `internal/agentd/hostprobe.go:65`（图未覆盖该 handler） | 直通镜像，仍不实现 WakeHome 肉 |
| `Client.Dispatch` 手搭 `/api/tasks` 请求 map | `internal/client/client.go#Client.Dispatch` | HomeDir nil 省略，非 nil 原样写入 `home_dir` |
| 派发 HOME 的可空透传字段 | `internal/ledgerstep/dispatch.go#DispatchOpts`、`internal/client/client.go#DispatchOpts`、`internal/agentd/server.go#dispatchRequest`、`internal/agentd/manager.go#DispatchReq` | nil=字段缺席，指向空串=显式空值，非空指向载体 HomeDir |

对侧常量执法：

| 常量 | 生产者 | 消费者 | 结论 |
| --- | --- | --- | --- |
| `ErrNoHealthy` | `admitInto` | `internal/agentd/scheddispatch.go` 与测试 | 活跃；不改名，语义收窄为「没有已上线且有空的载体」 |
| `healthy` JSON 键 | `PutCarrier` 翻真 + `carrierView` | web `CarrierView.healthy`、fixture `SquadsResp.json` | Ticket 0 双字段兼容；implement 删除前仍是活跃键 |
| `credRelPath` 三键 | 仅 `Detect` / `credRelPathFor` | `cmd/init.go` 表格 | 活跃；探测复用，不另造表 |
| `droppedEnvKeys["CODEX_HOME"]` | `serveSpec` | 任务启动 env | 活跃；本卡改变其在「载体 HomeDir 非空」时的执法 |
| proto `healthy:true` fixture | `TestContractFixtures` | `web/src/api/contract.test.ts` | 活跃；omitempty 的 `status` 不进现有样本 |

依赖库既成行为（与签名同等承重）：

| 既成行为 | 出处 | 约束 |
| --- | --- | --- |
| `forwardIfRequested`：空 machine / 已转发头 = 本机；未知机器 400；失败 502 不重试 | `internal/agentd/forward.go#Server.forwardIfRequested` | 探测/唤起不得自造第二套选路 |
| 转发体上限 1MB | 同文件 `forwardBodyLimit` | probe/wake body 远小于此 |
| `os.Stat` 不存在即 err | 标准库；`toolchain` 用 `statFile` 包装 | 探测「不存在」= empty |
| `json omitempty`：空串缺席、false 仍在场 | Go 标准库；`TestRowsCarryVersionsForCASLock` 钉 healthy 显式 true | status 空不进 GET；last_error 空缺席 |

## 2. 架构决定

不新开子系统。状态机与登记规则仍归 `d_scheduling`；本机文件系统/进程归 `d_execution_host`（`hostapi.Host`）；HTTP 编排归 `d_gateway`；设置页归 `d_web`。

不新开 target.json 方向：

```
d_gateway → d_scheduling   entries: scheduling.Service / scheduling（包级函数）  ← 加 ApplyDetect / DefaultHomeDir / RunCommand
d_gateway → d_execution    entries: hostapi.Host / hostapi（包级函数）          ← 加 ProbeHome / WakeHome
```

编制域继续零 import 执行域。检测编排（读载体 → 本机或 `?machine=` 唤起 → `ApplyDetect`）只允许出现在 gateway（`handleCarrierDetect`），实现票填肉。

架构形态声明（沿用 B156.3）：按子系统分域的平铺领域包，无横向 controller/service/dao 分层。

## 3. 精确签名

### 3.1 编制域（`internal/scheduling/status.go`）

```go
type CarrierStatus string
const (
    StatusPending     CarrierStatus = "pending"     // 未上线
    StatusOnline      CarrierStatus = "online"      // 已上线
    StatusQuota       CarrierStatus = "quota"       // 限额中
    StatusUnreachable CarrierStatus = "unreachable" // 不可达
)
const IsolatedHomeRoot = "~/.handoff/home"
func (s CarrierStatus) Label() string
func DefaultHomeDir(name string) string          // 空/空白 → ""
func RunCommand(c Carrier) string                // "HOME="+HomeDir+" "+CLI
type DetectEvidence struct{ Reachable, NeedLogin, Quota bool }
func (s *Service) ApplyDetect(name string, ev DetectEvidence, detail string) (Carrier, error)
var ErrDetectUnwired // Ticket 0 骨架哨兵；实现票接线后正常路径不再返回
```

`Carrier` 增 `Status` / `LastError`（`internal/scheduling/scheduling.go#Carrier`）。Ticket 0 仍保留 `Healthy` 以免存量测试红；实现票删除 `Healthy` 并改 `PutCarrier` / `admitInto`。

### 3.2 本机承载（`internal/hostapi/probe.go`）

```go
const DefaultDetectTimeout = 3 * time.Minute // B295 废止 30s：检测改为真发一条消息
type ProbeKind string // empty | logged_in | occupied
func (h *Host) ProbeHome(ctx context.Context, req ProbeRequest) (ProbeReply, error)
func (h *Host) WakeHome(ctx context.Context, req WakeRequest) (WakeReply, error)
type WakeRequest struct {
    CLI, HomeDir, Credential, Model string
    Timeout time.Duration
}
// Ticket 0 恒返回 hostapi.ErrUnavailable
```

`WakeHome` **经** `RunTurn` 发固定短消息 `ping`（B295）；仍不准在控制台拉登录 TUI。Timeout=0 用 `DefaultDetectTimeout`（3 分钟）。禁止 `--version`，禁止凭据文件存在当作 ready。

### 3.3 HTTP（gateway）

| 方法 | 路径 | 转发 | 请求 | 响应 |
| --- | --- | --- | --- | --- |
| POST | `/api/host/probe?machine=` | 是 | `HomeProbeReq` | `HomeProbeResp` |
| POST | `/api/host/wake?machine=` | 是 | `HomeWakeReq` | `HomeWakeResp` |
| POST | `/api/squads/carriers/{name}/detect` | **否** | 空对象 | `CarrierDetectResp` |
| GET | `/api/squads/carriers/{name}/run-command` | 否 | 无 | `CarrierRunCommandResp` |
| PUT | `/api/squads/carriers/{name}?expect=` | 否 | `CarrierInput`（仍不含 status） | `SquadPutResp` |
| GET | `/api/squads` | 否 | 无 | `CarrierView` 增 status/last_error |

DTO：`internal/proto/scheduling.go#HomeProbeReq` 等。TS 镜像：`web/src/api/scheduling.ts`。
`machine` 只走 query，不进 body。缺失载体：detect / run-command → 404。
Ticket 0：probe/wake/detect 因空壳 → 503。

### 3.4 小队派发 HOME 透传（条 52）

四层字段形状：

```go
// internal/ledgerstep/dispatch.go
type DispatchOpts struct {
    // existing fields...
    HomeDir *string // nil=缺席；指向空串=显式空值
}

// internal/client/client.go
type DispatchOpts struct {
    // existing fields...
    HomeDir *string
}

// internal/agentd/server.go
type dispatchRequest struct {
    // existing fields...
    HomeDir *string `json:"home_dir,omitempty"`
}

// internal/agentd/manager.go
type DispatchReq struct {
    // existing fields...
    HomeDir *string
}
```

`cmd/card_dispatch.go#cliTransport` 与 `internal/agentd/cardstep.go#Server.stepTransport` 只做字段透传。
`internal/client/client.go#Client.Dispatch` 手搭 `/api/tasks` 请求 map：HomeDir 为 nil 时省略
`home_dir`，非 nil 时原样写入字符串。请求缺席或值为空均表示非小队派发，执行机不得覆写
进程 `HOME`；非空值表示把载体 `HomeDir` 送到执行机。Ticket 0 只冻结并透传字段，
`startCardStep` 不在本轮读取载体或改变行为，执行者 HOME 执法归实现票 U5。

### 3.5 控制台派生串（假缝不占冻结名额，但跨语言同值）

`web/src/api/scheduling.ts#defaultHomeDir` 与 `CARRIER_STATUS_LABEL` 必须与 Go `DefaultHomeDir` / `Label` 逐字相同。

## 4. 凭据文件表（探测「已登录」）

权威是 `internal/toolchain/detect.go#credRelPath`，相对**被探测的 HOME**（隔离路径展开后），不是进程主 HOME——除非 `credential=main_home_sync` 去读主 HOME 作对照。

| CLI | 相对路径 | 无文件判据时 |
| --- | --- | --- |
| opencode | `.local/share/opencode/auth.json` | Windows：`credRelPathFor` 返回 false → 永不 `logged_in` |
| grok | `.grok/auth.json` | — |
| codex | `.codex/auth.json` | — |
| claude | （无） | 永不 `logged_in` |

既有锁：`internal/toolchain/detect_test.go`。探测实现必须调用同一 `credRelPathFor`，禁止另造路径表。

`main_home_sync` 且隔离路径为 empty、主 HOME 对该 CLI 已登录 → 探测报 `logged_in`（仍不修改隔离目录）。保存后的供给动作把表内文件拷进隔离 HOME；不搬技能/规则树。claude 无文件可拷 = 供给空操作。

## 5. 冻结清单（一条 = 一支可独立判 pass/fail 的断言）

**状态词表**

1. wire `status` 值 `pending` 的用户可见名是「未上线」。
2. `online` 的用户可见名是「已上线」。
3. `quota` 的用户可见名是「限额中」。
4. `unreachable` 的用户可见名是「不可达」。
5. `CarrierInput` JSON 不得出现 `status`、`last_error`、`healthy` 键的写入语义（请求设置无效，服务端忽略或不收该字段）。
6. GET 载体投影携带 `status`；空 status 以 omitempty 缺席。
7. GET 载体投影携带 `last_error`；空则以 omitempty 缺席。
8. `last_error` 不参与准入。

**PutCarrier（实现票改行为；Ticket 0 仍翻 Healthy）**

9. `expect=0` 新建后 `status=pending`。
10. 保存且 `home_dir` 相对上一版变化后 `status=pending`。
11. 第 10 条同时清空 `last_error`。
12. 保存且 `home_dir` 未变则不改 `status`。
13. 保存且 `home_dir` 未变则不改 `last_error`。
14. `PutCarrier` 不得再把零值/false 写成已上线（废止 `if !c.Healthy { c.Healthy = true }`）。

**准入**

15. 仅 `status==online` 且该载体有空位时，`admitInto` 可选中该成员。
16. `pending` 成员被跳过。
17. `quota` 成员被跳过。
18. `unreachable` 成员被跳过。
19. 小队里没有任何 `online` 成员 → 返回 `ErrNoHealthy`（配置/状态问题，不是满员）。
20. 有 `online` 成员但都满员 → 返回 `ErrNoSlot`。
21. `Admit` / `LaunchAdmit` / `Release` / 清队循环不写 `status`。

**路径探测**

22. `POST /api/host/probe` 不创建目录。
23. `POST /api/host/probe` 不覆盖/删除已有文件。
24. 路径不存在 → `kind=empty`。
25. 路径存在且目录无任何条目 → `kind=empty`。
26. 目标 HOME 内 `credRelPathFor` 命中的文件 `Stat` 成功 → `kind=logged_in`。
27. 目录非空且未见该 CLI 凭据（含无文件判据的 CLI）→ `kind=occupied`。
28. `credential=main_home_sync` 且隔离路径 empty 且主 HOME 已登录 → `kind=logged_in`。
29. claude 的探测结果不得为 `logged_in`。
30. Windows 上 opencode 的探测结果不得为 `logged_in`。
31. 请求里的 `~` 在**目标机**展开。
32. 非空 `?machine=` 走 `forwardIfRequested`，本机不解释对端文件系统。

**检测 / 一次性唤起**

33. `POST /api/squads/carriers/{name}/detect` 是写状态的入口（与按钮同一条）。
34. 检测有时限；`WakeRequest.Timeout==0` 时用 `DefaultDetectTimeout`（3 分钟；B295 废止 30s）。
35. 检测不得在控制台里进入交互登录。
36. 检测必须调用 `hostapi.Host.RunTurn` 发 `DetectPrompt`（`ping`）；禁止 `--version`；禁止凭据文件存在当作 ready（B295）。
37. 空白 HOME 被对应执行者落下自己的文件（边界型，真机补）。
38. 能跑且凭据可用 → `status=online`。
39. 识别为额度用尽 → `status=quota`。
40. 机器/网络不够着，且检测前状态是 `online`/`quota`/`unreachable` → `status=unreachable`。
41. 机器/网络不够着，且检测前状态是 `pending`（从未上过线）→ `status=pending`。
42. 需要登录或未见凭据 → `status=pending`。
43. `PutCarrier` 成功路径不调用 detect。
44. 控制台在新建或 `home_dir` 变更的 PUT 成功后必须立刻再 POST detect 一次。

**运行命令**

45. `GET /api/squads/carriers/{name}/run-command` 返回服务端命令。
46. 命令格式精确为 `HOME=<carrier.home_dir> <carrier.cli>`（home_dir 用已存串，可含 `~`）。
47. 客户端不得拼接或改写该命令。
48. 载体不存在 → 404。

**默认路径**

49. 用户可见默认串精确为 `~/.handoff/home/<trim(载体名)>`。
50. 载体名为空或纯空白时默认串为空串。

**运行时 HOME**

51. `hostapi.buildEnv`：`HomeDir` 非空覆写进程 `HOME`，且赢过 `req.Env` 里的 HOME 行（保持现状）。
52. 小队派发经 `POST /api/tasks` 的可空 `home_dir` 把载体 `HomeDir` 送到执行机。
52.1. `home_dir` 字段缺席或值为空表示非小队派发。
52.2. `home_dir` 字段缺席或值为空时，执行机不得覆写进程 `HOME`。
53. 载体 `HomeDir` 非空时，codex 不得再丢弃 `CODEX_HOME` 去复用用户级 `~/.codex`。
54. grok 任务级 `grokhome`（permission_mode）仍合法。
55. 第 54 条的凭据不得软链/指向机器主 HOME，除非本轮凭据来源是 `main_home_sync` 的供给动作。

**供给与永不做**

56. `credential=main_home_sync` 且隔离路径为 empty 时，`WakeHome` 先拷贝 §4 表内凭据文件再唤起。
56.1. `main_home_sync` 供给不搬技能/规则树。
56.2. claude 无文件可拷时，`main_home_sync` 供给为空操作。
57. `kind=occupied` 的目录不得被当成空目录清空或覆盖。

## 6. Ticket 0（本提交）

空壳与直通镜像已落：

- 类型/常量：`CarrierStatus` 四值、`IsolatedHomeRoot`、probe/wake 词表、proto DTO、TS 镜像。
- 可执行冻结：`DefaultHomeDir` / `Label` / `RunCommand` 有测试；run-command HTTP 穿过 `Handler()`；fixture 六份新类型进 `TestContractFixtures`。
- 直通镜像：`handleHomeProbe` → `ProbeHome`；`handleHomeWake` → `WakeHome`；`handleCarrierDetect` → `ApplyDetect`；`handleCarrierRunCommand` → `RunCommand`；`SetupAutomation` 装配 `s.hostAPI` 并与 `coordinatorRunner` 共用。
- 空壳：`ProbeHome`/`WakeHome` 返回 `ErrUnavailable`；`ApplyDetect` 返回 `ErrDetectUnwired`（HTTP 503）。
- **未改**（留给实现票，否则会无测试地改变准入）：`PutCarrier` 仍翻 Healthy；`admitInto` 仍读 Healthy。

轻档：无直通竖切。越过空壳的可观测行为（默认串、四态中文名、运行命令格式）均有能变红的测试。

## 7. 三重闸门拍板记录

命中六项（难逆转 × 无上下文会惊讶 × 真取舍）：

1. **四态取代 `Healthy bool`，而不是并行第二真相。** 被否：继续用 bool（创建/登录/限额/网络失败动作不同）。后人看到 `status` 字符串会想改回 bool。改回去要动编制域、wire、控制台、准入。
2. **PUT 不调用 detect；自动检测是控制台在新建/改 HOME 后的第二次调用。** 被否：PUT 内同步检测（CAS 写与有时限远程进程绑在一起，失败语义分不清）。反过来写（把 detect 塞进 PutCarrier）不会让 Ticket 0 的测试变红——故立字据。
3. **用户可见默认 HOME 固定 `~/.handoff/home/<名>`，不跟 `cfg.DataDir`。** 被否：`<DataDir>/home/<名>`（DataDir 可改，已登记串会漂）。`~` 的展开是目标机的事。
4. **探测/唤起可 `?machine=` 转发；detect 不能整段转发。** 被否：给 detect 套 `forwardIfRequested`（状态写在协调机 registry，整段转发会写到执行机的空账本或 404）。后人看到「别的本机端点都 forward」会顺手套上。
5. **`main_home_sync` 供给收进既有 `WakeRequest`/`HomeWakeReq`，不另开 sync-creds 面。** 被否：导出 `Host.SyncMainHomeCreds` 并新增 `POST /api/host/sync-creds?machine=`。检测本来就是「供给（若需要）+ 唤起」的一次用户动作；新跨机端点会扩大网关与客户端面，且 probe 仍必须只读。后人看到读写分离会想拆开，但这会让同一动作多一条路由与编排状态。
6. **小队 HOME 使用单个可空 `home_dir` 字段穿过既有派发链，不先造通用 env 通道。** 被否：把 HOME 塞进通用 `env` 列表。执行机没有编制账本，必须由协调者把载体 `HomeDir` 带过边界；可空指针同时保留缺席/显式空的三态，而通用 env 面更大、语义也更宽。

`DefaultDetectTimeout=3m`（B295 从 30s 改来）：真发一条消息需要模型往返，30s 经常回不来。

## 8. 可执行冻结

- **命中**：默认 HOME 串、运行命令格式、四态英文键与中文名、HomeWakeReq 的 `credential` fixture/空值省略、派发 `home_dir` 缺席/空串/非空三态——Go `internal/scheduling/status_test.go`、HTTP `TestCarrierRunCommandThroughWire`、`TestHomeWakeReqOmitsEmptyCredential` 与 `TestDispatchSerializesHomeDirThreeStates`、fixture 六类型。凭据相对路径由既有 `internal/toolchain/detect_test.go` 锁，本卡不另造表。
- **无命中**：哈希 / 密钥派生。无新编码格式算法。
- 本轮跑过：见 §11。

## 9. 移交 plan 附区

（实现级，不占冻结名额，不参与逐条打勾）

- 「目录为空」：`ReadDir` 零条目；是否忽略 `.DS_Store` 归实现票，默认不忽略（冻结清单把「无任何条目」当 empty）。
- `WakeHome` 各 CLI 的 argv（如何初始化空白 HOME、如何识别 quota/need_login）归实现票；禁 RunTurn、禁交互登录已冻。
- Windows 上 `RunCommand` 是否另附 `USERPROFILE=`：命令格式冻的是 `HOME=...`；USERPROFILE 叠层归实现票。
- 路径含空格时是否加引号：冻的是无引号拼接；有空格的 HOME 归实现票加测试后才能改格式（改格式=契约变更）。
- `ApplyDetect` 成功响应的 `version`：从 registry 行读取，Ticket 0 成功路径未接通。
- Ticket 0 双字段：实现票删除 `Healthy`、改 `admitInto`/`PutCarrier`、刷新 `SquadsResp` fixture 与 `SchedulingPage`。
- 设置页对照原型：四态药丸、检测/运行按钮、改路径即时探测、默认 HOME 随名字走直到用户改过。假缝（是否等于上一份默认）不占冻结。
- `handleCarrierDetect` 编排：本机直接 `WakeHome`；跨机 POST 对端 `/api/host/wake?machine=`（或对端本机、由对方 forward），再 `ApplyDetect`。
- 非空无凭据允许保存为未上线：PutCarrier 不因 occupied 拒绝。
- 执行者任务注入隔离 HOME 的具体 env 键（HOME vs `CODEX_HOME` vs `GROK_HOME`）在不违反 §5 条 52–55 的前提下由实现票选择。

## 10. 图覆盖债与欠账

**图覆盖债（基线已有代码、无节点）**：`Service.PutCarrier` / `admitInto` / `Carrier` 读方法、`handleCarrierPut` / `handleSquadsGet` 等 B156.3 后期符号未进 `baseline.json`。本卡不回灌全量。Ticket 0 新符号进 `codegraph/diffs/cards-B293-charter-3.json`，本轮新增/改动的补冻字段进 `codegraph/diffs/cards-B293-charter-5.json`。`k_web_api_scheduling*` 原在 baseline 有、best 未归属，本提交已补挂到 `d_web_contract`。

**本节点欠账**：

- PutCarrier 翻真、admitInto 按 Healthy：实现票，测试锁 §5 条 9–20。
- ProbeHome / WakeHome / ApplyDetect 空壳：实现票。
- 轻档无直通竖切。
- vitest：本工作树无 `web/node_modules`，TS 侧测试**未验证**；线格式由 Go `TestContractFixtures` 与 `TestCarrierRunCommandThroughWire` 锁。

「欠账：无」与「已实现但零测试」不并存：本提交越过空壳的行为均有测试。

## 11. 本轮验证记录

- `gofmt -l` 触碰的 Go 文件无输出；`git diff --check` 退出码 0。
- `go vet ./internal/scheduling/ ./internal/hostapi/ ./internal/proto/ ./internal/ledgerstep/ ./internal/client/ ./internal/agentd/` 退出码 0。
- `go build ./...` 退出码 0。
- `go test ./internal/hostapi/ ./internal/ledgerstep/ -count=1`：`hostapi` ok 0.313s，`ledgerstep` ok 7.336s，退出码 0。
- `go test ./internal/agentd/ -count=1`：`ok github.com/Xsxdot/handoff/internal/agentd 248.787s`，退出码 0。
- `go test ./internal/proto/ -run TestContractFixtures -update`：退出码 0；随后 `go test ./internal/proto/ -run TestContractFixtures -count=1`：`ok .../internal/proto 0.005s`，退出码 0。
- `go test ./internal/client/ -run TestDispatchSerializesHomeDirThreeStates -count=1`：`ok .../internal/client 0.008s`，退出码 0。
- `go test ./internal/proto/ -run 'TestContractFixtures|TestHomeWakeReqOmitsEmptyCredential' -count=1`：`ok .../internal/proto 0.003s`，退出码 0。
- `go test ./... -run '^$' -count=1`：全仓 Go 测试包编译检查通过，退出码 0（各包显示 `[no tests to run]` 或无测试文件）。
- 金样本：HomeWakeReq 的 `credential=main_home_sync` fixture 与 `home_dir` 缺席/空串/非空三态实际 JSON roundtrip 均由上列测试锁定。
- 图：`codegraph validate` 输出 `issues: null`、退出码 0；`codegraph check --view cards-B293-charter-5` 输出 `fails: []`、退出码 0。输出中的既有 warns 保留，未当作失败。
- 修正契约表项为 `internal/scheduling/scheduling.go:133` 后，`codegraph resolve --doc docs/superpowers/specs/b293-contract.md --view cards-B293-charter-5` 退出码 0；输出无 `vanished`，本轮新增字段/签名锚均为 ok，其余既有锚为 ok/moved。
- web：`web/node_modules` 不存在，vitest 未验证。
- 本轮未碰 handoff CLI、未起新 executor。

## 12. 拆解节点边界澄清（2026-08-29 出稿，已拍板）

核对中做出的边界澄清。拍板 2026-08-29，形状以 `docs/superpowers/specs/b293-breakdown.md` 稿首为准。本段**仍不新增冻结条目**；第 52 / 56 条已由本轮在 §3.2、§3.4 与 §5 补冻。

1. **`DetectEvidence` 标志互斥时的迁移优先级**（不退回、不新开字段）：按 §5 条 38–42 的顺序套用——`!Reachable` 先于 `Quota` 先于 `NeedLogin`，三者皆假且 `Reachable` → `online`。gateway 不得在编排层另写一份四态表。
2. **凭据相对路径表仍只此一份**：探测必须调用 `internal/toolchain/detect.go#credRelPathFor`，禁止在 hostapi 另造表。该函数今日未导出；`target.json` 无 `d_execution → d_maintenance`。**拍板：组装点注入导出函数，不加 `d_execution → d_maintenance` 边。**
3. **第 56 条供给动作在补冻前无对应签名**：当时 `WakeRequest` / `HomeWakeReq` 无 `Credential`，Host 无独立拷贝方法，HTTP 无 sync 端点；probe 只读（条 22–23）接不住。**本轮按已拍板方案 A 补入 `WakeRequest` / `HomeWakeReq` 的 `Credential`（空=standalone）；`WakeHome` 仍是 U2 空壳，冻结其在隔离 empty 且 `credential=main_home_sync` 时先拷 §4 表内文件再唤起。不新开 HTTP 路径，occupied 永不覆盖。**
4. **第 52 条小队派发消费 HomeDir 在补冻前无传输字段**：协调者拉起已走 `scheddrain` 读载体；`startCardStep` 只用 Binding 三元组。**本轮按已拍板方案 A 为 `DispatchOpts` / `client.Dispatch` 手搭 map / `dispatchRequest` / `DispatchReq` 补入可空 `home_dir`（空/缺席=不覆写执行机 HOME），并用可空区分缺席 vs `""` vs 非空；执行机 U5 消费行为仍未实现。**
5. **空 `status` 存量行的准入语义未冻**：Ticket 0 双字段下现网行是 `Healthy=true` 且 `status` omitempty 缺席。**拍板：不退回。`admitInto` 把空 status 当 pending 跳过；GET 仍 omitempty 缺席；控制台把缺席画成未上线。不扫库写成 online。不违反条 12。**
6. **`d_gateway` 图类型是 boundary**：本卡 HTTP 形状（handler 解码/编码、503/404）机内可闭环；跨机 `?machine=` 与 CLI 唤起的行为半边归真机清单。不改图类型。
