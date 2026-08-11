# B54 安装与自动更新设计

> 状态：已定案（2026-08-11）
> 来源：backlog B54
> 前置依赖：B34（DataDir 单实例锁）、B36（prochost/setsid 让执行者活过 agentd 重启）、B18（重启后重建订阅）、B38（断连窗口对账）

## 1. 背景

handoff 至今**没有分发形态**。README 里的安装方式是：

```bash
go build -o handoff . && sudo mv handoff /usr/local/bin/
```

即「每台机器自己有源码、自己有 Go 工具链、自己构建」。实测现状（2026-08-11）：

```
devbox  pid 20591  PPID 20587  ~/.handoff/bin/handoff-ssefix-108fae6 agentd --executor=opencode
本机    pid 73202  PPID 1      /tmp/handoff agentd --executor=opencode
```

三个可观察的后果：

1. **换版本靠人工**。devbox 上的二进制叫 `handoff-ssefix-108fae6`——手工 scp + 自造版本后缀。B52 的修复合进 main 之后，devbox 的生产 agentd 仍跑着修复前的二进制，因为没有任何机制把它换掉。
2. **没有任何进程管理器**。两台机器都是裸进程（无 launchd plist、无 systemd unit、无 tmux）。agentd 崩溃或机器重启后，没有任何东西会把它拉起来。
3. **没有版本概念**。仓库 0 个 tag，`buildinfo` 只读 `vcs.revision`，`handoff status` 能显示 commit 短号但显示不出「版本」。

配置侧同样是纯人工：`config.Load` 在文件不存在时生成默认配置与随机 token（[internal/config/config.go:141](../../../internal/config/config.go)），但 targets 配对要人工 `cat` 执行机的配置文件把 token 抄过来，代理 env、默认执行者、监听地址全部手改 yaml。

## 2. 目标与非目标

### 目标

- **一行安装**：`curl -fsSL https://handoff.gosuper.dev/install | bash` 在干净机器上装出可用的 handoff。
- **一次配置**：装完自动进 `handoff init`，交互式完成基础配置，包括现在必须人工抄的 token 配对。
- **进程托管**：`handoff service install` 把 agentd 交给 launchd / systemd，顺带解决「崩了没人拉起」。
- **自动更新**：agentd 定期检查新版本，下载校验就绪后，在空闲窗口自动换版并重启。
- **可回退**：换版失败或新版有问题时，有明确的人工回退路径。

### 非目标

- **不发 Windows 资产**。B37（prochost Windows）仍在 B 期未做，agentd 在 Windows 上跑不起来；发一个装了也用不了的二进制是负价值。`install.sh` 探到 Windows 直接报错说明。
- **不做自动回滚**。理由见 §3 的 D10。
- **不改 module path**。`go.mod` 是 `github.com/xushixin/handoff`，真实 owner 是 `Xsxdot`，两者不一致（影响 `go install`，不影响 Release 下载）。本次所有 GitHub URL 一律用真实 owner `Xsxdot`；module path 的修正单独立项。
- **不做灰度/分批发布**。所有机器盯同一个 latest release。

## 3. 决策记录

每条都是本次定案时明确权衡过的，实现时不得擅自改动。

