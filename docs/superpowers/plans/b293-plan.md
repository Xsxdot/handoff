# B293 实现计划

状态：可执行计划；目标分支 cards/B293-charter-6；基线 6a0fb082eded6a6b18aa9f2eb3fc543a15f4daa9。

本计划只安排 U1–U5 的实现与验证，不在本节点修改 Go/TypeScript 实现，不建脚手架。执行者按顺序完成所有 task；每个 task 的文件集合是封闭集合，未列文件不得顺手改动。契约、拆解、原型是本计划的输入：

- docs/superpowers/specs/b293-contract.md：冻结签名、行为与四重闸门。
- docs/superpowers/specs/b293-breakdown.md：L3 轻档、U1–U5 文件边界与拍板。
- docs/superpowers/specs/2026-08-29-b293-isolated-home-carrier-status-design.md：用户故事与原型形态。
- prototypes/b293-carrier-home/：设置页视觉与交互权威；原型目录不属于实现改动范围。
- docs/superpowers/specs/2026-08-21-handoff-instantiation-checklist.md：缺陷族与安全属性清单。

## 0. 基线、查图与总闸

### 已在基线亲自复核的判据

以下命令均在 6a0fb082、干净工作树上实际执行过；实现者开始每个 task 前仍须执行该 task 的最小命令，若命令在当天已改变导致结果不同，以实际输出为准，不把旧结果当新结果。

~~~text
go test ./internal/scheduling/ ./internal/hostapi/ ./internal/proto/ ./internal/client/ ./internal/ledgerstep/ -count=1
=> ok github.com/Xsxdot/handoff/internal/scheduling 0.855s
=> ok github.com/Xsxdot/handoff/internal/hostapi 0.337s
=> ok github.com/Xsxdot/handoff/internal/proto 0.010s
=> ok github.com/Xsxdot/handoff/internal/client 9.525s
=> ok github.com/Xsxdot/handoff/internal/ledgerstep 8.611s

go test ./internal/agentd/ -count=1
=> ok github.com/Xsxdot/handoff/internal/agentd 190.287s

go build ./...
=> exit code 0, no stdout

go vet ./internal/scheduling/ ./internal/hostapi/ ./internal/proto/ ./internal/ledgerstep/ ./internal/client/ ./internal/agentd/
=> exit code 0, no stdout
~~~

Web 基线实际检查结果是 web/node_modules absent，所以 vitest run 本轮未验证；U4 必须先在依赖可用的环境执行 npm test -- --run（或 npx vitest run），不能把未运行的 Web 结果写成通过。

图查询实际结果：cards-B293-charter-3 能定位 PutCarrier、admitInto、ApplyDetect、ProbeHome、WakeHome、三个 host/gateway handler 与 SetupAutomation；cards-B293-charter-5 能定位 startCardStep、handleDispatch、Client.Dispatch。who-calls 已确认 handleCarrierDetect → ApplyDetect、handleHomeProbe → ProbeHome、handleHomeWake → WakeHome、handleCardStep → startCardStep、CLI → Client.Dispatch。当前 codegraph CLI 没有 flow 子命令；实际原文为：

~~~text
Error: unknown command "flow" for "codegraph"
Run 'codegraph --help' for usage.
exit status 1
~~~

因此本计划的有序流程依据已读源码，不拿 chain 冒充 flow；这是图覆盖债，不是实现者可自行改变的架构依据。图领域声明缺失/未扫描入口警告也不改变下面的文件边界。

### 不得破坏的四重闸门

1. Healthy bool 不再是第二真相；四态 pending、online、quota、unreachable 是唯一状态。旧空 status 只按 pending 解释，不扫库迁移。
2. PutCarrier 只保存输入与状态规则，不调用 detect；控制台在新建或 HOME 变化的 PUT 成功后另发一次 detect。
3. 默认 HOME 固定为 ~/.handoff/home/strings.TrimSpace(name)，不能拼 DataDir；~ 只在目标执行机展开。
4. POST /api/host/probe、POST /api/host/wake 可用 ?machine= 一跳转发；POST /api/squads/carriers/{name}/detect 必须在协调机写 registry，不能整段 forward。

### DAG 与交付顺序

单一实现者按以下顺序工作，任何一步红时只修当前 task 的封闭文件集：

~~~text
U1 状态/准入
  ├── U3 HTTP 与检测编排 ── U4 设置页
  └── U5 小队派发 HOME 与 executor
U2 探测/唤起 ───────────────┘
~~~

实际序贯顺序为 U1 → U2 → U3 → U4 → U5；U3 必须等 U1/U2 的对外方法可用，U4 必须等 U3 的 wire 行为稳定，U5 与 U4 无源码依赖但放在最后以便集中跑 agentd 回归。

## Task U1：四态状态机、保存规则与准入

### 文件边界

只允许触及：

~~~text
internal/scheduling/scheduling.go
internal/scheduling/status.go
internal/scheduling/registry_read.go
internal/scheduling/scheduling_test.go
internal/scheduling/status_test.go
internal/scheduling/registry_read_test.go
~~~

### Interfaces

Consumes：

~~~go
type Carrier struct {
    Name string
    Machine string
    CLI string
    HomeDir string
    Model string
    Credential CredentialSource
    MaxConcurrency int
    Status CarrierStatus
    LastError string
}

func (s *Service) PutCarrier(c Carrier, expect int) error
func (s *Service) Carrier(name string) (Carrier, error)
func (s *Service) Admit(req IgnitionRequest) (Binding, error)
func (s *Service) LaunchAdmit(squad string) (Binding, error)
func (s *Service) Release(squad, carrier string) error
~~~

