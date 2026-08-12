# 操作者触发的更新与 skill 分发设计

> 取代 B54.3「自动更新」的自主决策部分。B54.3 的其余产出（`internal/release`、
> `handoff upgrade` 的本机三个 flag、CLI 侧限流提示）原样保留并被本设计复用。

## 1. 背景

B54 三期交付后，安装与升级链路已经打通：`curl … | bash` 一行安装（B54.1）、
`handoff service install` 托管（B54.2）、agentd 定时自更新（B54.3）。v0.1.0 已发布，
一行安装真机验收通过。

但把这条链路放到「多台机器」的真实用法里看，暴露出三个问题：

**一、自动更新的两条验收做不了。** B54.3 的 P3（完整自更新一轮）与 P4（有活跃任务
时不换、结束后换）要求存在一个比在跑的二进制更新的版本。只有一个 release 时，
自更新查到的永远是自己。这两条至今未验，而它们恰好是这个子系统最核心的行为。

**二、执行机必须能出网。** 自更新让每台 agentd 自己去 GitHub 拉资产。执行机在内网、
在跳板机后面，或 GitHub 恰好抽风时（08-11 本机 `github.com:443` 连续两次 75s 超时，
而 `api.github.com` 正常），升级就断在最需要它的时刻。

**三、skill 完全不随二进制分发。** Release 资产里只有 `handoff` 一个文件，一行安装
装的也只是二进制。skill 在仓库的 `skills/handoff/SKILL.md`，靠手工跑
`skills/install.sh` 安装——前提是本地有仓库 checkout，而一行安装的用户没有。
后果不是「少个文档」：skill 里写死了状态机前置条件、ID 必须是完整 UUID、
`handoff run` 的 flag 必须在任务名之前这类硬约束。二进制升到新版而 skill 停在旧版时，
旧 skill 会**主动误导**审核者按已经变了的规则操作。

三个问题指向同一个结论：**把「什么时候升级」的决策权从程序交还给操作者，
把「升级什么」的下载动作从执行机移到本机，并让 skill 与二进制同源。**

## 2. 目标与非目标

### 目标

- 一条命令看清所有机器（本机 + 全部 target）的版本，不需要记 target 名字
- 一条命令把落后的机器全部升到同一版本，执行机**无需出网**
- 有活跃任务的机器默认不动，但提供可复制的强制升级命令
- skill 内嵌进二进制，安装与升级时自动同步，版本不可能与二进制漂移
- 删除自动更新的定时循环与待命状态，同时**不破坏任何现有配置文件**

### 非目标

- 不做定时/后台自动升级。升级是操作者的决定（这正是本设计取代 B54.3 的部分）
- 不做灰度、不做版本回滚编排。回滚仍是单机 `handoff upgrade --rollback`
- 不给远端安装 skill。skill 服务于审核者，审核者在本机
- 不引入 npm/Node 分发通道。理由见 D5
- 不在更新路径上叠加超出 bearer token 的鉴权。理由见 D4

## 3. 决策记录