| # | 决策 | 选它的理由 | 放弃的替代 |
|---|------|-----------|-----------|
| D1 | **引入进程管理器托管**（launchd / systemd） | 「停旧起新」交给管理器，与 B34 的 DataDir 单实例锁天然配合——锁的交接由「旧进程退出」这一个事实保证，不需要额外握手。顺带解决 agentd 崩溃/机器重启无人拉起 | agentd 自己 exec 接力（要自己处理锁交接，中途失败可能两头都没了，且机器重启后仍无人拉起）；不做自动重启 |
| D2 | **GitHub Actions + 手写 workflow**，版本号 = tag | 零外部工具依赖，发版只需 `git push --tags`，任何机器都能发 | goreleaser（多一层配置语义要维护）；本地脚本发版（漏平台/漏资产靠自律） |
| D3 | **先下载就绪，等空闲窗口才重启** | 新版有回归时不会在有活跃任务的时刻炸 | 查到就换（简单但风险窗口大）；固定时间窗（多一层配置，且修了正在砸你的 bug 时白等一天） |
| D4 | **安装脚本只装二进制，托管是单独子命令** | 「怎么写服务单元、怎么重启单元」的逻辑只存在于 Go 里一份，自动更新重启服务用的是同一份 | 脚本带 `--agentd` 参数顺带装服务（同一逻辑 shell 与 Go 各一份，早晚漂） |
| D5 | **`service install` 不接 `--executor`** | [cmd/agentd.go:128](../../../cmd/agentd.go) 里该 flag 的语义只是覆盖 `cfg.Executor.Default`，五个 adapter 无论如何全注册。单元里写 `handoff agentd` 即可，执行者从 config.yaml 读——换执行者只需改配置重启，不用重装服务，自动更新重启时也不必记住装服务时带过什么参数 | 单元 ExecStart 写死 `--executor=xxx` |
| D6 | **安装脚本末尾 `handoff init < /dev/tty`** | 管道占了 bash 的 stdin，交互式 init 直接读到 EOF。`/dev/tty` 把键盘接回来（rustup 同法）。真正无 tty 的场景自动降级 | 脚本只打印「下一步跑 handoff init」（多一步人工）；init 全程不交互（targets 配对、代理 env 落不了地） |
| D7 | **下载与替换用 agentd 内置 Go 实现** | `install.sh` 只在「本机还没有 handoff」时用一次。让常驻进程去联网下载并执行远程脚本，等于把「执行远程脚本」做成常规路径，安全面白扩一圈；且脚本装到 `~/.local/bin`，agentd 可能装在别处 | agentd 联网重跑 install.sh |
| D8 | **版本检查打 `api.github.com/.../releases/latest`** | 一次请求同时拿到 tag、资产列表和下载 URL，能顺带验「本平台资产确实存在」。匿名限流 60 次/小时/IP，6 小时一轮绰绰有余 | 解析 `releases/latest` 的 302（不限流，但只能拿 tag，资产 URL 只能硬拼，缺失要到下载时才发现） |
| D9 | **优雅退出让管理器拉起** | 见 D1。**要求把 [deploy/handoff-agentd.service](../../../deploy/handoff-agentd.service) 的 `Restart=on-failure` 改成 `Restart=always`**——`exit(0)` 是成功退出，`on-failure` 不会拉起 | agentd 调 `systemctl restart` / `launchctl kickstart -k`（需额外权限，自己 restart 自己有时序问题） |
| D10 | **两道防线，不做自动回滚** | ① 替换前跑新二进制的 `handoff version` 自检，挡下载损坏/架构拿错/动态库缺失；② 旧二进制留成 `handoff.prev`，`handoff upgrade --rollback` 人工回退。自动回滚需要「新版启动后自证健康」的握手协议，而它挡不住的只剩「能起来但有逻辑回归」——那种情况自动回滚也判断不出来 | 自动回滚 |
| D11 | **自有域名只做重定向，只服务人工命令** | 版本检查与资产下载直接打 GitHub 原生 URL。域名过期、DNS 故障、重定向规则改错，任何一样都会让所有机器的自动更新一起哑掉，而这三样都在 GitHub 之外 | 自动更新链路也走自有域名 |
| D12 | **空闲判据 = `running` + `waiting_answer` 为 0** | `pending` 是还没起 executor，重启后 agentd 照常处理；`waiting_review` 是回合已结束、executor 空闲等审核者指令，重启安全（B18 已验），而它可能挂几小时几天——算进去等于让升级无限期阻塞 | 所有非终态都阻塞；只有 running 阻塞 |
| D13 | **CLI 限流后台检查 + 下次运行提示，不自动替换** | CLI 是交互工具，在用户敲命令时不知情地换掉自己不合适；脚本化场景下行为会突变 | CLI 也自动替换；CLI 完全不查 |