Produces：

~~~go
func (s *Service) ApplyDetect(name string, ev DetectEvidence, detail string) (Carrier, error)
func (s CarrierStatus) Label() string
func DefaultHomeDir(name string) string
func RunCommand(c Carrier) string
~~~

CarrierInput 的 JSON 仍只消费 name/machine/cli/home_dir/model/credential/max_concurrency；不得新增 status、last_error、healthy 的输入字段。删掉 Carrier.Healthy 及其所有 scheduling 读写，不保留兼容翻转。

### 实现动作与精确规则

1. 先跑 go test ./internal/scheduling/ -count=1，记录基线实际输出。先写会经过声明缝的失败断言：PutCarrier 新建/改 HOME/不改 HOME、Admit/LaunchAdmit 状态准入、ApplyDetect 优先级；只为这些锁缝行为跑红绿，不给 CAS 内部复制逻辑另设红绿周期。
2. PutCarrier 在写入前按 expect 读取当前 carrier。expect == 0 时强制新值 StatusPending、LastError = ""。expect > 0 时比较已存 HomeDir 与新值：变化则写 StatusPending 并清空 LastError；未变化则从旧记录保留两者。无论调用方传入何种零值，都不把它翻成 online。继续使用既有 registry CAS，不改变 putEntity 的版本语义。
3. ApplyDetect 以 registry 当前版本做一次 CAS 写；读取失败、解码失败、CAS 冲突都返回带载体名上下文的错误并保留旧状态。状态计算必须是以下顺序：

~~~go
switch {
case !ev.Reachable:
    if previous == StatusOnline || previous == StatusQuota || previous == StatusUnreachable {
        status = StatusUnreachable
    } else {
        status = StatusPending
    }
case ev.Quota:
    status = StatusQuota
case ev.NeedLogin:
    status = StatusPending
default:
    status = StatusOnline
}
~~~

status == StatusOnline 时清空 LastError；其他状态把 detail 原样写入 LastError（空 detail 仍由 omitempty 隐藏）。这使“凭据失效/需登录”回到 pending，“曾上线但不可达”保持 unreachable，而不把 last_error 当准入条件。
4. admitInto 只接受 carrier.Status == StatusOnline 且 slot CAS 成功的成员；空 status、pending、quota、unreachable 一律跳过。没有任何 online 成员返回既有 ErrNoHealthy，有 online 但所有 slot 满返回 ErrNoSlot。不得让 Admit、LaunchAdmit、Release、清队循环写 status 或 last_error；已有 slot CAS 保持原子重试。
5. 更新注释：CarrierStatus 四态的状态转移原因写在导出类型/方法附近；ApplyDetect 注释写明优先级、CAS 与 detail；PutCarrier 注释写明空 status 解释和 HOME 变化清错原因；非显然的“旧记录保留状态”写 why。日志用 slog/项目既有 logger，入口带 name/expect/home_changed/evidence，所有 registry 读写前后带版本，所有错误分支带 cause；不记录 credential 文件内容。

### U1 验收

缝级入口与断言：PutCarrier、Admit、LaunchAdmit、ApplyDetect 都由测试直接调用声明方法；不存在只测私有 helper 顶替缝的情况。

- expect=0 后重新 Carrier 得到 pending，无 last_error。
- HOME 变化后得到 pending 且旧错误被清空；HOME 未变化时 status/error 原样保留。
- 四态逐一覆盖；未知/空存量只能按 pending 处理，不能 online。
- 只有 online 有空槽能绑定；三种非 online 分别跳过；“全非 online”与“online 全满”分别能 errors.Is 到 ErrNoHealthy / ErrNoSlot。
- Healthy 不出现在 Carrier JSON、准入逻辑和源代码使用面；CarrierInput 不接受三类状态键。
- 并发 CAS 失败仍按既有重试预算处理；状态写和 slot 计数不互相覆盖。

测试范围只跑 go test ./internal/scheduling/ -count=1；task 完成后用 go vet ./internal/scheduling/ 和 gofmt -l 检查触及 Go 文件。验收行为不以行数、文件数或覆盖率数字代替。

## Task U2：目标 HOME 只读探测、凭据供给与有时限唤起

### 文件边界

只允许触及：

~~~text
internal/hostapi/hostapi.go
internal/hostapi/probe.go
internal/hostapi/probe_test.go
internal/toolchain/detect.go
internal/toolchain/detect_test.go
internal/agentd/server.go
~~~

server.go 只改 SetupAutomation 的 hostapi 组装行；不得把 toolchain import 放入 hostapi，也不得新增 agentd→maintenance 的 codegraph 边。

### Interfaces

Consumes：

~~~go
type ProbeRequest struct {
    Path string
    CLI string
    Credential string
}

type WakeRequest struct {
    CLI string
    HomeDir string
    Credential string
    Model string
    Timeout time.Duration
}

func New() *Host
~~~

Produces：

~~~go
type ProbeKind string
const (
    ProbeEmpty ProbeKind = "empty"
    ProbeLoggedIn ProbeKind = "logged_in"
    ProbeOccupied ProbeKind = "occupied"
)

type ProbeReply struct { Kind ProbeKind; Detail string }
type WakeOutcome string
const (
    WakeReady WakeOutcome = "ready"
    WakeNeedLogin WakeOutcome = "need_login"
    WakeQuota WakeOutcome = "quota"
    WakeUnreachable WakeOutcome = "unreachable"
)
type WakeReply struct { Outcome WakeOutcome; Detail string }

