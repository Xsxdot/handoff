# W4c 工作区文件写入与在线编辑 设计

**Backlog**：B81
**来源**：W4 总方案 §5 给 W4 划的范围是「文件 REST（浏览、读取、**编辑、冲突保护**）」。
浏览与读取已交付，写入这一半没做，中央区的文件 tab 至今只读。
**交接文档**：[W4 剩余工作交接](../notes/2026-08-12-w4-parallel-handoff.md) 的 P3。

---

## §0 范围

**做**：编辑工作树内**已存在的**文本文件，带冲突保护。

**不做**（每条都有理由，不是随手砍的）：

| 不做 | 理由 |
|---|---|
| 新建文件 / 删除 / 改名 | 白名单守的是工作树；一开新建就要回答「能不能建目录」「路径不存在时算不算逃逸」一串新问题。W4 的范围写的是「编辑、冲突保护」 |
| 自动保存 | executor 就在同一个工作树里干活，自动保存等于让人在不看屏幕的时候和 agent 抢写 |
| 语法高亮 / Monaco / CodeMirror | 与 `FileTab` 现有边界一致（见其文件头）：判据是「能不能改个配置、改段文案」，不是在浏览器里重建 IDE |
| 文件变更推送（watcher） | 见 §1.2 —— 这是本设计**刻意**没走的那条路 |
| 草稿服务端化（跨机接管不丢编辑） | 另立 B82，见 §7.3 |
| 把 `.git` 从文件树列举里隐掉 | 那是 W4 文件浏览的既有行为，改它属于另一条线的范围，混进来会让本 spec 的验收面糊掉。**写侧照挡**（§4.1） |

---

## §1 两条已经变了的前提

### 1.1 白名单不是安全边界

`workspacefiles.go` 原文件头那套「控制台会话是比主令牌弱的凭据」的说辞**已被证伪**
（[PTY spec §1](2026-08-12-w4-pty-terminal-design.md)：控制台会话在能力上等价于主令牌，
因为 auth 中间件让两者落在同一个 mux 上，其中包含 `POST /api/tasks/{id}/run` 的 `sh -c`）。
文件头注释已随 PTY 落地更正，但**状态码还留在旧说辞上**——白名单不命中返回 403
「你没有权限浏览这个目录」。

**本期一并收口**：读写两侧的白名单拒绝统一为 **400**，并改掉 `workspaceRootOrErr`
里那行仍在宣称权限语义的注释。留着 403 等于继续宣称一个不存在的安全边界。
破坏面实测为零：前端只显示 error 文本不看码，CLI 不走这两个端点。

白名单存在的理由仍然成立，只是理由换了：**防止前端传一个打错的路径把 agentd
变成任意目录浏览器**——参数校验。

### 1.2 冲突保护走服务端前置条件，不建 watcher

调研了 orca（`~/Downloads/AnyTimeDelete/orca-main`，其 `src/relay/` 就是它的远程文件通道，
与我们同类）。**它的冲突保护整个建立在文件系统 watcher 上**：

- `useEditorExternalWatch.ts`，**1024 行**，订阅 `fs:changed`
- 写之前在 self-write registry 盖戳去回声；本地 TTL 750ms，**远程 3000ms**——
  注释原文的理由是「SSH/runtime watcher 的回声走轮询加网络的路径，可能好几秒才落地」
- 脏文件被外部改动 → `externalMutation: 'changed'` 横幅；自动保存停摆、手动保存放行
- 基线用**内容签名**不是 mtime：`getDiskBaselineSignature` = 两条 FNV 通道 + 长度，
  注释点名 issue #7265「哈希碰撞意味着漏掉一次冲突」

**我们不抄这条路**，两个理由：

1. agentd 没有文件变更推送通道，web 控制台也没有订阅面。要照抄得先建一整条推送通道——
   而 orca 那个「远程 TTL 从 750ms 调到 3000ms」本身就说明这套机制在网络上会飘。
2. 更要紧的是架构前提反过来：handoff 的纪律是**「agentd 持有全部状态，客户端无状态、
   随时可崩溃换机接管」**。orca 的签名基线活在渲染进程内存里，刷新一次就没了；
   放到我们这儿它守不住任何东西。

