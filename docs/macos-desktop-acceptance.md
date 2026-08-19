# macOS 桌面薄壳：人工走查清单

本文件覆盖**版本同步与升级**那条路径（spec `2026-08-19-desktop-cli-sync-and-update-notice-design.md`）。
Windows 侧的对应清单在 `windows-desktop-acceptance.md`，两份的「已知且刻意如此」内容一致。

走查需要一个能看到桌面的 macOS 会话（同步会弹窗口、面板要肉眼看）。

---

## 走查前必须先确认（三条，缺一条整轮走查都是空转）

这三条不是形式：**它们各自都能让走查全程"通过"而其实什么都没验**。

### 1. `.app` 必须是 release 构建（带 `-tags embedbin`）

开发构建没内嵌二进制，`PlanSync` 恒为 `SyncNoEmbed`，整条同步路根本不跑。

```bash
log stream --predicate 'process == "handoff-desktop"' --info | grep 同步对账结果
```

或直接看壳的 stderr。判据：那行的 `plan` 字段。**看到 `plan=no-embed` 就是构建不对**，
换 Release 页下载的 `.dmg` 重来。

### 2. 已装的 CLI 版本号必须是 `vX.Y.Z` 形态

同步的判据是版本比较，而 `CompareVersion` 只认三段整数。开发构建的二进制自报的是
`web-console-0c75a56c9+b161fix` 这种分支+短号，**解析不出 → `DecideRelease` 走保守分支
`use-existing` → 永远不同步**。

```bash
handoff version | head -1
```

判据：必须形如 `v0.3.1`。看到分支名就说明这台机器装的是开发构建，
先换成 Release 页的 `handoff_<tag>_darwin_arm64.tar.gz` 里那个。

> 这条是 2026-08-19 验 P3 时实测踩到的：日志里一行完全正常的
> `释出决策 decision=use-existing`，看不出任何异常，而整轮验证什么都没验。

### 3. agentd 必须是 release 构建（带 `-tags embedweb`）

控制台页面是 agentd 伺服的。默认标签构建出来的二进制里根本没有页面，
会看到「此二进制未嵌入前端构建产物」——那不是薄壳的缺陷。判别同第 2 条。

---

## P1 换版后 launchd 真把新版拉起来

**这一项无法用隔离 HOME 做**：launchd 的 job label `dev.gosuper.handoff.agentd`
是机器级固定值，隔离 HOME 隔离不了服务。必须在真实环境做，做之前先记下当前版本。

准备：让已装的 CLI 比 `.app` 内嵌的旧（装一个老 tag 的 CLI，或装一个新 `.app`）。

```bash
handoff version | head -1                    # 记下旧版本
launchctl print gui/$(id -u)/dev.gosuper.handoff.agentd | grep -E "pid|state"
```

双击 `.app`，然后：

```bash
handoff version | head -1                    # 应等于 .app 内嵌的版本
handoff status | grep 版本                    # agentd 自报的版本也应是新的
launchctl print gui/$(id -u)/dev.gosuper.handoff.agentd | grep -E "pid|state"
```

**判据**：三处版本都变成新的，且 launchd 的 **pid 变了**（证明真的重启过，
不是只换了磁盘上的文件）。

**判否说明什么**：pid 没变而版本变了 = 换了文件但没重启，`RestartAgentd` 那一步没生效；
两者都没变 = 同步压根没触发，回头查上面三条前置。

日志上的取证（这一行是整条路径唯一的取证入口）：

```
同步对账结果 plan=do decision=embedded-newer installed=… installed_version=v0.3.0 embedded_version=v0.3.1 busy=0
换版完成 target=… prev=….prev
skill 同步完成 target=…
同步完成，已触发 agentd 重启
agentd 已带新版本回来 version=v0.3.1 attempts=N
```

**`attempts` 值得看一眼**：macOS 上 launchd 是秒级自拉，正常应该是个位数。

---

## P3 Gatekeeper 不拦释出到 `~/.local/bin` 的二进制

> **✅ 已于 2026-08-19 用 v0.3.0-rc10 验过，成立。** 取证原文见
> `docs/superpowers/plans/2026-08-19-desktop-cli-sync-and-update-notice.notes.md`。
> **换了签名方式、换了 CI 打包步骤、或苹果改了 Gatekeeper 策略时才需要重验。**

结论的根据：隔离属性传染到了 `.app`，但**没有**继续传染到它写出的文件上，
于是 Gatekeeper 对那份二进制根本不做评估。

重验时**必须避开两个坑**，否则整轮是假绿：

**坑一：`gh release download` 不设 `com.apple.quarantine`。** 只有浏览器下载才设。
拿 `gh` 下的 DMG 直接测必然"通过"，且完全测不到真实用户的处境：

```bash
xattr -w com.apple.quarantine "0081;$(printf %x $(date +%s));Safari;$(uuidgen)" xxx.dmg
```

补完挂载、`ditto` 拷出（**`cp` 不保扩展属性**），`.app` 上应出现 `0281;…` 且 UUID 与 DMG 的一致。

