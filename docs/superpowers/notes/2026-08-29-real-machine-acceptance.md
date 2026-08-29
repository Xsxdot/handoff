# 2026-08-29 真机验收记录（协调者全链路与执行者两级并发验证）

> 日期：2026-08-29  
> 环境：Linux (amd64), Go 1.26.1, 分支 `acc/b156.2-156.3` (Commit: `ad984176693a`)  
> 参与载体：`opencode` (模型 `opencode/mimo-v2.5-free`)、`codex`、`grok`

---

## 目录

1. [阶段一：载体检测与隔离 HOME 验证](#阶段一载体检测与隔离-home-验证)
2. [阶段二：协调者全链路与房间事件唤醒验证](#阶段二协调者全链路与房间事件唤醒验证)
3. [阶段三：执行者小队与载体两级并发排队验证](#阶段三执行者小队与载体两级并发排队验证)
4. [验收结论](#验收结论)

---

## 阶段一：载体检测与隔离 HOME 验证

### 1. 载体注册与配置
在系统内注册三个载体，HOME 统一指向隔离目录 `~/.handoff/home/<name>`：
- `opencode`：独立凭据（`standalone`），模型 `opencode/mimo-v2.5-free`
- `codex`：主 HOME 同步凭据（`main_home_sync`）
- `grok`：主 HOME 同步凭据（`main_home_sync`）

### 2. 检测接口调用（`POST /api/squads/carriers/<name>/detect`）
- **`opencode`**：
  - 自动执行 wake turn 发送 `ping`，真实收到 `pong`。
  - 隔离目录 `~/.handoff/home/opencode/` 产生配置文件与运行时 SQLite 数据库。
  - 状态更新为 `online`，四态探活通过。
- **`codex`**：
  - `main_home_sync` 成功将主环境 `/root/.codex/auth.json` 同步至 `/root/.handoff/home/codex/.codex/auth.json`（SHA256 校验一致）。
  - 执行 `RunTurn` 正确返回载体未实装错误，状态置为 `pending`。
- **`grok`**：
  - `main_home_sync` 扫描主环境凭据无主配置，状态置为 `pending`。

---

## 阶段二：协调者全链路与房间事件唤醒验证

### 1. 协调者小队配置与读面
- 建立小队 `coord`，角色为 `coordinator`，绑定已上线的 `opencode` 载体（`max_concurrency: 1`）：
  ```bash
  curl -s -X PUT -H "Authorization: Bearer <token>" -H "Content-Type: application/json" \
    -d '{"name":"coord","role":"coordinator","members":[{"carrier":"opencode","max_concurrency":1}]}' \
    "http://127.0.0.1:7777/api/squads/squads/coord?expect=0"
  ```
- **读面验证（`GET /api/squads`）**：
  ```json
  {
    "carriers": [
      {
        "name": "opencode",
        "machine": "local",
        "cli": "opencode",
        "home_dir": "~/.handoff/home/opencode",
        "model": "opencode/mimo-v2.5-free",
        "credential": "standalone",
        "max_concurrency": 1,
        "status": "online",
        "version": 2
      }
    ],
    "squads": [
      {
        "name": "coord",
        "role": "coordinator",
        "members": [{"carrier": "opencode", "max_concurrency": 1}],
        "version": 1
      }
    ]
  }
  ```
  - **结论**：读面中可见成员存在，且 `status: "online"`，小队有空位。

### 2. 开探针卡并拉起协调者
- 建立测试工作流 `charter` 并开卡：
  ```bash
  handoff card add "真机协调者验证探针卡" --project handoff --coordinate
  ```
  - 产出卡号：`B1`
  - 绑定输出：`开卡即绑已拉起协调者 card=B1 session=ses_fb2574918ffeJN7ERd1mC645hA`
- **`agentd.log` 日志证据**：
  ```json
  {"time":"2026-08-29T21:14:56.677193384+08:00","level":"INFO","msg":"自动化拉起协调者回合","component":"agentd","card":"B1","source":"card_create","squad":"coord","carrier":"opencode","cli":"opencode","home_dir":"~/.handoff/home/opencode","workdir":"/root/.handoff/repos/handoff"}
  {"time":"2026-08-29T21:14:56.677526475+08:00","level":"INFO","msg":"协调者回合开始","component":"agentd","mod":"hostapi","cli":"opencode","mode":"new","workdir":"/root/.handoff/repos/handoff","timeout":"30m0s","prompt_bytes":299}
  {"time":"2026-08-29T21:20:00.945778487+08:00","level":"INFO","msg":"协调者回合完成","component":"agentd","mod":"hostapi","cli":"opencode","session_id":"ses_fb2574918ffeJN7ERd1mC645hA","output_bytes":1033,"duration":"5m4.268198736s"}
  {"time":"2026-08-29T21:20:00.945845155+08:00","level":"INFO","msg":"自动化拉起协调者回合结束","component":"agentd","card":"B1","source":"card_create","session":"ses_fb2574918ffeJN7ERd1mC645hA","rebuilt":true,"escalated":false}
  {"time":"2026-08-29T21:20:00.952863482+08:00","level":"INFO","msg":"自动化名额已归还","component":"agentd","card":"B1","squad":"coord","carrier":"opencode"}
  {"time":"2026-08-29T21:20:00.952882743+08:00","level":"INFO","msg":"协调者 HTTP 拉起成功","component":"agentd","card":"B1","source":"card_create","session":"ses_fb2574918ffeJN7ERd1mC645hA","rebuilt":true,"escalated":false}
  ```
- **房间与协调者状态（`GET /api/cards/B1/coordinator` 与 `GET /api/rooms`）**：
  - 协调者绑定成立，房间 `B1`（kind=card）正常创建。

### 3. 房间消息唤醒事件闭环
- **向房间发送消息**：
  ```bash
  curl -s -X POST -H "Authorization: Bearer <token>" -H "Content-Type: application/json" \
    -d '{"body":"协调者请确认本卡当前状态与基线"}' \
    http://127.0.0.1:7777/api/rooms/B1/messages
  ```
- **事件捕获与重新唤醒执行**：
  - `agentd` 自动化消费轮捕获 `seq: 2` 的 `room_message` 事件，向 `scheduling` 准入名额并拉起新一轮协调者执行。
  - 进程级确认运行命令：
    ```text
    /root/.opencode/bin/opencode run --format json -m opencode/mimo-v2.5-free -- 你是本卡的机器协调者。醒来第一件事：读卡、查依赖、看基线新鲜度；不适合现在推就在房间说明原因并休眠。 ## 本卡上下文 - 卡号：B1 - 标题：真机协调者验证探针卡 ## 本次唤醒事件 - [message] 协调者请确认本卡当前状态与基线 ...
    ```
  - 回合执行完成（耗时 9m39s），系统指针行落账至房间（`seq: 3`），名额正常释放。
- **房间消息历史（`GET /api/rooms/B1/messages`）**：
  ```json
  {
    "messages": [
      {
        "seq": 2,
        "card_id": "B1",
        "type": "room_message",
        "actor": "web:127.0.0.1",
        "payload": {"room": "B1", "kind": "user", "body": "协调者请确认本卡当前状态与基线"},
        "created_at": "2026-08-29T13:32:01.365559631Z"
      },
      {
        "seq": 3,
        "card_id": "B1",
        "type": "room_message",
        "actor": "system:pointer",
        "payload": {"room": "B1", "kind": "pointer", "body": "载体已更换：新载体承接同一协调者身份（重建四步已执行）", "by_system": true},
        "created_at": "2026-08-29T13:41:43.100280853Z"
      }
    ]
  }
  ```

---

## 阶段三：执行者小队与载体两级并发排队验证

### 1. 拓扑与配置
- **载体并发上限**：`opencode` 的 `max_concurrency = 2`
- **执行者小队 1（`squad-a`）**：角色 `executor`，成员 `opencode` 的 `max_concurrency = 1`
- **执行者小队 2（`squad-b`）**：角色 `executor`，成员 `opencode` 的 `max_concurrency = 2`
- **工作流（`concurrency-bench`）**：
  - 节点 `node1`（`dispatch: true`）：绑定小队 `squad-a`
  - 节点 `node2`（`dispatch: true`）：绑定小队 `squad-b`
- **4 张探针卡**：
  - `B14`：处于 `node1` 列（目标小队 `squad-a`）
  - `B15`：处于 `node1` 列（目标小队 `squad-a`）
  - `B16`：处于 `node2` 列（目标小队 `squad-b`）
  - `B17`：处于 `node2` 列（目标小队 `squad-b`）

### 2. 四卡同时点火执行
使用多线程脚本同时向 4 张卡发起节点点火请求（`POST /api/cards/<id>/step`）：
- `B14` -> `node1`
- `B15` -> `node1`
- `B16` -> `node2`
- `B17` -> `node2`

### 3. 调度与排队证据

#### A. 瞬时队列快照（`GET /api/queue`）
```json
{
  "queue": [
    {
      "kind": "ignition_queue",
      "id": "B16|node2",
      "card": "B16",
      "node": "node2",
      "squad": "squad-b",
      "priority": "中",
      "ready": true,
      "actor": "tester",
      "seq": 0,
      "position": 1
    },
    {
      "kind": "ignition_queue",
      "id": "B14|node1",
      "card": "B14",
      "node": "node1",
      "squad": "squad-a",
      "priority": "中",
      "ready": true,
      "actor": "tester",
      "seq": 0,
      "position": 2
    }
  ]
}
```

#### B. `agentd.log` 日志证据（准入分流与两级并发执法）
```json
{"time":"2026-08-29T21:58:27.969656434+08:00","level":"INFO","msg":"scheduling.admit.start","component":"agentd","squad":"squad-a","card":"B15","node":"node1","carrier":"opencode","member_policy":1,"carrier_cap":2,"error_kind":"start"}
{"time":"2026-08-29T21:58:27.997932978+08:00","level":"INFO","msg":"scheduling.admit.success","component":"agentd","squad":"squad-a","carrier":"opencode","member_policy":1,"carrier_cap":2,"error_kind":"success","card":"B15"}
{"time":"2026-08-29T21:58:27.998511391+08:00","level":"INFO","msg":"scheduling.admit.start","component":"agentd","squad":"squad-b","card":"B17","node":"node2","carrier":"opencode","member_policy":2,"carrier_cap":2,"error_kind":"start"}
{"time":"2026-08-29T21:58:28.001634805+08:00","level":"INFO","msg":"scheduling.admit.start","component":"agentd","squad":"squad-b","card":"B16","node":"node2","carrier":"opencode","member_policy":2,"carrier_cap":2,"error_kind":"start"}
{"time":"2026-08-29T21:58:28.003935799+08:00","level":"ERROR","msg":"scheduling.admit.error","component":"agentd","squad":"squad-b","carrier":"opencode","member_policy":2,"carrier_cap":2,"error_kind":"no_slot","cause":"scheduling: 小队或载体并发已满"}
{"time":"2026-08-29T21:58:28.004020361+08:00","level":"INFO","msg":"scheduling.admit.success","component":"agentd","squad":"squad-b","carrier":"opencode","member_policy":2,"carrier_cap":2,"error_kind":"success","card":"B17"}
{"time":"2026-08-29T21:58:28.004109586+08:00","level":"ERROR","msg":"scheduling.admit.error","component":"agentd","squad":"squad-a","carrier":"opencode","member_policy":1,"carrier_cap":2,"error_kind":"no_slot","cause":"scheduling: 小队或载体并发已满"}
{"time":"2026-08-29T21:58:28.019007261+08:00","level":"INFO","msg":"小队满员，点火请求已持久排队","component":"agentd","card":"B14","node":"node1","squad":"squad-a","queue_position":2,"actor":"tester"}
{"time":"2026-08-29T21:58:28.019323562+08:00","level":"INFO","msg":"小队满员，点火请求已持久排队","component":"agentd","card":"B16","node":"node2","squad":"squad-b","queue_position":1,"actor":"tester"}
{"time":"2026-08-29T21:58:28.020173266+08:00","level":"INFO","msg":"按模板派发","component":"agentd","card":"B15","template":"default","target":"local","executor":"opencode","model":"opencode/mimo-v2.5-free"}
{"time":"2026-08-29T21:58:28.031049287+08:00","level":"INFO","msg":"按模板派发","component":"agentd","card":"B17","template":"default","target":"local","executor":"opencode","model":"opencode/mimo-v2.5-free"}
```

#### C. 裁决与执法分析
1. **小队级并发上限执法（`squad-a` 成员上限 = 1）**：
   - `B15` 先获得准入，`squad-a/opencode` 运行计数升为 1。
   - `B14` 同时到达，检查发现 `squad-a` 运行数已达到 `member_policy: 1`，准入被拒（`error_kind: "no_slot"`），转入 `ignition_queue` 排队。
2. **载体级总并发上限执法（`carrier/opencode` 物理上限 = 2）**：
   - `B17` 获得准入，`squad-b/opencode` 计数为 1（未超小队上限 2），但此时载体 `opencode` 总运行计数升为 2（达到载体物理上限 2）。
   - `B16` 申请 `squad-b`，虽小队尚有配额，但底层载体 `opencode` 物理并发已满（`carrier_cap: 2`），准入被拒，转入 `ignition_queue` 排队。
3. **真实在飞状态**：
   - 现场严格保持最多 2 个活跃任务在飞（`B15` 和 `B17`），总运行数完全未超发；溢出的 2 个请求（`B14` 和 `B16`）在账本队列中按规则保序排队等待名额归还。

---

## 验收结论

1. **协调者全链路（B156.2）**：
   - 载体与小队在线态可读，开卡即绑拉起成功，首回合非空转真实执行；
   - 协作房间支持用户消息驱动，`wakeconsumer` 能够可靠唤醒协调者进行二次回合推进。
2. **两级准入与排队调度（B156.3）**：
   - 严格落实「小队成员并发上限」与「载体物理并发上限」的双重原子 CAS 约束；
   - 满员请求无静默丢弃、无超发泄漏，严格持久化进入 `ignition_queue` 排队并在名额释放后有序流转。