**所以：把冲突判定放进写请求本身。** 读的时候 agentd 连内容一起给 sha256，写的时候
客户端原样带回来，agentd 比对不一致就 409。不需要 watcher，不需要 TTL，不怕客户端
崩溃换机——这正是 orca 用一千行客户端代码去补偿的那件事，而我们的架构本来就允许
一个前置条件解决。

**代价要说清楚**：没有 watcher 意味着「文件被 executor 改了」只在你按保存时才知道，
不会主动提示。这是本设计明知并接受的取舍，不是遗漏。

### 1.3 从 orca 抄的三条

反过来，orca 这三条判断是对的，直接采用：

1. **超限拒绝，绝不截断**。`MAX_TEXT_FILE_SIZE = 10 MiB`，超了 `throw`，
   连内部的 `readNodeFileWithinLimit` 都专门定义了 `NodeFileReadTooLargeError`
   而不是返回残缺 buffer。
2. **二进制是响应体上的一等字段**，不是塞进 content 的话术。判据朴素可抄：
   **前 8 KiB 出现 NUL 字节即二进制**，返回空内容 + `isBinary: true`。
3. **冲突判据用内容哈希不用 mtime**。这条对我们比对 orca 更成立：executor 在工作树里
   频繁跑 git 操作，`checkout`/`rebase` **会动 mtime 但不动内容**——用 mtime 会把
   大量无害情况报成冲突。我们用 Go 标准库 `sha256`，不必像它那样手搓 FNV 再为碰撞加通道。

一条**没有**抄：orca 对用户文件是裸 `writeFile`（它只对自己的状态文件用
`durable-file-write.ts` 的 tmp+fsync+rename）。我们用原子替换，理由见 §5。

---

## §2 读侧改造：写入必须踩在保真的读上面

### 2.1 `ReadFile` 返回结构体

```go
// FileRead 是一次文件读取的完整结论。
type FileRead struct {
    Content   string `json:"content"`             // Binary 为真时是空串
    Size      int64  `json:"size"`                // 磁盘真实大小，不是 Content 的长度
    Truncated bool   `json:"truncated,omitempty"` // 超过 maxRunOutput(1 MiB)
    Binary    bool   `json:"binary,omitempty"`    // 前 8 KiB 出现 NUL 字节
    SHA256    string `json:"sha256,omitempty"`    // 仅当 !Binary && !Truncated 才有值
}

func ReadFile(repo, rel string) (FileRead, error)
```

**`SHA256` 只在「完整且是文本」时才算**——它唯一的用途是当写入前置条件，而
Binary/Truncated 两种情况本来就不许写。**空值即「这文件不可编辑」**，前端不必再判一次，
后端也不必为一个注定被拒的写入算哈希。

`Size` 取自已打开句柄的 `Stat()`（现有实现已经这么做），不是 `len(Content)`——
截断时两者不同，而用户要看到的是真实大小。

### 2.2 截断提示搬家，CLI 契约零变更

现在 `ReadFile` 在超限时把这行**拼进正文**返回：

```
\n\n===== 内容已截断：文件共 %d 字节，以上仅为开头 %d 字节 =====\n
```

这对 `handoff fetch` 是对的（它就是要看开头，提示是给审核者看的），对编辑是致命的
（前端分不清这行字是文件真有的还是 agentd 加的，存回去就是把文件截断后再插一行中文注释）。

**做法**：`truncatedNotice` 从 `ReadFile` 里移到 `handleTaskFile` 里。

- `GET /api/tasks/{id}/file` 的响应体**一字不变**，仍是 `{"content": "..."}`，
  截断时正文仍带那行提示。`handoff fetch` 行为零变更，
  `internal/client/client.go:763` 那个 `struct{Content string}` 不用动。
- `GET /api/workspaces/file` 的响应体从 `{content}` 扩成完整 `FileRead`——**纯追加**，
  老前端读 `.content` 照常工作。

### 2.3 二进制判定

新增 `isBinaryPrefix(b []byte) bool`：前 `min(len, 8192)` 字节内出现 `0x00` 即为真。
判定放在 `ReadFile` 内、读完之后（我们的上限是 1 MiB，不像 orca 需要为 10 MiB 文件
先探前缀再决定要不要整读）。

Binary 为真时 `Content` 置空串——不返回被 UTF-8 替换字符打烂的内容，
那既没有展示价值又会诱使人存回去。

