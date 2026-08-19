# agentd 的浏览器鉴权 —— 设计

> 状态：待实现
> 来源：08-11 ADR-0009 定案「桌面控制台改为 agentd 托管的 Web UI」后，其中列为
> **唯一真正承重的改动**的一项
> 关联：[ADR-0009](../../adr/0009-desktop-console-is-agentd-hosted-web-ui.md)、
> [桌面端纵切设计](2026-08-09-handoff-desktop-vertical-slice-design.md) §31-32、§150

---

## 1. 背景与目标

### 1.1 卡口

`/ws/events` 注册在 [server.go:151](../../../internal/agentd/server.go)，被 [server.go:152](../../../internal/agentd/server.go)
的 `s.auth(mux)` 整体包住，鉴权靠 `Authorization: Bearer <token>` 头。

Go 客户端能在 [client.go:765](../../../internal/client/client.go) 设 `HTTPHeader`。
**浏览器的 `new WebSocket()` 不能设任何请求头**——这是 WebSocket API 的硬限制，
不是配置问题。

这一个卡口同时挡着三条路：浏览器 Web UI、移动端、未来上云。

### 1.2 目标

1. 浏览器（系统浏览器 / 桌面薄壳 / 将来的手机）能访问 agentd 的**全部** `/api` 与 `/ws` 路由。
2. **CLI 一行不改**。今天的 Bearer 路径必须原样继续工作。
3. 用户**零输入**：桌面壳点开即用，不粘贴、不复制、不扫码（手机除外，见 §7）。
4. 不给「未来把流量交给一台不可信中转」堵死路——具体约束见 §8。

### 1.3 非目标

| 不做 | 理由 |
|---|---|
| 中转服务器 | 用户 08-11 明确：「现在先不做，只是想清楚，留好口子」 |
| agentd 自持 TLS | 同上。但它是留口子的**必要条件**，冲突记录在 §8.3 |
| 移动端配对 UI | 依赖 agentd 监听非 loopback 或中转，两者都不在本轮 |
| 多用户 / 账号体系 | agentd 是单人单机工具，会话代表「一台设备」，不代表「一个人」 |
| `/ws/events` 的整机订阅 | 与本设计正交（一个管「你是谁」，一个管「你能订什么」）。现状与缺口见 §9 |

---

## 2. 先纠正一个前提：loopback 不是边界

设计过程中提出过「本机经 127.0.0.1 连接，无需凭据也安全」。**这一条不成立**，
而且今天挡住下列全部风险的恰恰就是那个 Bearer token。记录在此，避免后续被重新提出。

**一、本机任意进程都能连。** `POST /api/tasks/{id}/run` 是在任务仓库里跑 `sh -c`。
无凭据意味着任何 npm 包的 postinstall、任何编辑器扩展、共享机器上的任何其他用户，
都能在开发机上执行任意代码。WS 这条路对它们尤其敞开——`coder/websocket` 的
`authenticateOrigin` 第一行就是 `if origin == "" { return nil }`，非浏览器客户端
根本不发 `Origin` 头，一律放行。

**二、CORS 挡不住网页发请求。** CORS 限制的是**读响应**，不是**发请求**。
一个简单 POST 打到 `127.0.0.1:7777` 能落地；攻击页读不到返回值，但命令已经执行完了。

**三、DNS rebinding 连 Origin 校验一起绕。** [server.go:1019](../../../internal/agentd/server.go)
用的是 `websocket.Accept(w, r, nil)`，即库的默认校验，核心是：

```go
if strings.EqualFold(r.Host, u.Host) { return nil }   // accept.go:239
```

攻击者域名重绑到 127.0.0.1 后，浏览器发出的是 `Origin: http://evil.com` 与
`Host: evil.com:7777`——**两者相等**，这行直接判过。此时攻击页不但能发请求，
还能读到响应，因为浏览器认为是同源。

结论：本机的安全靠凭据，不靠 loopback。**去掉凭据，上面三条全部成立。**
本设计因此不引入任何「本机免鉴权」的路径。

但「本机不该让用户手输凭据」这半条是对的，§4 的机制正是为它服务。

---

## 3. 凭据模型

三类凭据，职责严格分开：