func (h *Host) ProbeHome(ctx context.Context, req ProbeRequest) (ProbeReply, error)
func (h *Host) WakeHome(ctx context.Context, req WakeRequest) (WakeReply, error)
~~~

新增的组装缝精确为 func NewWithCredentialPathFor(resolve func(string) (string, bool)) *Host；New() 仍保留并使用无凭据解析器，生产 SetupAutomation 传入 toolchain.CredRelPathFor。toolchain 只导出同一张既有表的包装函数：

~~~go
func CredRelPathFor(name string) (string, bool) {
    return credRelPathFor(name)
}
~~~

不复制 .local/share/opencode/auth.json、.grok/auth.json、.codex/auth.json 表；Windows opencode 和 claude 继续返回无文件判据。

### 实现动作与精确规则

1. 先跑 go test ./internal/hostapi/ ./internal/toolchain/ -count=1，记录基线实际输出。写失败测试覆盖 ProbeHome 与 WakeHome 声明缝：不存在/空目录/凭据文件/非空目录、main_home_sync、超时和不调用 RunTurn。
2. ProbeHome 先在目标 Host 进程用 os.UserHomeDir 展开请求路径中的 ~，再 filepath.Join/Clean；不得按协调机路径解释。路径不存在返回 ProbeEmpty；存在目录且 os.ReadDir 无条目返回 ProbeEmpty；目录有条目时仅对注入的 resolver 返回的相对 credential 做 os.Stat，成功才 ProbeLoggedIn，否则 ProbeOccupied。claude 与 Windows opencode 不得返回 logged_in。stat/read 失败必须原样带 path 上下文返回，不创建、删除或覆盖任何目录/文件。
3. credential == "main_home_sync" 时，隔离 HOME 为空且主 HOME 对该 CLI 的表内凭据 stat 成功，返回 logged_in，但 ProbeHome 仍只读隔离路径；不能在 probe 中复制文件。隔离目录非空时不得读主 HOME 代替隔离凭据，也不得清空目录。
4. WakeHome 的第一步按同一探测规则判断隔离目录。仅当结果为 empty 且 credential 为 main_home_sync 时，把 §4 表中的对应文件从主 HOME 拷进目标 HOME，先创建所需父目录，保持私密文件权限；不搬 .config 之外的 skill/rules 树，不复制其它用户文件；claude 没有文件判据时该供给步骤是空操作。occupied 永远不供给、不覆盖。供给成功后再唤起 CLI。
5. 唤起过程必须以 exec.CommandContext/现有 prochost 进程组回收约定启动对应 CLI 的无 prompt、非交互入口；各 CLI 的 argv 依照契约 §9 的实现票授权规则，并在实现测试中锁定实际 argv，不得调用 RunTurn、不得写用户 prompt、不得等待登录交互。Timeout == 0 使用 DefaultDetectTimeout，非零 timeout 使用调用值；上下文取消/超时要杀掉进程组并返回带 CLI、elapsed、cause 的错误，不能遗留孤儿。成功/need_login/quota/unreachable 的映射由已有 CLI 启动结果与探测证据组成，未知结果 fail closed，不伪造成 ready。
6. SetupAutomation 只在构造 Host 时注入 toolchain.CredRelPathFor；保留 s.hostAPI 与 coordinatorRunner 的共用组装关系。hostapi 包不 import toolchain，满足 d_execution → d_maintenance 不新增的拍板。
7. 新/改文件头写职责和边界；导出构造器与 ProbeHome/WakeHome 写参数、返回和“不创建/不交互/目标 HOME 展开”注意事项；外部 stat、copy、exec 前后打结构化日志，日志只写 CLI、目标路径摘要、kind/outcome、耗时和错误，不写 credential 值或 token。

### U2 验收

缝级测试入口为 Host.ProbeHome、Host.WakeHome；HTTP 入口留给 U3，不用 hostapi 私有函数测试顶替。

- 不存在/空目录分别得到 empty；非空无凭据得到 occupied；命中凭据 stat 得 logged_in。
- ProbeHome 全程不 mkdir、write、remove；已有文件字节和目录条目保持不变。
- main_home_sync 的 probe 在隔离 empty、主 HOME 已登录时得到 logged_in 且隔离仍为空；WakeHome 才复制对应表内文件，且不会出现 skill/rules；occupied 不发生复制/覆盖。
- claude 和 Windows opencode 永远不以 logged_in 返回；~ 在 Host 所在目标机展开。
- Timeout=0 使用 30s 常量；短 timeout 可取消；进程组无残留（单测替换进程启动/杀组缝，真机留给协调者）。
- 搜索 hostapi 源码确认不存在 RunTurn 调用；“无 prompt、无交互登录”由 argv/启动测试断言。

测试范围只跑 go test ./internal/hostapi/ ./internal/toolchain/ -count=1；再跑 go vet ./internal/hostapi/ ./internal/toolchain/。U2 不跑全仓测试。

## Task U3：协议投影、HTTP 检测编排与跨机唤起

### 文件边界

只允许触及：

~~~text
internal/agentd/schedapi.go
internal/agentd/hostprobe.go
internal/agentd/forward.go
internal/agentd/schedapi_test.go
internal/agentd/hostprobe_test.go
internal/proto/scheduling.go
internal/proto/contract_fixture_test.go
web/src/api/scheduling.ts
web/src/api/contract.test.ts
web/src/api/testdata/SquadsResp.json
~~~

forward.go 只复用/必要时补注释 forwardJSON，不改通用转发语义；不要把 handleCarrierDetect 接到 forwardIfRequested。

### Interfaces

