# B462 实现计划：本机构建换版 `handoff upgrade --from`

- 状态：**待拍板**（2026-10-22 plan subagent 出稿 r1；主会话拍板后逐条回写「待拍板」并改本行为「已拍板（日期）」）。
- 输入：`docs/superpowers/specs/b462.md` r1（L2，形态甲：本地盯）；台账 `docs/superpowers/specs/b462-ledger.md`（「plan」节 F1–F7 是本稿全部代码事实的取证记录，命令与关键输出都在那里）。
- 基线：分支 `cards/B462-charter-1` @ `ab03d907`（代码 = origin/main `d905c3ab`）。
- 读者：零上下文执行者。故事编号沿用 spec：S1–S5。行号以基线为准，漂移时以符号名为准。

---

## 待拍板（出稿者推荐，主会话裁决）

每条给推荐（**荐**）与理由。全部可由主会话定，无需回人；Q8 偏离了 spec 的字面写法，拍板时请明确。

1. **Q1 本地目录来源的实现机制。荐 甲：复用 `release.Installer`，只换传输层。**
   - 甲：在 `internal/release` 新增「读目录得出 `Release`」的函数。它扫描目录，资产 URL 写成 `file:///<文件名>`；再配一个 `Installer`，其 HTTP transport 用 `http.NewFileTransport(http.Dir(dir))`。`Fetch` / `FetchArchive` / `FetchChecksum` 一行不改，原样复用：sha 校验、`sumFor` 解析、缺资产报错（错误文本 `发布 <tag> 没有 <os>/<arch> 的资产（<资产名>）`，install.go:123）和自检。S2 要求的「点名缺哪个资产」「checksums 缺行或 sha 不符拒绝推送」都由既有代码给出，不新写一行判据。
   - 乙（否）：在 cmd 写一个直接 `os.ReadFile` 的 fetcher。`sumFor` 未导出，要么导出它，要么复写校验逻辑，形成第二套信任链实现。
   - 甲若在首个测试里撞到 `http.FileServer` 的意外行为（重定向、Content-Type 之类），执行者可在 `internal/release` 包内改用乙的形态，前提是包内复用 `sumFor` 与 sha 校验、不复写；属执行者权限，写进交回物的偏离节。

2. **Q2 S3（换版后账本可读）的适用面。荐：所有来源（release 与 `--from`）、远端与本机，同一判据；控制台升级路径行为不变。**
   - 理由：「换版后账本没挂上」与二进制从哪来无关。spec S2 要求两种来源的「闸门、判决、输出」逐字一致，只给 `--from` 加检查会让两种来源分叉。
   - 远端：判据收进 `internal/upgrade.RemoteOne`（该包已是「唯一判据来源」，machine.go:1-14）。CLI 填入换版前的探测结果；控制台路径（internal/agentd/machineupgrade.go:56）不填，即「未知 → 不检查」，行为一字不变。
   - 本机：`localUpgrade` 今天触发重启后**不等新版上线**（cmd/upgrade.go:603-610）。S3 要求「每台机器」，所以换版前账本可读时，本机路径在重启后补一段：先 `WaitVersion`（推送时限 120s），再探针。换版前不可读或未知时，保持今天的行为不变。

3. **Q3 账本可读探针端点。荐 `GET /api/disciplines`。**
   - 经 `withLedger` 守卫：未挂载时回 503（ledgerapi.go:83-93）。挂载时只执行一条 `SELECT DISTINCT name FROM disciplines`（disciplines.go:93），是真读账本、只读、轻量的请求。
   - 否 `GET /api/cards`：读全量卡、工单计数和运行锁（ledgerapi.go:199-240），太重。
   - 否 `GET /api/ledger/health`：恒回 200，未挂载时以 `enabled:false` 表示（1097-1100），挂载时还要读镜像健康全表。spec 要求「未挂载时必定失败」，它不满足。
   - 本卡在 `internal/client` 为这个**既有端点**新增一个方法（不改 wire）。
   - 映射：200 → 可读；503 → 不可读；404 → 对端过旧（不支持），按未知处理；其余（连不上、401、5xx）→ 错误。
   - 503 被解读为「账本未挂载」有一个前提：同一轮里刚看到 status 回 200，即启动闸已放行（bootgate.go:49-60）。换版前的探针紧跟 status 探测，换版后的探针紧跟 `WaitVersion` 成功，两处都满足这个前提。

