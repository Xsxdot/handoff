# B307：开卡不占座；坐下和叫机器人是两颗按钮

- **卡**：B307
- **级别与档位**：**L3 轻档**。席位身份与占用入口跨 `d_ledger`（席位权威）× `d_cli`（三颗按钮）× `d_keystone`（只对「叫机器人」拉起/Resume）× `d_collab`（书写者比较席位）× `d_web`（看板同一套按钮）× `d_gateway`（现网协调者/step HTTP，语义与 CLI 对齐）。不扇出子系统子卡：工作台是同一占用规则的第二个入口，不是另一套产品。
- **状态**：已批准（用户 2026-09-03：「同意」；含 r1：工作台一起改，审查 C2–C4 / I1–I6 按拟文）
- **基线**：`acc/b156.2-156.3`（与 main 同提交）
- **发现自**：合入 B156.2/B156.3 后用户纠偏。B156.3 §5.1「开卡即绑 = 新拉起协调者小队会话」不是 reopen，是产品做错了主语。
- **台账**：`docs/superpowers/specs/b307-ledger.md`
- **修订**：r1 子 agent 审核后回写（用户 2026-09-03：工作台一起改；其余按审查拟文）。

## 问题陈述

谁在什么条件下成为一张卡的协调者，现在是错的。

用户从 grok/claude 这场对话里开卡、聊 spec，人已经坐在桌边。系统却再拉起一个无头会话（今天是 opencode），把「协调者」写成那个机器人：房间书写者门认的是账本 `driver_session`（人尺度 `cli:<user>@<host>`），续接认的是 keystone 内存里的 `SessionRef`，两边还不是同一套身份。结果是：这场对话挂着 `card wait` 也收不到该收的消息；`card coordinate` / `--coordinate` 不写座位；`--step` 认领却会用 `cli:user@host` 去占座。

看板同一套错：新建勾选「开卡即绑」走 `POST .../coordinator/launch` `source=card_create`，抽屉「一键拉起」走 `manual`；两条都不写账本席位，`GET /coordinator` 的 `bound` 看的是 keystone 内存能不能 Locate，不是账本。CLI 若改了占座、HTTP 不改，两个入口会分叉。

正确主语：一张卡一个协调者席位；席位是 **哪个 CLI + 这条会话的 session id**。人要自己坐，按「坐下」；人要机器协调后面的流程，按「叫机器人」。建卡、领卡、聊需求，都不许偷偷安排座位。CLI 与工作台同一套规则。

现状读数（2026-09-03，本工作树；r1 按代码核对改正出处）：

- 小队成员只登记载体名，不存 session id（`internal/scheduling/scheduling.go` `SquadMember`）。
- 无头拉起的续接身份已是 `keysclient.SessionRef{CLI, SessionID, HomeDir, …}`（`internal/keysclient/keysclient.go`）。
- 账本座位 `cards.driver_session` 是另一套字符串：`cli:<user>@<host>`（`cmd/ledgercli.go` `ledgerActor`）；`ClaimCardAs` 会写入它（`internal/ledger/binding.go`）。
- `card add --coordinate` 在 `cmd/card_coordinate.go` 调 `CoordinatorLaunchAs(..., "card_create")`；`handoff card coordinate` 在 `cmd/card.go` 调 `CoordinatorLaunch`（服务端默认 `manual`）。两条都**不写** `driver_session`。
- `POST /api/cards/{id}/coordinator/launch` 只接受 `manual|card_create`，不读账本席位（`internal/agentd/coordapi.go`）。`GET /coordinator` 的 `bound` 是 `keystone.Locate` 成功与否，不是账本席位。
- 看板新建勾选后 `launchCoordinator(id, 'card_create')`（`web/src/app/cards/NewCardDialog.tsx`）；抽屉一键拉起走 `manual`。
- 房间协调者 kind 书写者门：`actor == card.DriverSession`；`relay` 另允许直接父卡席位（`internal/collab/room/room.go` `VerifyWriter`）。`room send` / 裸派发 / `--step` 的 actor 都是 `ledgerActor()`（`cmd/room.go`、`cmd/card_dispatch.go`、`cmd/card_node.go`）。
- `Wake` 在 `sessions[card]` 缺失时直接 `launchRound`（`internal/keystone/keystone.go`）。`sessions` 纯内存。
- 现网 `handoff card rebind --to` 必填任意字符串，`--expect=""` 表示要求空座并占座（`cmd/card_driver.go`）。
- `card wait` 是账本 `Follow`，不按席位过滤（`cmd/card_wait.go`）。房间 `@` 定向是 `Pending(consumer==DriverSession)`（`internal/collab/service.go`）。
- 项目群 / `@B号` 已在（B156.2），跨卡平级沟通不进对方卡房间。

