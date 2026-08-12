# executor 发现不再依赖「恰好正确的 PATH」（B71）设计

## 1. 范围与动机

2026-08-12，devbox（`/Users/sycm`）重启后第一次派发失败：

```
opencode 未安装: exec: "opencode": executable file not found in $PATH
```

opencode 装在 `/Users/sycm/.opencode/bin/`，该目录**不在用户的任何一个 rc 文件里**（`.zshrc`/`.zprofile` 只追加了 `go/bin`、`.grok/bin`、`.local/bin`）。重启前那个 agentd 之所以能跑，是因为它从一个恰好带着这个目录的 shell 里被拉起来的。用显式 `PATH=…` 重起一次能恢复，但下次重启会再踩一遍。

这不是一台机器的配置事故，是**产品缺陷**：handoff 把「executor 在哪」完全外包给了两个都不可靠的来源——进程继承的 PATH，和用户的 rc 文件。发版之后每一台执行机都会以同样的方式失败，且失败时间点是「重启之后的第一次派发」，离根因最远。

本 spec 覆盖三处：**运行期解析**（agentd 启动）、**安装流程**（`install.sh` → `handoff init`）、**托管**（`handoff service install` 的触达率）。

**不在范围内**：

- 不改四个 adapter 的 `exec.LookPath` 调用点。PATH 在 agentd 启动期就补全了，`LookPath` 自然能找到；改调用点等于把同一件事在四个地方各做一遍。
- 不做 nvm 版本目录的 glob 展开，理由见 §3.2。
- 不动 codex 现有的硬预检（`codex.Preflight`）。
- 不改 `install.sh` 「不写服务单元、不改 rc、不 sudo」的既有边界。

## 2. 事实基础：实测核实过的四条

设计基于以下实测，不是推断。

### 2.1 B7 的登录 shell 合并是活的，但对本次故障无效

`internal/agentd/loginpath.go`（B7，`98e027de`）在 agentd 启动时用 `$SHELL -l -i -c 'printf %s "$PATH"'` 取登录 shell 的 PATH 并追加。本机 launchd 实例的启动日志证明它生效：

```
msg="已合并登录 shell 的 PATH" shell=/bin/zsh added=[/Users/…/.kimi-code/bin /Users/…/.grok/bin … 共 24 个目录]
```

**但它的信息源是用户的 rc 文件。** `~/.opencode/bin` 不在任何 rc 文件里，登录 shell 自己都不知道这个目录，agentd 更不可能知道。B7 修的是「agentd 拿不到用户 PATH」，本次故障是「用户 PATH 里本来就没有它」——两个不同的洞。

### 2.2 托管下 agentd 拿到的 PATH 就是四条系统目录

本机 `launchctl print gui/501/dev.gosuper.handoff.agentd`：

```
inherited environment = { SSH_AUTH_SOCK => … }
default environment   = { PATH => /usr/bin:/bin:/usr/sbin:/sbin }
environment           = { OSLogRateLimit => 64, XPC_SERVICE_NAME => … }
```

`ps eww` 复核 agentd 进程实际环境：`PATH=/usr/bin:/bin:/usr/sbin:/sbin`，`HOME`、`USER`、`SHELL` 都有。

两条结论：

- **`$SHELL` 在 launchd 下是有的**，B7 那一层在托管形态下能跑（这一点此前没有被验证过，属于运气）。
- **`HOME` 在 launchd 下是有的**，§3 的已知目录扫描有立足点。

`internal/service/launchd.go` 的 `plistBody` 不写 `EnvironmentVariables`，`internal/service/systemd.go` 的 `unitBody` 不写 `Environment=PATH=`——两边都没有任何 PATH 约定。

### 2.3 探测与运行是两套口径

`handoff init` 的探测走 `internal/toolchain` 的裸 `exec.LookPath`（`detect.go:23`），跑在用户的交互 shell 里；agentd 的 `LookPath` 跑在补全过的进程 PATH 里。两者的 PATH 来源不同，**同一台机器上 init 说「就绪」而 agentd 说「未安装」是可能的**，反之亦然。这次故障现场恰好没暴露这一点，但它是同一个根因的第二个出口。

### 2.4 安装流程从头到尾没提过 `handoff init`

`install.sh` 的 `main()` 结尾只做两件事：跑 `handoff skill install`、在 `INSTALL_DIR` 不在 PATH 时提示一行。**没有任何一句指向 `handoff init`**，更不会提到托管。`handoff init` 自己在最后打了一行 `下一步 handoff service install`（`cmd/init.go:81`）——但用户要先知道该跑 init 才看得到它。