Consumes：

~~~go
func (s *Server) handleHomeProbe(w http.ResponseWriter, r *http.Request)
func (s *Server) handleHomeWake(w http.ResponseWriter, r *http.Request)
func (s *Server) handleCarrierDetect(w http.ResponseWriter, r *http.Request)
func (s *Server) handleCarrierPut(w http.ResponseWriter, r *http.Request)
func (s *Server) carrierView(c scheduling.Carrier, version int) proto.CarrierView
func (s *Server) forwardJSON(r *http.Request, name string, c *client.Client, token string, body []byte) (int, http.Header, []byte, error)
~~~

Produces（Go wire types）：

~~~go
type CarrierView struct {
    Name string
    Machine string
    CLI string
    HomeDir string
    Model string
    Credential string
    MaxConcurrency int
    Status string
    LastError string
    Version int
}

type HomeProbeReq struct { CLI string; Path string; Credential string }
type HomeProbeResp struct { Kind string; Detail string }
type HomeWakeReq struct { CLI string; HomeDir string; Credential string; Model string }
type HomeWakeResp struct { Outcome string; Detail string }
type CarrierDetectResp struct { Name string; Status string; LastError string; Version int }
~~~

JSON projection requirements for the preceding types are exact: carrier fields use name/machine/cli/home_dir/model/credential/max_concurrency/status/last_error/version; HomeProbeReq uses cli/path/credential with empty credential omitted; HomeWakeReq uses cli/home_dir/credential/model with empty credential and model omitted; replies use kind/detail or outcome/detail; detect uses name/status/last_error/version.

Produces（TS API）：

~~~ts
export type CarrierStatus = "pending" | "online" | "quota" | "unreachable"
export type ProbeKind = "empty" | "logged_in" | "occupied"
export type WakeOutcome = "ready" | "need_login" | "quota" | "unreachable"
export function probeHome(input: HomeProbeReq): Promise<HomeProbeResp>
export function wakeHome(input: HomeWakeReq): Promise<HomeWakeResp>
export function detectCarrier(name: string): Promise<CarrierDetectResp>
export function getCarrierRunCommand(name: string): Promise<CarrierRunCommandResp>
~~~

### 实现动作与精确规则

1. 先跑 go test ./internal/proto/ ./internal/agentd/ -run 'TestContractFixtures|TestCarrierRunCommandThroughWire|TestHomeProbe|TestHomeWake|TestCarrierDetect' -count=1；若过滤结果因现有测试命名不同为空，保留原始输出并改用 go test ./internal/proto/ ./internal/agentd/ -count=1，不得把未命中的过滤器当通过。Web 侧在依赖可用后先跑现有 npm test -- --run，基线已知未验证。
2. 删除 CarrierView.Healthy 及 TS healthy，保留 status/last_error 的 omitempty 投影；carrierView 手写映射必须逐字段包含 status、last_error、version，空字符串不编码。CarrierInput 仍不含三类状态字段。更新 Go fixture、TS fixture 与 contract test，逐个断言四态和空字段缺席。
3. 保留 handleHomeProbe/handleHomeWake 的解码、?machine= 先转发、Host 调用和错误码：未知 machine=400，forward 失败=502，Host unavailable=503，成功=200。HomeWake 必须把 credential 传到 WakeRequest；不要在 handler 中补默认 credential 或重写 home_dir。
4. 把 handleCarrierDetect 写成协调机编排：先从 scheduling registry 读 carrier；本机 machine 为空、local 或 本机 时直接调用 s.hostAPI.WakeHome；其他 machine 取 s.pool.For(machine) 与目标 token，构造路径为 /api/host/wake、body 为精确 HomeWakeReq 的 JSON，调用已有 forwardJSON，让目标端的 host handler 本地处理。远程错误状态原样作为可诊断的检测失败，不重试，不把 detect 自身 forward。目标响应反序列化为 HomeWakeResp 后按以下 whitelist 映射：

~~~go
switch resp.Outcome {
case string(hostapi.WakeReady):
    ev = scheduling.DetectEvidence{Reachable: true}
case string(hostapi.WakeNeedLogin):
    ev = scheduling.DetectEvidence{Reachable: true, NeedLogin: true}
case string(hostapi.WakeQuota):
    ev = scheduling.DetectEvidence{Reachable: true, Quota: true}
case string(hostapi.WakeUnreachable):
    ev = scheduling.DetectEvidence{Reachable: false}
default:
    return 502, fmt.Errorf("未知 wake outcome %q", resp.Outcome)
}
~~~

未知 outcome 必须 fail closed，不能套 default online。随后只在协调机调用 ApplyDetect(name, ev, detail)，再从 CarrierRows 取得当前版本，返回 CarrierDetectResp。ApplyDetect 错误按现有 ErrDetectUnwired/registry 错误映射，错误 body 带 name、machine、outcome 上下文但不带 credential。
5. RunCommand route 继续只从 server carrier 生成 HOME=已存 home_dir 加空格再接已存 cli，客户端不得 join、trim、展开 ~；不存在载体返回 404。PUT 成功路径只 PutCarrier，不触发 WakeHome/ProbeHome/ApplyDetect。
6. 为每个 hand projection 添加注释：协议投影不推导 status、不把空值变成 online；detect 编排注释写“registry 属协调机、host wake 属目标机”的边界；转发错误、目标 JSON 解码错误、ApplyDetect/CAS 错误各有结构化日志。入口日志包含 path/name/machine，外部 wake 前后包含 target/status/elapsed，成功返回也记录 status/version；不记录凭据。

### U3 验收