## 4. 设计

### 4.1 版本号

tag `vX.Y.Z` 是唯一真相。

- Actions 构建时注入：`-ldflags "-X github.com/xushixin/handoff/internal/buildinfo.releaseVersion=${TAG}"`
- `internal/buildinfo` 新增包级 `var releaseVersion string`（默认空），`Read()` 把它填进 `proto.BuildInfo.Version`
- `proto.BuildInfo` 新增 `Version string \`json:"version,omitempty"\`` 字段
- 本地 `go build` 出来的二进制 `Version` 为空，[cmd/status.go](../../../cmd/status.go) 的展示**退回现有的 vcs.revision 行为**——现有比较逻辑一行不动，只是多一个优先级更高的来源

新增 `handoff version` 子命令：打印版本到 stdout。**它同时是自更新的自检探针**（§4.6 步骤 ⑤），所以输出格式必须稳定且可精确比对——第一行为纯版本字符串（release 构建为 `vX.Y.Z`，非 release 构建为 `unknown`），其余信息（revision、Go 版本、平台）打在后续行。

### 4.2 Release 流水线

`.github/workflows/release.yml`，`push` tag `v*` 触发：

```
matrix: [darwin/arm64, darwin/amd64, linux/amd64, linux/arm64]
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
    -ldflags "-s -w -X github.com/xushixin/handoff/internal/buildinfo.releaseVersion=${TAG}" \
    -o handoff .
  tar czf handoff_${TAG}_${os}_${arch}.tar.gz handoff
汇总 job:
  sha256sum handoff_*.tar.gz > checksums.txt
  gh release create ${TAG} --generate-notes  <4 个 tar.gz> checksums.txt
```

**资产命名 `handoff_<tag>_<os>_<arch>.tar.gz` 是一处约定、两处消费**（`install.sh` 与 Go 里的自更新都按此拼名），改名必须同步改三处。

### 4.3 `install.sh`

放在仓库根。访问入口 `https://handoff.gosuper.dev/install` **302 重定向**到 `https://raw.githubusercontent.com/Xsxdot/handoff/main/install.sh`（`curl -fsSL` 的 `-L` 跟随重定向）。脚本的唯一真相在仓库里，改脚本 = 推 main，域名侧零改动。

动作序列：

1. `uname -s` / `uname -m` 归一成 `darwin|linux` × `amd64|arm64`；不在矩阵内（含 Windows）→ 报错退出并说明原因（Windows 需说明是 B37 未完成，不是遗漏）
2. 取 latest tag：**用 `https://github.com/Xsxdot/handoff/releases/latest` 的 302 解析**，不打 API——避免匿名限流影响安装本身
3. 下载 `.tar.gz` 与 `checksums.txt`，`sha256` 校验；不匹配即中止，不留残骸
4. 解包 → `install -m 0755` 到 `~/.local/bin/handoff`（已存在则原子替换，**不报错**，脚本可重复跑）
5. `~/.local/bin` 不在 `PATH` → 打印该往哪个 rc 文件加哪一行。**只打印，不改用户的 rc 文件**
6. 末尾 `"$BIN" init < /dev/tty`；`/dev/tty` 打不开（CI、非交互 ssh）→ 跳过并打印「未交互配置，请手动运行 `handoff init`」

脚本自身无法自校验（信任模型是「你信任这个域名/GitHub 账号」），因此**二进制的 checksum 校验是必须的，不得省略**。

### 4.4 `handoff init`

**探测**（`internal/toolchain`）：四家 executor 各查 `exec.LookPath` + 凭证判据，打印一张表。

判据一律用**轻量本地判据**（可执行文件是否在 PATH + 凭证文件是否存在），**绝不在 init 里发起真实模型调用**。README 里「`claude -p "hi"` 能出结果即视为就绪」是给人看的验证方法，不是 init 能用的判据——那是一次真实的付费模型调用，几秒到几十秒，且需要网络。