| 编号 | 决策 | 理由 | 被否方案 |
|---|---|---|---|
| D1 | **CLI 下载并推送二进制，agentd 不出网** | 消除一整类「需要升级时恰好升不了」的失败：内网执行机、跳板机后的机器、GitHub 抽风。本机通常网络更好，且只需下载一次即可推给多台同平台机器 | agentd 自己下载（复用现成 `Fetch` 最省事，但要求每台执行机能直连 GitHub）；两者都支持（多一条分支与一套配置语义，而 A 的适用场景 B 全覆盖） |
| D2 | **升级以「一组机器」为单位，本机与 target 同一入口** | 版本一致本身就是要解决的问题，逐台升级会让不一致成为常态；且操作者不必记住配置里有哪些 target | 每次只升一台（`--target` 必填）；单独的 `upgrade-all` 命令（同一件事两个入口） |
| D3 | **活跃任务默认拒绝，`--force` 可越过** | 优雅关停与 reattach 恢复路径已有实现与集成测试，但**从未在真实换版下走过**。默认保护，同时保留主动验证它的出口——这条 `--force` 路径正是 B54.3 的 P3/P4 验收的替代做法 | 硬拒绝（devbox 上挂着任务好几天是常态，正是它跑旧二进制的原因）；直接换只警告（把未验证路径设成默认，第一次踩坑的人不会知道是升级导致的） |
| D4 | **不在更新接口上叠加额外鉴权** | 持有 bearer token 的人本来就能 `handoff run <task> sh -c '任意命令'`，推二进制不构成提权。token 就是信任边界。校验和的职责是**完整性**不是**授权** | 要求回环地址；独立的更新令牌（安全戏：挡不住已有 token 的人，只增加配置面与配错的机会） |
| D5 | **skill 用 `go:embed` 进二进制，不走 npm/npx** | skill 版本 == 二进制版本，**结构上不可能漂移**——而漂移正是要解决的病根。npm 包版本与二进制版本是两条独立的线，用会漂移的通道修漂移自相矛盾。且 npx 并不替你省掉安装逻辑（拷贝+软链照样要写），只多一条发布流水线与 Node 依赖 | npx 分发（项目现无 `package.json`，零 Node 依赖）；放一份独立文件在 `~/.handoff/`（那份拷贝会和二进制一样漂移） |
| D6 | **安装动作留在二进制里，不交给 agent 提示词** | `skills/install.sh` 的拷贝+软链逻辑三十行、已在用；路径表随二进制更新，而提示词永远不会变好。更关键的是：**我们自己装，就知道装到了哪**，一致性检查因此能准确判断而不必猜 | 打印提示词让 agent 代劳（agent 装到哪、装没装成、装的是不是旧版都看不见，只能靠猜，而猜出来的诊断会说谎） |
| D7 | **配置项 `update.auto` / `update.interval` 保留字段并标废弃** | 配置是严格解析的（`KnownFields(true)`），未知键让 agentd **启动失败**。v0.1.0 首次运行会把这两个键写进 `config.yaml`，直接删字段等于让所有装过 v0.1.0 的机器升级后起不来——正是本设计要消灭的那类失配的最狠形态 | 直接删字段（破坏现有配置）；重新赋予新语义（键名与新行为对不上，比留个废弃键更迷惑） |
| D8 | **更新接口的 body 可选**：带二进制=换版并重启，不带=只重启 | 本机的二进制由 CLI 直接换（文件就在本地，没有推送的必要），但仍需要 agentd 重启才生效。同一个接口两种模式，比「推送接口 + 重启接口」两个入口更小 | 本机也走推送（把本地文件读出来再经 HTTP 发给自己，纯浪费）；单独的重启接口（YAGNI，且裸的重启接口是个更容易被误用的东西） |

## 4. 设计

### 4.1 命令形态

`handoff upgrade` 的本机三个 flag（`--check` / `--now` / `--rollback`）语义不变，
新增一个「机器范围」维度：

```
handoff upgrade                          # 巡检：列出所有机器的版本与结论
handoff upgrade --now                    # 升级所有落后的机器（含本机）
handoff upgrade --now --target devbox    # 只升这一台
handoff upgrade --now --force            # 越过活跃任务闸
handoff upgrade --rollback               # 本机回滚（不变，不支持 --target）
```

**`--force` 作用于本次选中的机器范围**：不带 `--target` 时对所有落后的机器生效，
带 `--target` 时只对那一台生效。它只越过闸一（活跃任务），**永远不越过闸二**
（非托管，见 4.3）。

巡检输出（`--check` 为默认行为）：

```
最新     v0.1.1
本机     二进制 v0.1.0 · agentd v0.1.0   需要升级
devbox   v0.1.0                          需要升级
prod     v0.1.1                          已是最新
aliyun   够不着（dial tcp 10.0.0.5:7777: connect: connection refused）
```

一条命令一张表，这是巡检与升级的同一个入口。target 名字取自
`config.yaml` 的 `targets`，不需要操作者记忆。

**「本机」一行必须分别显示二进制与 agentd 两个版本，因为它们会不一致。**
`upgrade --now` 换掉磁盘上的文件后，正在跑的 agentd 仍是旧进程——这是**正常且
常见**的中间态（非托管机器上它会一直保持）。合成一个数字就必然骗人：显示旧版
会让人以为升级没成功，显示新版会让人以为 agentd 已经在跑新代码。

本机没有 agentd 在跑时，该格显示「未运行」，只比较二进制版本。
远端只有 agentd 一个身份，因此只有一个版本号。

**`--rollback` 不接 `--target`。** 回滚是「这台机器上出了问题」的单机应急动作，
批量回滚一组机器不是任何真实场景，而给它一个批量入口只会让误操作更省事。

### 4.2 升级的数据流

对每台需要升级的机器：