Go 缝级测试必须从 Handler() 发 HTTP 请求，覆盖以下真实入口；不能只调用 carrierView、mapWakeOutcome 私有 helper：

- /api/host/probe 空机、本机与非空 machine 的 body/status；非空 machine 的本机测试 handler 不触碰本地临时目录。
- /api/host/wake 的 credential roundtrip、empty credential 缺席、forward 失败 502、unknown machine 400。
- /api/squads/carriers/{name}/detect 本机 wake→ApplyDetect 写协调机状态；远程 wake 只发 /api/host/wake 并在协调机 registry 写状态；请求本身不走 detect forward；WakeReady/NeedLogin/Quota/Unreachable 四种 outcome 各锁定结果；未知 outcome 不能得到 online。
- PUT 请求无 status/last_error/healthy；PUT 成功没有 hostapi 调用；GET 空 status/last_error 分别缺席，非空按原值出现。
- run-command 返回精确 server 字符串，载体不存在 404。

序列化接缝在本 task 必须穿过真实 json.Marshal/json.NewDecoder 与 Handler，不允许只做两端各自的结构体测试。范围只跑 go test ./internal/proto/ ./internal/agentd/ -count=1 及触及的单测过滤；Web 只跑 web 自身 Vitest。完成后再跑对应 go vet。

## Task U4：设置页四态、默认 HOME、探测按钮与运行命令

### 文件边界

只允许触及：

~~~text
web/src/app/settings/SchedulingPage.tsx
web/src/app/settings/SchedulingPage.test.tsx
web/src/api/scheduling.ts
web/src/api/contract.test.ts
web/src/api/testdata/SquadsResp.json
~~~

### Interfaces

Consumes：

~~~ts
export function getSquads(): Promise<SquadsResp>
export function putCarrier(input: CarrierInput, expect: number): Promise<VersionResp>
export function probeHome(input: HomeProbeReq): Promise<HomeProbeResp>
export function detectCarrier(name: string): Promise<CarrierDetectResp>
export function getCarrierRunCommand(name: string): Promise<CarrierRunCommandResp>
~~~

Produces：

~~~ts
type CarrierView = {
    name: string; machine: string; cli: string; home_dir: string;
    model?: string; credential: string; max_concurrency?: number;
    status?: CarrierStatus; last_error?: string; version: number;
}
type CarrierDraft = CarrierInput & { homeAuto: boolean }
~~~

### 实现动作与精确规则

1. 依赖存在前，先记录 Web 基线状态；依赖安装/缓存不可用时保持“未验证”，不伪造测试结果。依赖可用后先跑 npm test -- --run，再给 U4 写失败用例；测试只使用现有 React Testing Library/Vitest harness 和已有 SchedulingPage.test.tsx 的 API mock 形态。
2. 更新 API 类型与请求函数：probeHome/wakeHome 只在输入 credential 非空时写 credential；probe/wake 的 machine 只进入 query，使用 encodeURIComponent；detect 和 run-command 的 name 只由 API 层编码；客户端不拼 run-command。status 作为可选 wire 字段消费，渲染前 row.status ?? "pending"。
3. 新建 carrier 时，home_dir 初值为 defaultHomeDir(name)；name 改动时仅在 homeAuto == true 且当前 HOME 仍等于旧默认值时跟随，用户手工编辑 HOME 后将 homeAuto=false，后续 name 改动不得覆盖用户值。默认函数精确返回 ~/.handoff/home/trim(name)，空白 name 返回空。
4. 编辑框在 HOME/CLI/credential/machine 变化时以当前 draft 调 probeHome，显示 empty、logged_in、occupied 三类明确提示和错误原文摘要；请求期间禁用重复 probe，响应过时不得覆盖更新后的 draft。不得把 probe 结果改写成 status；状态只能来自 GET 或 detect response。
5. 保存时记录旧 name/home/status；先调用 putCarrier(input, expect)，只有 PUT 成功且是新建或 home_dir 相对旧值变化时，立即调用一次 detectCarrier(name)，随后刷新列表。PUT 失败不发 detect；保存已存在且 HOME 未变不发 detect。detect 失败在对话框/行内显示可行动错误，不把失败渲染成 online。
6. 列表使用四态药丸和冻结中文名：pending=未上线、online=已上线、quota=限额中、unreachable=不可达；last_error 非空可见，空不占位。派发/运行按钮的展示不读 healthy。运行按钮调用 getCarrierRunCommand，将返回字符串原样交给 navigator.clipboard.writeText，成功/失败有可见提示；不得浏览器侧拼 HOME=，不得让浏览器展开 ~。
7. 新增/修改组件注释说明“保存不检测、保存后的检测是第二次请求”“~ 由目标机解释”；网络动作入口、请求开始/成功/失败使用项目现有的结构化 console 事件对象（event、name、machine、status、elapsed），不打印 credential/token，不静默吞错误。测试用真实按钮/input/API mock 入口，不直接调用私有 formatter。

### U4 验收

入口到缝对照：测试从设置页按钮、name/HOME input、save、run button 进入 API mock；不存在只测 label helper 顶替 UI 缝。

- 新建默认 HOME 和 name 跟随规则准确；手工 HOME 后 name 不覆盖；空 name 默认空。
- 改 HOME 会把 machine query、CLI、path、credential 发给 probe；probe 结果三态可见，失败可见且不伪造 carrier status。
- 新建/HOME 变化的 PUT 成功后恰好一次 detect；PUT 失败、旧 HOME 未变不 detect；PUT 本身不 wake。
- 四态中文标签准确；status 缺席按 pending；last_error 非空可见、空不出现。
- run button 使用服务端原串写 clipboard；客户端没有拼接或改写命令；API 错误可见。
- API contract 测试断言 status/last_error/healthy 的序列化边界、ProbeKind/WakeOutcome union 值与 query 编码。