| 凭据 | 持有者 | 载体 | 生命周期 | 可吊销 |
|---|---|---|---|---|
| **主令牌** `cfg.Token` | CLI、桌面壳（经 CLI） | `Authorization: Bearer` 头 | 长期不变（即今天的 token） | 否（换它=全部重配） |
| **会话** | 浏览器上下文 | httpOnly cookie | 默认 30 天，滑动续期 | **是，单个** |
| **一次性 ticket** | 主令牌 → 会话的中介 | URL query 参数 | 60 秒，单次消费 | 自然作废 |

### 3.1 为什么要 ticket 这一跳

浏览器的第一次访问是一次 **GET 顶层导航**，此时它还没有任何凭据，凭据只能放在 URL 里。

- 放主令牌 → 主令牌进入浏览器历史、`Referer`、反向代理日志、**以及未来中转的访问日志**，且永久有效。
- 放 ticket → 即便全部泄漏，它已被消费，且窗口只有 60 秒。

这一跳是「**长期凭据永不进 URL**」这条约束的机制化实现——不靠自律，靠机制。
它是本设计中为未来不可信中转留的最重要一块（§8.1）。

### 3.2 为什么会话要可单独吊销

桌面壳与 CLI 在用户物理掌握的机器上，凭据本来就躺在那台机器的 `~/.handoff/config.yaml` 里，
吊销它们意义不大。

**手机不同**：它会离开视线，且经过中转。手机丢失时必须能只吊销那一台，
而不是换主令牌、把所有 CLI 与全部配对一起干掉。

会话表因此不是为本机安全建的，是**手机这个端能够存在的前提**。

---

## 4. 认证流程

### 4.1 桌面壳（主路径，零输入）

```
壳启动
 ├─ 探测 agentd 是否在监听（B54.2 落地后它是 launchd 常驻服务，此步常态命中）
 │    └─ 不在 → 拉起或显示「启动中」（代价已记于 ADR-0009）
 ├─ 执行 `handoff console --print-url`
 │    └─ CLI 用主令牌 POST /api/auth/tickets，拿回带 ticket 的 URL
 └─ loadURL(那个 URL)
      └─ agentd 校验并销毁 ticket → Set-Cookie → 302 到 /
```

**壳内零凭据逻辑。** 它不读配置文件、不碰主令牌、不实现任何鉴权代码，
仍是 ADR-0009 说的那个约 200 行薄壳。

为什么是「壳调 CLI」而不是「壳自己读 `config.yaml`」：

1. 壳自己读配置是 Node `fs` 捷径，会与纵切设计 §150 的约束产生争议。虽然读的是壳自己的
   引导凭据而非「开发资源」（§150 禁的是让本地与远端语义分叉的捷径，配置读取没有远端对应物，
   大概率能辩护过去），但 `handoff` CLI 本就持有主令牌，**没有必要去辩护**。
2. 只有一套机制：CLI、系统浏览器、桌面壳、将来的手机，全都是「换 ticket → 拿 cookie」。
3. 壳永不因鉴权改动而需要更新——正是 ADR-0009 第四条理由要的性质。

### 4.2 系统浏览器

```bash
handoff console
```

取 `TargetEndpoint()` → POST `/api/auth/tickets` → 调系统 `open`/`xdg-open` 打开返回的 URL。

### 4.3 会话过期后

- **壳**：收到 401，自动重取 ticket 并重新 `loadURL`，用户无感。
- **系统浏览器**：前端收到 401 导向说明页，提示重新执行 `handoff console`。

---

## 5. 服务端改动

### 5.1 新增路由

| 方法与路径 | 鉴权 | 作用 |
|---|---|---|
| `POST /api/auth/tickets` | 主令牌（Bearer） | 签发一次性 ticket。请求体可带 `device_name` |
| `GET /console?ticket=<t>` | ticket 本身 | 校验并**原子消费** ticket → `Set-Cookie` → 302 到 `/` |
| `GET /api/auth/sessions` | Bearer 或 cookie | 列出会话（id / 设备名 / 创建 / 过期 / 最后活跃） |
| `DELETE /api/auth/sessions/{id}` | Bearer 或 cookie | 吊销指定会话 |
| `POST /api/auth/logout` | cookie | 吊销当前会话 |

