# OpenCode v2.0.12 `serve` HTTP/SSE 契约探测报告（spike）

- 二进制：`/Users/xushixin/.opencode/bin/opencode`（`opencode2` / `opencode` 同一二进制），`opencode v2.0.12`
- 探测时间：2026-09-22，端口 45996-45999，全部 serve 已 kill，端口已释放
- 未改动 `~/.config/opencode`，未触碰任何既有 opencode 进程
- 产物目录：`/private/var/folders/hx/bw838qps02ngtg8wdz5z9sxw0000gn/T/opencode/spike-v2`

---

## 1. 基本面结论

| 问题 | 结论 | 证据 |
|---|---|---|
| `opencode2 --version` | `opencode v2.0.12` | 见 §7 |
| `opencode2 serve --help` | 支持 `--hostname/--port/--cors/--service/--stdio`；描述 "Start the v2 API and web server" | 见 §7 |
| `OPENCODE_SERVER_PASSWORD` | **支持**。设 `spikepwd` 后：无 auth 全站 401；`curl -u opencode:spikepwd` 得 200；错密码 401 | §2 |
| `OPENCODE_PASSWORD`（别名） | **也支持**。另一个 serve 用 `OPENCODE_PASSWORD=spikepwd2`，`-u opencode:spikepwd2` → 200 | §2 |
| 不设密码时 | serve **自动生成随机密码**并在 stdout 打 `server password <pwd>`，仍然强制认证（无 auth → 401） | `serve_config.log` |
| 认证方案 | HTTP Basic，`www-authenticate: Basic realm="Secure Area"`，用户名固定 `opencode`，401 响应体为空 | §2 |
| `OPENCODE_CONFIG` | **支持**。指向有效 json 时配置生效（`{"model":"opencode/spike-nonexistent-model-xyz"}` → `/api/model/default` 返回 `data:null`，对照组返回 mimo 模型）；指向**不存在**或**非法 json** 文件时 serve 照常启动、无报错（容忍/忽略） | §2、`serve_cfgmodel.log`、`serve_badcfg.log` |
| 二进制里还存在的相关 env | `OPENCODE_CONFIG_DIR`、`OPENCODE_CONFIG_CONTENT`、`OPENCODE_DB`、`OPENCODE_LOG_LEVEL`、`OPENCODE_PASSWORD` 等（strings 扫描，`OPENCODE_SERVER_PASSWORD` 字面量未被扫到但实测有效，疑为拼接构造） | §2 |

## 2. 关键原始证据（curl + 响应）

```
$ opencode2 --version
opencode v2.0.12

$ OPENCODE_SERVER_PASSWORD=spikepwd nohup opencode2 serve --port 45999 --hostname 127.0.0.1 &
server listening on http://127.0.0.1:45999          # serve1.log

$ curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:45999/api/session
401
$ curl -s -D - http://127.0.0.1:45999/ | head
HTTP/1.1 401 Unauthorized
www-authenticate: Basic realm="Secure Area"
vary: Origin

$ curl -s -u opencode:spikepwd -o /dev/null -w "%{http_code}" http://127.0.0.1:45999/api/info
200
$ curl -s -u opencode:wrong -o /dev/null -w "%{http_code}" http://127.0.0.1:45999/api/info
401

# 无密码 serve：日志打印随机密码
$ OPENCODE_CONFIG=/.../does-not-exist.json nohup opencode2 serve --port 45998 ... &
server listening on http://127.0.0.1:45998
server password rO1ZDoJ4Z22UK1o1wkwpXp-hWKAR2NwvJwNTiv4zCwM
$ curl -u 'opencode:rO1ZDoJ4...' http://127.0.0.1:45998/api/info -> 200；无 auth -> 401

# OPENCODE_PASSWORD 别名
$ OPENCODE_PASSWORD=spikepwd2 nohup opencode2 serve --port 45997 ...
$ curl -u opencode:spikepwd2 .../api/info -> 200；无 auth -> 401

# OPENCODE_CONFIG 非法 json：不报错
$ OPENCODE_CONFIG=bad-config.json opencode2 serve --port 45997   # bad-config.json 内容 '{ this is not valid json '
server listening on http://127.0.0.1:45997
server password zLGKgoZfG-...        # 无任何 config 报错

# OPENCODE_CONFIG 生效验证
$ OPENCODE_CONFIG=config-model-test.json opencode2 serve --port 45996 ...   # {"model":"opencode/spike-nonexistent-model-xyz"}
$ curl -u opencode:<pwd> http://127.0.0.1:45996/api/model/default
{"location":{"directory":"..."},"data":null}          # 对照组(45999) data 为 mimo-v2.6-flash-free
```