范围只跑 web 的 Vitest 文件（SchedulingPage.test.tsx、contract.test.ts）；不得把 Go 全量测试归入 U4。依赖缺失时验收标“未验证”，由协调者决定真机/依赖环境。

## Task U5：小队 dispatch 的可空 HOME 与 executor 运行边界

### 文件边界

只允许触及：

~~~text
internal/agentd/cardstep.go
internal/agentd/manager.go
internal/agentd/server.go
internal/ledgerstep/dispatch.go
internal/ledgerstep/dispatch_test.go
internal/client/client.go
internal/client/client_test.go
internal/executor/codex/taskenv.go
internal/executor/codex/proc.go
internal/executor/codex/taskenv_test.go
internal/executor/grok/taskenv.go
internal/executor/grok/authsync.go
internal/executor/grok/proc.go
internal/executor/grok/taskenv_test.go
internal/executor/grok/authsync_test.go
~~~

scheddrain.go 只读核对，不改：协调者拉起已把 carrier.HomeDir 写入 SessionSpec。cmd/card_dispatch.go、internal/agentd/forward.go、internal/agentd/scheddispatch.go 只读核对现有透传，不纳入改动。

### Interfaces

Consumes：

~~~go
type DispatchOpts struct {
    // existing fields...
    HomeDir *string // nil=字段缺席；指向空串=显式空值
}
type Transport func(ctx context.Context, opts DispatchOpts) (taskID string, err error)
func (d *Dispatcher) ViaTemplate(ctx context.Context, c ledger.Card, req TemplateDispatch) (DispatchResult, error)

type DispatchReq struct {
    // existing fields...
    HomeDir *string // nil=字段缺席；指向空串=显式空值
}
func (m *Manager) Dispatch(ctx context.Context, req DispatchReq) (*proto.Task, error)

type dispatchRequest struct {
    // existing fields...
    HomeDir *string
}
func (s *Server) handleDispatch(w http.ResponseWriter, r *http.Request)

func serveSpec(repoPath, taskDir string, port int, env []string) prochost.Spec
func EnsureAuthLink(homeDir string) error
func SyncAuthToAuthority(homeDir string, log *slog.Logger) error
~~~

dispatchRequest.HomeDir 的 JSON 名称固定为 home_dir 且使用 omitempty；其余字段沿既有 POST /api/tasks 契约。Produces：

~~~go
// startCardStep 组装 Dispatcher 时：binding.Squad != "" 才读取 carrier。
func (s *Server) startCardStep(cardID string, req proto.CardStepReq) error
// ViaTemplate 的 Transport opts.HomeDir 逐字节传给 stepTransport/client.Dispatch。
func (c *Client) Dispatch(ctx context.Context, opts DispatchOpts) (*proto.Task, error)
// manager 只在 req.HomeDir != nil && *req.HomeDir != "" 时为 executor 增加 HOME。
~~~

### 实现动作与精确规则

1. 先跑 go test ./internal/ledgerstep/ ./internal/client/ ./internal/agentd/ ./internal/executor/codex/ ./internal/executor/grok/ -count=1，记录真实输出。先加唯一的跨边界红测：从 startCardStep 注入 binding，到 Dispatcher.ViaTemplate 捕获 DispatchOpts.HomeDir，再经过 client 的真实 JSON body，断言 nil、指向空串、指向非空三态；随后才做 adapter 内部 env 断言。
2. 在 startCardStep 组装 binding 后，若 binding.Squad != ""，调用 s.scheduling.Carrier(binding.Carrier)，取 carrier.HomeDir，把同一个字符串指针放入新 Dispatcher.HomeDir 字段；无 squad 时 Dispatcher.HomeDir=nil。不把 HomeDir 加入 Binding，不把 status/error 写入 dispatch。错误分支带 card/squad/carrier 上下文并先释放已准入资源。
3. Dispatcher.ViaTemplate 构造 DispatchOpts 时把 d.HomeDir 原指针放入；stepTransport 现有 opts.HomeDir → client.DispatchOpts.HomeDir 保持直通。Client.Dispatch 的手搭 map 只在指针非 nil 时写 "home_dir": *opts.HomeDir：nil 不出现，指向空串写 JSON "home_dir":""，非空写精确原串；禁止编码为 null、trim、join 或展开 ~。已有 dispatchRequest、handleDispatch、DispatchReq 只做字段透传。
4. 在 Manager.Dispatch 调用 ad.Start 前，加入一个有注释的环境合成步骤：req.HomeDir == nil 或 *req.HomeDir == "" 时原样保留 envKVs，绝不覆盖现有 process HOME；非空时移除 envKVs 中已有的 HOME 行，再追加一行 HOME 加载入的载体路径，确保 carrier HomeDir 赢过 env 文件 HOME。日志只记录 home_override 布尔值与路径摘要，不记录凭据。不得修改通用 env JSON/协议。
5. codex 保持“普通派发丢 CODEX_HOME”旧安全边界，但载体 HOME 非空时走显式例外：serveSpec 从 env 中识别非空 HOME，仅在该分支从 dropped 集合移除 CODEX_HOME，然后保留传入 CODEX_HOME；无 carrier HOME 时现有 CODEX_HOME 丢弃测试继续通过。HOME 的最终值只保留 manager 的显式 carrier 行。
6. grok 保持任务级 grokhome 与 GROK_HOME 保护变量；当 env 含非空 carrier HOME 时，StartServe/EnsureAuthLink/SyncAuthToAuthority 的权威 .grok/auth.json 必须定位到该隔离 HOME 的 .grok/auth.json，不能再默认指向机器主 HOME。为此在 grok 内增加一个不破坏既有调用者的“带 authority home”私有路径 helper，并让 StartServe 从其 env HOME 传入；旧的无 HomeDir 单测仍使用测试 HOME。任务级 taskDir/grokhome/auth.json 仍是软链/权限隔离目标；只有本轮 main_home_sync 供给已经把文件拷进载体 HOME 时，才允许它间接代表主 HOME 凭据，不能自行指回主 HOME。
7. codex/grok 每个改动的文件头、导出函数和非显然过滤/权威选择逻辑补职责、参数、返回和 why 注释。adapter 启动前后、env 覆盖冲突、auth link 建立/修复/同步错误全部用 slog 带 task/home/keys/cause；不打 env 值、auth 内容、token。StartReq 不扩字段，保持 HOME 的唯一新入口是已冻结 home_dir。