---

## §3 写端点

```
PUT /api/workspaces/file?path=<工作树绝对路径>&rel=<相对路径>[&machine=<机器名>]
Content-Type: application/json

{"content": "...", "base_sha256": "..."}
```

```go
// FileWriteReq 是 PUT /api/workspaces/file 的请求体。
type FileWriteReq struct {
    Content    string `json:"content"`
    BaseSHA256 string `json:"base_sha256"` // 必填；调用方读到的那一版的哈希
}

// FileWriteResp 是写入成功后的响应。
type FileWriteResp struct {
    SHA256 string `json:"sha256"` // 新内容的哈希，调用方直接拿它当下一次的 base
    Size   int64  `json:"size"`
}

// FileConflictResp 是 409 的响应体：带上磁盘现状，省掉一次往返。
type FileConflictResp struct {
    Error   string   `json:"error"`
    Current FileRead `json:"current"`
}
```

`machine` 参数复用现有的 `forwardIfRequested`，与两个读端点同一条路。

### 3.1 服务端流程

先比对后写，中间不释放 `os.Root` 句柄：

1. `forwardIfRequested` → 需要转发就转发
2. `workspaceRootOrErr`（白名单）→ 不中 **400**（§1.1）
3. `rel` 为空 → **400**
4. `rel` 命中 `.git` 前缀 → **400**（§4.1）
5. `Root.Lstat(rel)`：不存在 → **404**；目录 → **400**；符号链接 → **400**（§4.2）；
   非普通文件 → **400**
6. 读现盘全文（走同一个 `ReadFile`）：`Binary` → **400**；`Truncated` → **400**（§4.3）
7. `cur.SHA256 != req.BaseSHA256` → **409** + `FileConflictResp{Current: cur}`
8. 新内容 `len(req.Content) > maxRunOutput` → **400**（§4.4）
9. 原子替换（§5）→ **200** + `FileWriteResp`

第 6 步复用 `ReadFile` 而不是另写一遍读取，是为了让「可编辑」的判据在读侧和写侧
**由同一段代码给出**——两边各判一次，早晚会分叉成「前端说能编辑、后端说不能」。

---

## §4 拒绝面

### 4.1 拒绝写 `.git/`

现在 `ListDir`/`ReadFile` 对 `.git` **完全不过滤**（实测：本 worktree 的 `.git` 是个
90 字节的文件，内容是 `gitdir: <路径>`）。可写意味着：

- **worktree**：改 `.git` 这个指针文件，能把工作树重指向任意目录
- **主仓库**：`.git/config` 写进 `core.pager` / `core.sshCommand` / `hooksPath`，
  就是下一次任何 git 操作时的任意命令执行；改 `HEAD`、删 `index` 也都能直接搞坏仓库

这不是提权（控制台会话本来就与主令牌等价，见 §1.1），是**「一次误操作就把仓库弄坏」**——
正是 §1.1 那条「参数校验」要挡的东西。

判据：`filepath.Clean(rel)` 归一化后，等于 `.git` 或以 `.git/` 开头即拒。
**读侧维持现状不动**（只读 `.git` 没有破坏面，且改了会波及已发布行为，见 §0）。

### 4.2 目标是符号链接就拒，不跟随

原子替换的代价：`rename` 会把链接本身换成普通文件，语义悄悄变了。与其猜用户想改
链接还是改目标，不如拒掉说清楚。用 `Lstat`（不是 `Stat`）判定。

### 4.3 现盘是二进制或已截断就拒

`base_sha256` 在这两种情况下必然是空值（§2.1），所以第 7 步的比对必定不通过——
但要在第 6 步就用**说得清的理由**拒掉，而不是让它掉进一个「哈希对不上」的 409，
那会让用户以为「文件被谁改了」。

### 4.4 新内容超过 1 MiB 就拒

与读侧同一个 `maxRunOutput`。理由对称：读不回来的东西，就不该写得进去——
否则存一次之后这个文件自己就变成不可编辑的了。

### 4.5 路径逃逸

沿用现有 `os.Root` + 词法双重防线（`ErrPathEscape` → 400）。写侧不新增判据，
只是把同一套复用过来。

---

## §5 原子替换，但不 fsync