**坑二：只隔离 `HOME` 不够，`PATH` 也要隔离。** `ResolveBinPath` 会搜 PATH，
只设 HOME 时它会找到你真实的那份并判 `use-existing` 直接不释出：

```bash
env -i HOME="$SCRATCH" PATH=/usr/bin:/bin:/usr/sbin:/sbin TMPDIR=/tmp \
  "$SCRATCH/handoff-desktop.app/Contents/MacOS/handoff-desktop"
```

判据是日志里 `释出决策 decision=install`，不是 `use-existing`。

**两条判据不要用**（它们问的是关于 `.app` bundle 的问题，被验对象是裸可执行文件）：

- `xcrun stapler validate` 报「没有票据」——苹果只支持给 `.app`/`.dmg`/`.pkg` 装订，
  裸 Mach-O 无法装订。**这不是缺陷**
- `spctl -a -t exec` 报 `rejected`——它同一行写着 `the code is valid`，
  拒的理由是「这不是个 app」。**这也不是缺陷**

真正的判据只有两条：`xattr -l` 里**没有** `com.apple.quarantine`，以及**真的执行一次**：

```bash
env -i HOME="$SCRATCH" PATH=/usr/bin:/bin:/usr/sbin:/sbin "$SCRATCH/.local/bin/handoff" version
```

---

## P4 有活跃任务时不同步，且强制路径走得通

准备：让本机有一个活跃任务（`running` 或 `waiting_answer`）。最省事的是用 `fake` 执行者
派一个不会自己结束的任务。

双击 `.app`。**判据**：

- 日志 `同步对账结果 plan=blocked … busy=N`，且**没有** `换版完成`
- `handoff version` 仍是旧版本
- 托盘出现「有更新待应用（N 个任务进行中）」

点它 → 面板弹出，显示活跃任务数与代价说明 → 点「带 --force 重试」：

- 日志出现 `换版完成` 与 `agentd 已带新版本回来`
- **执行者进程仍然存活**（这是最要紧的一条判据）：

```bash
ps -o pid,etime,command -p <执行者的 shim pid>
```

**判否说明什么**：执行者被杀了，说明 `setsid` 那条隔离在本路径上不成立——
那是个真缺陷，B59 V3 实测过它应该活过 agentd 重启（当时存活 16m29s）。

---

## P5 升级面板窗口能出来

托盘 →「升级执行机…」。**判据**：弹出一个独立窗口，逐行出现 `handoff upgrade` 的巡检表。

**判否说明什么**：如果**进程静默消失、没有任何输出**，那是 rc7 那个 bug 的第二次——
新窗口漏挂了 `WindowRuntimeReady`，`setURL` 导航进一个还没建好的 chromium。
Wails beta.8 的 `windowsWebviewWindow.setURL` 至今没有 nil 守卫。

如果窗口出来但一片空白，是另一回事：vite 的多页入口没生效，`dist/upgrade.html` 没被打进去。
Go 侧照样编得过，不会有任何构建期报错。

---

## P6 同步失败时控制台**仍然能打开**

**这是本次改动最严重的回归面**：双击打开应用是整个产品里唯一一个"必须成功"的动作，
而我们在它前面插了一段会换二进制、重启服务的逻辑。

制造一次必然失败的同步——把落点变成只读：

```bash
chmod 555 ~/.local/bin        # 让 release.Activate 的 rename 失败
```

失败点在 `release.Activate` 开头的 `checkDirWritable`——它在目标目录里实际建一个
临时文件来探可写性（不看权限位，因为权限位判可写要考虑 uid/gid/ACL/只读挂载，
判错的方向还不确定）。所以报错会是一句带路径的明确说明，不是扁平的 permission denied。

双击 `.app`。**判据**：

- 控制台**照常打开**
- 日志 `同步失败，将用现有版本继续打开控制台`
- 托盘出现「上次同步失败，查看详情」，点开能看到原因原文

```bash
chmod 755 ~/.local/bin        # 验完立刻改回来
```

**判否说明什么**：应用打不开 = spec D8 被破。这是本条路径唯一不可接受的失败模式，
优先级高于同步本身能不能成功。

---

## 已知且刻意如此（不要当缺陷上报）

- **「带 --force 重试」对非闸一原因的失败点了也没用**（如网络不通、磁盘只读）。
  按钮只按退出码非零就亮，**不解析输出文本**——输出是给人看的中文表格，解析它会在
  格式一改时静默失效，而失效方式是「按钮再也不出现」，没有任何报错。
  完整输出就在面板上，由用户自己判断。见 spec §7.2
- **agentd 被锁在你手上这份 `.app` 的版本。** 要升到更新的版本必须换 `.app`，
  托盘的「有新版 vX.Y.Z 可下载」就是提醒你去换。见 spec §11 风险③
- **开发构建下同步整条路不跑**，日志里 `plan=no-embed`。见上面「走查前必须先确认」第 1 条
- **已装 CLI 的版本号不是 `vX.Y.Z` 时同步永不触发**，日志里 `decision=use-existing`。
  这是设计要的保守行为（判不出就不覆盖用户手上的），不是缺陷
- **`stapler validate` / `spctl -t exec` 对裸可执行文件"失败"**，见 P3 那节