## 方案

**席位** = `(cli, session_id)` + 来源（坐下 / 叫机器人）。一张卡恰好一个席位。权威在账本。有没有协调者、消息投给协调者、谁能以协调者身份写这张卡的房间，**三件事都认这一对**。

席位里的 `cli` 是 hostapi/RunTurn 那套**物种名**（`grok` / `claude` / `opencode` / `codex` / `agy` …），不是小队载体登记名，也不是 `cli:user@host` 的前缀。比较是这一对编码后的字节相等（编码归 contract）。

同一会话可以兼任多张卡（B156.2 绑定席位制原句保留）。禁止把一个会话钉在小队或项目上当全项目唯一协调者。

存量 `driver_session=cli:user@host`（以及任何不是「物种名 + session id」的旧值）视为**非法席位**：有则 `bind` / `coordinate` 拒绝，必须显式 `rebind --self` 或 `--launch` 接班。不得自动升级成新身份。

**三颗按钮，CLI 与工作台同一套，互不代劳：**

| 按钮 | CLI | 工作台 | 桌子空着 | 桌子有人（含非法旧席位） |
|------|-----|--------|----------|--------------------------|
| 我来坐 | `handoff card bind <卡号>` | 卡抽屉「坐下」 | 当前这场对话写成席位（来源=坐下） | 拒绝，提示换绑 |
| 叫机器人 | `handoff card coordinate <卡号>` | 卡抽屉「叫机器人」（取代「一键拉起」） | 从协调者小队挑载体拉起，新会话写成席位（来源=叫机器人） | 拒绝，提示换绑 |
| 换绑 | `handoff card rebind <卡号> --self` 或 `--launch` | 抽屉「换绑：我来接」/「换绑：叫机器人」 | 拒绝（空座请用上面两颗） | `--self` = 当前对话接班（来源=坐下）；`--launch` = 新叫机器人接班（来源=叫机器人） |

换绑必须写明接班者，系统不许猜。本期接班者只有这两种。

用户面**废止** `rebind --to` / `--carrier` / `--expect`。任意 session id 不是隐藏 flag。CAS 留账本内部，不出现在 CLI。空座 `rebind` 失败并指向 `bind` 或 `coordinate`。

坐下 / 换绑给自己：**不查**协调者小队。小队只在叫机器人 / 换绑给机器人时用来挑载体。

**出示身份。** `bind` / `rebind --self` / 有席位时的 `--step` / 以协调者 kind 做 `room send` **共用同一个出示函数**，出示的就是席位那一对。出示失败 → 这些入口失败，文案让人到 grok/claude 里再按。禁止用 `ledgerActor()` 或 `web:<addr>` 冒充席位。

叫机器人写入的那一对，必须能被该机器人进程里后续的 `handoff` **再出示**（做不到则「有席位才能派活」对机器人不成立）。注入方式归 contract；产品要求是：拉起成功后，那场无头会话里跑 `card dispatch --step` / 协调者 kind `room send` 必须通过席位校验。