**做原子替换**：同目录写 tmp（`.<basename>.<pid>.<纳秒>.tmp`）→ `Root.Rename` 覆盖目标。
Go 1.26.1 的 `os.Root` 有 `Rename`/`Create`/`OpenFile`，全程不出根。

这里**没有**抄 orca（它对用户文件是裸 `writeFile`）。理由是场景不同：**executor 就在
同一个工作树里跑**，裸覆盖有个窗口能让它读到半截文件；orca 的编辑对象通常没有一个
高频读者在旁边。

**不做 fsync**（这一条与 orca 对用户文件的选择一致）：工作树在 git 管着，掉电丢一次
编辑不是灾难，而每次保存 fsync 的代价在远程更明显。orca 只对**自己的**状态文件
（`durable-file-write.ts`）做 fsync——那些文件丢了没有第二份，工作树文件不是。

**保留原文件 mode**：从第 5 步的 `Lstat` 结果取，`Root.OpenFile` 建 tmp 时带上。
不保留会把可执行脚本的 +x 丢掉，而那是个静默故障。

**tmp 清理**：任何未走到 rename 的路径都 `Remove` 掉（含 panic 路径，用 defer）。

---

## §6 前端：`FileTab` 从只读改成可编辑

### 6.1 头部三态

判据就是 `sha256` 有没有值（§2.1 那条「空值即不可编辑」在这里兑现）：

| 情况 | 右侧显示 | 正文 |
|---|---|---|
| `binary` | 「二进制文件，不支持在线编辑」 | 占位说明，不是空白 |
| `truncated` | 「文件 <Size> ，仅显示开头 1 MB，不支持在线编辑」 | 只读展示已读到的部分 |
| 可编辑 | 脏标记 `●` + 「保存」按钮（干净或保存中禁用） | `<textarea>` |

### 6.2 编辑器用 `<textarea>`

不引 Monaco/CodeMirror（§0）。等宽字体，不折行、横向可滚。

### 6.3 ⌘S 挂在 tab 容器上，走冒泡

和 B74 的 ⌘K 是同一个教训的另一面：**分屏里另一侧可能是终端，⌘S 在终端有焦点时
应该归终端**。监听挂 `FileTab` 自己的容器、走冒泡，不挂 `window`、更不用 capture。

### 6.4 关闭拦截

用 `WorkbenchPage` 已有的 `onBeforeClose`（PTY 那条线加的）在脏草稿关 tab 时弹确认。

---

## §7 草稿

### 7.1 必须解决的坑：切 tab 会卸载

`WorkbenchPage.tsx:141` 只渲染 `activeTab`——**切到别的 tab 会把 `FileTab` 卸载掉**。
草稿只要活在组件 state 里，「点一下隔壁终端再切回来，改的字全没了」。

### 7.2 双层：内存活过切换，localStorage 活过刷新

**内存层**（活过 tab 切换）：草稿进 `TabContent`，沿用终端 tab 回写 `sessionId` 的既有路子：

```ts
| { kind: 'file'; rel: string; draft?: string; baseSha?: string }
```

经 `setTabContent` 回写；`dedupKey` 对 file 仍只取 `rel`，草稿不参与去重。

**不能每敲一个字就回写**——那会把整棵 `WorkbenchPage` 重渲染一遍。orca 正是在这儿
栽过（issue #826：一次 reload dispatch 扇出成 N 次 EditorPanel 重建把渲染进程卡死，
只能加 75ms 去抖）。我们不用去抖这种概率性方案：**打字只动组件本地 state，用一个 ref
记住最新值，卸载时在 effect 清理里一次性刷上去**。精确，且打字期间父层零重渲染。

**localStorage 层**（活过刷新与误关）：键 `handoff:draft:<machine>:<workspacePath>:<rel>`，
值 `{draft, baseSha, savedAt}`。写入去抖 500ms（这一层不要求精确——刷新丢掉最后半秒
的输入可以接受，而每次按键写 localStorage 会掉帧）。

**为什么值得做**：在浏览器里编辑，最常见的丢失是刷新和误关，不是换机器。真换了机器，
连那个浏览器 tab 都没了，草稿跟着没有并不意外。