`GET /console` 是唯一不经主令牌/cookie 的路由，因为 ticket 就是它的凭据。

**本设计不负责托管前端。** `/console` 的 302 目标固定为 `/`；本轮结束时 agentd 尚未
`go:embed` 任何页面，所以 `/` 返回 404 是**预期结果**——此时 cookie 已经设好，
用任意 API 请求即可验证鉴权链路通了。前端托管是 ADR-0009 里另外那约 20 行的事，
不在本轮。实现时不得为了「让页面别 404」而顺手塞一个占位首页。

### 5.2 auth 中间件

现状只认 Bearer。改为**先 Bearer、后 cookie，任一通过即放行**：

```
cfg.Token == ""                     → 401（现有 fail-closed 行为不变，见 server.go:160 的 why）
Bearer 有效                          → 放行，identity = CLI
否则 cookie 命中未过期未吊销的会话      → 放行，identity = 该会话；顺带滑动续期与 last_seen
否则                                 → 401
```

`subtle.ConstantTimeCompare` 的用法保持不变；会话查找同样必须是**按哈希查表**而非线性比较明文（§5.4）。

**滑动续期与 `last_seen_at` 的写入频率必须节流**，否则每个请求都要写一次库——
文件树、事件流、终端这些高频路由会把 SQLite 写成瓶颈。规则定死为：

- **续期**：仅当剩余寿命 < 生命周期的一半（默认即剩余 < 15 天）时，把 `expires_at`
  推到 `now + 30 天`。因此正常使用下每 15 天最多写一次。
- **`last_seen_at`**：仅当与库中值相差 > 5 分钟时才写。它只用于 `handoff sessions` 的展示，
  不参与任何鉴权判断，精度到分钟足够。

两者都不在鉴权的关键路径上同步阻塞——写失败只打 Warn，不影响本次请求放行
（会话是否有效已经判完了，续期失败最坏结果是提前过期，属安全侧失败）。

### 5.3 Host 白名单中间件（在 auth 之前）

新增，用于堵 §2 第三条的 DNS rebinding：

- 校验 `r.Host` 的 host 部分 ∈ 白名单。
- 默认白名单：`127.0.0.1`、`localhost`、`[::1]`，加上 `cfg.Listen` 的 host（除非它是 `0.0.0.0`）。
- 可由新配置项 `web.allowed_hosts` 扩展（为将来的域名/中转场景预留）。
- 不匹配 → **403**，并打 Warn 记录 Host 与来源地址。

这一条同时把 `websocket.Accept` 那个洞补上了：rebinding 的 `Host: evil.com` 在白名单这一层
就被挡下，根本到不了 `Accept`。放过白名单之后，`EqualFold(r.Host, u.Host)` 的语义就变成了
「Origin 必须等于某个白名单内的 Host」，这是正确的。

**它挡不住本机恶意进程**（进程可以伪造任意 Host 头）——那一层由凭据兜住。两层各司其职，
不要指望其中任何一层单独成立。

### 5.4 会话与 ticket 的存储

沿用 [store.go:71](../../../internal/store/store.go) 的既有写法：`CREATE TABLE IF NOT EXISTS`
列进 `Open` 的 DDL 数组；本轮是新表，不需要 `ALTER` 迁移。

```sql
CREATE TABLE IF NOT EXISTS sessions (
  id           TEXT PRIMARY KEY,           -- 会话 id，可公开，用于列出与吊销
  token_hash   TEXT NOT NULL UNIQUE,       -- cookie 值的 SHA-256；明文不落库
  device_name  TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMP NOT NULL,
  expires_at   TIMESTAMP NOT NULL,
  last_seen_at TIMESTAMP NOT NULL,
  revoked_at   TIMESTAMP
);

CREATE TABLE IF NOT EXISTS auth_tickets (
  id          TEXT PRIMARY KEY,            -- ticket 明文的 SHA-256
  device_name TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMP NOT NULL,
  expires_at  TIMESTAMP NOT NULL,
  consumed_at TIMESTAMP
);
```

**为什么两张表都只存哈希、不存明文**：库文件会被备份、会被误传、`handoff pull` 也可能
拉到意料之外的东西。库泄漏不应等于会话被接管。主令牌在配置里是明文属没办法（要拿它发
Bearer），会话不需要明文，就不留。