4. **Q4 S3 四格中「探针够不着」的处置。荐：**
   - 换版前探针出错或 404：记为「未知」，不检查。理由是不为一次网络抖动拦住整次升级，且与「换版前就不可读则不检查」同向。
   - 换版后探针出错（不是 503）：按等待间隔（2s）重试至多 3 次，仍出错则该机判 **fail**，原因写「新版已上线，但账本是否挂载未能确认：<错误原文>」。理由：换版前可读、换版后说不清，属于 S3 要守的那一格，报成功等于「主张不是事实」（client/update.go:166-169 的同款纪律）。
   - 换版后探针回 503：立即判 fail，原因写「新版已上线但账本未挂载」，处置写「看该机 agentd 日志（自登记/账本挂载失败原因）」，并附 `prev:` 路径。

5. **Q5 S4 自登记重试期间已打开的账本句柄。荐 保留 `lst`，只重试失败的那一步。**
   - 失败步骤指 `SelfRegister`，或在其后失败的 `ImportLocalTargets`；两者都是幂等 upsert/跳过（selfregister.go:99-186）。
   - 瞬时错误发生在取连接或事务里，池会在下次调用时自己重新建连。关掉重开要重走 `ensureSchema`，多占建库短连接，还要再抢 schema 锁，在 `too many clients` 场景里是加压。
   - 停机（ctx 取消）放弃重试时，必须先 `lst.Close()` 再返回 `ctx.Err()`；调用方据 `bootCtx.Err()` 按优雅关停退出（cmd/agentd.go:316-320）。

6. **Q6 打包开关形态。荐 `scripts/build-deploy.sh --pack <输出目录> <os/arch> [<os/arch>…]`。**
   - 至少给一个平台；缺平台或缺目录退出 2。
   - 输出目录必须不存在或为空，否则退出 2。理由：目录里留着旧 tag 的包，会被 S2 的「多个 tag 拒绝」挡住，不如在打包侧就拦下。
   - 不带 `--pack` 时，现有两种用法（无参、单个 `os/arch` + `OUT=`）的行为逐字节不变。

7. **Q7 打包的「脏树」判据。荐：已跟踪改动（与 STAMP 同一条 `git status --porcelain --untracked-files=no`），外加未被忽略的未跟踪 `*.go` 文件与 `web/` 下的未跟踪文件，任一非空即拒绝。**
   - 理由：未跟踪的 `.go` 会被编进二进制，`web/` 的未跟踪文件会进前端产物，两者都让「部署物对应一个提交」失真。
   - 其余未跟踪文件不影响产物，例如仓根的 `.playwright-mcp/`（主工作树当前就有）。拒绝它们只会逼人去删无关目录。
   - 检查必须在前端构建**之前**做，让拒绝在一秒内返回。

8. **Q8 真机隔离托管实例的形态（偏离 spec 字面，需拍板）。**
   - linux-01：**荐系统级 transient unit**，不用 spec 写的 `systemd-run --user`。命令形如 `systemd-run --unit=handoff-b462-iso -p Restart=always -p RestartSec=3 -p KillMode=process --collect <bin> agentd --config <iso.yaml>`。
     - 理由：linux-01 上 root 的 `Linger=no`（台账 F6），`--user` 管理器随 ssh 会话退出而停，transient unit 会被一并带走。
     - 在役 `handoff-agentd.service` 本身就是系统级，测试实例与它同形态更有代表性。
     - spec 要求的是「INVOCATION_ID 生效、exit 0 后被拉起」，系统级 unit 同样满足：systemd 对每个 service unit 注入 `$INVOCATION_ID`，`Restart=always` 在 exit 0 时也会拉起。
   - 本机 macOS：**荐临时 plist + `launchctl bootstrap gui/$UID`**，不用 `launchctl submit`。
     - plist 与生产模板同形：`KeepAlive=true`（launchd.go:116），Label 用独立名 `dev.gosuper.handoff.b462-iso`。
     - 理由：`man launchctl` 对 submit 只说「keep alive in the event of failure」，exit 0 后是否拉起没有写明。plist 的 KeepAlive=true 拉起 exit 0 的进程，是生产实测过的行为（launchd.go:98-99）。
   - 两处托管判据都是 `IsManaged`（managed.go:34-45）。上线后用 `handoff status` 的 `update.managed=true` 复核，不按推断判定。

9. **Q9 推送等待超时的原因文案。荐 追加 `WaitVersion` 的错误原文。**
   - 今天推送超时只报「已换版但新进程未在 120s 内上线」（remote.go:240），把错误原文丢了。S4 之后，对端若还在重试自登记，原文正是「agentd 未就绪（账本未挂载：自登记第 N 次重试…）」。spec S3 要「超时如实报」。
   - 改动对 release 来源同样生效：只在原因后加一段原文，前缀措辞与回滚处置不变，既有测试（`TestUpgradeReportsUnconfirmedRestart`、`TestRemoteOneWaitFailureWordingDiffersByMode`）的断言不受影响。