凭证路径已在 devbox 上查实（2026-08-11）：

| executor | 装了 | 凭证判据 | 判据可靠性 |
|---|---|---|---|
| opencode | `LookPath("opencode")` | `~/.local/share/opencode/auth.json` 存在 | 可靠 |
| codex | `LookPath("codex")` | `~/.codex/auth.json` 存在 | 可靠 |
| grok | `LookPath("grok")` | `~/.grok/auth.json` 存在 | 可靠 |
| claude | `LookPath("claude")` | **无可靠文件判据** | 见下 |

**claude 的例外必须如实呈现**：devbox 上不存在 `~/.claude/.credentials.json`，Claude Code 的 OAuth 凭据存在 macOS Keychain 里，轻量判据够不着（`~/.claude.json` 存在但那是配置不是凭证，不能拿来当登录判据）。所以 claude 只有两态：**没装 / 已安装（登录态未知）**。UI 上如实标「登录态未知」，**不得猜成「就绪」，也不得猜成「未就绪」**——猜哪边都是编造。

其余三家三态：**没装 / 已安装但无凭证 / 就绪**。

探测结果**只影响候选项的排序与标注，不阻断任何选择**——没装任何 executor 也能配完（纯审核者机的正常情况），选了一个「无凭证」的执行者也只警告不拦。

探到 codex 已装时额外打印 B30 的提醒（**只打印，不问**）：

> codex 若需代理才能连 OpenAI，请在 config.yaml 的 `env` 段配 `codex: codex.env`。漏配的症状是会话建得起来、状态 running、一个 token 不产，只有 serve.log 里刷 `failed to refresh available models`。

**问答面**（共 11 项，先问角色再按角色分支）：

| 问 | 何时问 | 出厂默认 |
|---|---|---|
| 角色（执行机 / 审核者机 / 两者） | 总是 | 探到就绪 executor 则默认「执行机」 |
| `executor.default` | 角色含执行机 | 探到的第一个就绪项，否则 `opencode` |
| `executor.model` | 角色含执行机 | 空（用执行者自身默认） |
| `listen` | 角色含执行机 | `127.0.0.1:7777`，并提示「要被外机访问需改成 `0.0.0.0:7777`」 |
| `repo_root` | 角色含执行机 | 空 |
| `approver.executor` / `approver.model` | 角色含执行机 | 空（不启用审批链） |
| 服务托管（是否现在装并启动） | 角色含执行机 | 是 |
| `update.auto` / `update.interval` | 总是 | `true` / `6h` |
| `sync.auto` | 角色含审核者机 | `true` |
| `targets` 配对（名字 + addr + token + ssh user） | 角色含审核者机 | 循环添加，可跳过 |
| 本机 token + 现成配对 yaml 片段展示 | 角色含执行机，末尾打印 | — |

**未进 init 的项一律用出厂默认写盘**：`datadir`、`stalltimeout`、`env`、`approver.timeout`、`approver.blacklist`、`terminal.auto`。

**幂等**：`config.yaml` 已存在时，每一问的默认值取**当前配置的实际值**而非出厂值，回车即保持不变。因此 `handoff init` 可随时重跑当改配置用。

**非交互降级**：stdin 非 tty 时不问任何问题，只做探测 + 写出厂默认配置，并打印「未交互配置，请手动运行 `handoff init`」。

### 4.5 `handoff service` 与托管

`internal/service` 提供平台无关接口，`launchd.go` / `systemd.go` 两实现。

