# W4 PTY 终端 · 真机走查记录

> 执行者：审核者会话（本机带浏览器）。
> 被走查分支：`feat/w4-pty-terminal` @ `97c52f56`。
> 依据：`docs/superpowers/specs/2026-08-12-w4-pty-terminal-design.md` §9 走查项 + §11 验收标准 12 条。
>
> 这份记录取代 `docs/superpowers/walkthroughs/2026-08-12-w4-pty-terminal.md` 里那份
> 「全部未验（待审核者在本地带浏览器的环境执行）」的骨架——执行者当时没有浏览器环境，
> 如实留了空，没有编造证据，那是对的。本次由审核者补齐。

---

## 0. 走查环境（可复现）

| 项 | 值 |
|---|---|
| 工作树 | `.claude/worktrees/w4-pty` @ `97c52f56`（`feat/w4-pty-terminal`） |
| agentd 二进制 | `/tmp/handoff-pty`（**从被走查分支现编**，不是已安装的生产二进制） |
| 旁路实例 A | datadir `/tmp/w4-pty-walk`（700），listen `127.0.0.1:7788`，独立 token（`openssl rand -hex 32`） |
| 旁路实例 B | datadir `/tmp/w4-pty-b`（700），listen `127.0.0.1:7799`，独立 token —— 充当「另一台开发机」 |
| 前端 | vite dev server，`http://localhost:5174`，经 `web/vite.config.ts` 反代到 `AGENTD_URL=http://127.0.0.1:7788` |
| 浏览器 | Browser pane，视口先后为 1440×1024 / 1100×760 / 1500×900 / 900×700（尺寸差异是走查内容之一） |

**为什么必须起旁路实例**：生产 agentd（7777）跑的是 main @ `76c3d6cc`，根本没有 PTY 接口；
而同一个 DataDir 起第二个 agentd 会被文件锁挡下。所以隔离实例 + 自己的 datadir 是唯一合规路径
（先例：B38/B48）。**全程没有停过生产 agentd，也没有改过 `~/.handoff/` 下的任何东西。**

**必须用 `localhost` 而不是 `127.0.0.1` 访问前端**：会话 cookie 是 host-only 的，两个主机名混用会得到
「登录过却 401」。vite 侧不能开 `changeOrigin`——agentd 的 Host 白名单与 WS 的 Origin 校验都要浏览器
Host 原样透传。

### 本次的两处临时改动（诊断用，已还原）

走查中为了把「环境伪影」和「真缺陷」分开，在工作树里做过两处**未提交**的临时改动，
结论落定后已 `cp` 还原，`git status --porcelain` 为空：

1. `TerminalTab.tsx` 把 `loadAddon(new WebglAddon())` 临时挂在一个 localStorage 开关后面
   —— 用来判定白屏到底是不是 WebGL 引起的（见缺陷 A）。
2. `main.tsx` 临时去掉 `<StrictMode>` —— 用来判定重复建会话与恢复失效是不是 StrictMode 双调用引起的
   （见缺陷 B、C）。

**这两处只影响「怎么观察」，不影响被观察的服务端行为。** 下面凡是依赖它们的结论，都在该条里点明了。

### 没有用到的工具

SuperDev 未接管本项目，日志与进程全部用 `handoff footprint` / `curl` / `ps` 直接取证。

---

## 1. 验收结论总表（spec §11）

| # | 验收项 | 结论 |
|---|---|---|
| 1 | 本机工作树终端可交互，`stty size` 与窗口一致，中文输入正常 | ✅ 通过 |
| 2 | home 基准（悬浮按钮）终端可用 | ✅ 通过 |
| 3 | 远程开发机终端可用，且走 WS 反代而非浏览器直连 | ⚠️ 链路已验、真远程与 UI 入口未验 |
| 4 | 终端里 `git push` / `ssh` 可用；日志有 `env_forward` 三态结论 | ✅ 通过 |
| 5 | 关页重开自动恢复、输出连续；`truncated` 先清屏不重复 | ✅ 通过（**前提：关掉 StrictMode**，见缺陷 B） |
| 6 | 两客户端同时接入：双方都看到输出、任一方可输入、尺寸取最小 | ✅ 通过 |
| 7 | 切基准**不杀**、点 `×` **才杀**，杀后 footprint 归零 | ✅ 通过 |
| 8 | shell 自己 `exit` 后显示退出码，列表标为已退出 | ✅ 通过 |
| 9 | `pty_supported=false` 的对端（Windows）说实话而非死按钮 | ❌ 未验（手上没有不支持 PTY 的对端） |
| 10 | 老 agentd（`pty_supported` 缺席）不被误判为「不支持」 | ✅ 通过（拿生产 main agentd 当真·老对端验的） |
| 11 | 未显式配置时 `config.yaml` 不含 `env_forward`，且转发照常工作 | ✅ 通过 |
| 12 | W4 spec §2.6 已追加指向 PTY spec §1 的修正说明 | ✅ 通过 |

