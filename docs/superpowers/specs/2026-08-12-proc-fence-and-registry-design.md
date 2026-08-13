# executor 进程围栏与出生登记设计（B73 + B72）

> 状态:待评审
> 日期:2026-08-12
> 关联:B73(围栏+归因,先做)、B72(出生登记,后做)、B69/B70(已交付的足迹原语是本设计的地基)
> 实现拆两个 plan:围栏先落地,登记后跟。

## 0. 背景:08-12 事故的最终定性

两个并发任务(各 7 个 subagent,每个都跑 `go test ./...`)把 devbox 的
`kern.maxprocperuid`(2666)瞬间顶穿。此后 43 分钟(12:01 首条 EAGAIN →
12:44 连 `ps | wc -l` 都 fork 不出来 → 12:48 重启)机器不自愈,根因三层:

1. **无上限**:executor 树想 fork 多少就 fork 多少。人肉开发天然串行、有感官
   反馈;agent 没有,且让并发变得零成本正是 handoff 的产品本意。所以上限
   必须由机器自己给。
2. **进程表耗尽没有 OOM killer 的对应物**:内核只拒绝新 fork,从不主动杀进程
   腾名额。回收全靠占位者自己退出——而占位主力是 executor 死后遗留的逃逸树,
   其中卡死的部分(等一个永远 fork 不出来的子进程)形成自喂死锁,永不退出。
3. **清扫双重失效**:自然死亡路径不触发清扫(B69,已修);触发了也按 pgid
   找不到 setsid 脱队的树(B72 盲区,实测证实:opencode Bash 工具对每条命令
   setsid,pid=sid=pgid 完全独立会话)。

分工:**B73 围栏管点火(峰值到不了天花板),B72 登记管灭火(死后认得出、
杀得到),B69/B70 已交付的 footprint 是烟雾报警器。** 只要不点火,灭火队
晚建没关系;反之不行——故围栏先行。

## 1. 目标与非目标

**目标**

- 整机 fork 瘫痪、只能重启的事故形态从此不可能;最坏降级为「单轮任务失败」。
- 撞墙的报错在三个受众(agentd 日志、审核者、执行者)处都能归因为「配额耗尽」
  而非代码 bug——会报小/会误导的防护比没有防护更糟,这是本设计的第一原则。
- executor 死后,其逃逸后代可被安全回收(不误杀,B47 红线)。

**非目标**

- 不给 executor 节流(围栏贴天花板,九成额度不动)。
- 不做运行期熔断(围栏立起后断崖不可达,且熔断器在耗尽时自身难保)。
- 不阻止 setsid 本身(无特权系统调用,macOS 无 seccomp/pid-ns 类原语可禁;
  围栏让逃逸失去杀伤力,登记让逃逸藏不住名,即为围死)。
- 不做任务间配额公平分配(uid 级计数先到先得;准入闸负责把「B 无辜撞墙」
  变成「B 被拒发并得到解释」)。

## 2. 围栏(B73 核心)

### 2.1 原理与实验地基

`RLIMIT_NPROC` 的语义:**限值挂在每个进程身上,计数是整个 uid 的**。fork 时
内核拿「调用者自己的限值」比「uid 当前活着的进程总数」。推论:

- 给所有 executor 树设同一个 L,则无论几个任务、扇出多少 subagent,uid 总数
  不会因 executor 的 fork 越过 L——内核替我们做加法,零协调。
- agentd/sshd/登录 shell 仍认系统默认(2666),永远有 `limit − L` 个名额
  executor 碰不到——救护车道,12:44 型「看都看不了」绝境不再可能。

真机实验(08-12 devbox)验证三条,全过:

| 假设 | 结果 |
|---|---|
| macOS 强制执行低于 maxprocperuid 的 NPROC | ✅ 系统上限 2666,围栏 380 时 fork 即 EAGAIN(errno 35) |
| setsid 完全逃逸(pid=sid=pgid)后仍带围栏 | ✅ 独立会话里 rlimit 原样 (380,380) |
| 软硬限同压后抬不回去(单向门) | ✅ `ulimit -u` 回抬报 Operation not permitted |

