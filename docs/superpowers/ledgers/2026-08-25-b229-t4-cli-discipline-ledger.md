# B229 T4 · CLI discipline 命令族落地台账

- 2026-08-25：开工核对 `git status --short --branch && git log --oneline -3`；原始输出：`## cards/B229.4-charter`（工作树干净）、HEAD `e24b9a31 docs(b229): breakdown …` ← `f848f0e6 chore(ledger) …` ← `97dcaf96 contract(b229): freeze …`。判断：本卡分支已含 Ticket 0 骨架与拆解稿，基线正确。
- 2026-08-25：实读契约 §2.8 与拆解稿 T4 卡面：命令族 put/get/list、形态照 template 族（template.go:83 必填校验风格）、put 走 openLedger()+PutDiscipline、get 打印正文与版本号；精确 flag 表归 plan 节点——仓内无 b229 plan 文档（grep 零命中），按契约 §2.8 字面命令形实现：`put <name> <file>` 位置参数、`get <name> [--version N]`。
- 2026-08-25：实读 internal/ledger/disciplines.go 全文：PutDiscipline(name,body)(int,error)/GetDiscipline(name,version)(Discipline,error)/ListDisciplineNames()([]string,error)、名字校验（空/./.. /filepath.Separator/'/'）、正文空与超 64KiB 校验全在库层——CLI 不复制规则，只透传错误。
- 2026-08-25：实读 cmd/ledgercli.go openLedger()：dsn 空回退 DataDir/ledger.db；Enabled=false 拒绝（该分支退休属 T5，本卡不动）。
- 2026-08-25：实读 cmd/ledgercli_test.go runLedgerCLI 测试基座：临时 dir 写 config（Ledger.Enabled=true、无 dsn），进程内 Execute()——本卡测试穿真实 SQLite 临时库即由此达成；main.go 核实 Execute 出错走 os.Exit(cmd.ExitCode(err))，通用错误=1（exit.go），故进程内断言 err!=nil 即等价于退出非零。
- 2026-08-25：先写测试 cmd/discipline_test.go（三支：TestDisciplinePutGetRoundtrip / TestDisciplineListAscendingDedup / TestDisciplinePutRejectsBadInput，名字全部命中验收正则 TestDiscipline(Put|Get|List)）；首红原始输出：三支全 FAIL，原因均为 `unknown command "discipline" for "handoff"`——功能缺失红非 typo 红。
- 2026-08-25：实现 cmd/discipline.go（list/get/put 三子命令，形态照 template 族；put `<name> <file>` 位置参数照契约 §2.8 字面，get 带 --version flag，校验零复制全透传库层）。绿灯：`go test ./cmd -run 'TestDiscipline(Put|Get|List)' -count=1` 原始输出 `ok github.com/Xsxdot/handoff/cmd 0.247s`，-v 下三支全 PASS（穿真实 SQLite 临时库 /tmp/TestDiscipline*/ledger.db）。
- 2026-08-25：变异自验两发，均先核锚点唯一（`grep -c` 各得 1）→ 编译过 → 行为断言红 → 回复。M1 `ORDER BY name`→`… DESC`：仅 TestDisciplineListAscendingDedup 红（`--- FAIL: TestDisciplineListAscendingDedup (0.06s)`）。M2 GetDiscipline 的 `if version > 0 {`→`if false && version > 0 {`：仅 TestDisciplinePutGetRoundtrip 红（历史版断言拦住）。两发证明测试有牙，变异后均已 `git checkout` 还原并重编译。
- 2026-08-25：收尾全量检查原始输出：`go build ./...` → `BUILD_OK`；`go vet ./cmd/` → `VET_OK`；`gofmt -l cmd/` → 无输出；`go test ./cmd -count=1` 全包 → `ok github.com/Xsxdot/handoff/cmd 7.052s`。
- 2026-08-25：范围声明——本卡只动 cmd/discipline.go（新）、cmd/discipline_test.go（新）、本台账；openLedger 的 Enabled 分支退休属 T5、P2 七连 put 属 T6，均未越界触碰。