另：spec §9 走查项 7（`pathenv` 装的目录在终端 PATH 里生效）—— ✅ 通过。

**10 条通过 / 1 条部分 / 1 条未验。** 同时发现 3 个缺陷，见 §3。

---

## 2. 逐条证据

### 1 —— 本机工作树终端 ✅

左栏选 `feat/w4-pty-terminal` 工作树 → 新终端。真 zsh 起来，提示符、颜色、cwd 都对。

```
$ stty size; echo "中文测试 OK"; echo $TERM $COLORTERM
65 122
中文测试 OK
xterm-256color truecolor
```

服务端 `GET /api/pty/sessions` 同一会话报 `cols:122 rows:65` —— 与 `stty size` 的 `65 122` 完全一致。
中文既能输入（命令行回显正确）也能输出。`TERM` / `COLORTERM` 按 spec 注入。

### 2 —— home 基准终端 ✅

右下悬浮按钮 →「基准 home（不挂在任何项目上）」→ 新终端。
面包屑显示 `home`，tab 标题 `bash · home`，`echo PWD=$PWD` → `/Users/xushixin`。
服务端该会话 `base_kind=home`。

顺带验掉 spec §9 走查项 7：

```
$ command -v handoff
/Users/xushixin/.local/bin/handoff
```

`pathenv` 装的目录确实在终端 PATH 里。

### 3 —— 远程终端与 WS 反代 ⚠️ 部分

**验到的**：起了第二个旁路 agentd B（`127.0.0.1:7799`，独立 datadir 与 token），
在 A 的配置里把它登记成 target `boxb`，然后走完了整条跨机链路：

- REST 转发：`POST /api/pty/sessions?machine=boxb` 打给 A，会话建在 **B** 上
  （A 的本机会话数 0，B 的本机会话数 1，pid 44190）。
- `scope=all` 扇出：A 的列表里该会话带 `machine="boxb"`。
- WS 反代：浏览器连 A 的 `ws://localhost:5174/ws/pty?session=…&machine=boxb`，
  收到 `{"type":"attached","since":0,"truncated":false}`，键入的字节（含 UTF-8 中文）
  原样到达 B 的 shell，输出原样回来：

  ```
  跨机反代 MacBook-Pro.local 44190
  ```

  `44190` 正是 B 上那个会话的 pid。
- close 传播：在这条反代连接上敲 `exit 7` → 收到
  `{"type":"exit","exit_code":7}` 后 `CLOSE 1000 会话已退出`。

**没验到的，以及为什么**：

- 真的 devbox。devbox 上跑的是 main，没有 PTY 接口；要验它得先把这个分支部署过去，
  那已经不是走查而是发布。
- **UI 上的远程终端入口**。左栏的机器节点来自**项目位置**（`internal/agentd/projectfanout.go` 是现场扇出），
  旁路实例 B 的项目库是空的，所以 `boxb` 压根不会出现在树上，点不到。
  这不是 PTY 的问题，是我这套「假远程」拓扑的固有限制。

所以这条记 ⚠️：**反代这段全新基础设施本身是通的**（这也是 spec 点名要端到端钉子的那段），
但「在真远程机上从界面点出一个终端」没验。

### 4 —— ssh / git 与 env_forward 三态 ✅

终端里：

```
$ echo "SOCK=${SSH_AUTH_SOCK:-EMPTY}"; ssh -T git@github.com; echo rc=$?
SOCK=/var/run/com.apple.launchd.***/Listeners
Hi Xsxdot! You've successfully authenticated, but GitHub does not provide shell access.
rc=1
```

`rc=1` 是 GitHub 对 `ssh -T` 的正常退出码，认证本身是成功的——**agent socket 确实转发进了 PTY 会话**。
（只跑了 `ssh -T` 这种只读探测，没有对任何远端仓库做写操作。）

agentd 日志里每建一个会话都有一条结论，且**只记变量名与来源、不记值**：