- **macOS**：`~/Library/LaunchAgents/dev.gosuper.handoff.agentd.plist`，`KeepAlive: true`（对应 systemd 的 `Restart=always`），`AbandonProcessGroup: true`
- **Linux**：`/etc/systemd/system/handoff-agentd.service`，需 sudo；**无权限时必须明确提示「需要 sudo」而不是扁平报 500/权限错误**（B45 的教训：真因只落在日志里等于没有）
- 单元的 `ExecStart` 写 `<EvalSymlinks 解析后的绝对路径>/handoff agentd`，**不带 `--executor`**（D5）
- `install` 的动作：生成单元 → 写盘 → 加载 → 启动 → **复核真的起来了**（探 `listen` 端口或查管理器状态），失败则回滚（卸载单元）并报真因
- `uninstall` / `status` 对称实现

**现有 systemd 模板要改**：[deploy/handoff-agentd.service](../../../deploy/handoff-agentd.service) 的 `Restart=on-failure` → `Restart=always`（D9）。`KillMode=process` 保持不变（B36 的硬要求）。副作用要写进 README：手动 Ctrl-C 会被管理器拉回，停服务需 `systemctl stop` / `launchctl bootout`。

### 4.6 自更新数据流

agentd 启动时若 `update.auto` 为 true，起一个 goroutine，每 `update.interval` 跑一轮：

```
① 已有 pending 待命？ → 有则直接跳到 ⑥
② GET api.github.com/repos/Xsxdot/handoff/releases/latest → tag
③ tag == 本进程版本 → Debug「已是最新」，本轮结束
④ 下载本平台资产到「当前二进制同目录」的临时文件 .handoff.new-<tag>
   （必须同目录：rename 的原子性只在同一文件系统内成立）
⑤ 校 sha256 → chmod 0755 → 跑 `.handoff.new-<tag> version`，首行须等于期望 tag
   → 过了才写 <DataDir>/update/pending.json（版本、临时文件路径、下载时刻）
   → Info「新版本已就绪 vX，等空闲窗口」
⑥ 空闲判定：running + waiting_answer 计数为 0？（D12）
⑦ 是 → os.Executable() + EvalSymlinks 拿真实路径
      → 现二进制 rename 成 handoff.prev
      → 临时文件 rename 到目标路径
      → 触发优雅关停
⑧ 优雅关停：停收新请求 → http.Server.Shutdown(10s 上限) → 关 SQLite → 释放 DataDir 锁 → exit(0)
⑨ 管理器拉起新二进制；新进程见 pending.json 的版本 == 自己的版本
   → 删掉 pending.json，Info「更新完成 vOld → vNew」
```

**优雅关停是新建能力，不是复用**。[cmd/agentd.go:13](../../../cmd/agentd.go) 明确写着「优雅关停（signal 处理）不在 MVP 范围，进程退出即断开全部连接」；`ListenAndServe` 是启动流程最后一条语句，`defer st.Close()` 实际永远跑不到。本次新建 `internal/agentd/shutdown.go`：接 SIGTERM/SIGINT + 自更新的内部触发，走同一条关停路径。

### 4.7 错误处理

| 场景 | 处理 |
|---|---|
| 查版本失败（网络 / 限流 / GitHub 挂） | Warn 一行，本轮结束。**不重试不退避**——`interval` 本身就是退避 |
| 本进程版本未知（非 release 构建） | Warn **一次**（用一个 sync.Once 之类的闸，不每轮刷），跳过自动更新。提示「手动升级用 `handoff upgrade --now`」 |
| checksum 不匹配 | Error，删临时文件，本轮结束。**不重试**——完整性失败重试只会重下同一份坏数据 |
| 自检失败（新二进制跑不起来） | Error 带 stderr 原文，删临时文件，**记住该 tag 已失败，同 tag 不再重试**（避免每轮白下一次） |
| 二进制所在目录不可写（装在 `/usr/local/bin`） | Warn 一次说明真因与两条解法（装到 `~/.local/bin`，或手动 `handoff upgrade`），跳过自动更新 |
| rename 失败（跨文件系统 / 权限） | Error，保留 pending，下轮重试 |
| **当前进程非托管启动** | **拒绝自动更新**：只下载待命，Warn「vX 已就绪，但当前进程非托管启动，自动重启会导致 agentd 停止服务；请先 `handoff service install`，或手动 `handoff upgrade --now`」。见下 |
| 长期有活跃任务，一直不空闲 | 每轮 Info「vX 已就绪，等 N 个活跃任务」；`handoff status` 同步显示；`handoff upgrade --now` 强制立即换 |
| 新版起来后崩溃循环 | 不自动回滚（D10）。`handoff.prev` 在，`handoff upgrade --rollback` 人工回退 |