---

## 1. 交付目标（S1–S5 映射）

本卡单轮交付全部五个故事，不扇出（单子系统 L2，各段之间只有施工排序）。

| 故事 | 本轮做到 | 留待后续（spec Out of Scope） |
|---|---|---|
| S1 打包模式 | `build-deploy.sh --pack`：每个平台一个 release 形态的包和一份 `checksums.txt`，tag = 12 位短号；拒绝脏树；末行打印 `handoff upgrade --from <绝对目录>` | — |
| S2 `--from` | 巡检与 `--now [--target] [--force]`。巡检表「最新」行写 `<tag>（本机构建 <目录>）`。远端强制推送；本机走既有本机分支。缺平台的机器单独 fail 并点名资产，其余照常 | 控制台「执行机升级」接本地来源 |
| S3 换版后账本可读 | 四格判据（Q2–Q4），远端与本机都检查，所有来源都适用 | — |
| S4 自登记瞬时错误重试 | `setupLedger` 内对 `SelfRegister` / `ImportLocalTargets` 的瞬时错误做退避重试，期间启动闸播报；非瞬时错误保持 fail-closed | — |
| S5 文档 | README 中英、SKILL.md、CHANGELOG、`--help`、脚本头注释 | 全局记忆的换版步骤由主会话在 finish 改（不在仓内，执行者不动） |

不做：远端一键回滚、多级备份、在执行机上构建、伪造 `vX.Y.Z`。

## 2. 改动边界

### 2.1 文件集（有界）

| 文件 | 改什么 | 故事 |
|---|---|---|
| `internal/release/`（新文件，名字执行者定，例如 `localdir.go`） | 读目录 → `Release`（tag 唯一性、资产清单）；构造读目录的 `Installer` | S2 |
| `internal/release/*_test.go` | 目录来源的核心方法测试 | S2 |
| `cmd/upgrade.go` | `--from` flag；按 `--from` 替换 `newReleaseChecker` / `newReleaseFetcher` 两个缝的产物；`PreferPush` 强制；「最新」行文案；互斥校验；本机 S3 补段；`agentdPeer` 与 `upgradeDialErr` 加探针方法 | S2 S3 |
| `cmd/upgrade_test.go`（及 helpers） | 竖切与 `fakePeer` 补方法 | S2 S3 |
| `internal/upgrade/machine.go` | `Machine` 加换版前账本可读三态（`*bool`，nil = 未知，与 `Managed` / `Pull` 同纪律）；S3 判据纯函数 | S3 |
| `internal/upgrade/remote.go` | `Peer` 加探针方法；`RemoteOne` 在 `WaitVersion` 成功后施加 S3；推送超时原因追加原文（Q9） | S3 |
| `internal/upgrade/*_test.go` | 四格与 RemoteOne 接线 | S3 |
| `internal/client/`（`update.go` 或新文件） | 探针方法：`GET /api/disciplines`，按 Q3 映射 | S3 |
| `internal/client/*_test.go` | 探针状态码映射（httptest） | S3 |
| `cmd/agentd.go` | `setupLedger` 自登记/导入的瞬时重试；`ledgerBoot` 加两个可注入缝（register / importTargets，nil = 生产函数） | S4 |
| `cmd/agentd_ledger_retry_test.go` | S4 重试序列 | S4 |
| `scripts/build-deploy.sh` | `--pack` 模式 | S1 |
| `README.md` / `README.zh-CN.md` / `skills/handoff/SKILL.md` / `CHANGELOG.md` | S5 | S5 |

文件集之外的改动（尤其是 `internal/agentd/` 任何文件、`internal/agentd/machineupgrade.go`、`proto`）须升级（§5）。

### 2.2 真实入口

- S1：`scripts/build-deploy.sh --pack …`（shell）。
- S2/S3：`upgradeCmd.RunE`（cmd/upgrade.go:277）→ `newReleaseChecker().Latest`（311）→ `probeMachine`（377）→ `process`（502）→ 远端走 `remoteUpgrade`（641）再到 `upgrade.RemoteOne`（remote.go:97），本机走 `localUpgrade`（582）或 `localSwap`（559）。
- S4：`agentdCmd` → `setupLedger`（cmd/agentd.go:659）→ `openLedgerWithRetry`（586）→ 自登记分支（687-699）。

### 2.3 复用的既有符号（不复写）