```json
{"msg":"终端环境变量已转发","name":"SSH_AUTH_SOCK","source":"inherited"}
{"msg":"终端会话已建立","session":"9cf8da98-…","pid":39718,"cwd":"…/w4-pty","base_kind":"workspace"}
```

默认清单只有 `SSH_AUTH_SOCK` 一个变量，三态里本次命中的是 `inherited`；
`launchctl` 与 `unavailable` 两态本次没有现场触发（agentd 是我手起的，变量本来就在环境里），
它们由 `internal/ptyhost/envforward_test.go` 的三态用例覆盖。

### 5 —— 关页重开的恢复与 truncated ✅（有前提）

**恢复与连续性**：终端里跑 `for i in $(seq 1 600); do echo "tick $i $(date +%H:%M:%S)"; sleep 1; done`，
把视口从 1100×760 改成 1500×900，整页刷新，再点回同一个工作树 —— 终端 tab 自动回来了，
回放从 tick 1 一路接到当前的 tick 27，中间没有断口。

**truncated**：环形缓冲是 256 KiB（`ptyhost.ringSize`）。灌 `seq 1 40000 | sed 's/^/truncate-probe /'`
把 `bytes_out` 顶到 871338，再以 `since=0` 接一条新 WS：

```json
{"type":"attached","since":609194,"truncated":true}
```

`609194 = 871338 − 262144`，随后第一帧数据正好 262144 字节 —— 丢弃点与回放量都对得上。
界面上恢复出来的终端只有尾部（…40000、`TAIL-MARKER-DONE`、提示符），没有重复内容。

**前提**：以上必须在关掉 `<StrictMode>` 之后才成立。开着 StrictMode 时**一个 tab 都恢复不出来**
（见缺陷 B）。这条记 ✅ 是因为缺陷 B 只发生在开发模式；但它意味着**任何人在 dev 下都验不到这条**。

**一处轻微瑕疵**：换尺寸恢复时，回放的首行会和旧命令行叠字（`tick 1 01:26:33q 1 600); do echo …`）。
输出本身是连续的，只是回放的字节流按新宽度重排时在边界那一行画重了。不影响可用性，记在这里备查。

### 6 —— 两客户端广播与最小尺寸 ✅

开第二个浏览器 tab（900×700）指向同一前端，它自动恢复出同一个会话：

- 服务端该会话 `attached=2`。
- 尺寸变成 `cols=75 rows=45` —— 是两窗口里**小的那个**（大窗口是 122 列）。
- 在小窗口按 Ctrl-C 打断长任务，两个窗口同时看到 `^C` 与 `C:130` 的提示符。

### 7 —— 切基准不杀、× 才杀 ✅

- 左栏切到 `w4-delivery`：会话 `attached` 从 1 掉到 0，**进程还在**（pid 39718 存活）。
- 切回来后点 tab 的 `×`：弹确认框「关闭终端会话 / 关闭会终止这个终端会话，里面正在运行的命令会被一并结束。
  只是想切走的话直接切到别的 tab——会话会继续在后台跑。」
- 点「关闭并终止」后：

  ```
  $ handoff footprint --config /tmp/w4-pty-walk/config.yaml
  进程     665/5333（本机 uid 已用/上限）
  足迹     无残留（共体检 0 个任务）
  ```

  终端会话段整个消失，`ps -p 39718` 无输出。**归零。**

  （对照：点 `×` 之前 footprint 会多出一段
  `终端会话  9cf8da98  …/w4-pty  pid 39718  1 进程`。）

### 8 —— 退出码 ✅

home 终端里 `exit 42`：

- 界面底部出现「shell 已退出，退出码 42。关闭这个 tab 即可清理」。
- `GET /api/pty/sessions` 里该会话 `exit_code=42`、`attached=0`。

### 9 —— `pty_supported=false` 的对端 ❌ 未验

需要一台 Windows agentd 或其它明确不支持 PTY 的对端，手上没有。
`web/src/app/data/usePtySupport.test.ts` 有「三态原样转达：true / false / 没上报」的用例覆盖
`false` 分支的映射，但**界面上那句文案长什么样、按钮是不是真的不可点，本次没有看到**。
不打勾。

### 10 —— 老 agentd 不被误判 ✅

这条拿**真·老对端**验的：生产 agentd（main @ `76c3d6cc`）就是一台还没换版的机器，
它连 `/api/machines` 这个端点都没有（直接 404）。把它作为只读 target `oldbox` 登记进旁路实例 A 后：

```
name=''       reachable=True  pty_supported键存在=True  值=True
name='boxb'   reachable=True  pty_supported键存在=True  值=True
name='oldbox' reachable=True  pty_supported键存在=False 值=None
```