浏览器页本身不是一场可坐下的对话。工作台「坐下 / 换绑：我来接」在出示不出 agent 会话时不可用（失败文案与普通终端相同）。工作台**能做**的是叫机器人、换绑给机器人、展示账本席位。

`card add` / `card move spec` / 随手 `note` **不占座**。`card add --coordinate` 删掉；仍传该 flag 则命令失败并指向 `card coordinate`。工作台新建对话框去掉「开卡即绑」勾选，建卡后不得调 launch。

聊需求的这场对话默认只是围着桌子说话。卡聊透了之后由用户选：叫机器人负责后面，或让这场对话坐下。

**来源决定叫醒方式（叫醒前先读账本席位来源）：**

- 坐下：不拉起、不隔离 HOME、不 `Resume`、不 `Launch`。`Wake` 对坐下席位是 no-op（不得因内存没有 `sessions[card]` 就 `launchRound`）。送达 = 这场对话已挂的 `card wait`（`Follow` **不**改成按席位过滤）。坐下成功不代替 wait：命令立刻返回；skill 要求坐下后由这场对话挂 wait。
- 叫机器人：沿用 Launch（隔离 HOME）+ `Wake`/`Resume`；`Resume` 失败才重建并落「载体已更换」。重建只对来源=叫机器人的席位。
- 房间 `@` 定向：`Pending`/`Consume` 的 consumer 改为席位身份，`@B号` 仍进该协调者队列。
- 禁止第三种「只投给绑定会话的 wait 进程」。

任何换绑成功后，keystone `sessions[card]` 必须与席位对齐：`--self` **删除**该项（旧机器人不得再被 Resume）；`--launch` 换成新引用。agentd 重启后：来源=叫机器人必须能从账本恢复到可 `Resume` 的引用（含隔离 HOME）；来源=坐下恢复后仍禁止 `Resume`/`Launch`。

**派发不占座、不换座。** 席位空：仍可 `--step`（无人值守还在），但不写入席位。席位有人：当前出示必须等于席位，否则拒绝并提示换绑。`ClaimCard` 不得再用 `cli:user@host` 覆盖席位。运行锁与席位继续分立（B239）。

**房间：** 书写者比较的字符串改成席位身份，矩阵不改——协调者 kind 仅本卡席位；`relay` 仅本卡或一级父；平级进对方卡房间继续拒绝。跨卡平级只走项目群 `@B号`。

**HTTP 与 CLI 同一占用语义，不是旁路。** `source=card_create` 退役：HTTP 不再接受该值；开卡路径不得调 launch。`POST .../coordinator/launch` = 叫机器人（空座才写席位；有人 拒绝）。`GET /coordinator` 的 `bound` = 账本席位是合法的 `(cli, session_id)`，不是 Locate 成功。设置页「拉起通道（开卡即绑 / 一键拉起）」改成与三颗按钮同词。

**人尺度 `takeover` / `release`：** 保留命令，成功路径**不再改席位**。席位非空 → 退出非零，打印当前席位，文案指向 `rebind`。合并或删除这两条命令是 OOS。

**活性心跳** `driver_leases` 本期不接新席位身份（三法零生产消费的现状不在本卡复活）。

## 弃选

- **开卡 / 领卡自动占座**（含 B156.3 §5.1 开卡即绑、看板勾选）：随手记一张以后再做的 bug 不该把当前对话绑上去，也不该叫机器人。
- **CLI 改占座、工作台保持现网 HTTP**：两个入口会分叉；用户裁决工作台一起改。
- **坐下和叫机器人合成一颗按钮**：一按又变成「叫机器人」，回到现在的错。
- **有人坐着时叫机器人直接换人**：换人必须看得见。
- **换绑允许任意 session id / 保留 `--to` 后门**：本期只有两种接班者。
- **坐下要查协调者小队**：人已经在对话里。
- **一个会话钉在小队/项目上当唯一协调者**。
- **平级卡写对方房间**。一级父 `relay` 保留。
- **继续让 `driver_session` 做人尺度、`SessionRef` 做会话尺度**。
- **浏览器页冒充可坐下的对话**（`web:<addr>` 当席位）。
- **Wake 在内存无绑定时当「没有协调者」去 Launch**：坐下只写账本时会叫出第二协调者。