**ticket 的一次性靠 SQL 原子性保证**，不靠应用层判断：

```sql
UPDATE auth_tickets SET consumed_at = ?
 WHERE id = ? AND consumed_at IS NULL AND expires_at > ?
```

判 `RowsAffected == 1`。并发消费同一个 ticket 时只有一个会成功。

### 5.5 Cookie 属性

| 属性 | 取值 | 理由 |
|---|---|---|
| 名称 | `handoff_session` | — |
| `HttpOnly` | 恒定 | JS 读不到，XSS 偷不走 |
| `SameSite` | `Strict` | 跨站请求一律不带 cookie，这是 CSRF 的主防线 |
| `Path` | `/` | `/api` 与 `/ws` 都要 |
| `Secure` | **仅**当 `r.TLS != nil` 时设置 | 明文 loopback 下设 `Secure` 会让 cookie 直接失效 |
| `Max-Age` | 会话剩余寿命 | — |

`Secure` 的判据**只能是 `r.TLS != nil`，不得读 `X-Forwarded-Proto`**。那个头由上游代理写入，
而本设计的整个前提之一就是「上游可能是一台不可信中转」（§8）——让不可信的一方决定
cookie 的安全属性，方向是反的。将来若确实要支持可信反向代理，须由一条显式配置项
声明「信任来自某地址的转发头」，而不是默认相信。

**是否还需要独立的 CSRF token：不需要。** 理由是两层叠加已经封闭：
`SameSite=Strict` 使跨站请求（含表单 POST、`<img>` GET、WebSocket 握手）都不携带 cookie；
而唯一能让攻击页变成「同站」的手段是 DNS rebinding，已被 §5.3 的 Host 白名单挡死。
两层缺任何一层，这个结论都不成立——实现时不得以「反正有 SameSite」为由省掉 Host 白名单。

### 5.6 吊销后踢掉在线连接

`Hub` 只按 `taskID` 路由（[hub.go:32](../../../internal/agentd/hub.go)），不持有会话身份，
所以吊销一个会话不会自动断开它已经建立的 WS。手机丢失场景下这不可接受。

**做法：在 WS 的读循环里周期性复验会话（每 30 秒），失效则以 close code 1008 关闭。**

**为什么不建「会话 id → 连接 cancel 函数」的中心注册表**：封存清单缺陷 #4 的教训正是
「中心注册表式门禁若失败时不报错，就会变成永久静默故障」。注册表漏登记、漏清理都不会报错，
只会表现为「吊销了但没断」或「连接泄漏」，且两者都难以观察。周期性复验是自愈的——
查询失败就关连接，fail-closed，最坏情况只是 30 秒的滞后。

复验只在 identity 为会话时进行；Bearer（CLI）连接不受影响。

---

## 6. CLI 改动

```
handoff console [--print-url] [--device <name>] [--no-open]
handoff sessions
handoff sessions revoke <session-id>
```

- `console` 默认打开系统浏览器；`--print-url` 只打印（桌面壳用它）。
- `--device` 缺省取**本机主机名**（CLI 没有 User-Agent 可推断）。兑换 ticket 时，
  若该次请求带了 `User-Agent`，服务端把浏览器名补进展示名（如 `xushixin-mbp / Safari`）。
  设备名**纯展示，不参与任何鉴权判断**。但它来自客户端，所以 `handoff sessions` 输出前
  必须转义控制字符并截断长度——否则一个构造过的 `User-Agent` 能往终端里注入 ANSI 转义序列。
- 沿用 [cmd/root.go:98](../../../cmd/root.go) 的 `TargetEndpoint()` 取地址与主令牌，
  因而 `--target` 天然可用：`handoff console --target devbox` 打开那台机器自己的控制台。
  这是**诊断入口**，不是产品路径——产品路径仍是纵切设计 §31-32 的「只连本机 agentd，
  由它向远端转发」（§9）。不要因为这条 flag 好用就把它当成跨机方案。
- agentd 未运行时给明确报错，不要退化成连接超时。

现有命令**零改动**。

---

## 7. 手机（本轮只定形，不实现）

手机上跑不了 CLI，拿不到 ticket，所以第一次连接必须有一次人的动作：

