# Changelog

本文件记录 handoff 的所有值得用户知道的改动。

格式依据 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号依据 [语义化版本](https://semver.org/lang/zh-CN/)。

**这份文件是承重的**：release workflow 按 tag 抽取对应小节作为 GitHub Release
的说明。抽不到时会回落成自动生成的 commit 列表，并在日志里打一条警告。

## [Unreleased]

### 修复

- 自更新的下载遇到瞬时网络故障会自动重试，不再一次抖动就判整台机器升级失败。
  传输层错误（超时、连接重置、EOF）与 HTTP 5xx / 429 最多重试 3 次，间隔
  2s、4s；4xx 不重试——404 是「这个版本没有这个资产」，确定性的缺失，重试
  只会把一句清晰的错误拖成更久的沉默。退避期间响应中断信号，Ctrl-C 立刻退出。
  实测触发场景：`github.com:443` 间歇不通（同期 `api.github.com` 正常），
  连续两次分别死在 `unexpected EOF` 与 `dial tcp …: i/o timeout`，而 7MB 的
  资产每次都要重下。

## [v0.2.2] - 2026-08-13

### 修复

- `install.ps1` 的一行安装真的能用了。真机（Windows Server 2025，PowerShell 5.1，
  zh-CN）实测暴露两条，v0.2.1 里那份**任何机器上都装不上**：
  - `checksums.txt` 被 GitHub 按 `application/octet-stream` 发，
    `Invoke-WebRequest` 的 `.Content` 于是给 **byte[]** 而不是字符串（5.1 与 7
    都一样），拿它去切行只得到一个无用对象——每次安装都死在「checksums.txt
    里没有 xxx 的条目」，而条目其实好端端在那儿。
  - v0.2.1 给 `install.ps1` 补的 UTF-8 BOM 打断了 `irm ... | iex`：PowerShell 5.1
    不把 U+FEFF 当空白，BOM 会粘进首个 token，脚本第一行就报「无法将 ?# 识别为
    cmdlet」。BOM 修好的是「存成文件再跑」那条路，代价是打断了文档里的主路径。
    两个要求互斥，唯一同时满足的解是**不含非 ASCII 字节**，所以 `install.ps1`
    改成纯英文、去掉 BOM。`install_test.ps1` 只从磁盘跑，保持带 BOM 的中文。

## [v0.2.1] - 2026-08-13

### 新增

- Windows 协调者分发：发 `windows/amd64` 与 `windows/arm64` 资产（`.zip`，内含
  `handoff.exe`），新增 `install.ps1` 一行安装。**Windows 上 handoff 只能当协调者**，
  agentd 仍不支持 Windows。
- macOS 资产做 Developer ID 签名与公证，从 Releases 页面用浏览器下载不再被
  Gatekeeper 拦下。
- 新增 CI 验证门：PR 与发布前跑同一套 `go build` / `vet` / `test` / `gofmt` /
  Windows 交叉编译 / 安装脚本单测。
- 本项目以 Apache License 2.0 发布。

### 变更

- `handoff init` 在 Windows 上只提供「协调者」一个角色选项。

### 修复

- `install.sh` 的下载临时目录改用显式模板 `${TMPDIR:-/tmp}/handoff-install.XXXXXX`。
  此前在 macOS 上用的是裸 `mktemp -d`，而 BSD 的 mktemp 无模板时**忽略 TMPDIR**，
  用户设的 TMPDIR 不起作用。
- `install.ps1` 与 `install_test.ps1` 补上 UTF-8 BOM。没有 BOM 时
  PowerShell 5.1（Windows 自带的那个）会按系统 ANSI 代码页解码脚本，中文
  Windows 上整个脚本会被解析成语法错误、一行都跑不了。
- 收到 `completed` / `failed` 事件时，任务状态保证已经是 `waiting_review`——
  紧跟着发 `continue` / `done` 不会再偶发 409。此前 agentd 是「先落事件再迁状态」，
  而事件有两条送达路径：实时广播在迁移之后，但 WS 建连时的**历史重放**直接读
  数据库，事件一落库就可见。于是断线重连、以及每轮新建连接的一次性 `wait`
  都可能在状态尚未回迁时就拿到事件。顺序已改为「迁状态 → 落事件 → 广播」。
- 后台更新检查不再在测试进程里拉起子进程。它 spawn 的是 `os.Executable()`，
  在 `go test` 下那不是 handoff 而是 `<包>.test`，而 go test 会忽略
  `update-check` 这类位置参数——子进程于是把整套测试从头重跑，跑的过程中又走到
  同一段代码，指数级炸开。只在**干净环境**触发（开发机 `~/.handoff` 里有新鲜
  的检查时间戳，判定不陈旧直接返回），所以本地怎么跑都绿：实测新建的 CI runner
  上 85 秒被 SIGTERM 打断，一台 4c8g 机器上炸出 1011 个测试进程、load 500+。

## [v0.2.0] - 2026-08-13

### 新增

- 项目位置模型：派发输入从「哪台机器哪个目录」收敛为「哪个项目 + 哪台机器」，
  目标机缺项目时自动登记并重发一次；`repo_root` 收拢默认到 `<DataDir>/repos`。
- 任务进程足迹：`handoff footprint` 只读体检各任务进程占用与本机进程余量，
  executor 已不在时自动清扫残留进程组。
- 进程围栏（`proc_fence`）：开工前按进程余量准入，防失控 fork 拖垮整机；耗尽
  拒发并给出带数字的理由，撞限的报错不再被误当成普通失败。
- `handoff reclaim`：无参列、带 id 收，回收终态任务残留的 managed worktree，
  脏树默认拒绝并给出 `--force` 出路。
- `handoff done --note`：归档时留说明，`archived` 事件携带 note 下发。
- `path_dirs` 配置项：agentd 启动时并进 PATH，executor 发现不再依赖恰好正确的
  PATH；`handoff init` 在执行机上追问并代跑 service install。
- 审批通道整顿：同一权限描述人工批过一次后自动复用；提问工单按原生 id 幂等；
  `--deny --reason` 的原因下发给 executor。
- `handoff status` 显示每任务进程数与本机 uid 进程占用/上限。

### 变更

- `dispatch` 改用 project 定位：删除 `--repo` 与 `repo` 子命令族，首次派发到
  新项目时自动登记。
- `dispatch --base <分支名>` 的起点解析成 sha，从源头消灭 git DWIM 静默改写
  任务分支；派发时回显分支名与解析后起点。
- `dispatch` 成功后默认不弹终端，提示行改走 stderr。
- 升级结论收敛：巡检与 `--now` 共用同一判据，过旧不再被报成非托管。
- 终态迁移统一作废挂起的权限/提问工单，`tickets_voided` 留审计痕迹。

### 修复

- 无 trailer 但有新提交时不再替模型宣布完成——executor 有产出但没写完成标记
  的回合统一收口，杜绝假完成。
- 游标不再钉死在 `$HOME`：按 agentd 分篓、读失败分类、旧布局一次性清除。
- `handoff reclaim --force` 不再因请求体被序列化丢失而静默失效。
- `store` 的 repos 迁移改为事务，中途失败不再把库锁死。

## [v0.1.2] - 2026-08-11

### 修复

- 版本提示只在缓存严格更新时才打，不再劝人降级。

## [v0.1.1] - 2026-08-11

### 新增

- `handoff upgrade` 加机器范围维度，一条命令巡检并升级所有机器；升级改为
  操作者触发，agentd 不再有自动更新循环。
- skill 用 `go:embed` 进二进制，随安装/升级自动同步，版本不再漂移。
- opencode 原生提问转成审核者工单，答复分级折算回 opencode；`wait --follow`
  每次建连前对账，断线重连把积压折成一行 `backlog_summary`。

### 变更

- 删除 agentd 自动更新循环，更新由操作者触发（配置字段标废弃）。

## [v0.1.0] - 2026-08-11

### 新增

- 首个公开版本。