## 用户故事

1. 作为正在聊需求的 grok，我新建或领来一张卡，座位仍空；聊透之后用户说「你来坐下」，这场对话成为协调者，之后的房间消息进这边的 `card wait`。
2. 作为同上，聊透之后用户说「叫一个机器人负责后面」，系统拉起小队里的协调者会话；这场 grok 不再是协调者。机器人里后续 `--step` / 协调者 kind 发言能出示座位，不会被自己的座位拒绝。
3. 作为已经坐下的对话，用户再说「换成机器人」，必须走换绑 `--launch`；直接 `card coordinate` 或看板上点「叫机器人」被拒。
4. 作为另一张卡的协调者，我不能往这张卡房间写协调者 kind；要说话就在项目群 `@` 这张卡。父卡仍可对子卡发 `relay`。
5. 作为普通终端（出示不出 session id），`card bind` / `rebind --self` 失败；空座上 `card coordinate` 仍可叫机器人。工作台「坐下」同样失败。
6. 作为同一场 grok，我可以为两张卡分别坐下（兼任）；我不是全项目唯一协调者。
7. 作为看板用户，新建卡没有开卡即绑勾选；空座点「叫机器人」与 CLI `coordinate` 同效并写账本席位；grok 已坐下时看板显示已绑定，再点「叫机器人」不得再拉一个 opencode。
8. 作为旧账本上的卡（`driver_session=cli:user@host`），`bind`/`coordinate` 被拒，必须显式换绑接班，不会被当成空座或自动升级。

## 契约语义与接缝

跨子系统只定语义：

1. **席位权威在账本。** 身份是物种名 + session id + 来源。keystone 内存 `sessions[card]` 只是「叫机器人」席位的续接缓存。叫醒、Locate、GET `bound` 都先读账本来源，不得只信内存。
2. **占用入口是三颗按钮，CLI 与 HTTP 同语义。** `LaunchForCard` 只为叫机器人 / 换绑给机器人服务。`source=card_create` 退役（含 wire 不再接受）。
3. **collab 书写者门与 `@` 定向读账本席位身份。** 群房间规则不改（仅 `user`、actor 非空）。
4. **调度编制不存会话。** 小队成员仍是载体；叫机器人才解析载体 → Launch。坐下不经过小队。
5. **派发认领不再写入席位。** 有席位则校验出示等于席位；无席位不占座。
6. **出示函数唯一。** CLI 协调者入口与机器人进程内后续 handoff 共用；`ledgerActor()` / `web:<addr>` 不是席位。

签名、列名、JSON 字段、身份注入方式归 contract。

## 测试决定

接缝三条（每条至少一支缝级断言；假缝不占名额）：

1. **账本席位占用导出面**（新占用语义，contract 定名。调用方**仅** CLI `bind`/`coordinate`/`rebind --self|--launch` 与同语义的 HTTP 占用入口）。`ClaimCard` / `TakeoverCard` / `ReleaseCard` 是负例，不得再写席位。  
   - 空座 `bind` 写入当前 `(cli, session_id)` 且来源=坐下；再 `bind` / 再 `coordinate` 拒绝。  
   - 空座 `coordinate` 与 HTTP launch 来源=叫机器人；有人后两者都拒绝。  
   - 有人 `rebind --self` / `--launch` 覆写席位并剥旧会话写权；空座 `rebind` 拒绝。  
   - `card add` 不写席位；`--coordinate` flag 失败；看板新建不调 launch。  
   - `--step` 在有席位且出示不匹配时拒绝且席位不变；无席位时派发成功且席位仍空。  
   - 非法旧席位上 `bind`/`coordinate` 拒绝。  
   - `TakeoverCard`/`ReleaseCard` 不改席位。  
   - HTTP `GET bound` 在仅有 keystone 内存、账本无合法席位时为假；账本有坐下席位、keystone 无 sessions 时为真。