### U5 验收

缝级入口必须覆盖：startCardStep、Dispatcher.ViaTemplate、Client.Dispatch、handleDispatch、Manager.Dispatch/adapter Start 的真实传输链；内部 serveSpec 测试只能作为附加锁，不能替代三态 wire 测试。

- nil、空指针、非空指针的 /api/tasks JSON 分别表现为字段缺席、"home_dir":""、精确非空字符串；解码到 DispatchReq 后保持同态。
- 无 squad 的 startCardStep 不读取 carrier、不写 home_dir、不改变目标进程 HOME；空值同样不覆盖 HOME。
- 非空 carrier HomeDir 覆盖 env 文件 HOME，executor 子进程最终 HOME 精确等于载体字符串，字符串可含 ~；协调者/客户端不展开。
- codex 无载体 HOME 时仍丢 CODEX_HOME；有载体 HOME 时保留 CODEX_HOME，不回落到用户 ~/.codex。
- grok grokhome/permission_mode 仍生成，auth link 权威为隔离载体 HOME；普通旧路径测试不回归；除 main_home_sync 供给外不指向机器主 HOME。
- 现有 hostapi.buildEnv 的 HomeDir 赢过 req.Env HOME 行的测试保持通过；不要用 adapter 逻辑替代该 hostapi 判据。

范围先跑上述五个触及包，再跑 go vet ./internal/ledgerstep/ ./internal/client/ ./internal/agentd/ ./internal/executor/codex/ ./internal/executor/grok/、gofmt -l；不在 U5 内跑全仓测试。

## 贯穿序列化边界清单

下面每个字段都必须至少有一条真正穿过产生端→手写投影→消费端的回归断言；两端各自解析的单测不能替代它。可空 home_dir 必须用 pointer/三态断言，不能把空字符串误当缺席。

| 字段 | 产生→消费的每一处手写投影 | 必须锁的行为 |
|---|---|---|
| status | scheduling Carrier JSON → carrierView → proto CarrierView → web/src/api/scheduling.ts → SchedulingPage | 四态原值；存量空 status 在 UI/准入按 pending；空仅在 wire 缺席 |
| last_error | ApplyDetect/PutCarrier → carrierView → proto JSON → TS row | 非空出现且原文可见；空不出现；不参与准入 |
| home_dir | Carrier → startCardStep/Dispatcher → ledgerstep DispatchOpts → client map → POST /api/tasks → dispatchRequest → DispatchReq → Manager env | nil 缺席、指向空串显式空、非空精确值；不得 trim/join/null；空不覆盖 HOME |
| credential | TS HomeWakeReq → JSON → handleHomeWake → WakeRequest → WakeHome；main_home_sync fixture | 非空传递；空因 omitempty 缺席；main_home_sync 只在 WakeHome 供给 |
| ProbeKind | Host ProbeReply → HomeProbeResp → TS union/提示 | empty/logged_in/occupied 原值；未知不默认 logged_in |
| WakeOutcome | Host WakeReply → HomeWakeResp → detect handler evidence → CarrierDetectResp → TS status | ready/need_login/quota/unreachable 映射正确；未知 fail closed，不 online |

## 五族缺陷对抗审查与门禁

实现者在收口前逐条回答以下问题，答案写进最终验收记录或 task 相关测试注释；不能用“测试通过”替代行为解释。

1. 生命周期/状态中断：agentd 重启、CAS 冲突、超时、进程被杀、remote 502 时是否仍为可见 pending/unreachable，是否有 orphan？U1 的 CAS、U2 的 context/进程组、U3 的“不重试且不写 online”、U5 的 Start 失败路径分别锁定；Web 不做 polling，用户可重试 detect。
2. 静默失败/误导错误：空 status 是否误称 online，last_error 是否偷当健康位，未知 enum 是否默认成功，forward/credential 错误是否被吞？所有错误分支带 name/machine/task/cause；unknown outcome/probe kind fail closed；UI 显示错误和 actionable 登录命令。
3. 跨平台假设：~ 是否由 target 展开，Windows opencode/claude 是否不伪造凭据，进程组回收是否遵循现有 unix/other 约定，浏览器是否不展开 HOME？U2 单测/协调者真机清单覆盖；无法在当前环境运行 Windows 的结果标“未验证”。
4. 假红/假绿：测试入口是否真的穿过 Handler、Dispatcher/client map、真实 JSON 和设置页按钮，是否只测私有 helper/fixture 字段？U3/U4/U5 接缝矩阵逐项对照；npm 缺依赖只记未验证。
5. 闸门绕过：PUT 是否偷偷 detect，detect 是否整段 forward，non-online 是否仍 admission，nonempty HOME 是否被 env 文件压回，occupied 是否被覆盖，hostapi 是否 import maintenance？代码审查配合 rg 与测试锁定。