**「非托管则拒绝自动更新」是最重要的一条防线**。如果 agentd 不是被管理器拉起的（当前两台机器的现状），换完 `exit(0)` 之后没人拉起，机器上就此没有 handoff 在跑，而且没有任何信号告诉任何人。这条把该场景从「可能」变成「不可能」。

**判据不能用 PPID**。手工 `nohup` / `zsh -c … &` 起的进程被 init 收养后 PPID 同样是 1——本机 pid 73202 正是这种：`PPID=1` 且完全未托管。拿 PPID 1 当托管判据会把所有裸进程误判成托管，正好打穿这条防线。

正确判据是**进程管理器注入的环境变量**：

- Linux / systemd：`INVOCATION_ID` 非空（systemd 为每个 unit 调用注入唯一 id）
- macOS / launchd：`XPC_SERVICE_NAME` 非空且等于 job label

判不出来时**按「非托管」处理**（fail-closed）。

> **实现前必须验证**：这两个环境变量的实际存在性与取值，要在 devbox 的隔离实例上各起一次托管进程实测（`handoff service install` 后读进程自身的 environ），不得凭印象写死。这条并入 P1 探针一起做。

### 4.8 CLI 侧更新提示

- 每 24h 最多查一次，**异步进行，不阻塞当前命令**，结果写缓存文件（`<DataDir>/update/cli-check.json`）
- 下次运行任何子命令时读缓存，若有新版则在 **stderr** 打一行：`有新版本 vX（当前 vY），运行 handoff upgrade 升级`
- 不自动替换（D13）

### 4.9 配置变更

`internal/config` 新增：

```yaml
update:
  auto: true      # 默认 true
  interval: 6h    # 默认 6h
```

```go
// UpdateConfig 描述 agentd 的自动更新行为。
type UpdateConfig struct {
    Auto     bool
    Interval time.Duration
}
```

`Config` 加 `Update UpdateConfig` 字段；`Load` 的默认字面量加 `Update: UpdateConfig{Auto: true, Interval: 6 * time.Hour}`；**[config.go:210](../../../internal/config/config.go) 那条「未知字段」错误消息里的键名清单必须同步加上 `update{auto,interval}`**——否则用户配了正确的键却看到「不支持」的误导性报错。

## 5. 文件结构

**新增**

| 文件 | 职责 |
|---|---|
| `install.sh` | 探测 os/arch → 拉资产 → 校 sha256 → 装进 `~/.local/bin` → `handoff init < /dev/tty` |
| `.github/workflows/release.yml` | tag 触发：四平台交叉编译 → checksums → 建 Release 传资产 |
| `cmd/version.go` | `handoff version`；同时是自更新的自检探针 |
| `cmd/init.go` | `handoff init`：探测 + 11 项问答 + 写配置 |
| `cmd/service.go` | `handoff service install/uninstall/status` |
| `cmd/upgrade.go` | `handoff upgrade [--check\|--now\|--rollback]` |
| `internal/toolchain/detect.go` | 探测四家 executor 的三态 |
| `internal/release/client.go` | 查 GitHub latest release，解析 tag 与本平台资产 URL |
| `internal/release/install.go` | 下载 → 校验 → 自检 → 原子替换 → 留 `handoff.prev` |
| `internal/service/service.go` | 托管接口 + 平台分发 |
| `internal/service/launchd.go` | macOS plist 生成/加载/卸载 |
| `internal/service/systemd.go` | Linux unit 生成/加载/卸载 |
| `internal/selfupdate/updater.go` | 更新循环：定时查 → 下好待命 → 等空闲 → 触发退出 |
| `internal/agentd/shutdown.go` | 信号处理 + `http.Server.Shutdown` + SQLite 关闭 + 退出码约定 |