2. **`internal/collab/room/room.go#VerifyWriter`**（调用方 `collab.Service.Send`）以及 `Pending` 的 consumer 比较。  
   - 席位身份能发协调者 kind；另一张卡的席位身份发本卡协调者 kind → `ErrNotWriter`。  
   - 一级父席位仍可发子卡 `relay`；平级不可。  
   - `@B号` 进该卡席位身份的 Pending 队列。

3. **`internal/keystone/keystone.go#Service.LaunchForCard` 与 `Wake`**（调用方 `launchCoordinatorRound` / `wakeCoordinatorRound`）。  
   - `bind` 不调用 `LaunchForCard`。  
   - `coordinate` / `rebind --launch` / 工作台「叫机器人」「换绑：叫机器人」各调用一次 Launch。  
   - 来源=坐下：`Wake` 不 `Resume`、不 `Launch`（含 `sessions[card]` 缺失时）。  
   - 来源=叫机器人：`Wake` 仍 `Resume`（隔离 HOME 沿 B299）。  
   - `rebind --self` 后 `sessions[card]` 不在，后续 Wake 不得 Resume 旧机器人。

变异：把 `add --coordinate` 或看板勾选留着仍拉起 → 缝 1 或 3 红。`Wake` 对坐下席位或内存缺失走 `launchRound` → 缝 3 红。`ClaimCard` 仍用人尺度覆盖席位 → 缝 1 红。HTTP launch 不写席位仍 200 → 缝 1 红。

## 实现决定

- 用户可见文案用「坐下 / 叫机器人 / 换绑」，不要再用「开卡即绑」「一键拉起」。
- 坐下不搬 HOME、不新开进程。叫机器人才走现有隔离 HOME + RunTurn（本期不扩 grok/claude RunTurn）。
- `handoff` / `product-backlog` skill 与本方案对齐：建卡不加 `--coordinate`；占座走三颗按钮；`rebind --to` 的排障句改掉。
- 工作台改现有新建对话框与卡抽屉，不是新路由。形态以本方案按钮表为准；实现后对照现页改按钮与文案，不另起页面。
- 原型基准 `prototypes/base/` 的看板/新建文案在实现后刷新（finish 回流），本节点不 fork 原型——改的是勾选和抽屉按钮语义，不是新页面布局。

## Out of Scope

- **永不做**：开卡或领卡自动占座；把一个会话做成全项目唯一协调者；平级卡写对方卡房间；坐下与叫机器人合成一颗会猜的按钮；浏览器页当协调者席位。
- **本期不做、后续要做**：把 `RunTurn` 接到 grok/claude/codex；换绑任意 session id；把人尺度 `takeover`/`release` 与席位合并或删除；`driver_leases` 活性心跳接新席位身份。

## 备注

对话冻结（2026-09-02/03）：绑当前 agent 会话，不是 `cli:user@host`；身份同一套 CLI+session id；一张卡一个协调者；兼任合法、禁止项目唯一会话；三件事认这一对；平级项目群 `@`、保留一级父 relay；新建/领卡不占座；专门命令绑定；坐下与叫机器人两颗按钮；有人坐着必须显式换绑；换绑暂时两种接班者；坐下不查小队。

r1（2026-09-03）：工作台一起改（用户裁决，覆盖审查 C1「HTTP 保持现网」拟文）。C2–C4、I1–I6 按审查拟文回写。

B156.3 §5.1「开卡即绑」与 `source=card_create` 被本卡废止，不 reopen B156.3。B156.2「派发即认领上席」「人开会话领卡第三通道」被本卡废止为占座入口，不 reopen B156.2。