**过期草稿是白送的**：草稿连 `baseSha` 一起存，打开文件时拿它和磁盘现在的 `sha256`
一比，不等就直接亮 §8 那条冲突条——同一套 UI、同两个出口，不发明新逻辑。

**配额**：localStorage 每源约 5 MB，可编辑文件上限 1 MiB，攒几个就满。按 `savedAt`
最近使用淘汰；写不进去就**静默**退回内存草稿——不弹错，因为这时候用户正在打字，
一个存储配额的报错帮不上任何忙。保存成功 / 放弃草稿时删除对应键。

### 7.3 草稿服务端化不在本期

放 agentd 侧（`~/.handoff/drafts/…`）能活过换机接管，与 handoff「客户端无状态」的
纪律最贴。但它是**一整块新功能**：草稿目录的布局与键、按键节流往远端写（**这就是
自动保存的机器，只是写到别处**）、GC（任务 done、managed worktree 删掉之后草稿成孤儿）、
以及「草稿 / 草稿的基线 / 磁盘现状」三方状态。**另立 B82。**

**已排除**：草稿绝不能落在工作树里。那是 executor 正在干活的 git 仓库——草稿文件会进
`git status`、会被 agent 顺手 commit、会污染 `handoff diff`、会让下一次 `dispatch` 的
「工作区必须干净」检查直接拒发。

---

## §8 冲突：409 与两个出口

409 的 body 带着 `current`（§3），所以冲突条不用再跑一趟就能显示磁盘现状：

> **文件已在磁盘上变了**（很可能是 executor 改的）
> [ 放弃我的改动，载入磁盘版本 ]　[ 用我的内容覆盖 ]

- **左**：草稿换成 `current.content`，`baseSha` 换成 `current.sha256`，清脏标记
- **右**：二次确认后，**拿 `current.sha256` 当新的 `base_sha256` 重发**——
  覆盖是「我看过了，接受这个新基线」，不是「跳过校验」。若这中间磁盘又变了，
  第二次照样 409，这正确

这里和 orca 分道扬镳的地方值得记一笔：orca 的对应逻辑是「自动保存停手、手动保存放行，
横幅已经警告过了」——它敢这样是因为**有 watcher，横幅在你按保存之前就出现了**。
我们没有 watcher，冲突只在保存那一刻才暴露，所以覆盖这个动作必须自带二次确认，
不能当成「警告过了」。

打开文件时命中过期 localStorage 草稿（§7.2）走同一条冲突条，文案改成
「本地草稿基于的版本已经变了」。

---

## §9 错误呈现

沿用既有纪律：**agentd 的中文错误原文原样透传**，不吞成「操作失败」。

| 场景 | 码 | 文案（agentd 侧） |
|---|---|---|
| 白名单不命中 | 400 | 路径不是本机已探测到的工作树，拒绝访问 |
| 缺 rel | 400 | 缺少 rel 参数 |
| `.git` 下 | 400 | 不允许写入 .git 目录 |
| 符号链接 | 400 | 目标是符号链接，不支持在线编辑 |
| 目录 / 非普通文件 | 400 | 路径是目录，不是文件 / 路径不是普通文件 |
| 二进制 | 400 | 二进制文件不支持在线编辑 |
| 超限（现盘或新内容） | 400 | 文件超过 1 MB，不支持在线编辑 |
| 逃逸 | 400 | 路径不合法（不允许逃出工作树） |
| 不存在 | 404 | 文件不存在 |
| 哈希不匹配 | 409 | 文件已被改动 |
| 写失败 | 500 | 写入文件失败 |

---

## §10 日志与注释（instrumenting-code）

**Go 侧关键节点**（用 `s.log`，禁 `fmt.Printf`）：

- 进入写请求：`Info` 带 `path` / `rel` / `bytes`（**不打 content**）
- 每个拒绝分支：`Warn` 带原因与 `rel`
- 409：`Warn` 带 `base` 与 `current` 两个哈希的**前 8 位**（全量哈希无意义地长）
- 原子替换前后：`Info` 带 tmp 路径；rename 失败 `Error` 带 cause
- 成功出口：`Info` 带 `rel` / `bytes` / 新哈希前 8 位——**成功路径不静默**
- 红线：主令牌、ticket 明文、cookie 明文不进日志；**文件正文也不进日志**

