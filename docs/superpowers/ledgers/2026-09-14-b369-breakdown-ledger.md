# B369 breakdown 台账（移动端 App 拆解提案）

节点：breakdown（上游 contract `docs/superpowers/specs/b369-contract.md` 已冻结 @ `6f10ba7c`；本工作树分支 `cards/B369-charter-4`，有效基线 `cards/B233.1-charter-7` @ `977720e2`；本节点在 `cards/B369-charter-4` 上工作，未切分支）。法定产出：`docs/superpowers/specs/b369-breakdown.md`。

## 一、现状查证（本轮实测）

- 工作树干净起点：`git status` = clean，HEAD = `6f10ba7c`（contract 提交），branch = `cards/B369-charter-4`。
- 图：`codegraph summary` = 5299 节点 / 6618 边 / 26 领域。Ticket 0 新符号在 view `cards-B369-charter` 命中：`Core`（`m_mobilecore_Core`）、`Core.Pair`（`n_mobilecore_Core_Pair`）、`EncodePairBundle`（`n_proto_EncodePairBundle`）、`DecodePairBundle`（`n_proto_DecodePairBundle`）、`newReverseProxy`（`n_mobilecore_newReverseProxy`）。基线下这些符号不在图（须 `--view`）——图覆盖债记入产出物 §1.2。
- 根 `go list ./...` 不含 `/mobile`（实测 0 命中）；`mobile/` 是独立嵌套 module（`mobile/go.mod`，图外，与 `desktop/` 同例）。`mobile/go.mod` **未带** `tool golang.org/x/mobile/cmd/gobind`（grep 无命中）——contract §8.1 欠账。
- `internal/mobilecore` 源文件 2（core.go/proxy.go）+ 测试 1；import 仅 relay/client/proto/stdlib，不 import agentd/collab/ledger（contract §4 条 24 复核通过）。
- `internal/proto/pairing.go` 139 行，wire 完整（版本信封/双形态/拒收/roundtrip 金样本）。`pairing_fixture_test.go` 104 行（4 测试），Go 侧金样本在案。
- CLI：`cmd/console.go` 无 `--qr`/`--bundle`（flag 仅 print-url/device/no-open）；`cmd/root.go#newTargetClient:237` 是组装点；`internal/client/client.go#Client.IssueAuthTicket:1267` 可领 ticket；`internal/agentd/authroutes.go#handleConsole:167` 兑换走 302（`internal/client/client.go#Client.NoRedirect:354` 已有不跟随重定向的副本）。
- web：`App.tsx` 仅 `*`→`Shell`；`Shell.tsx#fullPageRoute:491` = 桌面整页路由集合；无移动专用 IA。`terminalInput.ts:112#installTerminalInputFix` 只处理 WKWebView Option 组合键，无移动键条/IME 组件。`web/src/app/workbench/` 非测试源 `terminal*` = 5 个（`terminalInput/terminalWheel/terminalDebug/terminalHostResponse/terminalOsc52`），命中架构法第三条判据 1——已在产出物 §1.1 显式回答「仍能圈出有界文件集、不插竖切卡」。
- `mobile/bind/bind.go` 导出面 Pair/Origin/MachineNames/Close，包级单例 `core`（`:24`）。
- 壳工程零落盘：`git ls-files mobile/` = bind.go + go.mod + go.sum；全仓无 `.kt`/`.swift`。
- 移动原型 `prototypes/mobile-app/` 十一屏**未入库**（`git log --all -- prototypes/mobile-app` 空；`prototypes/.gitignore` 只保留 base/）——产出物 S6 的形态基准只能引 `prototypes/base/README.md` 的移动端行 + spec 十一屏清单。

## 二、上游状态位核对