**修改**

| 文件 | 改动 |
|---|---|
| `internal/config/config.go` | 加 `update` 段 + 默认值 + 未知字段消息里的键名清单 |
| `internal/buildinfo/buildinfo.go` | 加 `releaseVersion` 注入点，填进 `proto.BuildInfo.Version` |
| `internal/proto/status.go` | `BuildInfo` 加 `Version` 字段 |
| `cmd/agentd.go` | 接优雅关停；启动时按 `update.auto` 起 selfupdate 循环 |
| `cmd/status.go` | 版本展示优先用 `Version`，为空时退回现有 revision 逻辑；显示 pending 更新状态 |
| `deploy/handoff-agentd.service` | `Restart=on-failure` → `Restart=always` |
| `README.md` | 安装章节改成一行安装；补 `init` / `service` / `upgrade` 说明；补「停服务需 systemctl stop」的形态变化 |

**边界**

- `install.sh` 只在「本机还没有 handoff」时用一次。它不懂服务单元、不懂配置、不 sudo。后续所有换版都走 Go 里那一份实现。
- `internal/release` 只负责「把某个版本的二进制正确落到某个路径」，不知道 agentd、不知道任务、不决定何时替换。**何时替换是 `internal/selfupdate` 的事**。
- `internal/service` 只负责「生成/安装/卸载/查询服务单元」，不下载、不判断版本。

## 6. 实施分期

本 spec 覆盖面较大，但三段之间是**线性依赖**而非彼此独立的子系统，因此不拆成三个 spec；拆的是**交付节奏**。每期结束都是可工作、可验收、可停下的软件。

| 期 | 交付物 | 范围 | 验收 | 可以停在这里吗 |
|---|---|---|---|---|
| **A · 供给与安装** | 「一行装上 handoff」 | `handoff version`、`buildinfo` 版本注入、`proto` 扩字段、`status` 展示、`.github/workflows/release.yml`、`install.sh` | 打一个 tag，四平台资产 + checksums 产出；干净机器上一行命令装出可用二进制（P2） | 能。此时换版仍是人工重跑安装命令，但已经比 scp 好得多 |
| **B · 配置与托管** | 「装完能配、能被托管」 | `handoff init`、`internal/toolchain`、`handoff service`、`internal/service`、`internal/agentd/shutdown.go`、systemd 模板改 `Restart=always`、config 加 `update` 段 | init 在两种角色下都能配完并幂等重跑；service install→status→uninstall 往返；**P1 探针通过** | 能。此时 agentd 有人托管、崩了会被拉起，只是不会自己换版 |
| **C · 自动更新** | 「自己换版」 | `internal/release`、`internal/selfupdate`、`handoff upgrade`、CLI 更新提示、agentd 接更新循环 | P3（完整自更新一轮）、P4（有任务时不换、任务结束后换） | 这是终点 |

**P1 是 B 期的阻塞探针**，必须在 `internal/service/launchd.go` 开工之前完成（见 §10）。

对应的 backlog 结构：B54 转 `📦 epic`，拆出 `B54.1 供给与安装` / `B54.2 配置与托管` / `B54.3 自动更新` 三个一级 leaf，共用本 spec。三个 leaf 依次领取、依次交给 writing-plans。

## 7. 测试策略

### 单元测试（表驱动 + fake HTTP server）

