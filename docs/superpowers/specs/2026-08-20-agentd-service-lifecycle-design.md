# agentd 服务生命周期命令：handoff service start / stop / restart

日期：2026-08-20
分支：claude/agentd-restart-command-4e9ea8

## 1. 问题

改完 handoff 配置后没有办法重启本机 agentd。现状只有 `handoff service install /
uninstall / status`，想让新配置生效只能 kill 掉进程等进程管理器把它拉回来——
这既不可发现，也不给任何反馈（kill 完不知道它起没起来、起的是不是新配置）。

「暂时停一下 agentd」同样没有入口，只能 `uninstall` 再 `install`，而重装在
Windows 上会把计划任务删了重建，用户对任务定义的修改与任务历史一并消失。

## 2. 范围

**只管本机 agentd。** 不做远端 start/stop/restart。

远端 restart 在技术上可行（`POST /api/update` 空 body 就是不换版只重启，upgrade
在用），但远端 **stop** 有一个不对称：停掉之后没有任何通道能把它再启起来，
agentd 死了就够不着了，只能人肉上机。为一致性把三个动作一起留在本机。

## 3. 决策记录

以下四条是本设计的承重决定，附上被否掉的选项与理由。

### 3.1 命令名：挂在 `service` 下，不加顶层别名

`handoff stop <task>` 已经是「中止任务」（[cmd/stop.go](../../../cmd/stop.go)）。
再加一个顶层 `handoff stop` 会在「停任务」和「停 agentd」之间产生歧义，
而这两件事的破坏性天差地别。三个新命令一律挂 `service` 下，与既有的
install/uninstall/status 同族。

### 3.2 stop 的语义：停到显式 start，跨重启

三个平台都配了「退出就拉起」——launchd `KeepAlive=true`、systemd
`Restart=always`、Windows 计划任务每分钟重复触发。所以「杀进程」这个动作
在任何一个平台上都停不住 agentd。

stop 必须同时关掉自动拉起，且这个「关掉」要跨重启生效，直到用户显式
`handoff service start`。

被否掉的选项：**只停到下次登录/开机**。Windows 上做不到——计划任务每分钟
重复触发，不 disable 的话 60 秒内又起来了，三个平台行为会不一致。

代价：用户 stop 完忘了 start，会发现本机派不了活。用两处兜住——stop 成功
时明说「它不会自己回来，包括重启机器」，`status` 能把这个状态单独报出来
（见 3.4）。

### 3.3 restart 走进程管理器，不走 HTTP

agentd 内部已有一条重启通路（`POST /api/update` 空 body，D8），带两道闸
（非托管硬拒、活跃任务闸 force 可越）和优雅关停。本设计**不复用它**。

理由：restart 最需要被用到的时刻，恰恰是 agentd 卡死或不可达的时刻。依赖
HTTP 的命令在那个时刻正好用不了。走管理器则不依赖 agentd 是否活着。

**但走管理器不等于要硬砍。** agentd 对 SIGTERM 的处理和 HTTP 重启触发的是
同一条优雅关停代码路径（[cmd/agentd.go:258](../../../cmd/agentd.go)）：停收
新连接 → 等在途请求 → 按序收尾。所以 launchd 上用 `launchctl kill SIGTERM`
让 KeepAlive 拉起，而不是 `kickstart -k`（那个发 SIGKILL）。systemd 的
`systemctl restart` 本来就发 SIGTERM，且 unit 里的 `KillMode=process` 保证
执行者不被连坐。

### 3.4 不在 CLI 侧补活跃任务闸

走管理器就绕过了 agentd 内部那两道闸。**刻意不在 CLI 侧补回来**：这三个
命令是纯粹的进程管理动作，说停就停，不读任务状态、不依赖 agentd 可达、
执行得快。

已知代价（不是猜测）：agentd 重启会丢 codex 的终态事件，任务干完了却卡在
running。优雅关停（3.3）能缓解但不能消除。用户在有活跃任务时 restart，
后果由用户承担。

## 4. 设计

### 4.1 接口改动：`internal/service`

`Manager` 新增两个方法：

```go
// Stop 停止已安装的单元，并关掉自动拉起，直到显式 Start。
// 单元没装时返回错误，不代为安装。
Stop() error

// Restart 就地重启已安装的单元，不改动单元定义。
// 单元没装时返回错误，不代为安装。
Restart() error
```

`Status` 新增字段：

```go
type Status struct {
    Installed bool
    Running   bool
    // Disabled 表示单元被显式停用（handoff service stop），自动拉起已关掉。
    // 与「装了没跑」是两种状态：前者的处置是 start，后者的处置是查日志。
    // 合并成一个布尔会让 status 给出错误的处置建议。
    Disabled bool
    Detail   string
}
```

### 4.2 各平台实现

| | restart | stop | start |
|---|---|---|---|
| **launchd** | `launchctl kill SIGTERM <target>`，KeepAlive 自动拉起 | `launchctl disable <target>` + `launchctl bootout <target>` | `launchctl enable <target>` + `launchctl bootstrap <domain> <plist>` |
| **systemd** | `systemctl restart <unit>` | `systemctl disable --now <unit>` | `systemctl enable --now <unit>` |
| **schtasks** | `/End` + 回收进程树 + `/Run` | `/Change /Disable` + 回收进程树 | `/Change /Enable` + `/Run` |

四条实现约束：