1. **查远端 status**（`GET /api/status`）拿当前版本、平台、托管状态、活跃任务
2. **预检两道闸**（见 4.3）——在下载那 15MB **之前**拒掉，不白费带宽
3. **下载该机器平台的资产**并对 `checksums.txt` 校验（复用 `internal/release`，但需拆分，见下）
4. **POST `/api/update`**，body 为 **tar.gz 资产原文**，头部带目标 tag 与 sha256
5. **agentd 侧**：复检两道闸 → 落盘到目标二进制**同目录**的临时文件 → 重算 sha256 →
   自检（跑 `<临时文件> version`，首行必须等于目标 tag）→ `release.Activate`
   原子换版并保留 `.prev` → 返回 200 → 触发优雅关停
6. **进程管理器拉起新版**（systemd `Restart=always` / launchd `KeepAlive`）
7. **CLI 轮询 status 直到新版本上线**，超时则如实报告（见 4.6）

第 7 步不是修饰。不确认就报「升级成功」是主张不是事实；而 agentd 起不来恰恰是
最需要立刻知道的时刻——那时要给出 `.prev` 路径与回滚命令。

**本机是这条流程的特例**：跳过第 3–5 步的推送，由 CLI 直接换本机二进制
（`upgrade --now` 现有逻辑，原子 rename + `.prev`），然后向 `127.0.0.1` 的 agentd
发一个**不带 body** 的 `/api/update` 让它重启（D8）。本机没有 agentd 在跑、或
agentd 非托管时，退回纯换文件并如实提示「正在跑的 agentd 仍是旧进程，需要你自己
重启」——即 `upgrade --now` 现有行为，一字不改。

**顺序：远端全部处理完，本机最后。** 本机换版会重启本机 agentd，而操作者很可能
正用它盯着任务；把干扰最大的一步放最后，前面出问题时不至于白扰一次。

#### `internal/release.Fetch` 必须拆开（订正）

现有 `Fetch`（install.go:73）做了五件事：取本平台资产 → 下载 → 校验 sha256 →
解包 → **执行新二进制自检**。跨平台推送时后两件都不成立：

- `goos, goarch := CurrentPlatform()` 写死本平台，取不到远端要的那份资产；
- `selfCheck` 靠 `exec` 跑 `<新二进制> version`——在 macOS 上执行 linux 产物必然失败。
  这条自检本身是对的（4.5 的第三道校验），但它的执行地点是**远端 agentd**，不是本机。

因此按职责拆成两半，`Fetch` 保留为二者的组合（本机升级路径行为一字不变）：

| 函数 | 做什么 | 谁调 |
|---|---|---|
| `FetchArchive(ctx, rel, goos, goarch) ([]byte, string, error)` | 按指定平台下载 tar.gz，对 `checksums.txt` 校验，返回**字节与期望 sha256**。不解包、不自检 | CLI（跨平台推送）与 `Fetch` 自身 |
| `InstallArchive(tgz []byte, wantSum, wantTag, destDir string) (string, error)` | 重算 sha256 比对 → 解包 → chmod → 自检 → 返回临时文件路径 | agentd（收到推送后）与 `Fetch` 自身 |

**推送的 body 是 tar.gz 原文而非解包后的裸二进制**，正是为了让信任链不断节：
`checksums.txt` 声明的是 tar.gz 的哈希，CLI 校验它、把它原样发出、agentd 再校验同一个
值——三处比的是同一个来自 release 的声明。若本机先解包再传裸二进制，agentd 校验的就
变成 CLI 自己算出来的哈希，等于让传输的两端互相背书。

### 4.3 两道闸

CLI 预检 + agentd 复检。预检所需数据 `status` 里已经有了，所以能在下载前拒掉；
agentd 收到时再检一次，因为两次之间状态会变。

**闸一：活跃任务。** `running` + `waiting_answer` 之和大于 0 且未带 `--force` 时拒绝。
`waiting_review` 不计入——它可能挂几天，计入等于无限期阻塞升级（沿用 B54.3 的 D12）。

**闸二：非托管。** `selfupdate.IsManaged` 为 false 时**硬拒绝**，`--force` 也不能越过。
换完 `exit(0)` 后没人拉起，这台机器上就此没有 agentd 在跑，且没有任何信号告诉任何人。

闸二对远端与本机的严格程度不同，这是刻意的：本机 `upgrade --now` 纯换文件的路径
不受闸二约束（敲命令的人知道要自己把 agentd 起回来），但**触发重启**的路径受约束。
远端没有人在那台机器前面。