托管是「重启后 agentd 还在」的唯一保障，而它躺在两跳之外的一行提示里。devbox 那台就是这么变成手工拉起的。

## 3. 设计 A：`internal/pathenv` —— PATH 解析的唯一真相

### 3.1 包职责与接口

新包 `internal/pathenv`，把「本进程能看到的 PATH」补成「这台机器上用户实际可用的 PATH」。`internal/agentd/loginpath.go` 整体迁入（连同其测试），`agentd.MergeLoginShellPATH` 随之删除——它只有 `cmd/agentd.go` 一个调用方。

```go
// Options 描述一次解析要启用哪些来源。
type Options struct {
    // IncludeLoginShell 是否执行登录 shell 取 PATH（约 0.1–3s）。
    // agentd 必须开；CLI（init）关——它本来就跑在登录 shell 里。
    IncludeLoginShell bool
    // ExtraDirs 是 config.path_dirs：用户显式声明的目录。
    ExtraDirs []string
}

// Apply 解析并写回 os.Setenv("PATH")，返回本次新增的目录（按加入顺序）。
// 任何一步失败都只记 WARN，绝不返回 error——PATH 不全是「找不到某些工具」，
// 而启动失败是「整机不可用」。
func Apply(ctx context.Context, opt Options, log *slog.Logger) (added []string)
```

**为什么返回 `added`**：init 要用它来判断「这个工具是靠补全才找到的」，从而对用户说清楚（§5.2）。不返回就只能让 init 自己再算一遍，那就又是两套口径。

### 3.2 四个来源与优先级

按顺序追加，**一律不覆盖既有条目**（沿用 B7 的边界：不改动 launchd/systemd 等显式注入的路径优先级）：

| 序 | 来源 | 说明 |
|---|---|---|
| 1 | 进程继承的 `PATH` | 原序保留，最高优先 |
| 2 | 登录 shell PATH | `$SHELL -l -i`，B7 原样迁入（3s 超时、只取 stdout、不看退出码、stderr 丢弃）。仅 `IncludeLoginShell` 时 |
| 3 | `config.path_dirs` | 用户显式声明，优先于内置猜测 |
| 4 | 内置已知目录表 | **目录存在才追加** |

去重按目录字符串精确匹配（不做 symlink 归一：`EvalSymlinks` 会在网络盘/权限受限目录上引入新的失败模式，而重复条目对 `LookPath` 无害）。

内置表，每条对应一个真实安装落点：

| 目录 | 谁装在这 |
|---|---|
| `$HOME/.opencode/bin` | opencode 官方安装器（**本次故障现场**） |
| `$HOME/.grok/bin` | grok CLI |
| `$HOME/.claude/local` | Claude Code 本地安装（migrate installer 落点） |
| `$HOME/.local/bin` | Claude Code native install / pipx / handoff 自己 |
| `$HOME/bin` | 传统用户 bin |
| `$HOME/.bun/bin` | bun 全局 |
| `$HOME/.npm-global/bin` | npm 自定义 prefix 的常见落点 |
| `$HOME/.cargo/bin` | rust |
| `$HOME/go/bin` | go |
| `/opt/homebrew/bin`、`/opt/homebrew/sbin` | Homebrew（Apple Silicon） |
| `/usr/local/bin`、`/usr/local/sbin` | Homebrew（Intel）/ 手工安装 |
| `/snap/bin` | Linux snap |

**为什么不展开 `~/.nvm/versions/node/*/bin`**：用 nvm 的机器，rc 文件里必然有 nvm 初始化，来源 2 已经覆盖且拿到的是**用户当前选中的那个版本**。glob 只能靠字典序猜一个版本，猜错时的症状（工具在、但 node 版本不对）比找不到更难诊断。

`HOME` 取 `os.UserHomeDir()`，失败退 `user.Current().HomeDir`；两者都失败则跳过全部 `$HOME` 系条目并打一条 WARN（老 systemd 不为 `User=` 设 `HOME` 的情况）。

### 3.3 日志

- 有新增：`INFO 已补全 PATH`，带 `login_shell`/`extra_dirs`/`known_dirs` 三段各自新增了什么。B7 现在只打一个扁平的 `added`，分不清某个目录是哪一层带来的——而排障时「它是靠内置表兜住的」和「它本来就在你 rc 里」是完全不同的结论。
- 无新增：`INFO PATH 无需补全`。
- 每一步失败：`WARN` + 原因，继续走下一步。

