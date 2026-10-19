# B441 agentd 机器解析统一走设备表 + 自名当本机（bug 流，spec 即 plan）

状态：用户裁决「先恢复 mac-mini targets，再按 bug 流修代码：统一机器解析 + 自名当本机」（2026-10-19）。

## 偏离与自主决定

- 范围只收**请求路由入口**（控制台用户直接撞到的）：HTTP 通用转发、建树转发、drop 转发、终端 WS 转发、任务路由、远程载体检测、执行机升级。CLI `--target`（`cmd/root.go`/`pull.go`/`card_dispatch.go`）与「未挂账 target 摘要」（`ledgerapi.go#unlinkedSummary`）同属「只认配置 targets」族，但改法涉及 CLI 读设备表 / 摘要缓存一致性判据的设计，未在本卡做，落 roadmap。自行拍板，理由：本卡是生产止血，二者不在用户报告的症状链上。
- 独立审查（子 agent，Important I1）补入第八处 `tasksfanout.go#tasksAll`：看板 `scope=all` 以 `conf().Targets` 判「有哪些机器」，配置清空后远端任务全被当已删机器跳过、机器行消失；改为 `pool.Names()` 去掉本机自名。属本卡同族、在控制台请求路径上，就地修。
- 报错文案由「未在本机配置的 targets 中定义」改为「未登记（设备表/配置中均无此名）」：语义（400/502/404 状态码、点名机器）不变；`hostprobe_test.go` 一处断言改认新文案。

## 现象（生产 mac-mini，628d9311）

1. 关闭含 linux-01 终端的标签组：`机器 linux-01 未在本机配置的 targets 中定义`，tab 关不掉。
2. 开发机详情面板（执行纪律 / Env / 自定义启动项 / 缺省执行者）四块全红：`机器 mac-mini 未在本机配置的 targets 中定义`（选中本机时也报）。

## 根因

- B439 起 `targetclient.Pool` 读设备表（`SetDeviceSource`），agentd 启动日志明说「本地 targets: 已被设备表取代，池不再读它；请从 config.yaml 删除这些条目」。14:04 的清理照做删掉了三台的 `targets:`。
- 但各转发入口仍先查 `conf().Targets[name]` 判「机器是否定义」再 `pool.For(name)`：判据与取客户端**两个来源**，配置一清空，设备表里在线的机器全被 400。
- 自名：B439 起机器列表里本机以自名（mac-mini）出现，控制台对本机也带 `?machine=mac-mini`；转发入口没有「自名即本机」判，按远端查 targets → 400。

## 修法

- `Server.remoteTarget(name)` = `pool.Target(name)`（与 `pool.For` 同源）；七个入口统一改用它，报错统一 `unknownMachineMsg`。
- 看板汇总 `tasksAll` 的机器名单改取 `pool.Names()`（去自名）。
- `?machine=` 命中 `IsSelfTarget`（自名 ∪ 指向本机的登记名）→ 本机处理：通用转发、建树、drop、终端 WS 四入口。

## 验收

- 红→绿：`internal/agentd/b441_machine_resolve_test.go`（有设备表源、无本地 targets 的生产形态）：设备表机器 HTTP 转发（日志坐实确实转发）、终端 WS 不再 400、任务路由按设备表转发、看板汇总列出设备表机器任务与机器行且不含自名行、`?machine=自名` 在通用转发 / 终端 WS / drop 留本机、未知机器仍 400 点名。
- 全量：gofmt / vet / go test 全仓 / web tsc+vitest+eslint。
- 真机：生产 mac-mini 换新二进制后，`/api/discipline?machine=mac-mini|linux-01|macbook-pro` 三台 200；标签组可关；详情面板不红。