### 4.4 平台字段（新增）

远端可能是 `linux/amd64` 而本机是 `darwin/arm64`，CLI 必须知道该下哪个资产。
但 `proto.BuildInfo` 目前只有 Version/Revision/Time/Modified/Go，**没有平台**——
`handoff version` 打的那行 platform 是本地 `runtime.GOOS`/`GOARCH` 现算的，
远端拿不到。

因此给 `proto.BuildInfo` 增加：

```go
// Platform 是构建目标平台，形如 "linux/amd64"，在 buildinfo.Read() 里用
// runtime.GOOS + "/" + runtime.GOARCH 现算填入（CLI 与 agentd 同一条路径，
// 不会出现只有一端填的情况）。
//
// **空串表示对端没给这个字段**（老 agentd）。此时远程升级必须明确拒绝而不是
// 猜一个默认值——猜错就是给一台 linux 机器推一个 darwin 二进制，自检会拦下，
// 但那是白跑一次 15MB 上传换来的一条晦涩错误。
Platform string `json:"platform,omitempty"`
```

新增字段 + `omitempty`：老 CLI 忽略它，老 agentd 不发它，双向兼容
（与 `Watchers *int`、`Update *UpdateStatus` 同一条纪律）。

对端未上报平台时，该机器在巡检输出里报「对端 agentd 过旧，未上报平台，
需先手工升级一次」，并在 `--now` 时跳过它。

### 4.5 信任链

三道校验，各管各的：

| 环节 | 校验 | 防的是什么 |
|---|---|---|
| CLI 下载后 | 对 release 的 `checksums.txt` 比对 sha256 | 来源真实性（下到了这个 release 声明的那个文件） |
| agentd 收到后 | 重算 sha256 与头部声明比对 | 传输完整性（15MB 经 HTTP 传输中损坏） |
| agentd 换版前 | 跑 `<临时文件> version`，首行 == 目标 tag | 拿错架构、包装错、二进制根本跑不起来 |

**关于「谁有权推二进制」——这条要写清楚，免得后人在这里加安全戏：**
持有 bearer token 的人本来就能 `handoff run <task> sh -c '任意命令'`，
推一个二进制不构成任何提权。token 就是信任边界。在这条路径上叠加回环限制或
独立令牌，挡不住已经有 token 的人，只增加配置面与配错的机会（D4）。

### 4.6 错误处理

**部分失败是常态，不做事务语义。** 某台机器够不着或闸没过时，**继续处理其余机器**，
最后按机器逐行报结果，任一台失败则退出码非零。这些机器之间本来就没有事务关系，
让一台连不上的机器阻止其余全部升级是纯损失。

**处置建议必须对症，不对症就不给。** 给一条注定失败的命令比不给更糟：

| 失败原因 | 处置行 |
|---|---|
| 活跃任务被拦 | `handoff upgrade --now --target <name> --force`（可直接复制） |
| 非托管 | 提示先在该机器上 `handoff service install`。**不给 `--force`**——它不越过闸二 |
| 够不着 / HTTP 失败 | 只报原始错误原文，**不编处置** |

```
devbox   跳过   3 个活跃任务
         handoff upgrade --now --target devbox --force
prod     跳过   agentd 非托管启动，重启后不会被拉起
         先在该机器上 handoff service install
aliyun   失败   dial tcp 10.0.0.5:7777: connect: connection refused
```

**换版失败时旧二进制原封不动**（`release.Activate` 已保证：第二次 rename 失败会把
`.prev` 换回去）。自检失败就删临时文件拒绝换版，不留半成品。

**轮询超时是最要紧的一条**：报「已换版但新进程未在 N 秒内上线」，附 `.prev` 路径与
回滚命令，**绝不含糊成「升级完成」**。等待时限 60s，轮询间隔 2s。

### 4.7 skill 分发

**内嵌。** `main.go` 里 `//go:embed skills/handoff/SKILL.md` 注入 cmd
（cmd 在子目录，`go:embed` 不能引用父目录，因此必须在 root package 做）。

**安装逻辑**照搬 `skills/install.sh`，落在 `internal/skill`：

- 基准副本写 `~/.handoff/skill/SKILL.md`
- 给**存在的** agent 目录建软链：`~/.claude/skills`、`~/.codex/skills`、
  `~/.config/opencode/skills`、`~/.grok/skills`