### 类型白名单

Go 与 TS 对 pending|online|quota|unreachable、empty|logged_in|occupied、ready|need_login|quota|unreachable 都使用显式 union/常量。未知值只能产生错误或 pending 的保守显示，不能进入 online/ready 分支。状态比较必须用 StatusOnline，不得残留 Healthy。

### 接缝双向覆盖表

| 声明缝 | 至少一支缝级测试入口 |
|---|---|
| Service.PutCarrier | U1 PutCarrier 保存行为表 |
| Service.Admit / LaunchAdmit | U1 真实准入测试 |
| Service.ApplyDetect | U1 状态表；U3 Handler detect 写 registry |
| Host.ProbeHome / Host.WakeHome | U2 Host 测试；U3 Handler HTTP 测试 |
| handleCarrierDetect | U3 Handler() 本机/远程 detect |
| carrierView/协议 JSON | U3 GET contract roundtrip |
| SchedulingPage buttons/inputs | U4 Testing Library 真实事件 |
| startCardStep → dispatch transport | U5 三态 wire roundtrip |
| Manager.Dispatch → executor env | U5 manager/adapter boundary test |

表中每条缝都被至少一支测试锁住；内部 helper 测试只能附加，不能顶替。退路不改变入口：没有“若意外先绿就直喂 helper”的条件分支。

## 协调者真机清单

以下项目属于外部 CLI、目标机器、真实浏览器/clipboard 或 Windows 环境，标记为“本 task 由协调者执行，不派发”，实现者不得以本地 fake 结果代替：

1. 四种 CLI 在真正 blank HOME 下启动一次，确认各自会落自己的初始化文件；无 prompt、无交互登录、30s 超时和进程组回收。
2. 两台机器之间 probe/wake 的 ~、文件系统和 detect registry 归属；目标机不可达时协调机状态符合 pending/unreachable 规则。
3. occupied HOME 保留原文件；main_home_sync 空隔离 HOME + 主 HOME 已登录时 probe logged_in，WakeHome 后只出现 §4 凭据文件，不出现 skill/rules。
4. claude 永不 logged_in；Windows opencode 永不 logged_in；Windows ~、RunCommand 的 HOME= 串与实际目标 CLI 行为。
5. 在线→断网为 unreachable、恢复后再 detect 为 online；quota 账号可复现则验证 quota，否则明确记录未验证。
6. 真实 codex/grok 载体 HOME 使用隔离凭据；grokhome 仍存在且非 main_home_sync 时不指回机器主 HOME。
7. 设置页真实 webview clipboard 能复制 server 原串。

## 收口自审、台账与提交

收口前执行者必须：

~~~text
rg -n "Healthy|healthy" internal/scheduling internal/agentd/schedapi.go web/src/app/settings web/src/api
rg -n "home_dir|status|last_error|credential|ProbeKind|WakeOutcome" internal/scheduling/scheduling.go internal/scheduling/status.go internal/agentd/schedapi.go internal/agentd/hostprobe.go internal/proto/scheduling.go web/src/api/scheduling.ts web/src/app/settings/SchedulingPage.tsx internal/ledgerstep/dispatch.go internal/client/client.go internal/agentd/server.go internal/agentd/manager.go
gofmt -l internal/scheduling/scheduling.go internal/scheduling/status.go internal/scheduling/registry_read.go internal/agentd/schedapi.go internal/agentd/hostprobe.go internal/proto/scheduling.go internal/ledgerstep/dispatch.go internal/client/client.go internal/agentd/server.go internal/agentd/manager.go internal/executor/codex/taskenv.go internal/executor/codex/proc.go internal/executor/grok/taskenv.go internal/executor/grok/authsync.go internal/executor/grok/proc.go
git diff --check
go build ./...
go vet ./internal/scheduling/ ./internal/hostapi/ ./internal/proto/ ./internal/ledgerstep/ ./internal/client/ ./internal/agentd/ ./internal/executor/codex/ ./internal/executor/grok/
~~~

rg Healthy 只允许命中历史注释/冻结 fixture 的迁移说明，不能有实现读取；命令输出若失败必须原样记录，不归因。Web test 若依赖仍缺失写“未验证”。

每确立一个事实、每跑一个命令、每放弃一条尝试，都追加到 docs/superpowers/specs/b293-ledger.md，包含日期、命令、原始关键输出、判断与未验证项；计划与台账同批提交。

计划自审结论：spec 故事已映射到 U1（状态/准入）、U2（探测/供给/唤起）、U3（wire/detect/run-command）、U4（控制台交互）、U5（小队 HOME/executor）；无跨卡 Produces/Consumes 不一致。每个 task 文件集合有界；未使用骨架测试例外；未使用未决占位语句；无新增 agentd 竖切。跨卡审计不适用本 L3 轻档单卡序贯计划，仍按上表逐条做接缝双向审计。

完成计划与台账后，执行者必须 git add docs/superpowers/plans/b293-plan.md docs/superpowers/specs/b293-ledger.md 并创建提交，不 push。提交消息建议：docs(B293): add implementation plan。最后输出一行 handoff-verdict JSON（pass），再输出本回合规定的 branch/commit JSON；不得输出实现代码。