## 4. 设计 B：agentd 启动自检

`cmd/agentd.go` 里 `agentd.MergeLoginShellPATH(...)` 换成 `pathenv.Apply(ctx, pathenv.Options{IncludeLoginShell: true, ExtraDirs: cfg.PathDirs}, logger)`。**位置不变**：必须早于任何 fork 子进程的动作。

紧随其后加一段启动自检：

```go
results := toolchain.Detect()
// 四家各自解析到哪个绝对路径（或没找到），成表写进启动日志
logger.Info("executor 探测", "opencode", …, "claude", …, "grok", …, "codex", …)
if 缺省执行者那家是 StateMissing {
    logger.Warn("缺省执行者未找到，派发会失败",
        "executor", cfg.Executor.Default,
        "处置", "在本机安装它，或把它所在目录写进 config.yaml 的 path_dirs")
}
```

**不阻断启动**：一台机器不该因为少装一个 executor 就彻底起不来（托管形态下还会变成崩溃循环）。codex 那条现有的硬预检保持不变——它拦的是「缺省执行者是 codex 且环境不全」这个更窄的判据，与本条不冲突。

这一条是本次故障的直接对策：重启完看一眼启动日志就知道 opencode 在不在，而不是等下一次派发失败。

## 5. 设计 C：`handoff init`

### 5.1 探测前先补全

`toolchain.Detect()` 之前先跑 `pathenv.Apply(ctx, Options{IncludeLoginShell: false, ExtraDirs: cfg.PathDirs}, log)`，让 init 的探测表与 agentd 将来看到的是同一件事。

关掉登录 shell 那一层是有意的：init 本来就跑在用户的登录 shell 里（来源 2 的内容已经在继承的 PATH 里了），再跑一次只是白等最多 3s。

### 5.2 探测表说清楚「是补出来的」

`printDetection` 增加一个入参 `added []string`（`Apply` 的返回值）。当某个工具的解析路径落在 `added` 里的某个目录下时，在该行下面补一句：

```
  opencode  就绪  /Users/sycm/.opencode/bin/opencode
            ↳ 该目录不在你的 PATH 里，agentd 启动时会自动补上。
```

这句话是整个改动里用户唯一能直接看到的价值：它同时回答了「为什么我 shell 里 which opencode 找不到，handoff 却说就绪」和「重启之后还灵不灵」。

### 5.3 追问托管

角色选了执行机或两者、且 stdin 是 tty 时，在 `printPairing` 之后追问：

```
现在把 agentd 交给 launchd/systemd 托管吗？(Y/n)
```

- 答 Y（默认）：走与 `handoff service install` **同一条代码路径**——把 `serviceInstallCmd.RunE` 的主体抽成 `installService(out io.Writer, cfgPath string) error`，两处共用。托管本身的输出（单元路径、日志路径、那两句「Ctrl-C 会被拉回」）原样打印。
- 答 n / 非 tty / 安装失败：退回现在的行为，打印可直接复制的 `handoff service install`。**安装失败不让 init 失败**——配置已经写盘了，为一个附属动作把整条 init 退非零，用户会以为配置没保存（与 `install.sh` 对 `skill install` 的处置同一个道理）。
- **Linux 上非 root 时不代跑**：systemd 单元要写 `/etc/systemd/system`，需要 root。此时只打印 `sudo handoff service install`，保持 init 不 sudo 的边界。判据用 `runtime.GOOS == "linux" && os.Geteuid() != 0`。

## 6. 设计 D：`install.sh`

结尾在现有的 skill 与 PATH 提示之后，补一段指向下一步：

```
下一步   handoff init
         执行机会探测 executor 并问你是否把 agentd 交给 launchd/systemd 托管。
         没有托管的 agentd 在机器重启后不会自己回来。
```

最后那句是必须的：不说清后果，用户会把这一步当成可选的美化步骤跳过——devbox 那台正是这么来的。

`install.sh` 的边界不变：仍不写服务单元、不改 rc 文件、不 sudo。

## 7. 配置与兼容性

`Config` 顶层新增：

```go
// PathDirs 是本机额外的可执行文件搜索目录：agentd 启动时追加到 PATH 末尾。
// 内置的已知目录表（internal/pathenv）没覆盖到的安装位置写在这里。
PathDirs []string `yaml:"path_dirs,omitempty"`
```

放顶层而不是放进 `Executor`，理由与 `RepoRoot` 一致：它是「**这台机器**上工具装在哪」，不是执行者的属性。