## 3. 文档 / OpenAPI 端点

| 路径 | 无 auth | 有 auth | 实际内容 |
|---|---|---|---|
| `/` | 401 | 200 `text/html` | v2 Web UI（SPA） |
| `/doc` | 401 | 200 `text/html` | 同 SPA（文档 UI） |
| `/openapi.json` | 401 | 200 `application/json` | **OpenAPI 3.1.0 全文（249KB，113 paths）→ 已存 `openapi.json`** |
| `/swagger.json` | 401 | 200 `text/html` | **是 SPA 兜底 HTML，不是 JSON**（md5 与 doc.html 不同但都是 HTML） |
| `/api` | 401 | 404 | 不存在（404 响应体为空） |

OpenAPI meta：

```json
{"openapi":"3.1.0","info":{"title":"opencode HttpApi","version":"0.0.1",
 "description":"Experimental HttpApi surface for selected instance routes."},
 "security":[],"securitySchemes":{}}
```

注意：**所有业务路由都在 `/api` 前缀下**（v1 的根路径 `/session`、`/event` 等在 v2 全部落入 SPA 兜底，GET 返回 HTML，非 API）。

## 4. v1 假设逐条对比表

| v1 假设 | v2 实测 | 判定 |
|---|---|---|
| `OPENCODE_SERVER_PASSWORD` 环境变量 | 生效（另发现 `OPENCODE_PASSWORD` 也生效；不设则随机生成并打印） | **不变** |
| `OPENCODE_CONFIG` 环境变量 | 生效；但不存在/非法 json 文件被静默容忍 | **不变（容错更宽）** |
| `GET /session` | 200 但是 `text/html`（SPA 兜底），真接口为 `GET /api/session` 200 JSON | **变了（加 /api 前缀）** |
| `POST /session` | 405（SPA 兜底只允许 GET）；真接口 `POST /api/session` → 201/200 JSON | **变了** |
| `POST /event`（v1 用 POST 收 SSE？） | 405；v2 SSE 是 `GET /api/event`（`text/event-stream`） | **变了** |
| `GET /event` SSE | 200 但是 HTML；真 SSE 端点为 `GET /api/event` | **变了** |
| `POST /session/<id>/message` | 404（v2 无写入型 message 端点，只有 `GET /api/session/{id}/message` 只读列表） | **变了（取消）** |
| `POST /session/<id>/prompt` | `POST /api/session/{sessionID}/prompt` → 200，返回 `Session.Inbox.User` | **不变（加 /api 前缀；改名 prompt 保留）** |
| `POST /permission/<id>` | 不存在；v2 为 `POST /api/session/{sessionID}/permission/{requestID}/reply` → 204 | **变了** |
| OpenAPI 文档端点 `/doc` | `/doc` 返回 SPA HTML；唯一 OpenAPI JSON 是 `/openapi.json` | **变了** |
| Basic auth 用户名 `opencode` | 有效 | **不变** |
| SSE 事件 `event:` 字段 | 帧只有 `data: {...}`（无 `event:` 行），事件名在 JSON 的 `type` 字段；另有 `: heartbeat` 注释帧 | **变了（v1 形态未验证，按 v2 实测实现）** |
| `Last-Event-ID` 断线重放 | 带该头重连只收到 `server.connected` + heartbeat，**未观察到重放**（证据不足以断言支持） | **未验证** |

## 5. 路由清单（path + method + 简述）

完整 136 条见 `routes.md`（由 `openapi.json` 生成）。核心（Go adapter 必备）：