- 资产名 `release.AssetName`（client.go:82）。tag 反解只做「拆名 → 用 `AssetName(tag, goos, goarch)` 回拼 → 与原文件名逐字相等」，回拼不等的文件不算资产。命名规则只此一处。
- `release.ChecksumsName`（client.go:43）；`Release.AssetFor` / `Checksums`（client.go:118-136）。
- `Installer.FetchArchive` / `Fetch` / `FetchChecksum` / `InstallArchive` / `selfCheck`（install.go:120、323、170、270、536），`sumFor`（420），`extractBinary`（459）。
- `release.Activate` / `.prev`（install.go:570）。
- `upgrade.Classify` / `IsLatest`（machine.go:94、122），`upgrade.RemoteOne`（remote.go:97）。
- `client.PushUpdate` / `WaitVersion`（client/update.go:57、170），`client.Status`（client/client.go:528）。
- `openLedgerWithRetry` 的退避常量、`jitter`、`sleepCtx`、`ledgerBoot.report/sleep/ctx`（cmd/agentd.go:566-657）；判据 `ledger.IsTransientOpenError` / `OpenErrorReason`（internal/ledger/open_errors.go:60、102）。
  - 抽一个「瞬时错误退避重试」小函数给打开与自登记共用，属执行者权限。约束：既有三条重试测试（`TestSetupLedgerRetriesTransientOpenError` 等）不改断言、照样绿。
- release.yml 的打包形态（release.yml:78-91、300、809）：包根只有一个文件，`handoff` 或 Windows 下的 `handoff.exe`；checksums 是 `sha256sum` 两空格格式。

### 2.4 架构方向与契约

- `internal/upgrade` 不认识 cobra 和 Endpoint，不排版（machine.go:7-12、remote.go:6-10）。S3 判据进这里，文案由调用方渲染；`Result.Reason` / `Remedy` 照既有方式承载结论。
- `internal/release` 不读 handoff 配置（client.go:7-11）。目录路径由 cmd 传入。
- 本机路径留在 CLI（remote.go:7-8）。本机的 S3 由 CLI 调用 `internal/upgrade` 的同一个判据函数，不复制判据。
- 复用契约：`POST /api/update` 推送模式（`mode=push`、`tag`、`sha256`、body = 归档原文，update.go:65-193）、`GET /api/status`、`GET /api/disciplines`（既有，`withLedger` 守卫）、release 资产命名。均不改。

## 3. 工作顺序

先做承重最重的根因修复（S4，独立），再从来源向编排推进。各段收尾时跑全量编译和触及包测试（§4.1）。

**段 0 承重假设（已在出稿时核实，执行者只需在首个测试里自然复验）**

- tag 自检：`releaseVersion=<12 位>` 构建的 `handoff version` 首行恰为该短号（台账 F1，实测）。因此 `--from` 的 tag 用短号原样，不做任何形态转换。
- IsTransientOpenError 穿透：`RegisterDevice` 的错误是层层 `%w` 包裹的 PgError 或 ConnectError，`errors.As` 可穿透（台账 F5）。S4 第一个测试就用 `fmt.Errorf("自登记机器 x: %w", fmt.Errorf("开账本事务: %w", &pgconn.PgError{Code:"53300"}))` 这种多层形态断言重试。若意外判为非瞬时，**停下升级**，不得在自登记侧另写判据。

**段 1 S4 自登记瞬时重试（cmd/agentd.go）**

1. 先写红测（见 §4.2 S4）。注意既有重试测试都是无 DSN（shared=false），走不到自登记。新测试需要：
   - `cfg.Ledger.DSN` 非空、`Name` + `Advertise` 齐（`cfgHasLedgerIdentity`，806-814）；
   - `boot.open` 返回临时目录下的 `ledger.Open(<sqlite 路径>)`；
   - 新增的 register / importTargets 缝注入失败序列。
   - **假设 + 验证动作**：shared=true 配 SQLite 句柄时，`setupLedger` 后续步骤（`NewDeviceTableSource`、`StartAutomation` 等）能跑通。首个测试即验证。跑不通时，只在 `ledgerBoot` 上加最小缝绕开，并写进偏离节，不改生产语义。
2. 实现：瞬时错误按 Q5 保留 `lst` 并退避重试。启动闸播报 `账本未挂载：自登记第 N 次重试（<OpenErrorReason>），约 Xs 后再试`，导入步骤同理（措辞里出现「导入本地 targets」）。日志限频与打开一致。成功后若重试过，打一条 WARN 带总次数（与 615 行同级）。非瞬时错误保持原 `Close` + `return func(){}, nil` 不变（688-698）。停机见 Q5。

**段 2 本地目录来源（internal/release）**