1. **launchd 的 stop 必须配 `disable`，且 disable 在前。** 光 bootout 的话
   plist 还躺在 `~/Library/LaunchAgents` 里，下次登录 launchd 会自动
   bootstrap 回来，3.2 的语义撑不过一次重启。`launchctl disable` 写进 launchd
   的 disabled 覆写数据库，跨重启生效。
   顺序有取舍：disable 成功而 bootout 失败，留下的是「还在跑但已停用」，
   重启后自己下去；反过来 bootout 成功而 disable 失败，留下的是「停了但仍
   启用」，下次登录悄悄回来，把用户的 stop 无声撤销。选前一种失败形态。
2. **launchd 的 start 必须先 `enable` 再 `bootstrap`。** 被 disable 过的
   service target，bootstrap 会直接拒。
3. **现有 `Manager.Start()` 要改。** 它现在是 kickstart（launchd）/ 裸
   `systemctl start`（systemd），两者都接不住「被 disable 过」的情形——
   kickstart 在 bootout 之后必然失败，`systemctl start` 能起但不恢复开机
   自启。改成上表的 enable + bootstrap / `enable --now`。
   `Start()` 的既有契约（单元没装时返回错误、不代为安装）保持不变。
4. **Windows 的进程回收复用现成实现。** `Uninstall` 里已经解决过「`/End`
   只杀外层 cmd.exe，杀不到 agentd 孙进程」这个坑，把那段抽成私有方法供
   Uninstall / Stop / Restart 共用，不复制第二份。

### 4.3 复核

沿用本包既有惯例：命令退 0 只说明请求被受理，必须复核真实状态。

- restart / start：复核到 Running。复核不过报「已触发但复核不到（可能起来
  即退出）」，不报成功。
- stop：复核到**不** Running。

复核手段沿用各平台 `Status()` 已在用的那套（launchd `launchctl print` 并看
`state = not running`；systemd `systemctl is-active`；Windows 现成的
`waitRunning`）。

### 4.4 单元没装时

三个命令一律硬拒，**不代为 install**。沿用 `Start()` 既有的分工注释：
Install 负责「让单元存在并跑起来」（为此会重写单元定义），Start/Stop/Restart
只负责「让已存在的单元变状态」。让 start 悄悄替 install 干活，调用方就再也
分不清这两种情形。报文直接给出 `handoff service install`。

### 4.5 `service status` 输出

现状「已安装但未运行」的处置提示是「看日志找原因，或 handoff service install
重装」。stop 之后这条是错的——会把用户支去重装一个本来好好的单元。加
`Disabled` 后分三档：

```
已托管        launchd   <unit>                     # Installed && Running
已停止        被 handoff service stop 停住           # Installed && Disabled
处置          handoff service start
已安装但未运行  launchd   <unit>                     # Installed && !Running && !Disabled
处置          看日志找原因，或 handoff service install 重装
未托管        launchd 上没有 handoff 的服务单元        # !Installed
处置          handoff service install
```

### 4.6 桌面壳不再复活被停住的 agentd

`desktop/internal/shell/lifecycle.go` 的 `EnsureRunning` 现在是「没在跑 →
`Start()`，Start 失败 → `Install()` 自愈」。launchd 上 stop 做了 bootout 之后
`Start()` 必然失败 → 回落 Install → **把用户刚显式停掉的 agentd 装回来跑起来**，
3.2 的语义当场作废。

改动：`EnsureRunning` 在 `st.Disabled` 为真时直接返回 nil，如实记一条 Info
日志说明「agentd 被显式停用，不自愈」，既不 Start 也不 Install。

同时把 `desktop/main.go` 文件头那句「托盘不提供停止 agentd：service.Manager
没有 Stop，用 Uninstall 冒充是错的语义」改掉——改完 Manager 就有 Stop 了，
那个理由不成立。**托盘仍然不加「停止 agentd」**，那是产品决定，理由换成产品
决定本身，不在本次范围内实现。

### 4.7 权限

systemd 的 unit 落在 `/etc/systemd/system`（system unit，不是 `--user`），
三个命令都需要 root。沿用 `Uninstall` 的做法识别 permission denied，报
「请用 sudo 重跑」，而不是把原始错误直接抛出去。

### 4.8 形态提示

`stop` 成功后必须打一句：

```
已停止   agentd 不会自己回来，包括重启机器；恢复用 handoff service start
```

这与 `install` 打「Ctrl-C 只会让它被重新拉起」是同一类：形态变了就得说清楚，
否则用户下次发现派不了活时不会想到是自己停的。

## 5. 测试

三个平台 manager 的 `run` / `writeFile` / `remove` 都已是可注入字段，照
`launchd_test.go` / `systemd_test.go` / `windows_test.go` 现成的路子加：

- 命令序列断言：stop 发出的是 disable 在前、bootout 在后（理由见 §4.2 第 1 条）、
  start 发出的是 enable + bootstrap、restart 发出的是 `kill SIGTERM` 而
  **不是** `kickstart -k`
- 复核失败路径：管理器命令退 0 但复核查到状态不对时返回错误
- 未装单元时三个命令都报错且不发出任何变更类命令

cmd 层用 fake Manager（`newServiceManager` 已是缝）测三个子命令的输出文案与
退出码，含 stop 的形态提示那一行。

`desktop/internal/shell/lifecycle_test.go` 加一条：`Status` 报 Disabled 时
`EnsureRunning` 既不调 Start 也不调 Install。

## 6. 不做什么

- 不做远端 start/stop/restart（见 §2）
- 不加顶层命令别名（见 §3.1）
- 不在 CLI 侧检查活跃任务（见 §3.4）
- 不给桌面托盘加「停止 agentd」（见 §4.6）
- 不动 agentd 的 HTTP 重启通路（`POST /api/update` 空 body），upgrade 继续用它