```
handoff console --qr    # 桌面把 ticket URL 渲染成二维码
```

手机扫码 → 打开 URL → 换 cookie → 此后打开即登录态。走的是**同一套** ticket→cookie，
服务端一行不用改。

不在本轮实现的原因：它需要 agentd 监听非 loopback 地址，或需要中转——两者都是独立决策。

---

## 8. 为「不可信中转」留的口子

用户 08-11 的原话：「现在先不做，只是想清楚，留好口子。未来考虑建中转服务器给开放给用户用。」
且明确要求「让用户知道他们的数据和信息不会被服务器截流」。

由此倒推出三条约束。前两条本轮已机制化，第三条本轮不实现但必须记录。

### 8.1 长期凭据永不进 URL —— 本轮已落实

中转服务器的访问日志会记下 URL。把主令牌放进 query 参数，等于把「服务器不能截流」
提前判死刑，且不可挽回。§3.1 的 ticket 跳转就是为此。

### 8.2 凭据可单独吊销 —— 本轮已落实

见 §3.2、§5.4、§5.6。

### 8.3 agentd 必须能自己终止 TLS —— **与 ADR-0009 冲突，待裁决**

ADR-0009 写的是「上云所需的 TLS 计划由反向代理或 Tailscale 承担，不写 Go 代码」。

**这条在不可信中转的前提下站不住**：如果反向代理位于中转侧，中转就是明文终止点，
它能看到全部代码、终端输出与凭据，「不会被服务器截流」只剩承诺，没有机制。

要让中转**技术上无法**截流，只有一条路：证书由 agentd 自己持有，中转按 SNI 做 TCP 拼接、
只转发密文、从不持有密钥。这要求 agentd 支持自己终止 TLS——而今天它一行 TLS 代码都没有
（非测试代码中 `tls.` / `ListenAndServeTLS` / `x509` 零命中）。

本设计**不裁决**这条冲突，只记录它，并要求：在中转真正动工之前，先由一条 ADR 裁决
ADR-0009 的 TLS 条款是否被取代。本轮的任何实现不得引入「假定 TLS 一定由外部代理承担」
的硬编码假设。

---

## 9. 已知的相邻缺口（不在本轮范围）

`/ws/events` 今天**强制要求 `task` 参数**（[server.go:1003](../../../internal/agentd/server.go)，
缺参数直接 400），Hub 按任务路由，因此**订阅不了「一整台机器」**。

而「总览工作台一眼看到所有正在执行的 agent」需要的正是整机订阅——不可能「每任务一条 WS ×
机器数」。刚合入 main 的 B56 也没有改这条：它的做法是「同开 5 条」
（[B56 spec §325](2026-08-11-review-loop-continuous-subscription-design.md)），
且全仓 `Subscribe` 只有 `/ws/events` 一个调用点（同 spec §159）。

这与鉴权正交（一个管「你是谁」，一个管「你能订什么」），故不并入本设计，
但它是 Web UI 的硬前置，应单列 backlog 条目。

同样单列的还有 **agentd 之间的转发**：纵切设计 §31-32 已定「桌面端只连接本机 agentd，
本机 agentd 持有开发机注册表、凭据、连接」，而今天 `cfg.Targets` 只被 `cmd/` 读
（[pull.go:65](../../../cmd/pull.go)、[root.go:125](../../../cmd/root.go)），
`internal/agentd/` 里一处都没有，agentd 从不把自己当客户端。

**注意：这里不需要新建注册表。** `Server.cfg` 就是 `*config.Config`
（[server.go:64](../../../internal/agentd/server.go)），`cmd/agentd.go` 的 `config.Load`
把整个 cfg 传进了 `NewServer`，`s.cfg.Targets` 现在就能取到；`proto.Task` 也早已带 `Target`
字段（[proto.go:69](../../../internal/proto/proto.go)）。缺的只是「拨号并订阅」这个动作。

ADR-0009 第三条理由「把窗口指向远程 agentd 的 URL」与纵切设计 §31-32 是两个模型，
需要一并订正。

---

## 10. 错误处理