**前端**：`web/src/` 生产代码零 `console.*`（B74/B75 已确立），本期不破例。
`instrumenting-code` 的义务由**意图注释 + 测试**兑现。

**注释**：新文件写文件头（职责 + 边界）；导出函数写参数/返回/注意；
以下几处必须有「为什么」注释——`SHA256` 为何只在完整文本时才算、`.git` 为何拒、
符号链接为何拒、为何原子替换但不 fsync、⌘S 为何走冒泡不走 capture、
草稿为何卸载时刷新而不是去抖。

---

## §11 测试

**Go**（`internal/agentd`）：

- `ReadFile` 返回结构：正常 / 截断（`Truncated` 真、`SHA256` 空、`Size` 是真实大小、
  **正文不再含中文提示**）/ 二进制（`Binary` 真、`Content` 空）
- `handleTaskFile` **仍返回带提示的旧形状**——CLI 契约的回归防线
- 写入：哈希匹配成功、不匹配 409 且 body 带 `current`、`base_sha256` 为空 → 409
- 拒绝面逐条：`.git` / `.git/config` / 符号链接 / 目录 / 二进制 / 现盘超限 /
  新内容超限 / 逃逸 / 不存在
- mode 保留：对 `0o755` 的文件写入后仍是 `0o755`
- tmp 清理：rename 失败后目录里没有残留 tmp
- 契约 fixture：`go test ./internal/proto/ -run TestContractFixtures -update` 后
  review diff（新增 `FileRead` / `FileWriteReq` / `FileWriteResp` / `FileConflictResp`）

**Web**（vitest）：

- 脏标记出现/消失；保存成功后回基线、按钮变灰、`baseSha` 换成响应里的新哈希
- 409 → 冲突条出现；两个动作各自的结果，**尤其「覆盖」必须带新 sha 重发**，不是空 base
- binary / truncated → 无保存按钮 + 对应理由文案
- ⌘S 在 `FileTab` 内触发保存，容器外不触发
- **切走 tab 再切回来草稿还在**——§7.1 那个卸载坑的回归防线
- localStorage：写入去抖、刷新后恢复、过期草稿（baseSha 对不上）亮冲突条、
  配额写不进时静默降级

---

## §12 验收标准

1. 在控制台打开工作树里一个文本文件，能改、能保存，刷新页面后文件内容确是新的
2. 保存后按钮变灰、脏标记消失，再次保存不报冲突（基线已换）
3. 保存前用 `handoff run <task> 'echo x >> <文件>'`（或直接在执行机改）制造外部改动，
   再保存 → 出现冲突条，两个出口都按预期工作
4. 二进制文件（如 PNG）打开显示「不支持在线编辑」，无保存按钮，**正文不是乱码**
5. 超过 1 MB 的文件打开显示真实大小 + 「仅显示开头 1 MB」，无保存按钮
6. `.git` 下的文件在树里仍能看到、能点开只读，**保存被拒且理由说得清**
7. 改了字之后切到隔壁终端 tab 再切回来，改动还在
8. 改了字之后刷新页面，改动还在；若这期间文件被改过，回来时直接是冲突条
9. `handoff fetch` 对一个 2 MB 文件的输出**与本期之前逐字节一致**（含那行截断提示）
10. 回归全绿：
    ```
    go build ./... && go test ./... && go vet ./... && gofmt -l .
    ```
    ```
    cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
    ```

---

## §13 未决

- **`.git` 的读侧过滤**：本期只挡写。要不要在文件树列举里也隐掉，属 W4 文件浏览
  的既有行为，另议（§0）。
- **watcher**：没有它就没有「文件被改了」的主动提示（§1.2）。真需要时它是一整条
  推送通道，该有自己的 spec。
- **前置条件收窄了窗口，没有消灭它**：§3.1 第 6 步读哈希与第 9 步 rename 之间不是
  原子的，executor 恰好在这个窗口里写同一个文件，本设计检测不到，结果是它的改动被
  覆盖。真要堵死得在 agentd 侧按路径加锁——**而那也只挡得住 agentd 自己的并发写，
  挡不住 executor**（它直接动文件系统，根本不经过这个端点）。所以这不是「加把锁就
  解决」的问题，本期如实接受：窗口从「整个编辑时长」缩到「一次读+一次 rename」，
  已经是这条路能拿到的全部。