| METHOD + PATH | operationId | 简述 |
|---|---|---|
| `GET /api/info` | server.info | `{version,pid,urls[],paths.tmp}`，可作健康检查 |
| `GET /api/event` | event.subscribe | SSE：`text/event-stream`，帧为 `data: {id,type,created,location,data,durable?}` + `: heartbeat` |
| `GET /api/session` | session.list | 查询 `limit/order/search/cursor`，响应 `{data:[Session.Info],cursor}` |
| `POST /api/session` | session.create | body 可空或 `{title,agent,model,permissions[],metadata,location,id}`，响应 `{data:Session.Info}` |
| `GET/DELETE/PATCH /api/session/{sessionID}` | session.get/delete/update | 会话读改删 |
| `POST /api/session/{sessionID}/prompt` | session.prompt | 发消息，body `{text,id?,files[],agents[],skills[],metadata,delivery:steer\|queue,...}` → `{data:Session.Inbox.User}`；错误 400 `{"_tag":"InvalidRequestError",...}`，404 会话不存在，409 冲突 |
| `GET /api/session/{sessionID}/message` | session.message.list | 只读消息分页 `{data:[Session.Message.Info],cursor{previous,next}}` |
| `POST /api/session/{sessionID}/interrupt` | session.interrupt | 打断，query `resume=true\|false` |
| `POST /api/session/{sessionID}/permission` | session.permission.create | 造权限请求 → `{data:{id:"per_...",effect:allow\|deny\|ask}}` |
| `GET /api/session/{sessionID}/permission` | session.permission.list | 本会话待决权限列表 `{data:[Permission.Request]}` |
| `GET /api/permission/request` | permission.request.list | 全局待决权限 `{location,data:[...]}` |
| `POST /api/session/{sessionID}/permission/{requestID}/reply` | session.permission.reply | body `{"decision":"once"\|"always"\|"reject"}` → 204 |
| `GET /api/permission/saved` / `DELETE /api/permission/saved/{id}` | permission.saved.* | "always" 记录（`{id:"psv_...",projectID,action,resource,time}`） |
| `POST /api/experimental/session/{sessionID}/wait` | experimental.session.wait | 等会话结束，204/503 |
| `GET /api/model/default`、`GET /api/model`、`GET /api/provider` | model/provider 只读 | 默认模型与厂商 |
| `GET /api/plugin` | plugin.list | 内置 + 本地插件及 `state.status/error` |
| `GET /api/config`、`PATCH /api/experimental/config` | config | 配置读改 |
| 其余 | — | agent/command/credential/integration/mcp/pty/shell/fs/skill/vcs/worktree/websearch/rpc/debug/migration 等，见 `routes.md` |

## 6. 事件与权限 JSON 形状样例（原文）

SSE 首帧与心跳：

```
data: {"id":"evt_0c72ad0fd001qSSkSUfad4HLyH","type":"server.connected","data":{}}

: heartbeat
```

真实 prompt 全生命周期（截选，全文 `sse.txt`）：

```
data: {"id":"evt_0c72ad4f5001rfbJUCHcdZHeOE","created":1790047868149,"type":"session.inbox.enqueued","location":{"directory":".../spike-v2"},"data":{"inboxID":"msg_0c72ad4f4001664Bn7VumQYuY4","sessionID":"ses_...","item":{"type":"user","payload":{"text":"Reply with exactly: PONG"},"delivery":"steer"}},"durable":{"aggregateID":"ses_...","seq":1,"version":1}}
data: {"id":"evt_...","type":"session.execution.started","data":{"sessionID":"ses_..."},"durable":{...}}
data: {"id":"evt_...","type":"session.step.started","data":{"sessionID":"ses_...","agent":"build","model":{"id":"mimo-v2.6-flash-free","providerID":"opencode"},"assistantMessageID":"msg_...","started":1790047868197},...}
data: {"id":"evt_...","type":"session.reasoning.delta","data":{"sessionID":"...","assistantMessageID":"...","ordinal":0,"delta":"..."}}
data: {"id":"evt_...","type":"session.text.delta","data":{"sessionID":"...","assistantMessageID":"...","ordinal":0,"delta":"PONG"}}
data: {"id":"evt_...","type":"session.text.ended","data":{"sessionID":"...","assistantMessageID":"...","ordinal":0,"text":"PONG"},...}
data: {"id":"evt_...","type":"session.step.ended","data":{"sessionID":"...","assistantMessageID":"...","finish":"stop","rawFinish":"stop","cost":0,"tokens":{"input":19477,"output":4,"reasoning":12,"cache":{"read":3456,"write":0}}},...}
data: {"id":"evt_...","type":"session.usage.updated","data":{"sessionID":"...","cost":0,"tokens":{...}}}
data: {"id":"evt_...","type":"session.execution.succeeded","data":{"sessionID":"ses_..."},...}
data: {"id":"evt_...","type":"session.renamed","data":{"sessionID":"ses_...","title":"Ping check for connection"},...}
```