### 2.2 L 的取法:贴天花板留车道,不是节流

`L = procLimit() − reserve`,`reserve = max(procLimit()/10, 200)`
(2666 → reserve 266 → L 2400)。理由:救护车道不需要宽(agentd+ssh+shell
一两百个进程),只需要一定在;L 压得低(如 1200)不增加安全,只放大
「executor 更早撞墙 + 审核者误判」的面积。

### 2.3 安装点与数据流

- **策略在 agentd,机制在 shim**:`prochost.Start` 计算 L,经 `Spec` 新增
  字段 `NprocLimit int`(`omitempty`,0 = 不设围栏——兼容旧 shim/旧 spec,
  遵循既有指针+omitempty 协议约定的精神)下发。
- shim 在 `AcquireLock` 之后、`cmd.Start()` 之前对**自己** setrlimit
  软硬限同为 L;子进程全盘继承。shim 一辈子只 fork 一次,自己被限无碍。
- setrlimit 失败:WARN + 继续拉起 executor(防护装置故障不阻断业务),
  日志写明「围栏未生效」。
- 平台:darwin/linux 生效;windows 与 other 平台 no-op(字段忽略)。
- 配置:`config.yaml` 新增 `proc_fence` 段,`disabled`(默认 false)与
  `reserve_ratio`(默认 0.1)。字段一律 `omitempty`,不污染旧版 config
  (B59 spec D7 反向教训)。

## 3. 归因:撞墙必须认得出是配额,不是 bug

前提:B69 交付的 `UIDUsage()` 走 sysctl,**零 fork,在耗尽时刻依然读得出数**
——这是 12:44 当天全场缺失、如今已有的能力,归因全部建在它上面。

### 3.1 agentd 侧:EAGAIN 机械翻译(硬)

新增 `prochost.ExplainForkFailure(err error) string`:判定 `errors.Is(err,
syscall.EAGAIN)`(含 `fork/exec ... resource temporarily unavailable`
的 wrap 链)后零 fork 读占用:

- 占用 ≥ 0.9L:改写为「进程配额耗尽(uid 2390/围栏 2400),命令未执行,
  这不是代码问题」。
- 占用 < 0.9L:如实说「fork 失败但占用仅 30%,不像配额问题,原因未知」
  ——不猜,读数说话。

接入点:`handoff run` 命令执行、git 调用、adapter 拉起 executor、shim spawn。
错误改写只动**文案**,不动错误类型与控制流。

### 3.2 审核者侧:水位先说话,事后附快照(硬)

- agentd 既有看门狗周期里加一条:`UIDUsage() ≥ 0.9L` → 向每个活跃任务发一次
  `resource_pressure` 事件(带 used/L 数字;去抖:越线沿触发一次,回落复位)。
  审核者在第一条 fork 失败之前就知道「快满了」。
- 任务 `failed` 与 executor 死亡事件的 payload 附当时占用快照。
  「死亡时 98%」与「死亡时 30%」一眼定性两个方向,双向堵误判。

### 3.3 执行者侧:预埋纪律(软,由 3.2 兜底)

派发模板固定加一句:「若见 `resource temporarily unavailable`:这是机器
进程配额耗尽,不是你的代码 bug——立即停止并行操作、收敛后报告审核者,
不要重试,不要改代码。」执行者内部工具的报错文案我们改写不到(opencode
内部),此条为约定而非机制;误导风险由审核者(手握水位事件与快照)纠偏。

### 3.4 准入闸

`Dispatch` 与 `handoff run` 执行前零 fork 查占用:

- `used ≥ L`:拒发,400,文案带数字(「当前 2450/围栏 2400,余量不足」)。
- `used ≥ 0.9L`:放行,但 stderr 警告 + 事件记录。