老对端那一行**根本没有 `pty_supported` 这个键**，而不是 `false`。
`usePtySupport` 只收 `typeof === 'boolean'` 的值，缺席自然落到 `null`，
调用方对 `null` 的反应是「照常放行」——正是三态设计要挡住的那个回归。

（对生产 agentd 只发了只读的状态探测，没有登记项目、没有派发、没有任何写操作。）

### 11 —— 默认值不落库 ✅

分两步验的：

- **转发照常工作**：旁路实例的 `config.yaml` 里从头到尾没有 `env_forward` 键，
  而 `SSH_AUTH_SOCK` 照样转发进了会话（见第 4 条）。
- **Save 确实不落默认值**：另起一个干净实例（datadir `/tmp/w4-c11`，配置只有 `listen` / `datadir` / `token`），
  起来再停掉 —— agentd 在关停时**确实重写了** `config.yaml`（`stalltimeout` 这种没配的键同样没被补进去），
  重写后 `grep -c env_forward` 仍然是 0。所以走的是真 Save 路径，不是「文件没被碰过」的假通过。

往返的两个方向另有单测钉住：`internal/config/config_test.go:449-489`——未配置时不落盘、解析时用内置默认清单。

### 12 —— W4 spec 的修正说明 ✅

`docs/superpowers/specs/2026-08-12-w4-shell-calibration-design.md:179`：