**`omitempty` 是硬要求，不是风格问题。** 配置是 `KnownFields(true)` 严格解析的，未知键让 agentd **启动失败**。没有 `omitempty` 时，新版 `config.Save` 会把 `path_dirs: []` 写进 `config.yaml`，而一台还没换版的**旧** agentd 读到它会直接起不来——正是 B59 spec D7 那个坑的同款，只是方向反过来。加了 `omitempty`，没配过的机器磁盘上根本不出现这个键，新旧二进制都能读。

反方向（旧配置被新二进制读）天然安全：缺键即零值 `nil`。

## 8. 测试

### 8.1 `internal/pathenv`（新，表驱动）

`Apply` 的测试缝：`loginShellPATH`（B7 已有的包级 var）、`statDir`（判目录存在）、`homeDir`。

| 用例 | 断言 |
|---|---|
| 已知目录存在 | 追加进 PATH，出现在 `added` 里 |
| 已知目录不存在 | 不追加 |
| 目录已在继承 PATH 里 | 不重复追加，不进 `added` |
| `path_dirs` 与内置表都有同一目录 | 只出现一次，且位置在 `path_dirs` 的序上 |
| `path_dirs` 排在内置表之前 | 顺序断言 |
| 登录 shell 解析失败 | 其余三层照常生效，`Apply` 不 panic 不返回错 |
| `IncludeLoginShell: false` | 不调用 `loginShellPATH`（用会 panic 的假实现钉住） |
| `homeDir` 失败 | 跳过全部 `$HOME` 系条目，绝对路径条目仍生效 |
| 继承 PATH 的原有顺序 | 前缀不变 |

B7 现有的两条测试（追加缺失目录、失败时不动 PATH）随文件迁入，不丢覆盖。

### 8.2 agentd 启动自检

- 缺省执行者 `Missing` 时：打了 WARN，且 `RunE` **不**返回错误。
- 四家探测结果都进了启动日志。

### 8.3 `handoff init`

- 非 tty：不追问托管（沿用现有的 `initStdinIsTTY` 缝）。
- tty + 角色=审核者机：不追问。
- tty + 角色=执行机 + 答 n：不调用 service manager（用 fake manager 的调用计数钉住）。
- tty + 角色=执行机 + 答 Y：调用了，且 manager 返回错误时 init 仍返回 nil。
- `printDetection`：解析路径落在 `added` 里时打了那句补充说明；不在则不打。

### 8.4 `install_test.sh`

断言安装成功的输出里出现 `handoff init`。

## 9. 真机验收（devbox）

**这是唯一能证明修对了的证据，全程不允许有任何一次手工 `export PATH`。**

| 步 | 动作 | 判据 |
|---|---|---|
| V1 | 把新二进制推到 devbox（`handoff upgrade --now --target devbox`） | 版本号变了 |
| V2 | `handoff service install` | 单元装上，`handoff service status` 报已托管 |
| V3 | 从一个**裸最小 PATH** 的非交互 ssh 会话重启 agentd（`env -i` 起步，或直接 `launchctl kickstart -k`） | 启动日志里 `已补全 PATH` 的 `known_dirs` 段含 `/Users/sycm/.opencode/bin` |
| V4 | 同上一次启动的日志 | `executor 探测` 一行里 opencode 解析到 `/Users/sycm/.opencode/bin/opencode`，无 WARN |
| V5 | 真派发一个任务 | 跑通，不出现 `opencode 未安装` |
| V6 | `handoff init` 在 devbox 上重跑一次（不改配置，只看输出） | 探测表里 opencode 那行带「该目录不在你的 PATH 里」的补充说明 |

V3 里 devbox 若有他人任务在跑，先确认再重启——重启 agentd 不杀执行者（setsid，B36/B59 已实证），但仍需当事人知情。

## 10. 已知风险

- **内置目录表会随生态漂移。** 这是接受的代价：它保证「装好即能用」，`path_dirs` 保证漂移时用户不被卡死。表本身写在一处，加一行的成本是一行。
- **`init` 代跑 `service install` 扩大了 init 的职责边界**（现注释写着「不装服务」）。这条边界要在 `cmd/init.go` 的文件头注释里显式改写并说明原因，不能让代码和注释对不上。
- **`init` 追问会改变非交互脚本的行为吗**：不会，非 tty 分支在追问之前就 return 了。
- **PATH 变长**（本机实测合并后约 30 个目录）。`LookPath` 是逐目录 stat，几十个目录的开销在派发路径上可忽略。