| 情况 | 响应 | 用户看到 |
|---|---|---|
| ticket 不存在 / 已过期 / 已消费 | 401 + 说明页 | 「这个链接已失效，请重新执行 `handoff console`」 |
| cookie 对应会话过期或已吊销 | 401 | 壳自动重取 ticket；浏览器导向说明页 |
| WS 上会话中途失效 | close code **1008** | 与现有「任务不存在」的处理一致（[server.go:1029](../../../internal/agentd/server.go)） |
| Host 不在白名单 | 403 + Warn 日志 | 一般只有攻击者会看到 |
| `cfg.Token` 为空 | 401 + Error 日志 | 现有 fail-closed 行为，不变 |
| agentd 未运行时执行 `handoff console` | 非零退出 + 明确报错 | 不退化为连接超时 |

---

## 11. 可观测性要求

按 `instrumenting-code` 的纪律，下列节点必须有日志，且实现完成前逐条自检：

- **签发 ticket**：Info，带 `device_name`、过期时刻。**不打 ticket 明文**。
- **消费 ticket**：Info，带结果（成功 / 已消费 / 已过期 / 不存在）与新会话 id。
- **会话建立**：Info，带会话 id、设备名、过期时刻。
- **鉴权失败**：Warn，带 `remote_addr`、`method`、`path`、失败原因（无凭据 / Bearer 不匹配 / 会话过期 / 会话已吊销）。沿用现有 [server.go:177](../../../internal/agentd/server.go) 那条 Warn 的字段。
- **Host 白名单拒绝**：Warn，带实际 Host 与 `remote_addr`。这是 rebinding 攻击的唯一信号。
- **吊销**：Info，带会话 id、发起方（Bearer / 哪个会话）。
- **在线连接被踢**：Info，带会话 id。缺了它「吊销了但手机还连着」将无从排查。

**凭据纪律**：主令牌、ticket 明文、cookie 明文一律不得进日志。设备名与会话 id 可以。

---

## 12. 测试断言

### 12.1 不回归

1. 现有 Bearer 路径全部照常（现有测试全绿即可，不新增）。
2. `cfg.Token == ""` 仍然拒绝一切请求。

### 12.2 ticket

3. 有效 ticket 换得 cookie，并 302 到 `/`。
4. 同一 ticket **第二次**使用失败。
5. **并发**消费同一 ticket，恰好一个成功（验证 §5.4 的 SQL 原子性，非应用层判断）。
6. 过期 ticket 失败。
7. ticket 明文不落库（查表只见哈希）。

### 12.3 会话

8. cookie 能通过 `/api` 与 `/ws` 两类路由。
9. 吊销后，**新**请求立即 401。
10. 吊销后，**已建立的 WS** 在一个复验周期内被以 1008 关闭（§5.6）。
11. 会话过期后 401。
12. 滑动续期生效：临近过期的请求把 `expires_at` 推后。

### 12.4 Host 与 Origin

13. 伪造 Host（如 `evil.com`）→ 403，且**先于**鉴权发生（不应泄漏「凭据对不对」）。
14. **rebinding 回归**：`Host: evil.com` + `Origin: http://evil.com`（两者相等，正是
    `accept.go:239` 会放过的组合）→ 被 Host 白名单挡下。这条是 §2 第三条的直接回归测试，
    必须存在。
15. 无 `Origin` 头的非浏览器客户端带 Bearer 仍能连（保证 CLI 不受影响）。

### 12.5 CLI

16. `handoff console --print-url` 输出可用 URL 且不打开浏览器。
17. `handoff sessions` 列出会话；`revoke` 后再列已消失或标记吊销。
18. agentd 未运行时 `handoff console` 明确报错而非超时。

---

## 13. 实现顺序建议

1. 会话与 ticket 的存储层（表 + 原子消费 + 哈希）
2. Host 白名单中间件（独立、可先合，立即消除 rebinding 面）
3. auth 中间件扩展为「Bearer 或 cookie」
4. 三个 auth 路由 + `/console`
5. WS 的周期性会话复验
6. `handoff console` / `handoff sessions`
7. 桌面壳接线（`--print-url` + `loadURL`）

第 2 步与第 3 步之间没有依赖，但**第 3 步不得早于第 2 步合入**——先放开 cookie 再补
Host 白名单，中间会存在一个 rebinding 可用的窗口。