- spec 头部「状态:**已批准**」逐字核对通过（`2026-09-13-mobile-app-design.md:4`）。
- contract 头部「上游状态：已批准」「冻结状态：…冻结」「有效基线 cards/B233.1-charter-7 @ 977720e2」核对通过（`b369-contract.md:3,6,7`）。
- `codegraph/target.json`：`d_transport→d_protocol` budget=0、entries=[`proto 实体`,`proto（包级函数）`]（实测 python 读数）；B369 注记在 `target.json:402`。无新方向、无预算变化。

## 三、本节点命令与原始输出（历史读数）

| 命令 | 退出码/结果 |
| --- | --- |
| `git status` | `nothing to commit, working tree clean`；branch `cards/B369-charter-4` |
| `go test ./internal/proto/... ./internal/mobilecore/... -count=1` | `ok github.com/Xsxdot/handoff/internal/proto`；`ok github.com/Xsxdot/handoff/internal/mobilecore` |
| `go build ./...`（根模块） | 退出码 0 |
| `go build ./...`（mobile/ 模块） | 退出码 0 |
| `go list ./...`（根） | 69 行，不含 `/mobile` |
| `go test ./cmd/ -run TestRepoContractGate` | `ok github.com/Xsxdot/handoff/cmd` |
| `codegraph validate --view cards-B369-charter` | `issues=null`、`edgeIssues=null`（EXIT=0） |
| `codegraph check --view cards-B369-charter` | `fails=[]`（fails=0，EXIT=0） |
| `codegraph check`（基线） | `fails=[]`（EXIT=0） |
| `codegraph resolve --doc docs/superpowers/specs/b369-breakdown.md --view cards-B369-charter` | 全部锚 `ok`，EXIT=0（首轮 2 坏锚：`proxy.go#newReverseProxy` file_missing、`config.go#Config.Targets` vanished → 修为 `internal/mobilecore/proxy.go#newReverseProxy` 与 `config.go#Config`/`#Target`，复跑 EXIT=0） |

## 四、试错记录

- `codegraph resolve` 不带 `--view cards-B369-charter` 时，Ticket 0 符号（`Core.Pair` 等）在基线图不存在 → `vanished`。必须带卡视图。已把该事实写进产出物 §1.2 图覆盖债。
- 首轮锚 `proxy.go#newReverseProxy` 少了目录前缀 → `file_missing`；`config.go#Config.Targets`（字段锚）`vanished`，改为类型锚 `config.go#Config`/`#Target` 后 ok。
- 移动原型 fork 未入库，S6 无法给「file#Symbol」形态锚，只引 spec 屏清单与 base README 行——记入产出物 §1.2/§3.7。

## 五、本节点判断与待拍板

- contract §4（32 条）+ §8（10 条欠账）逐条对照：**无退回项**（产出物 §2.3）；三条边界澄清已回写 `b369-contract.md` 末尾修订记录（wire 金样本属契约面、proto 包级函数 entry 覆盖、`mobile/bind` 图外组装点）。
- 待拍板 P1–P6 集中列产出物 §0；行为闭环核对发现「轮换操作文档」无归属，挂 P6 一并裁。
- 未验证条目汇总产出物 §5（七条）。

## 六、提交事实（历史读数）

```
$ git add docs/superpowers/specs/b369-breakdown.md docs/superpowers/ledgers/2026-09-14-b369-breakdown-ledger.md docs/superpowers/specs/b369-contract.md && git commit -m "breakdown(B369): ..."
[cards/B369-charter-4 96a0a20a] breakdown(B369): 移动端 App 拆解提案——六子卡 + P1-P6 待拍板 + 契约无退回
 3 files changed, 445 insertions(+)
 create mode 100644 docs/superpowers/ledgers/2026-09-14-b369-breakdown-ledger.md
 create mode 100644 docs/superpowers/specs/b369-breakdown.md
```

随后按红线只 amend 一次把本台账行收进同批提交——amend 会换 hash（历史读数 96a0a20a 即提交当时读数，不复写 amend 后 HEAD）。