价值定位:**拦截不是它的贡献**(08-12 开工时余量是好的),它换的是
「一句人话」和「A 吃满时 B 得到解释而非莫名撞墙」。

## 4. 出生登记(B72 核心,后一个 plan)

### 4.1 原理

逃逸毁掉的不是「管住」,是「事后凭亲缘认人」:setsid 改 pgid/sid,
**改不了 ppid,更改不了出生事实**。树活着时父子链完整可数;executor 一死,
后代 reparent 给 launchd,链才断。所以在**活着的时候记账**:

- shim 内周期 goroutine(15s):`enumProcs()` 全量拿 uid 进程表,从自身 pid
  沿 ppid 链闭包出后代集合,原子落盘 `roster.json`(临时文件 + rename;
  独立文件,不碰 proc.json——那是 adapter 独占,shim 文件头边界已言明)。
- 条目:`{pid, started_at}`。**最后一次快照 ≈ executor 死亡时刻的存活者**
  ——早退的短命进程自然不在最后快照里,无需增量维护。

### 4.2 回收

`Sweep`(B69 已建,现按 pgid)扩展为两段:

1. 现有 pgid 组清扫(不变);
2. 读 roster,逐条「pid 存活 **且** started_at 与当前进程表完全一致」才 kill;
   任一不符即视为 pid 已易主,**绝不发信号**(B47 红线:误杀 114 次的教训,
   宁漏勿错)。

### 4.3 已知局限(如实写,不粉饰)

- 采样间隔内出生并在下次快照前逃逸+存活的进程可能漏记(窗口 ≤15s);
  漏网者由围栏兜底(只吃预算,不致命)。
- roster 陈旧度 ≤ 一个采样周期;started_at 匹配保证陈旧不会变成误杀,
  只会变成漏杀。

## 5. 硬约束:防线全链路零 fork

数余量(sysctl)、装围栏(setrlimit)、读进程表(sysctl)、发信号
(syscall.Kill)——**全部进程内系统调用**。防线路径(围栏、归因、准入闸、
清扫)内出现任何 `exec.Command` 即为实现失败:它会在最需要的时刻,
和 12:44 那条 `ps | wc -l` 死在同一个地方。此约束写进两个 plan 的
每个相关 task,作 review 检查项。

## 6. 测试与验收

- **纯逻辑单测**:L 计算(含 reserve 下限)、EAGAIN 判定与文案分支、
  roster 后代闭包与 pid+started_at 匹配——全部经既有 seam
  (`enumProcsFn` 等 package-level var)注入假进程表,不依赖真机。
- **继承性测试**:go test 内 spawn helper 进程验证 setrlimit 后子进程
  读到同值(darwin/linux 各自路径)。
- **变异检验**(B47 纪律):至少覆盖「matched 判据去掉 started_at 比对」
  「EAGAIN 误判为一律配额」「reserve 归零」三条,逐条改码→指定用例 FAIL→
  还原→`git diff --exit-code`。
- **真机烟测**(隔离实例,独立端口+DataDir):压低围栏复现 EAGAIN,验证
  ①翻译文案带真实数字;②resource_pressure 事件到达;③拒发文案;
  ④roster 点名回收一个 setsid 逃逸的构造进程,且 pid 易主场景不发信号。
  烟测进程在宿主 shell 构造,不派发真任务(08-12 刚瘫过,省着用)。

## 7. 实现拆分

| Plan | 范围 | 为什么先/后 |
|---|---|---|
| A(先) | §2 围栏 + §3 归因/准入闸 | 防点火;机制简单(一处 setrlimit + 若干翻译点),先把「不可能整机瘫痪」拿到手 |
| B(后) | §4 出生登记 + Sweep 扩展 | 灭火;涉及 shim 职责扩边与误杀防线对表,精细活,在围栏保护下从容做 |

两个 plan 各自走完整 task 化(含日志/注释自检步骤,instrumenting-code 硬要求)。