| 包 | 覆盖点 |
|---|---|
| `internal/release` | 资产名拼装（四平台）、checksum 通过/失败、自检通过/失败、原子替换、`prev` 保留、目标目录不可写 |
| `internal/toolchain` | 四家探测的三态（没装 / 装了未就绪 / 就绪） |
| `internal/service` | plist 与 unit 的 golden 文本、install→status→uninstall 往返、无权限时的错误消息含「sudo」 |
| `internal/selfupdate` | 空闲判定（六态各一例，验 D12 的判据）、非托管拒绝、同 tag 失败不重试、pending 持久化与重启后清理、版本未知时只 Warn 一次 |
| `internal/config` | `update` 段默认值、显式配置覆盖、未知字段消息含 `update{auto,interval}` |
| `internal/buildinfo` | 注入版本时返回它，未注入时 `Version` 为空且 revision 行为不变 |
| `cmd` | `handoff version` 首行格式稳定（自检依赖它） |

### 真机探针（devbox）

**隔离要求与 B52 一致**：独立端口、独立 DataDir、独立配置文件（每条命令带 `--config`）、`HOME` 指向临时目录（`cursorPath` 硬编码取 `os.UserHomeDir()`）、独立二进制路径。**绝不触碰 7777 上的生产 agentd，绝不触碰 `~/.handoff/`**。停止验证实例只能按二进制全路径精确匹配，禁止 `pkill -f agentd`。

| 探针 | 内容 | 阻塞性 |
|---|---|---|
| **P1** | **launchd 重启 agentd 时执行者是否活下来**。macOS 没有 `KillMode=process` 的直接等价物；已知要设 `AbandonProcessGroup: true`，但是否足够未经验证 | **阻塞设计**：必须先于实现完成。不过则 macOS 托管方案要重想（可能退回「不托管 macOS，只在 Linux 上启用自动更新」） |
| P2 | `install.sh` 在干净环境跑通（至少 darwin/arm64 与 linux/amd64） | 阻塞发布 |
| P3 | 完整自更新一轮：发 `v0.0.1-test` → 装上 → 发 `v0.0.2-test` → 观察走完 §4.6 全流程，日志链完整 | 阻塞发布 |
| P4 | 有活跃任务时不换（观察 Info「等 N 个活跃任务」），任务结束后自动换 | 阻塞发布 |

## 8. 人工前置动作

代码之外、需要人做一次的事：

1. **给 `gosuper.dev` 加重定向规则**：`handoff.gosuper.dev/install` → 302 → `https://raw.githubusercontent.com/Xsxdot/handoff/main/install.sh`（Cloudflare 重定向规则或 DNS 服务商的 URL 转发均可，不需要服务器）。
2. **打第一个 tag**：Actions 就位后 `git tag v0.1.0 && git push --tags`，验证流水线产出四个资产 + checksums.txt。
3. **两台现有机器的迁移**：devbox 与本机的 agentd 目前是裸进程且二进制不是 release 构建，装上新版并 `handoff service install` 之后自动更新才会生效。**迁移必须挑没有活跃任务的时机**（停旧起新期间 7777 会短暂不可用）。

## 9. 已知风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| P1 探针不过（launchd 杀执行者） | macOS 上自动更新会中断正在跑的任务 | 探针阻塞设计。不过则退回「macOS 不托管 / 不自动更新」，Linux 侧照做 |
| 新版有逻辑回归但能正常启动 | 自动传播到所有机器 | 设计内无解（D10 已说明自动回滚也判断不出来）。缓解靠发版纪律：tag 前跑全量测试 |
| GitHub API 匿名限流 | 版本检查失败 | 6h 一轮远低于 60 次/小时；失败即 Warn 跳过，下轮再来，不影响任何既有功能 |
| 同一台机器上多个 agentd 实例（如验证实例）都开了自动更新 | 互相干扰 | 验证实例的配置一律 `update.auto: false`；且 §4.7 的「非托管则拒绝」已挡住绝大多数情况 |

## 10. 待验证的空白

- **launchd 的 `AbandonProcessGroup: true` 是否足以让 setsid 的执行者活过 agentd 重启**——P1 探针要回答的正是这个。在它出结果之前，`internal/service/launchd.go` 的实现不应开工。