1. 红测：目录 → `Release` 的解析（§4.2 S2 核心方法），以及用该 `Installer` 对**真实构造的** tar.gz 跑 `Fetch`（包内 `handoff` 是一个打印 tag 的脚本，同 install_test.go:97 `tgzWith` 手法），断言自检过、产出临时文件。
2. 实现 Q1 甲。解析规则：
   - 目录必须存在且含 `checksums.txt`；
   - 资产 = 名字以 `handoff_` 开头且能用 `AssetName` 回拼成功的文件；
   - 零个资产 → 错误；资产间 tag 不一 → 错误，列出全部 tag；
   - `checksums.txt` 里出现、但目录里没有的资产不入清单；
   - 目录里有、但 `checksums.txt` 缺行的资产照样入清单，留到 `FetchArchive` 时由既有 `sumFor` 报「没有 X 的校验和」，即 S2 的「缺行拒绝推送」，按机器单独 fail。

**段 3 `--from` 接入 CLI（cmd/upgrade.go）**

1. 红测：竖切（§4.2 S2）。
2. 实现：
   - `--from <目录>` 先转绝对路径；与 `--rollback` 互斥（报错，退出非零，同 288-290 的写法）。
   - 有 `--from` 时，两个工厂缝返回目录来源：checker 的 `Latest` 返回目录 `Release`，fetcher 是目录 `Installer`。
   - 「最新」行写 `最新     <tag>（本机构建 <绝对目录>）`；无 `--from` 时这一行逐字不变。
   - 远端 `Options.PreferPush = upgradePush || from != ""`（647）。
   - 自拉的 checksum 缓存只在 pull 时触发，`--from` 下不会走到，无需特判。
   - `Long` 帮助加一句 `--from`。

**段 4 S3 换版后账本可读（client → upgrade → cmd）**

1. client 探针方法与 httptest 映射测试（Q3）。
2. `upgrade.Machine` 加三态字段，加判据纯函数（四格，Q4），`Peer` 接口加探针方法；`RemoteOne` 在 `WaitVersion` 成功后、返回 OK 前施加判据。Q9 文案顺带做。
3. cmd：
   - `probeMachine` 在 status 成功后补一次探针，填三态（只在 `--now` 时探也可，执行者定，但巡检表输出不得变）；
   - `agentdPeer` 与 `upgradeDialErr` 补方法；
   - `localUpgrade` 按 Q2 补「等上线 + 探针」。
4. 控制台路径（machineupgrade.go）不改：它不填三态 → 未知 → 不检查。编译期由 `*client.Client` 自动满足新接口方法。

**段 5 打包模式（scripts/build-deploy.sh）**

1. 先实现快速失败路径（参数、目录非空、脏树，均在前端构建前），手工验收命令见 §4.2 S1。
2. 主体：
   - 前端构建一次；
   - 每个平台 `go build` 到暂存目录，产物名固定 `handoff` / `handoff.exe`；ldflags 四注入点与现有一致；每个产物各跑一次注入自检（98-103）；
   - 打包：tar.gz 用 `COPYFILE_DISABLE=1 tar czf <名> -C <暂存> handoff`，防 macOS bsdtar 夹带 `._handoff`；Windows 用 `zip -j`；包名必须与 `release.AssetName` 一致，在脚本注释里写明契约四处同步（同 release.yml:76-77）；
   - 输出目录内执行 `sha256sum handoff_* > checksums.txt`，没有 `sha256sum` 时用 `shasum -a 256`，两者都是两空格格式（台账 F2）；
   - embedweb 测试跑一次；
   - 末行 `handoff upgrade --from <绝对目录>`。
3. 头注释「用法」加 `--pack`。

**段 6 文档（S5）**：README 中英 `upgrade` 节与命令表加 `--from`，并写打包模式两行用法；SKILL.md 中 `upgrade` 的出处行（约 958 行）提一句本机构建换版；CHANGELOG `[Unreleased]` 的新增和变更（S3 对 release 来源也生效、推送超时原因带原文、S4）。与 B453 同改这几个文件，见 §7 施工排序。

**段 7 真机（主会话执行，不派发）**：见 §4.3。执行者交回时须附 §4.2 的机内证据。真机项由主会话按清单执行，执行者不碰任何机器上的 agentd。

## 4. 验收方式

### 4.1 测试范围（implement 三段律）