- **目录不存在就跳过，不代为创建**（保留现有行为：不给没装的 agent 造目录）
- 返回实际写了哪几处，由命令层打印。不猜、不静默

`internal/skill` 的 `Install` **不含 embed**，签名是 `Install(content, home string)
(Report, error)`——纯粹是入参的函数，测试时给临时 HOME 与任意内容即可，
不需要构建产物。

**命令：**

```
handoff skill            # 报告状态：装在哪几处、是否与当前二进制一致
handoff skill install    # 安装/重新同步
```

**触发点：**

- 一行安装脚本装完二进制后调一次：`"$INSTALL_DIR/handoff" skill install`
- `handoff upgrade --now` 换版后调一次

**换版后必须调新二进制，不能调自己。** 当前进程是旧二进制，它内嵌的是**旧 skill**；
新 skill 在刚换上去的那个文件里。因此是
`exec.Command(target, "skill", "install")` 而不是直接调用本进程的函数。
一行安装脚本天然正确（它调的就是刚装好的文件）。

**一致性检查。** 因为安装由我们自己做，落点已知，所以可以拿内嵌内容的 sha256 与
每个已安装位置逐个比对，准确报出哪一处旧了。`handoff status` 在不一致时加一行提示。
这不是猜测——落点是我们写的。

**删 `skills/install.sh`。** 开发时用 `go run . skill install`：`go:embed` 在构建时
读工作树里的 `SKILL.md`，投影出的正是刚改的那份。一套逻辑覆盖开发与生产两个场景，
不会漂移。README 第 337 行的说明同步更新。

### 4.8 清理清单

| 文件 | 处置 |
|---|---|
| `internal/selfupdate/updater.go` + `updater_test.go` | **删**（518 行）。定时循环与自主决策是本设计取代的部分 |
| `internal/selfupdate/pending.go` 的 `Pending` / `LoadPending` / `SavePending` / `ClearPending` + 对应测试 | **删**（约 200 行）。没有「下载完等空闲再换」就没有待命状态 |
| `internal/selfupdate/pending.go` 的 `IsManaged` | **保留**，移到 `internal/selfupdate/managed.go`。闸二要用它，连同「绝不能用 PPID」的注释与 `TestIsManagedIgnoresPPID` 反例一并搬走 |
| `internal/selfupdate/clicheck.go` + 测试 | **原样保留**。它就是「改成提示」里的提示 |
| `internal/release/install.go` | **拆 `Fetch`**：析出 `FetchArchive`（按指定平台下载+校验，不自检）与 `InstallArchive`（校验+解包+自检），`Fetch` 变成两者的组合。其余（`Activate` / `Rollback` / `PrevPath` / `TempName` / `client.go`）原样保留 |
| `cmd/upgrade.go` | **扩展**：加机器范围维度与远程推送 |
| `cmd/agentd.go` 的 updater 接线 | **删** |
| `proto.UpdateStatus` | **改**：去掉 `Pending` / `DownloadedAt`（没有待命概念了），保留 `Managed`（闸二与巡检要用）。老 CLI 读不到已删字段就不显示，向后兼容 |
| `internal/config` 的 `update.auto` / `update.interval` | **保留字段，标注废弃**；取值非默认时打一条 Warn 说明该配置已无效果。理由见 D7 |

净删约 700 行，新增接口 + skill 约 400 行。

## 5. 文件结构

| 文件 | 职责 |
|---|---|
| `main.go` | 新增 `//go:embed skills/handoff/SKILL.md`，把内容注入 cmd |
| `cmd/upgrade.go` | 扩展：巡检表、机器范围解析、远程推送编排、逐行报告与处置建议 |
| `cmd/skill.go` | 新增：`handoff skill` / `handoff skill install` |
| `internal/skill/install.go` | 新增：`Install(content, home) (Report, error)`，拷贝 + 软链 + 报告 |
| `internal/skill/state.go` | 新增：读各落点内容算 sha256，与内嵌内容比对 |
| `internal/release/install.go` | 改：`Fetch` 拆成 `FetchArchive` + `InstallArchive`（见 4.2 订正），`Fetch` 保留为组合 |
| `internal/agentd/update.go` | 新增：`POST /api/update` 处理器——复检两道闸、落盘、校验、自检、换版、触发关停 |
| `internal/client/update.go` | 新增：客户端侧的推送与轮询确认 |
| `internal/selfupdate/managed.go` | 新增（从 `pending.go` 搬来）：`IsManaged` |
| `internal/proto/status.go` | 改：`BuildInfo` 加 `Platform`；`UpdateStatus` 去掉 `Pending`/`DownloadedAt` |
| `internal/buildinfo/buildinfo.go` | 改：`Read()` 填 `Platform`（两条返回路径都要填，含读不到 `debug.BuildInfo` 的降级分支） |
| `internal/agentd/status.go` | 改：`Update` 恒返回（只带 `Managed`），不再读 `pending.json`——闸二与巡检需要每台机器都有这个字段 |
| `cmd/status.go` | 改：skill 不一致时加提示行；更新状态段落随 `UpdateStatus` 简化 |
| `install.sh` | 改：装完二进制后调 `"$INSTALL_DIR/handoff" skill install` |
| `skills/install.sh` | **删**，由 `go run . skill install` 取代 |