帧结构：`{id, type, created?, location?{directory}, data, durable?{aggregateID,seq,version}}`；`durable` 表示可持久化/可排序事件（`seq` 单调）。

权限事件（`sse-permission.txt` / `sse-permission-reply.txt`）：

```
data: {"id":"evt_0c72c36fc002SwIub7zuy9624t","created":1790047958780,"type":"permission.asked","location":{"directory":"..."},"data":{"id":"per_0c72c36fc0016qsMhIzmtBGPXQ","sessionID":"ses_...","action":"shell","resources":["echo SPIKE_OK"],"save":["echo *"],"source":{"type":"tool","messageID":"msg_0c72c2b910014Rvs0az74HPy7x","id":"call_129dfce8f4a24205b63fc9df"}}}

data: {"id":"evt_0c72c2b66001CRxrlj5kucTcNu","created":1790047955814,"type":"permission.replied","location":{"directory":"..."},"data":{"sessionID":"ses_...","requestID":"per_0c72c0daf001JFsQY5UyZbGccS","reply":"once"}}
```

权限 API 实测流程：

```
POST /api/session/{sid}/permission  {"action":"bash","resources":["echo spike"]}
  -> {"data":{"id":"per_0c72c0da2001AD39Ii28pAJGoY","effect":"ask"}}      # 规则集为 ask 时；无规则时 effect:"allow" 直接放行
GET  /api/permission/request
  -> {"location":{"directory":"..."},"data":[{"id":"per_...","sessionID":"ses_...","action":"bash","resources":["echo spike"]}]}
POST /api/session/{sid}/permission/{pid}/reply  {"decision":"once"}
  -> HTTP 204（空 body）
GET  /api/permission/saved   # decision=always 后
  -> {"data":[{"id":"psv_0c72cbf3b001hWIaID4UeV7SWi","projectID":"...","action":"shell","resource":"echo *","time":{...}}]}
```

权限形状（OpenAPI `components.schemas`）：

```json
Permission.Request = {"id":"^per","sessionID":"^ses","action":string,"resources":[string],"save":[string]?, "metadata":object?, "source":{"type":"tool","messageID":string,"id":string}?, "message":string?}
Permission.Reply   = "once" | "always" | "reject"
Permission.Effect  = "allow" | "deny" | "ask"
Permission.Rule    = {"action":string,"resource":string,"effect":Permission.Effect}   # Permission.Ruleset = Permission.Rule[]
```

会话创建/提示请求响应样例：

```
POST /api/session  {}
-> {"data":{"id":"ses_f38d56fe7ffeXwlmvmuMvPJNvY","projectID":"3238...","cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1790047850521,"updated":1790047850521},"location":{"directory":".../spike-v2"}}}

POST /api/session/{sid}/prompt  {"text":"Reply with exactly: PONG"}
-> {"data":{"id":"msg_0c72ad4f4001664Bn7VumQYuY4","sessionID":"ses_...","time":{"created":1790047868149},"type":"user","payload":{"text":"Reply with exactly: PONG"},"delivery":"steer"}}

POST /api/session/spikeid/prompt   -> 400 {"_tag":"InvalidRequestError","message":"Invalid session ID","field":"sessionID"}
```

错误体统一形如 `{"_tag":"InvalidRequestError|UnauthorizedError|...","message":...,"kind":?,"field":?}`；未知路由 404 **空 body**。

观测到的事件 type 全集（本次 3 段 SSE 合并）：
`server.connected, session.inbox.enqueued, session.inbox.delivered, session.instructions.updated, session.execution.started, session.execution.succeeded, session.step.started, session.step.streamed, session.step.ended, session.reasoning.started|delta|ended, session.text.started|delta|ended, session.tool.called, session.tool.input.started|ended, session.tool.progress, session.tool.success, session.usage.updated, session.renamed, permission.asked, permission.replied, shell.created, shell.exited`。

## 7. CLI 一元命令形态

```
opencode run --help
  opencode run [flags] [<message...>]
  --standalone --server <url> --continue/-c --session/-s <id> --fork --model/-m
  --agent --format default|json --file/-f --title --thinking --auto

opencode session --help
  opencode session <subcommand>
  SUBCOMMANDS: list | delete | export | import

opencode api --help
  opencode api [flags] <operation | method path...>       # 可直接用 OpenAPI operationId 调 server
  --standalone --server --data/-d --header/-H --param key=value
```