- task 内循环（只跑触及包）：`go test ./internal/release/ ./internal/upgrade/ ./internal/client/ -count=1`；`go test ./cmd/ -run 'Upgrade|RemoteUpgrade|PushFlag|Checksum|RenderCheckRow|CheckAndNow|SetupLedger' -count=1`。
- 每段收尾：`go build ./... && go vet ./cmd/ ./internal/release/ ./internal/upgrade/ ./internal/client/`，加上述触及包测试。
- 全量 `go test ./...` 只在 review 与 finish 跑。已知两条环境敏感测试在 `CLAUDE_CODE_SESSION_ID` 或嵌套 worktree 下会红，不算回归（记忆 handoff-env-sensitive-tests）。

### 4.2 机内断言（成功 + 关键反例）

**S2 核心方法（internal/release）**

- 目录含 `handoff_<t>_linux_amd64.tar.gz`、`handoff_<t>_darwin_arm64.tar.gz`、`checksums.txt` → `Release.Tag == t`，两个资产，`Checksums()` 存在。
- 两个 tag 混在一个目录 → 错误，文本含两个 tag。
- 无 `checksums.txt` → 错误；零资产 → 错误。
- 干扰文件（`handoff-desktop_*`、`README`、`handoff_x.tar.gz.bak`）不入清单。
- 命名一致性：解析得到的每个资产名 == `AssetName(tag, goos, goarch)`（断言调用的就是 `AssetName`；Windows 用 `.zip`）。
- 序列化边界（穿过真实解包与自检）：用目录 `Installer.Fetch` 对真实 tar.gz 跑通 `InstallArchive`。tar.gz 的包内名是 `handoff`，内容是 `echo <tag>` 脚本。另做一个 zip 资产（包内名 `handoff.exe`），走 `FetchArchive` + `extractBinary`，只断言解包成功，不在非 Windows 上执行。
- 反例：`checksums.txt` 的 sha 被改一位 → `FetchArchive` 报 `sha256 校验不通过`；`checksums.txt` 缺某资产行 → 报 `没有 <资产名> 的校验和`。

**S2 竖切（cmd，假 peer + 真实目录来源）**

- `--now --from <dir>`，假远端 `linux/amd64`、`Pull=true`（会自拉的对端）→ 断言：走 `PushUpdate` 而非 `PullUpdate`；推送的字节和 sha 与目录里文件的字节、`checksums.txt` 行逐字相等；tag == 目录 tag。
- 两台远端，目录只有 `linux/amd64` 包，另一台 `linux/arm64` → arm64 那台输出 `失败` 并含 `handoff_<t>_linux_arm64.tar.gz`，amd64 那台 `成功`，命令退出非零（「有 1 台机器升级失败」）。
- 巡检（无 `--now`）：首行含 `<tag>（本机构建 <绝对目录>）`，不调用 GitHub（checker 缝若被调用就让测试失败）。
- `--from` + `--rollback` → 报错。
- 无 `--from` 的既有测试全部不改、照绿（「输出逐字一致」的护栏）。

**S3 核心方法（internal/upgrade，判据纯函数 + RemoteOne）**

| 换版前 | 换版后 | 期望 |
|---|---|---|
| 可读 | 可读 | OK |
| 可读 | 503（不可读） | Fail，Reason 含「新版已上线但账本未挂载」，Remedy 含「日志」与 prev |
| 不可读或未知（nil） | 任意（含探针报错） | OK，且**不调用**换版后探针（假 peer 计数为 0） |
| 可读 | 探针出错 ×3 | Fail，Reason 含「未能确认」与错误原文；探针调用 3 次 |
| 可读 | 探针出错 ×1 后可读 | OK |

- 时序：探针只在 `WaitVersion` 成功之后调用（假 peer 记录调用序列）。`WaitVersion` 失败时不调用探针，失败文案沿用既有措辞（Q9：推送超时原因多一段原文）。

**S3 client 映射**：httptest 回 200 → 可读；503 → 不可读；404 → 未知（专用错误或三态，执行者定）；500 和连接拒绝 → 错误。

**S3 本机**：本机换版前可读 → 断言重启后调用了 `WaitVersion` 与探针；探针 503 → 本机 `失败`，原因同上。换版前未知 → 不等待（与今天行为一致）。

**S4（cmd/agentd_ledger_retry_test.go）**

- register 前两次返回**多层包裹**的 53300，第三次成功 → 账本挂载（`srv.LedgerMounted()`）；register 调用 3 次；`boot.open` 只调用 1 次（句柄保留，Q5）；report 播报含「自登记」与「53300」；sleep 被调用 2 次。
- importTargets 第一次瞬时、第二次成功 → 挂载；register 只调用 1 次。
- register 瞬时后停机（ctx 取消）→ 返回 `context.Canceled`；句柄已关闭（可用「关闭后再调用即报错」之类的方式观察，执行者定）。
- register 返回包裹 `ledger.ErrDeviceConflict` → 不重试（调用 1 次），账本不挂载，返回 `(stop, nil)`，与今天的 fail-closed 一致。
- 既有三条重试测试原样通过。

