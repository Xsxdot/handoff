# B466 台账

## 偏离与自主决定

见 spec `b466.md` 同名节（本台账只记过程与原始读数）。

## 排查

1. 被验对象身份（2026-10-22 23:47）：`handoff status --target linux-01` → `版本 bebbb425a98e`；
   本分支起点 `c0a6c8d2`（main，含 bebbb425 合并），permgate 包自 bebbb425 起无改动 → 本地判据 = 线上判据。
2. 取证源：`handoff show 9e598686-8f7b-4363-b381-664a67297f1a --target linux-01`（只有最近 100 事件，权限原文截断）；
   全文取自 linux-01 `/root/.handoff/tasks/9e598686-…/frames.jsonl`（scp 到 scratchpad，10638 行），
   抽出 113 条 `cd ` 开头的 bash tool_call（`input.workdir` 全为空 → Request.Cwd 为空，opencode 不给 cwd）。
3. 红色回路：临时探针测试（不入库）把 113 条原文按 Scope{Workdir:/root/.handoff/worktrees/9e598686,…}
   逐条喂 `Gate.Judge`：`auto_allow 60 / consult 44 / escalate 9`。escalate 9 条：
   - 2507 `cd server && cat > …/service/ai.go <<'EOF' … EOF` + 后继 `gofmt -w system/cs/internal/service/*.go` → `"system/cs/internal/service/*.go"`
   - 2514 `cd server && python3 - <<'EOF'` → `"*p"`；2967 → `"*p.policy"`；2698 `cd server && cat > …/dao/ai.go <<'EOF'` → `"0,"`
   - 3484 / 3495 / 4630 / 9776 `cd server && python3 - <<'EOF' … EOF` + `gofmt -w … system/cs/internal/app/*.go` → `"system/cs/internal/app/*.go"`
   - 8718 `cd admin && cat > test/cs-workbench-draft.test.ts <<'EOF' … EOF` + 后继 `node --test …` → `"{"`
   线上同任务升级 3 条（最近 100 事件内：4630、8718、9776 同形）与本地逐字一致 → 回路确定、秒级、agent 可跑。
4. 根因（读 `cdbase.go` `cdResolvedTargets.resolve` 与 `heredoc.go` `closedHeredoc`）：
   - `cd server` / `cd admin` 本身**可证明**（`proveCDInBases` 按 Workdir 拼接，基准生效）；卡 note 的「cd 落点
     不可静态证明」是**文案误导**：`resolve` 在「基准已生效但落点含 `$\~{},*?['`」时返回同一句拒因。
   - 族 A（`{`、`0,`、`*p`、`*p.policy`）：heredoc 闭集要求终止符后无后继命令；有后继（`node --test`、`gofmt -w`、
     `echo done`）或消费者不是裸 cat（`python3 -`）时正文不剥离，正文被当 shell 文本切段提取重定向/写参数：
     TS 的 `=> {`、Go 的 `*p`、python 的 `x > 0,` 都成了「写落点」；基准生效后这些含花括号/glob/逗号 → 升级。
     无 cd 时同样的垃圾落点按 Workdir 拼接判范围内（不升级）——所以只有 `cd 子目录` 形态被拦。
   - 族 B（`system/cs/internal/app/*.go`）：正文之后真实的 `gofmt -w system/cs/internal/app/*.go`。无 cd 时 glob 落点
     原样交 InScope（按字面拼接 → 范围内）；基准生效后 `*` 被一律判不可解释 → 升级。同一命令有无 `cd server &&` 结论相反。
5. 不属本卡（cd 无关，无 cd 时同样 Consult）：`node --test`（不在白名单）、`go list`、`sed -i`、`git add/commit`、
   `python3 -`（人工清单，设计如此）、cat-heredoc 带后继（闭集不认后继）。

## implement

6. 红（2026-10-22 23:55，`stripDataHeredocBodies` 空壳 `return cmd`）：
   `go test ./internal/permgate -run 'TestB466|TestStripDataHeredocBodies' -count=1` → FAIL：
   python3 正文+gofmt glob 后继 `escalate … "system/cs/internal/app/*.go"`；cat 正文+node --test `escalate … "{"`；
   `cd server && gofmt -w system/cs/internal/app/*.go` `escalate`；`cd server && gofmt -w …/ai.go` `consult（黑名单未命中）`；
   剥离单元 4 条正例断言红。反例 `TestB466CounterexamplesStillCaught` 修前即绿（守边界）。
7. 新增排查发现：`compoundSilent` 的 B376 守卫（cd 后相对参数位写一律不静默）早于 B383 cd 基准解析，造成
   `cd server && gofmt -w x.go` Consult、`gofmt -w x.go` AutoAllow——放宽为 cd 链不可信才拒（spec 偏离第 4 条）。
8. 实现后 `go test ./internal/permgate -count=1` 两条既有用例红：
   `TestJudgeHeredocUnmatchedKeepsStatusQuo/cat_无重定向正文藏越界写`（期望 escalate 得 consult）、
   `TestJudgeCompoundNotSilent`（`cd src && gofmt -w x.go` 被静默）——均为旧误判的固化，改期望（spec 偏离第 4、5 条），
   随后全包 `ok github.com/Xsxdot/handoff/internal/permgate 0.624s`。
9. E40.3 113 条回放（探针同第 3 条，未入库）：`auto_allow 60 / consult 53 / escalate 0`；逐 seq 对比修前：
   变化只有 9 条 escalate→consult（8 条人工清单 python、8718 黑名单未命中），其余 104 条出口不变，AutoAllow 集合不变。
10. 全仓编译：`go build ./... && go vet ./...` + `gofmt -l` 空，EXIT=0。
11. 提交：`git commit`（implement）→ `ab2e9532 fix(B466): 落点解析剥离数据型 heredoc 正文、cd 基准下 glob 写落点可证明、cd 链可信时 cd 后参数位写可静默`