## 8. 插件与凭证（只读确认）

```
$ ls ~/.config/opencode/plugins/
handoff-monitor.ts
$ opencode2 plugin list
ID  VERSION  SOURCE
-   local    /Users/xushixin/.config/opencode/plugins/handoff-monitor.ts

GET /api/plugin -> 该本地插件 state: {"status":"failed","error":"Plugin must export a default definition with an id and an effect or setup function.","ref":"err_2cf7aba9"}
                   其余 ~90 个 builtin 插件均 active
```

→ `~/.config/opencode/plugins/` 仍是 v2 全局插件目录（且被 v2 加载，当前 handoff-monitor.ts 因导出格式不符处于 failed）。

凭证：

```
$ ls -la ~/.local/share/opencode/auth.json ~/.config/opencode/auth.json
-rw-------  744  /Users/xushixin/.local/share/opencode/auth.json    # 存在
ls: /Users/xushixin/.config/opencode/auth.json: No such file or directory   # 不存在
```

→ 凭证在 `~/.local/share/opencode/auth.json`；`~/.config/opencode/auth.json` 已废弃。

## 9. Go adapter 实现要点（结论浓缩）

1. 连接：`http://<host>:<port>` + HTTP Basic（user=`opencode`，password 取 `OPENCODE_SERVER_PASSWORD` 或 `OPENCODE_PASSWORD`，未设则读 serve stdout 的 `server password` 行）；所有请求（含 SSE）都要带 auth，否则 401 空 body。
2. 所有 API 都在 `/api` 前缀下；OpenAPI 固定取 `GET /openapi.json`（`/doc`、`/swagger.json` 都是 HTML）。
3. 事件流：`GET /api/event`（Accept 默认即可，返回 `text/event-stream`）；解析 `data: ` 行的 JSON，事件名看 `type`；`: heartbeat` 是注释帧需忽略；重连不带 `Last-Event-ID` 也能收到 `server.connected`（重放未验证）。
4. 发消息走 `POST /api/session/{sid}/prompt`（`delivery:"steer"` 或 `"queue"`）；等结束用 `POST /api/experimental/session/{sid}/wait` 或监听 `session.execution.succeeded`；打断用 `POST /api/session/{sid}/interrupt`。
5. 权限闭环：听 `permission.asked` → `GET /api/permission/request` 复核 → `POST /api/session/{sid}/permission/{pid}/reply` `{"decision":"once|always|reject"}` → 204 → 听 `permission.replied`。
6. 错误处理：400/401/404/409/500 的 JSON 带 `_tag`；404 未知路由为空 body，不要假设 JSON。
7. `OPENCODE_CONFIG` 传坏路径不会失败启动，但要生效必须是合法 json。

## 10. 产物文件

- `openapi.json` — OpenAPI 3.1 全文（113 paths / 245 schemas）
- `routes.md` — 全部 136 条 method+path+operationId+summary 表
- `sse.txt` — 真实 prompt 生命周期 SSE 原文（15s）
- `sse-permission.txt` — 含 `permission.asked` / `permission.replied` 的 SSE 原文（30s）
- `sse-permission-reply.txt` — 回复权限后工具执行（`session.tool.success`、`shell.*`）SSE 原文（12s）
- `serve1.log` / `serve_config.log` / `serve_badcfg.log` / `serve_cfgmodel.log` / `serve_altpwd.log` — 各次 serve 日志（含密码打印行）
- `doc.html`、`root.html`、`swagger.json` — SPA HTML（证明它们不是 OpenAPI）
- `bad-config.json`、`config-model-test.json` — OPENCODE_CONFIG 探测用配置
- `ses2.txt`、`reply1.out`、`reply2.out` — 会话 id 与 204 响应证据
- `serve1.pid`、`serve_config.pid` — 记录的 pid（进程已 kill）
- `REPORT.md` — 本报告

会话清理：两个探测会话已 `DELETE`（204），探测产生的事件/权限数据落在全局 `~/.local/share/opencode/opencode.db`（opencode 自身行为，未手动改动）。端口 45996-45999 均已释放（curl → 000 / connection refused）。