**S1（本机手工验收，命令与输出存进 implement 报告）**

- 在有已跟踪改动的工作树运行 `scripts/build-deploy.sh --pack /tmp/x linux/amd64` → 1 秒内拒绝，说明原因，不触发 npm。
- 平台写成 `linux` → 退出 2；输出目录非空 → 退出 2。
- 在干净工作树运行 `--pack <dir> darwin/arm64 linux/amd64` → 目录中恰有两个包和 `checksums.txt`。`(cd <dir> && sha256sum -c checksums.txt)` 全 OK；`tar tzf` 每个包只列出 `handoff`。本机包解出后运行 `version`，首行 == 当前 HEAD 前 12 位。末行是 `handoff upgrade --from <绝对目录>`。
- 用本卡新构建的 CLI 运行 `handoff upgrade --from <dir>` 巡检 → 「最新」行显示该 tag 和目录（只读，不换版）。
- 不带 `--pack` 的旧用法（`scripts/build-deploy.sh`）产物路径与输出照旧。

### 4.3 真机清单（全部**未验证，需真机**；主会话执行，不派发；绝不动在役 agentd）

前置条件：隔离实例用独立的 DataDir、端口、config 和测试账本库；测试库由主会话按 credentials 纪律建立（凭据只经脚本进 config，不打印）。旧版本用 `d905c3ab` 的 `build-deploy.sh` 构建，新版本用本卡分支 HEAD 的 `--pack` 产物。CLI 一律带 `--config <隔离 CLI 配置>`，其 `listen` 指向本机隔离实例，`targets` 只含 linux-01 隔离实例。

- **R1 托管形态成立**：linux-01 按 Q8 起系统级 transient unit，本机按 Q8 起临时 plist。两边 `handoff status` 都显示 `update.managed=true`。
- **R2 远端换版**：`handoff upgrade --now --from <dir> --target <linux 隔离>` → 版本上线；该机二进制同目录出现 `.prev`；对端日志是 push 模式（`换版：开始校验与解包`），没有自拉。
- **R3 本机换版**：本机隔离实例由旧二进制 P 启动后，把新 CLI 换到 P（制造「二进制新、agentd 旧」的中间态），再运行 `P --config … upgrade --now --from <dir>`。结果：本机输出成功，agentd 以新 tag 上线（`localUpgrade` 的等待生效）。
- **R4 S3 正例**：R2/R3 两机换版前后 `GET /api/disciplines` 都回 200，CLI 没有误报。
- **R5 S3 反例**：在测试库把 linux 隔离实例的 `devices.instance_id` 改成另一个值（制造撞名），再换一次版（另一提交的包，或同包 `--force` 重推，执行者与主会话定）。新进程以撞名不挂账本，CLI 对该机报「新版已上线但账本未挂载」，退出非零。之后恢复该行。
- **R6 缺平台**：用只含 `darwin/arm64` 的目录对 linux 隔离实例运行 `--now --from` → 该机失败并点名 `handoff_<t>_linux_amd64.tar.gz`。
- **R7 S4 日志（可选，做不出则标「未验」，以单测为准，不判等价）**：对测试库的测试账号设 `ALTER ROLE … CONNECTION LIMIT` 制造 53300 窗口，观察 linux 隔离实例启动日志有「自登记第 N 次重试」，`status` 回 503，解除后挂载。
- **R8 清理**：`systemctl stop handoff-b462-iso`（`--collect` 自动回收）；`launchctl bootout gui/$UID/dev.gosuper.handoff.b462-iso` 并删除临时 plist；删除测试库与隔离目录。在役三台 `handoff upgrade --check` 输出与开始前一致。
- 保存的证据：每项的命令、CLI 输出全文、对端日志关键行，写进 `docs/superpowers/specs/b462-ledger.md` 的真机节。
- 生产使用（finish）：主会话用本命令把三台换到合并后的版本，即为生产冒烟；手工流程作为回落（spec「测试决定」）。

## 5. 决策权限

执行者可自行决定（交回物「偏离与自主决定」逐条列出）：

- 新文件名、私有函数名、内部类型形态（例如探针返回三态还是 `(bool, error)` 加专用错误）、重试小函数的抽取方式。
- 日志措辞，以及非断言锚点的文案。断言锚点固定为：`本机构建`、`新版已上线但账本未挂载`、`未能确认`、`自登记`、缺资产时含资产全名。
- Q1 在 `internal/release` 包内由甲退到乙（前提见 Q1）；S4 测试需要的最小 `ledgerBoot` 缝。
- 换版前探针放在 `probeMachine` 无条件探，还是只在 `--now` 时探（巡检输出不变即可）。

