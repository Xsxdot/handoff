# B85 loopback 辅助监听 —— 真机烟测记录

- 日期：2026-08-13
- 机器：darwin/arm64，网卡 IP `192.168.0.92`（en0）
- 二进制：`go build -o /tmp/b85/handoff .`（工作区 HEAD `efab110a` 之后）
- 配置：`listen: "192.168.0.92:7877"` / `token: "b85-smoke"` / `datadir: "/tmp/b85/data"`

> 注：plan Task 6 的配置键写的是 `data_dir`，实际 yaml.v3 对无 tag 字段的映射是
> `datadir`（严格解码器按 KnownFields 拒掉 `data_dir`）。烟测用 `datadir` 通过，
> plan 键名有误，本记录按实测为准。

## 断言 1：启动日志含 listen_aux（agentd 追加 loopback 辅助监听）

```bash
/tmp/b85/handoff agentd --config /tmp/b85/config.yaml &
```

实测输出（日志原行）：

```
time=2026-08-13T15:00:42.908+08:00 level=INFO msg="agentd 服务启动" component=agentd addr=192.168.0.92:7877 data_dir=/tmp/b85/data default_executor=opencode proc_fence_disabled=false proc_fence_reserve_ratio=0.1 listen_aux=127.0.0.1:7877
```

断言成立：`listen_aux=127.0.0.1:7877` 出现在启动 Info 中。

## 断言 2：本机 status 走 loopback（确定性改写生效），输出带辅址

```bash
/tmp/b85/handoff status --config /tmp/b85/config.yaml; echo "exit=$?"
```

实测输出（首行与监听行摘录）：

```
agentd   http://127.0.0.1:7877   可用
版本     未知（非 go build 产物）  go1.26.1
本地     本地版本未知（非 go build 产物）
数据     /tmp/b85/data   已运行 0m
监听     192.168.0.92:7877（辅 127.0.0.1:7877）
执行者   claude  codex  fake  grok  opencode(默认)
更新     agentd 非托管启动，换版会被拒绝（--force 也不越过）
         处置 在该机器上 handoff service install
skill    有落点与当前二进制不一致，handoff skill install 重新同步

任务     无
进程     346/2666（本机 uid 已用/上限）
```

- `exit=0`
- 首行 addr 为 `http://127.0.0.1:7877`（cfg.Listen 是 `192.168.0.92:7877`，确定性改写生效）
- 输出含 `监听     192.168.0.92:7877（辅 127.0.0.1:7877）`

断言成立。

## 断言 3：经网卡 IP 可达（远程方向不受影响）

```bash
curl -s -o /dev/null -w "HTTP=%{http_code}\n" http://192.168.0.92:7877/api/status -H "Authorization: Bearer b85-smoke"
```

实测输出：

```
HTTP=200
```

响应体摘录（含 `listen_aux` 外露）：

```json
{"version":{...},"listen":"192.168.0.92:7877","listen_aux":"127.0.0.1:7877","data_dir":"/tmp/b85/data",...}
```

断言成立：网卡 IP 方向照常可达，status JSON 外露 `listen_aux`。

## 断言 4：辅址被占 → 启动失败 fail-fast

顺序（关键：旧 agentd 已确认退出后，才用 nc 占住 loopback 端口，再起第二个实例；
否则撞的是 data_dir 文件锁而非端口，证据无效）：

```bash
pkill -f '/tmp/b85/handoff agentd'; pgrep -fl 'handoff agentd'   # 确认旧实例已死
nc -l 127.0.0.1 7877 &                                            # 占住 loopback 辅助端口
/tmp/b85/handoff agentd --config /tmp/b85/config.yaml
```

实测输出：

```
RC=1
Error: 监听 127.0.0.1:7877: listen tcp 127.0.0.1:7877: bind: address already in use
```

- 进程退出码 1（启动失败）
- 错误报文指明被占地址 `监听 127.0.0.1:7877`

断言成立。收尾：杀掉 nc，`rm -rf /tmp/b85`。