## 6. 测试策略

### 单元测试

- **更新接口**：`httptest` 覆盖两道闸各自拒绝、`--force` 越过闸一但不越过闸二、
  sha256 不符拒绝、自检失败拒绝且清理临时文件、body 带与不带两种模式
- **平台协商**：对端未上报 `Platform` 时拒绝而不是猜默认值
- **多机编排**：fake target 覆盖部分失败不中断其余、逐行报告、退出码非零、
  处置建议对症（活跃任务给 `--force` 行、非托管不给）
- **skill 安装**：临时 HOME 覆盖「目录不存在则跳过」「重复执行幂等」
  「软链指向基准副本」「报告与实际落点一致」
- **skill 一致性**：改坏某一处落点后能准确报出是哪一处
- **配置兼容**：含 `update.auto` / `update.interval` 的旧配置**必须能正常启动**，
  且非默认值时产生 Warn

### 变异检查

每个关键断言至少做一次「改坏实现看测试翻红」，避免写出恒真的测试。至少覆盖：
闸二（把 `IsManaged` 改成恒真）、自检（把 tag 比对去掉）、
skill 跳过逻辑（把「目录不存在则跳过」改成强制创建）。

### 真机验收

在 devbox（100.73.238.21）实机执行，**顶替 B54.3 卡住的 P3/P4**：

- **V1 干净升级**：devbox 无活跃任务时 `handoff upgrade --now --target devbox`，
  验证换版、重启、CLI 轮询确认新版本上线，且 `handoff status` 版本一致
- **V2 闸一生效**：devbox 有活跃任务时同样命令被拒绝，输出里含可复制的 `--force` 行
- **V3 强制升级不伤执行者**：接 V2，带 `--force` 执行，验证
  ①换版成功 ②执行者进程存活（这是 setsid + `KillMode=process` 的实证）
  ③该任务随后能 `continue` 并正常产出
- **V4 skill 同步**：升级后 `handoff skill` 报告与二进制一致；
  手工改坏一处落点后能被准确报出

V3 比 B54.3 原定的 P3/P4 更接近真实用法，也更好做——不需要等自动窗口，
不需要造版本差之外的任何条件。

## 7. 人工前置动作

- 发布 v0.1.1（本设计实现后）才能真机验证升级路径。v0.1.0 → v0.1.1 这一跳
  正好同时验掉 V1–V4 与遗留的版本一致性问题

## 8. 已知风险

- **首次升级仍是「旧接旧」**：v0.1.0 的 agentd 没有 `/api/update`，也不上报
  `Platform`。所以从 v0.1.0 升到 v0.1.1 这一跳**必须手工做一次**（scp 或在该机器上
  跑 `handoff upgrade --now` 再重启）。本设计从 v0.1.1 起才生效。巡检输出必须把
  这个状态说清楚，而不是报一个含糊的失败
- **`--force` 路径的执行者存活性尚未实证**。B54.2 验过优雅关停退出码 0、
  集成测试覆盖过 reattach，但真实换版下的完整链路要等 V3。在 V3 通过之前，
  `--force` 的文案应保持「这条路径尚未在真实换版下验证过」的措辞

## 9. 待验证的空白

- 一次升级多台同平台机器时是否该复用同一份下载。当前设计每台各下一次（简单、
  正确）；若真实使用中机器数量上去了再优化，不预先设计
- `handoff skill` 的一致性检查是否该扩展到「agent 装到了我们表外的第五个位置」。
  当前不做：报有不报无，永远不说「你没装」——避免造出会说谎的诊断