须停下升级给主会话：

- 任何 wire 变化：新端点、新字段、改 `/api/update` 或 `/api/status` 的语义。
- 改动 `internal/agentd/` 下任何文件，或改控制台升级路径的行为。
- 改 `upgrade.Classify` 的结论或优先级，或改 `Peer` / `Fetcher` 接口中「加探针方法」以外的部分。
- 改变非瞬时错误的处置（撞名 fail-closed），或改变 `IsTransientOpenError` 判据本身。
- 改变 `build-deploy.sh` 不带 `--pack` 时的行为；放宽脏树判据。
- 自检规则（`version` 首行 == tag）需要为本地构建开例外。按段 0 核实，不应发生；若发生，说明假设破了。
- 需要在任何真机上起停 agentd（段 7 归主会话）。
- 同一问题修复失败 3 次（implement 熔断）。

## 6. 需冻结的外部承诺

无新增冻结对象。复用契约：`POST /api/update` push 模式（update.go:65-193，`tag`/`sha256`/body）、`GET /api/status`、`GET /api/disciplines`（既有端点，`withLedger` 未挂载 503）、release 资产命名 `handoff_<tag>_<goos>_<goarch>.{tar.gz|zip}` + `checksums.txt`（client.go:82、release.yml:78-91/809）、`handoff version` 首行 == tag（version.go:10-12）。

## 7. 风险与施工排序

- **三态塌缩**：「账本可读」用 `*bool`，nil = 未知，与 `Managed` / `Pull` 同纪律（cmd/upgrade.go:197-209）。把「没探到」当成「不可读」只会少检查，把「不可读」当成「可读」才是漏报。表格第 3 行锁住前者，第 2 行锁住后者。
- **spec S3 括注更正**：spec 写「换版前账本就不可读（无 DSN 或无身份）」，但无 DSN 时 agentd 走 SQLite 单机回退，账本是挂载且可读的（cmd/agentd.go:675-677）。真正不可读的只有「有 DSN 但缺身份」和挂载失败。本稿判据按探针结果，不按配置推断，两种情形都处理对了；finish 时 spec 括注随文档对齐更正。
- **重启窗口的 503 歧义**：启动闸与 `withLedger` 都回 503。探针只在 status 回 200 之后调用（Q3）。时序由 S3 测试的调用序列断言锁住。
- **部分失败不中断**：缺平台、单机 S3 失败都只让该机 fail，其余机器照常，退出码非零（既有 `TestUpgradePartialFailureContinues` 同款）。
- **信任链**：sha 只来自 `checksums.txt`，推送时 CLI 与 agentd 两端比的是同一份声明（install.go:111-113）。本地来源不得跳过任何一道校验。
- **假部署物**：拒绝脏树（Q7），每个产物各做一次注入自检；tag 由包名得出，不由目录名得出。
- **句柄泄漏、停机**：S4 停机路径必须关闭 `lst`（测试锁住）。
- **退避风暴**：自登记重试沿用 jitter，不另设常量。
- **施工排序（非契约依赖）**：`cards/B453-charter-1` 也改 `cmd/agentd.go`（setupLedger 内 `SetHostAPI` 附近，与本卡 685-698 不同块）、README 中英、SKILL.md、CHANGELOG。后合并的一方 rebase，冲突预计只在 CHANGELOG 的 `[Unreleased]` 列表。

## 8. 核实读数

本稿引用的代码与环境事实，全部记录在 `docs/superpowers/specs/b462-ledger.md`「## plan」节 F1–F7（含命令与关键输出）。承重四条摘要：

- F1：注入 `releaseVersion=d905c3ab1234` 的构建，`handoff version` 首行实测为 `d905c3ab1234`（含 `2>&1` 合流三次）。12 位短号 tag 可过 `selfCheck`；agentd 不校验 tag 形态。
- F4：`GET /api/disciplines` 经 `withLedger`，未挂载回 503；启动闸在 setupLedger 之后才放行，所以 `WaitVersion` 成功即挂载已有定论。
- F5：`RegisterDevice` 的错误各层 `%w`，pgx v5.10.0 `ConnectError` 可 Unwrap，`IsTransientOpenError` 能识别；撞名判非瞬时。
- F6：linux-01 systemd 255，root `Linger=no`，所以用系统级 transient unit；macOS 用 KeepAlive plist，不用 submit。