> **⚠️ 本节的安全模型在写下时就已被证伪，见 [PTY 终端设计 §1](…#1-前提修正控制台会话在能力上等价于主令牌)。**

---

## 3. 本次发现的缺陷

> **修复状态（2026-08-13 回填）**：三条已全部修复并归档，落在 `feat/w4-pty-terminal`
> 上——`83f71004`（B）、`bcb3e2c3`（A）、`895166f8`（C），走查记录回填在 `a393b130`。
> 计划见 [2026-08-13-w4-pty-defect-fixes.md](../plans/2026-08-13-w4-pty-defect-fixes.md)。
>
> 审核者的独立验证：把三个测试文件单独 checkout 到**未修复**的源码上跑，4 条新用例
> 全红、22 条老用例全绿（即 StrictMode 双调用在 vitest 环境里确实发生，那两条不是
> 空壳）；修复后全量 311 用例通过，`tsc -b` 0 错误，eslint 0 错误（10 个警告在
> `97c52f56` 上同样存在，非本次引入），后端零改动。
>
> 唯一仍欠的一步：缺陷 A 的验证对着替身做——触发 mock 的 `onContextLoss` 断言
> `dispose` 被调用。「dispose 之后 xterm 确实回退 DOM 渲染并继续画」这一步靠的是
> xterm 的文档契约，不是实测（真实 GPU 上下文丢失没法可靠制造）。
>
> 本分支按用户决定**不合入 main**。

### A. WebGL 上下文丢失后不回退，终端永久白屏 —— 真缺陷

**现象**：终端开出来能用，敲一条命令后整块终端区变白，只剩一个碎图标。控制台：

```
Uncaught TypeError: Cannot read properties of undefined (reading 'dimensions')  @xterm/xterm
WebGL: CONTEXT_LOST_WEBGL: loseContext: context lost
webglcontextlost event received
webgl context not restored; firing onContextLoss
```

**判定**：把 `WebglAddon` 挂到开关后面禁用它，同一套后端、同一个操作序列，终端完全正常
（见第 1、4 条的证据都是在这个模式下取的）。所以问题在渲染层，PTY 链路无辜。

**根因**：`TerminalTab.tsx:74-80` 只用 `try/catch` 包住了**构造**：

```ts
try { term.loadAddon(new WebglAddon()) } catch (err) { /* 回退到 canvas */ }
```

这只覆盖「WebGL 一开始就不可用」。**运行期上下文丢失走的是另一条路**：xterm 要求监听
`webglAddon.onContextLoss` 并主动 `dispose()` 掉 addon，才会退回 DOM 渲染器；不 dispose 就是
一个挂着死渲染器的终端，之后每次 `fit()` 都在读已经没了的 `dimensions`——正是那条 TypeError。

那段注释还把这件事说反了：它写「WebGL 不可用时 xterm 自动回退……**不能白屏**（spec §6.3）」，
但代码只做到了构造期。**spec §6.3 明确要求的那条线，恰恰在运行期这一侧是断的。**

**这不只是这台机器的问题**：上下文丢失在真实环境同样会发生——GPU 驱动重置、系统休眠唤醒，
以及**浏览器的 WebGL 上下文数量上限**（这个产品可以同时开很多个终端 tab，每个一个上下文，
超限时浏览器会驱逐最老的那个，表现就是 `CONTEXT_LOST_WEBGL`）。

**建议**：注册 `onContextLoss` → `addon.dispose()`；顺便把那段注释改成实话。

### B. `ranRef` + `cancelled` 的组合在 StrictMode 下自毁 —— 开发期功能全失效

**现象**：开着 `<StrictMode>`（仓库 `main.tsx` 的现状）刷新页面，服务端明明有 4 个活会话，
界面一个终端 tab 都不恢复。关掉 StrictMode 后恢复立刻正常。

**根因**：`usePtyRestore.ts` 里这两段各自都对，合起来互相拆台：

```ts
if (ranRef.current) return      // 挡住第二次 effect
ranRef.current = true
let cancelled = false
fetchPtySessions('all').then(resp => { if (cancelled) return; … })
return () => { cancelled = true }   // 第一次 effect 的 cleanup
```

StrictMode 下：第一次 effect 发出请求 → cleanup 把 `cancelled` 置真 → 第二次 effect 被 `ranRef` 挡回
→ 唯一那次请求的回调撞上 `cancelled === true`，一条都不恢复。

同样的写法也在 `usePtySupport.ts`。那里的后果温和些（能力表为空 → 全 `null` → 照常放行），
但同样是「本轮加载什么也没做成」。

**影响面**：生产构建不会双调用 effect，所以**线上不受影响**；但开发模式 100% 失效，
而验收恰恰是在开发模式下做的——这条如果不揪出来，第 5 条验收会被记成「恢复不工作」的假失败，
反过来也可能把它当成「环境问题」放过去。

**建议**：`ranRef` 已经保证只跑一次，cleanup 里就不该再置 `cancelled`（或改用 AbortController 并在
`ranRef` 命中的那次跳过 abort）。

### C. 建会话后被卸载会漏一个孤儿 shell

**现象**（StrictMode 下必现）：点一次「新终端」，服务端出现**两个**会话——
一个 `attached:1`（在用），一个 `attached:0` 的孤儿。实测 pid 33618/33619、36510/36511 两组。
关掉 StrictMode 后一次点击只产生一个会话，孤儿消失。

**根因**：`TerminalTab.tsx` 的 start 流程里，`createPtySession` 返回后先判 `disposed`：

```ts
const created = await createPtySession(…)
id = created.id
if (disposed) return          // ← 服务端已经建好了，这里直接扔掉
onSession(id)
```

被卸载时既不 `DELETE` 这个已经存在的服务端会话，也不上报给上层，于是没有任何人再认识它。
而 `internal/ptyhost` **没有空闲回收**——`reap` 只等 shell 自己退出。孤儿 shell 会一直活到 agentd 停。

**影响面**：StrictMode 之外，「在建会话的这一个往返里被卸载」在生产也够得着——
快速切换左栏目录、HMR 重挂载都会走到。每次漏一个常驻 `/bin/zsh`，且 `handoff footprint` 会把它们
如实列出来（这点是好的：至少看得见）。

**建议**：`if (disposed)` 分支里补一次 `DELETE /api/pty/sessions/{id}`（尽力而为即可）。
要不要给 ptyhost 加空闲回收是另一个话题，本轮 spec 没要求，不在这里主张。

---

## 4. 本次**没有**做的事

- 没有合并 `feat/w4-pty-terminal`，也没有把 `w4-delivery` 追到 main。两者仍共享 merge-base `850ae61a`。
- 没有改动被走查分支的任何一行代码（两处诊断改动已还原，`git status` 干净）。
- 没有停过生产 agentd，没有动 `~/.handoff/` 下的配置或数据。
- 没有在终端里跑任何写操作（`ssh -T` 是只读认证探测）。
- 没有验第 9 条（无不支持 PTY 的对端），没有验第 3 条的「真远程 + UI 入口」。

## 5. 待办

1. 缺陷 A（WebGL 不回退）——建议回 `feat/w4-pty-terminal` 的执行者修，附 `onContextLoss` 用例。
2. 缺陷 B（StrictMode 自毁）——同上，两个 hook 一起修。
3. 缺陷 C（孤儿会话）——同上。
4. 第 9 条与第 3 条的剩余部分：等有 Windows 对端 / 等这个分支真的部署到 devbox 之后补验。
