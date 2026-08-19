# 执行纪律（先读这段，再读 plan）

你收到的是一份完整实现计划。用你自己的 subagent 机制按以下纪律执行，不要单上下文从头写到尾：

1. 逐 task 派全新 subagent 实现。每个 subagent 只给三样东西：该 task 的完整需求原文（含精确值、签名、测试用例）、它要接触的接口、全局约束。不要把会话历史或前序 task 总结灌进去。
2. 实现 subagent 不并行（避免改动冲突）。
3. 每个 task 完成后，派一个独立审查 subagent 做双裁决：spec 符合性（要求全实现、没有多做）+ 代码质量。输入是该 task 的需求原文 + 完整 diff。缺任一裁决不算过。
4. 审查不过进修复回路：一轮 = 一次修复 + 一次只看修复 diff 的复审，最多 5 轮。前 3 轮回原实现者，4-5 轮换全新实现者接手。5 轮后仍有未决项：非承重的记账搁置；承重的（后续 task 依赖它、或暴露 plan 缺陷）停下上报 BLOCKED。
5. 进度落盘到 ledger 文件：每 task 完成、每轮修复各追加一行，含 commit 范围。恢复现场以 ledger + git log 为准，不信记忆。
6. Minor 发现记账不进回路，留给终审统一 triage。
7. 全部 task 完成后做一次整分支终审（相对分支起点的完整 diff）。有发现项就一次性派一个修复 subagent 全量修，再做一次范围复审；不搞逐项派发，也没有第二轮修复波。
8. 协调上下文保持干净：你自己不亲自改代码，所有改动经 subagent 产出且经审查。
9. 每个 task 完成即 commit，提交信息说清做了什么。
10. 不停下来问「要不要继续」。只在 BLOCKED、真歧义、全部完成三种情况停；需求取舍拿不准就发工单问，等审核者裁决。

---

# B103 出生名册盲区修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让「executor 的 Bash 工具用 `&` 留下的、换了进程组的长命后代」能被任务名下的进程点名数到，并在任务归档/中止时被清掉。

**Architecture:** 两处改动。①出生名册（`internal/prochost`）从「整份覆盖」改成「仍存活的旧条目 ∪ 本轮 ppid 闭包」，采样间隔 15s→1s，内容未变则不落盘。②`Manager.Done` 与 `Manager.Stop` 在停完 executor 之后各补一次残留清扫，并对 `ErrExecutorAlive` 竞态做有界重试。

**Tech Stack:** Go 1.26，标准库。既有测试缝：`enumProcsFn`（进程枚举）、`rosterInterval`（采样间隔）、`Manager.sweepProcs`（清扫）。

**Spec:** [docs/superpowers/specs/2026-08-15-b103-proc-roster-blindspot-design.md](../specs/2026-08-15-b103-proc-roster-blindspot-design.md)
**排查报告（根因原文与实测数据）:** [docs/superpowers/notes/2026-08-15-b103-proc-roster-blindspot.md](../notes/2026-08-15-b103-proc-roster-blindspot.md)

## Global Constraints

- **不改 `classify` 的 pgid 判据**（`internal/prochost/footprint.go:115-117`），不改 `rosterMembers`，不改 `Kill`/`Sweep` 的判定逻辑。
- **不做 pgid 增强**：不要给名册加「见过的后代 pgid」并据此扩大清扫面。spec §3.2 写明了理由（pgid 会被复用，那是新的误杀面）。想做也不做。
- **绝不**用「`ppid==1` 且启动时刻 ≥ shim」去扫描认领进程——那是 B47 误杀 114 次的判据。
- `rosterEntry` 的 JSON 结构保持 `{"pid":..,"started_at":..}` **不变**，名册文件新旧互读。
- 进程枚举实现**一律不得 fork**（不得调 `ps`/`lsof`）——`internal/prochost/procenum.go:9-11` 的既有约束，本次不得破坏。
- 日志一律用既有的 `*slog.Logger`（`prochost` 内是 `log()` 或传入的 `l`，agentd 内是 `m.log`）。**禁止 `fmt.Printf`**。
- 名册相关的周期日志一律 **Debug** 级；只有异常（耗时超标、写失败、清扫放弃）才升 Warn/Error——它每秒一次，Info 会把任务日志刷满。
- 新建文件写文件头注释（职责 + 边界）；新增导出函数写 doc 注释（参数、返回、注意事项）；非显然分支写「为什么」的中文注释。
- **不合并进 `main`**，不 `git push` 到 `main`/`w4-delivery`，不动任何 tag。只交分支。
- **不动 `~/.handoff`** 目录下的任何文件（那是这台机器正在服役的 agentd 数据目录）。
- 不顺手修 B100/B101/B104；发现的新问题写进 ledger，留给终审。
- 提交信息前缀 `fix(b103):`。

---

### Task 1: 名册合并的纯函数

**Files:**
- Modify: `internal/prochost/roster.go`（在 `descendantsOf` 之后新增）
- Test: `internal/prochost/roster_test.go`

**Interfaces:**
- Consumes: 既有 `rosterEntry{PID int; StartedAt int64}`、`procEntry{PID, PPID, PGID int; StartedAt int64}`
- Produces: `func mergeRoster(prev, cur []rosterEntry, procs []procEntry) []rosterEntry`，`func marshalRoster(entries []rosterEntry) ([]byte, error)`，`func writeRosterBytes(path string, b []byte) error`

- [ ] **Step 1: 写失败的测试**

在 `internal/prochost/roster_test.go` 追加：

```go
func TestMergeRosterKeepsEscapedDescendant(t *testing.T) {
	// 第一轮：工具壳 200 与它的子进程 300 都能由 ppid 闭包到
	prev := []rosterEntry{{PID: 200, StartedAt: 1000}, {PID: 300, StartedAt: 1100}}
	// 第二轮：工具壳已退出，300 被 reparent 给 launchd（ppid=1），闭包走不到它
	procs := []procEntry{
		{PID: 100, PPID: 1, PGID: 100, StartedAt: 900},   // shim
		{PID: 300, PPID: 1, PGID: 200, StartedAt: 1100},  // 逃逸后代，仍活着
	}
	cur := descendantsOf(100, procs) // 本轮闭包为空
	got := mergeRoster(prev, cur, procs)
	if len(got) != 1 || got[0].PID != 300 || got[0].StartedAt != 1100 {
		t.Fatalf("逃逸后代必须留在名册里，got=%v", got)
	}
}

func TestMergeRosterDropsDeadAndReusedPID(t *testing.T) {
	prev := []rosterEntry{
		{PID: 300, StartedAt: 1100}, // 已从进程表消失
		{PID: 400, StartedAt: 1200}, // pid 还在，但 StartedAt 变了 = pid 被复用
	}
	procs := []procEntry{
		{PID: 100, PPID: 1, PGID: 100, StartedAt: 900},
		{PID: 400, PPID: 1, PGID: 400, StartedAt: 9999},
	}
	got := mergeRoster(prev, nil, procs)
	if len(got) != 0 {
		t.Fatalf("已死与 pid 复用的条目都必须剪掉，got=%v", got)
	}
}

func TestMergeRosterUnionsAndSortsByPID(t *testing.T) {
	prev := []rosterEntry{{PID: 500, StartedAt: 1000}}
	cur := []rosterEntry{{PID: 300, StartedAt: 1100}, {PID: 500, StartedAt: 1000}}
	procs := []procEntry{
		{PID: 300, PPID: 100, PGID: 100, StartedAt: 1100},
		{PID: 500, PPID: 100, PGID: 100, StartedAt: 1000},
	}
	got := mergeRoster(prev, cur, procs)
	if len(got) != 2 || got[0].PID != 300 || got[1].PID != 500 {
		t.Fatalf("并集须去重且按 pid 升序，got=%v", got)
	}
}

func TestMarshalRosterStableForSameSet(t *testing.T) {
	a, err := marshalRoster([]rosterEntry{{PID: 2, StartedAt: 20}, {PID: 1, StartedAt: 10}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := marshalRoster([]rosterEntry{{PID: 2, StartedAt: 20}, {PID: 1, StartedAt: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("同一批条目的序列化结果必须逐字节一致：%s vs %s", a, b)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd internal/prochost && go test -run 'TestMergeRoster|TestMarshalRoster' ./...`
Expected: FAIL，`undefined: mergeRoster` / `undefined: marshalRoster`

- [ ] **Step 3: 实现三个函数**

在 `internal/prochost/roster.go` 的 `descendantsOf` 之后加入（`import` 需补 `sort`、`bytes` 不需要）：

```go
// mergeRoster 把上一轮名册与本轮 ppid 闭包合并成新名册。
//
// 参数：
//   - prev: 上一轮落盘的名册（readRoster 的结果，可为 nil）
//   - cur: 本轮 descendantsOf 的结果（可为 nil）
//   - procs: 本轮进程快照，用于判断 prev 里哪些条目还活着
//
// 返回：按 pid 升序、去重后的新名册；两边都空时返回空切片（非 nil，便于序列化成 []）
//
// 为什么是并集而不是整份覆盖（这就是 B103 的全部）：executor 的 Bash 工具会把
// 每条命令 setsid 成新会话（grok 1.0.3 / opencode 都是），命令里 `cmd &` 留下的
// 后代继承的是**工具壳的 pgid**；工具壳往往只活约 1 秒就退出，后代随即被 reparent
// 给 init/launchd——ppid 链当场断。整份覆盖会在下一轮把这些**仍然活着**的长命
// 后代当成「早退的短命进程」抹掉，于是既数不到也清不掉（08-15 实测：450 个
// `sleep 900` 全部漏记，任务那一行只报 3 个进程）。
//
// 删除条件只有两条：pid 已不在进程表，或 StartedAt 对不上（pid 被内核复用）。
// **不能**用「本轮从 shim 走不到」当删除条件——那正是这个 bug 本身。
// StartedAt 判据沿用宁漏勿错语义（见 rosterEntry 的注释，B47 教训）。
func mergeRoster(prev, cur []rosterEntry, procs []procEntry) []rosterEntry {
	live := make(map[int]int64, len(procs))
	for _, p := range procs {
		live[p.PID] = p.StartedAt
	}
	seen := make(map[int]bool, len(prev)+len(cur))
	out := make([]rosterEntry, 0, len(prev)+len(cur))
	keep := func(e rosterEntry) {
		if seen[e.PID] {
			return
		}
		if started, ok := live[e.PID]; !ok || started != e.StartedAt {
			return // 已死，或 pid 被复用成了别的进程
		}
		seen[e.PID] = true
		out = append(out, e)
	}
	for _, e := range prev {
		keep(e)
	}
	for _, e := range cur {
		keep(e)
	}
	// 排序不是为了好看：writeRoster 的调用方要用「序列化结果是否变化」来决定
	// 这一轮要不要落盘，而进程快照的顺序在两次采样之间并不保证稳定。
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// marshalRoster 序列化名册，供「内容未变则不落盘」的比对使用。
//
// 参数：entries 必须已按 pid 升序（mergeRoster 保证）
//
// 返回：JSON 字节；序列化失败时返回错误
func marshalRoster(entries []rosterEntry) ([]byte, error) {
	b, err := json.Marshal(entries)
	if err != nil {
		return nil, fmt.Errorf("序列化名册: %w", err)
	}
	return b, nil
}

// writeRosterBytes 把已序列化的名册原子写到 path（临时文件 + rename）。
//
// 参数：
//   - path: 名册路径（rosterPath 的结果）
//   - b: marshalRoster 的结果
//
// 返回：临时文件写失败或 rename 失败时返回错误
//
// 原子性的理由见 writeRoster 的注释——本函数是它的字节版，两者共用同一套语义。
func writeRosterBytes(path string, b []byte) error {
	if path == "" {
		return fmt.Errorf("名册路径为空")
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("写名册临时文件 %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp) // 尽力而为：留着它下次会被覆盖，删不掉也不影响正确性
		return fmt.Errorf("落盘名册 %s: %w", path, err)
	}
	return nil
}
```

同时把既有的 `writeRoster` 改成复用它，**保持签名与行为不变**（它的既有用例必须继续通过）：

```go
func writeRoster(path string, entries []rosterEntry) error {
	if path == "" {
		return fmt.Errorf("名册路径为空")
	}
	b, err := marshalRoster(entries)
	if err != nil {
		return err
	}
	return writeRosterBytes(path, b)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd internal/prochost && go test -count=1 ./...`
Expected: PASS（含既有的 `writeRoster`/`readRoster`/`descendantsOf` 用例）

- [ ] **Step 5: 更新文件头注释**

`roster.go` 文件头「边界」一节里这一条**现在是错的**，必须改：

```
//   - 不做增量维护：每次快照都是全量重算。最后一次快照 ≈ executor 死亡时刻的
//     存活者，早退的短命进程自然不在里面，无需追踪它们的死亡
```

改成：

```
//   - 名册是**累积**的：每轮把「上一轮里仍存活的条目」与「本轮 ppid 闭包」取并集，
//     只在进程消失或 pid 被复用时删除条目（B103）。不能整份覆盖——Bash 工具 setsid
//     出来的后代在工具壳退出后 ppid 链就断了，覆盖会把仍活着的它们抹掉
//   - 仍有残余盲区：活得比采样间隔（rosterInterval）还短、且存活窗口内一次都没被
//     采到的工具壳，它的后代仍然漏记。1s 采样把窗口收小，不等于消除
```

- [ ] **Step 6: Commit**

```bash
git add internal/prochost/roster.go internal/prochost/roster_test.go
git commit -m "fix(b103): 名册合并改并集语义，只按进程消失/pid 复用删条目"
```

---

### Task 2: `snapshotRoster` 接上并集，间隔压到 1s，内容未变则不落盘

**Files:**
- Modify: `internal/prochost/shim.go:44-52`（`rosterInterval`）、`shim.go:276-294`（`snapshotRoster`）
- Test: `internal/prochost/shim_test.go`

**Interfaces:**
- Consumes: Task 1 的 `mergeRoster` / `marshalRoster` / `writeRosterBytes`，既有 `readRoster`、`rosterPath`、`enumProcsFn`
- Produces: `type rosterSampler struct` 及其方法 `func (s *rosterSampler) sample(l *slog.Logger)`；`snapshotRoster` 保留为一次性入口（内部用 `rosterSampler`）

- [ ] **Step 1: 写失败的测试**

在 `internal/prochost/shim_test.go` 追加（`enumProcsFn` 是既有的包级测试缝，用法照抄同文件里已有的用例）：

```go
func TestRosterSamplerKeepsEscapedDescendantAcrossRounds(t *testing.T) {
	dir := t.TempDir()
	info := filepath.Join(dir, "proc.json")
	self := os.Getpid()

	orig := enumProcsFn
	defer func() { enumProcsFn = orig }()

	// 第一轮：工具壳 200（ppid=self）与它的子进程 300 都在树里
	enumProcsFn = func() ([]procEntry, error) {
		return []procEntry{
			{PID: self, PPID: 1, PGID: self, StartedAt: 900},
			{PID: 200, PPID: self, PGID: 200, StartedAt: 1000},
			{PID: 300, PPID: 200, PGID: 200, StartedAt: 1100},
		}, nil
	}
	s := &rosterSampler{path: rosterPath(info)}
	s.sample(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 第二轮：工具壳退出，300 被 reparent 给 launchd
	enumProcsFn = func() ([]procEntry, error) {
		return []procEntry{
			{PID: self, PPID: 1, PGID: self, StartedAt: 900},
			{PID: 300, PPID: 1, PGID: 200, StartedAt: 1100},
		}, nil
	}
	s.sample(slog.New(slog.NewTextHandler(io.Discard, nil)))

	got, err := readRoster(rosterPath(info))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PID != 300 {
		t.Fatalf("工具壳退出后逃逸后代必须仍在名册里，got=%v", got)
	}
}

func TestRosterSamplerSkipsWriteWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	info := filepath.Join(dir, "proc.json")
	self := os.Getpid()

	orig := enumProcsFn
	defer func() { enumProcsFn = orig }()
	enumProcsFn = func() ([]procEntry, error) {
		return []procEntry{
			{PID: self, PPID: 1, PGID: self, StartedAt: 900},
			{PID: 300, PPID: self, PGID: self, StartedAt: 1100},
		}, nil
	}

	l := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &rosterSampler{path: rosterPath(info)}
	s.sample(l)
	st1, err := os.Stat(rosterPath(info))
	if err != nil {
		t.Fatal(err)
	}
	if s.writes != 1 {
		t.Fatalf("第一轮必须落盘一次，writes=%d", s.writes)
	}
	s.sample(l)
	if s.writes != 1 {
		t.Fatalf("同一批后代第二轮不得再落盘，writes=%d", s.writes)
	}
	st2, err := os.Stat(rosterPath(info))
	if err != nil {
		t.Fatal(err)
	}
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatal("内容未变时不应重写文件")
	}
}

func TestRosterIntervalIsOneSecond(t *testing.T) {
	// 间隔是本次修复的承重件之一：工具壳只活约 1 秒，15s 的 tick 打不中它。
	if rosterInterval != time.Second {
		t.Fatalf("rosterInterval 应为 1s，实为 %v", rosterInterval)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd internal/prochost && go test -run 'TestRoster' ./...`
Expected: FAIL，`undefined: rosterSampler` 与 `rosterInterval 应为 1s，实为 15s`

- [ ] **Step 3: 实现**

把 `shim.go` 的 `rosterInterval` 注释与取值改成：

```go
// rosterInterval 是后代名册的采样间隔。
//
// 为什么是 1s（B103 从 15s 下调）：名册现在是累积的（mergeRoster），漏记只可能
// 发生在「工具壳的整个存活窗口内一次都没采到」。executor 的 Bash 工具壳往往只活
// 约 1 秒（grok 把每条命令 setsid 成新会话后立刻返回），15s 的 tick 几乎必然错过
// 它——08-15 实测 450 个 `sleep 900` 一个都没进名册。1s 把这个窗口压到最小。
//
// 代价是每秒一次全进程表枚举。可接受的依据：enumProcs 走 sysctl/procfs，**不 fork**
// （procenum.go 的硬约束），所以它在「机器已经 fork 不动」时仍然可用，也不会自我
// 加剧；并且内容未变时不落盘，稳态下没有磁盘写入。
//
// 是变量而非常量：测试要把它调到毫秒级，否则每条周期用例都真等 1s。
var rosterInterval = time.Second
```

在 `snapshotRoster` 位置替换为：

```go
// rosterSampler 持有名册的采样状态：路径与上一轮落盘的字节。
//
// 为什么要有状态：名册现在每秒采一次，稳态下内容根本不变；把上一轮的序列化
// 结果留着比一比，就能把「每秒一次原子写 + rename」降成「变了才写」。
// 2000 进程的任务名册约 60KB，不做这件事就是每秒几十 KB 的无谓 I/O。
//
// 边界：本类型不负责启停节奏（那是 RunShim 里的 ticker），也不做任何存活判定
// 与信号发送（那是 footprint.go 的事）。
type rosterSampler struct {
	path   string
	last   []byte // 上一轮落盘的序列化结果；nil 表示还没写过
	writes int    // 实际落盘次数，仅供测试断言「未变则不写」
}

// sample 采一轮名册：枚举进程、与上一轮合并、必要时落盘。
//
// 参数：l 为日志器；本方法所有失败都只记日志并返回，不中断任务——名册写不出去
// 只意味着这一轮没有第二段清扫的依据，不值得让任务失败。
//
// 注意：周期日志一律 Debug 级（每秒一次，Info 会把任务日志刷满）；只有单次采样
// 耗时超过间隔一半时才升 Warn——那意味着采样本身开始拖累这台机器，是必须看见的事。
func (s *rosterSampler) sample(l *slog.Logger) {
	if s.path == "" {
		l.Warn("无 info_path，无法落盘后代名册，本任务不做出生登记")
		return
	}
	start := time.Now()
	procs, err := enumProcsFn()
	if err != nil {
		l.Warn("枚举进程失败，本轮跳过出生登记", "cause", err)
		return
	}
	prev, err := readRoster(s.path)
	if err != nil {
		// 名册损坏：这一轮从空名册重建，不能因此放弃采样——否则一次损坏会让
		// 这个任务此后永远没有名册
		l.Warn("读回上一轮名册失败，本轮从空名册重建", "path", s.path, "cause", err)
		prev = nil
	}
	entries := mergeRoster(prev, descendantsOf(os.Getpid(), procs), procs)
	b, err := marshalRoster(entries)
	if err != nil {
		l.Warn("序列化后代名册失败，本轮跳过出生登记", "cause", err)
		return
	}
	if s.last != nil && bytes.Equal(b, s.last) {
		l.Debug("后代名册未变，跳过落盘", "count", len(entries), "cost", time.Since(start))
		return
	}
	if err := writeRosterBytes(s.path, b); err != nil {
		l.Warn("落盘后代名册失败，本轮跳过出生登记", "path", s.path, "cause", err)
		return
	}
	s.last = b
	s.writes++
	cost := time.Since(start)
	if cost > rosterInterval/2 {
		// 采样耗时逼近间隔意味着「名册把机器拖慢了」——这是必须能被看见的事，
		// 否则它只会表现为一台莫名其妙变慢的机器
		l.Warn("后代名册采样耗时偏高", "path", s.path, "count", len(entries),
			"cost", cost, "interval", rosterInterval)
		return
	}
	l.Debug("后代名册已更新", "path", s.path, "count", len(entries), "cost", cost)
}

// snapshotRoster 采一轮名册（无状态入口，仅供不需要跨轮比对的调用方使用）。
//
// 参数：l 为日志器；infoPath 为 proc.json 路径（名册与它同目录）
func snapshotRoster(l *slog.Logger, infoPath string) {
	(&rosterSampler{path: rosterPath(infoPath)}).sample(l)
}
```

把 `RunShim` 里那段周期落盘的 goroutine 改成复用同一个 sampler（否则跨轮比对失效，
每轮都会重写）：

```go
	stopRoster := make(chan struct{})
	rosterDone := make(chan struct{})
	go func() {
		defer close(rosterDone)
		// 同一个 sampler 跨轮复用：它持有上一轮的序列化结果，"内容未变则不写"
		// 依赖这份状态；每轮新建一个等于关掉这个优化
		sampler := &rosterSampler{path: rosterPath(spec.InfoPath)}
		sampler.sample(l)
		tk := time.NewTicker(rosterInterval)
		defer tk.Stop()
		for {
			select {
			case <-stopRoster:
				return
			case <-tk.C:
				sampler.sample(l)
			}
		}
	}()
```

`shim.go` 的 import 需补 `"bytes"`。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd internal/prochost && go test -count=1 ./...`
Expected: PASS（既有的 shim 周期落盘用例也必须继续通过）

- [ ] **Step 5: Commit**

```bash
git add internal/prochost/shim.go internal/prochost/shim_test.go
git commit -m "fix(b103): 名册采样改累积语义、间隔 15s→1s、内容未变不落盘"
```

---

### Task 3: 清扫函数返回错误，`ErrExecutorAlive` 不再被吞

**Files:**
- Modify: `internal/agentd/reconcile.go:224-269`（`SweepTaskProcs`）、`internal/agentd/manager.go:139-160`（`sweepProcs` 测试缝与 `sweep` 方法）、`manager.go:2710` 附近（`handleResult` 的调用点）
- Test: `internal/agentd/`（放在既有 B93 清扫用例同一文件）

**Interfaces:**
- Consumes: 既有 `prochost.Sweep`、`prochost.ErrExecutorAlive`、`m.adapterFor`、`m.notifyOrphanRisk`
- Produces: `func (m *Manager) sweep(taskID string) error`（原地改签名）、`func (m *Manager) sweepTaskProcsOnce(taskID string) error`；`SweepTaskProcs(taskID string)` 签名与 best-effort 语义**不变**
- 测试缝 `sweepProcs` 的类型从 `func(taskID string)` 改为 `func(taskID string) error`

- [ ] **Step 1: 写失败的测试**

在既有 B93 清扫用例所在文件追加：

```go
func TestSweepReturnsExecutorAliveToCaller(t *testing.T) {
	m := newTestManager(t) // 沿用该文件里既有的构造方式
	m.sweepProcs = func(taskID string) error { return prochost.ErrExecutorAlive }
	if err := m.sweep("t1"); !errors.Is(err, prochost.ErrExecutorAlive) {
		t.Fatalf("sweep 必须把 ErrExecutorAlive 透传给调用方，got=%v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd internal/agentd && go test -run TestSweepReturnsExecutorAlive ./...`
Expected: FAIL —— `m.sweep(...) used as value`（当前 `sweep` 无返回值）

- [ ] **Step 3: 实现**

`manager.go` 的测试缝与 `sweep`：

```go
	// sweepProcs 是「清扫某任务残留进程」的测试缝。**生产路径恒为 nil**，
	// 由 sweep 方法退回 m.sweepTaskProcsOnce；非测试代码不得赋值。
	//
	// 为什么返回 error（B103）：Done/Stop 要对 ErrExecutorAlive 做有界重试，
	// 而 ErrExecutorAlive 恰恰是最容易让这条修复静默失效的竞态——存活锁的释放
	// 依赖 shim 真正退出，它落后于 stopExecutor 返回。吞掉它就是 B93 犯过的错：
	// 宣称「终态即清扫」，实际每次都被拒，直到 B103 排查才发现。
	sweepProcs func(taskID string) error
```

```go
// sweep 调用清扫，走测试缝或真实实现。
//
// 返回：prochost.Sweep 的错误；ErrExecutorAlive 表示执行者仍活着（调用方可重试）
func (m *Manager) sweep(taskID string) error {
	if m.sweepProcs != nil {
		return m.sweepProcs(taskID)
	}
	return m.sweepTaskProcsOnce(taskID)
}
```

`reconcile.go`：把现有 `SweepTaskProcs` 的函数体整体改名为 `sweepTaskProcsOnce`
并让它**返回 error**（各分支的日志与 `notifyOrphanRisk` 一字不改，只在末尾按分支
返回相应错误：`ErrExecutorAlive` 分支返回该错误，`err != nil` 分支返回 err，
其余返回 nil），然后：

```go
// SweepTaskProcs 清扫一个任务的残留进程，best-effort。
//
// 参数：taskID 为目标任务
//
// 注意：
//   - 无返回值是刻意的：它的调用方（watchdog、RecoverOnStartup、reconcileExecutorGone）
//     全都处在收尾路径上，清扫成败不该反过来影响那件事
//   - 需要知道清扫结果的调用方（Done/Stop 的有界重试）走 sweepTaskProcsOnce
//   - 导出是因为 RecoverOnStartup 的接线点在 cmd/agentd.go（与 ResumeTask 同理），
//     不是给外部当通用 API 用
func (m *Manager) SweepTaskProcs(taskID string) {
	_ = m.sweep(taskID)
}
```

`handleResult` 里那处调用改成 `_ = m.sweep(taskID)`（**注释保留原样**，那段话仍然成立）。

既有的 B93 用例 `TestHandleResultSweepsProcsOnFail` / `...OnSuccess` 里赋给
`sweepProcs` 的闭包要补 `return nil`——**只改签名，断言一个字都不许动**。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd internal/agentd && go test -count=1 ./...`
Expected: PASS，且 `TestHandleResultSweepsProcsOnFail` / `...OnSuccess` 仍为 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agentd/reconcile.go internal/agentd/manager.go internal/agentd/*_test.go
git commit -m "fix(b103): 清扫结果不再被吞，sweep 把 ErrExecutorAlive 透传给调用方"
```

---

### Task 4: `Done` 与 `Stop` 在停完 executor 后补清扫（带有界重试）

**Files:**
- Modify: `internal/agentd/manager.go`（`Done` 的 `stopExecutor` 之后、`Stop` 的 `stopExecutor` 之后；新增 `sweepAfterStop`）
- Test: `internal/agentd/`（同 Task 3 的文件）

**Interfaces:**
- Consumes: Task 3 的 `m.sweep(taskID) error`
- Produces: `func (m *Manager) sweepAfterStop(taskID string)`

- [ ] **Step 1: 写失败的测试**

```go
func TestDoneSweepsProcsAfterStop(t *testing.T) {
	m := newTestManager(t)
	var got []string
	m.sweepProcs = func(taskID string) error { got = append(got, taskID); return nil }
	// 构造一个处于 waiting_review 的任务（沿用该文件里既有的建任务辅助函数）
	id := newWaitingReviewTask(t, m)
	if err := m.Done(context.Background(), id, ""); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != id {
		t.Fatalf("Done 必须在停完 executor 后清扫一次，got=%v", got)
	}
}

func TestStopSweepsProcsAfterStop(t *testing.T) {
	m := newTestManager(t)
	var got []string
	m.sweepProcs = func(taskID string) error { got = append(got, taskID); return nil }
	id := newRunningTask(t, m)
	if _, err := m.Stop(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != id {
		t.Fatalf("Stop 必须在停完 executor 后清扫一次，got=%v", got)
	}
}

func TestSweepAfterStopRetriesWhileExecutorAlive(t *testing.T) {
	m := newTestManager(t)
	calls := 0
	m.sweepProcs = func(taskID string) error {
		calls++
		if calls < 3 {
			return prochost.ErrExecutorAlive
		}
		return nil
	}
	sweepRetryGap = time.Millisecond // 测试缝，避免真等 200ms
	m.sweepAfterStop("t1")
	if calls != 3 {
		t.Fatalf("ErrExecutorAlive 必须重试到成功或用尽，calls=%d", calls)
	}
}

func TestSweepAfterStopGivesUpAfterBoundedRetries(t *testing.T) {
	m := newTestManager(t)
	calls := 0
	m.sweepProcs = func(taskID string) error { calls++; return prochost.ErrExecutorAlive }
	sweepRetryGap = time.Millisecond
	m.sweepAfterStop("t1")
	if calls != sweepRetryAttempts {
		t.Fatalf("重试必须有界，calls=%d want=%d", calls, sweepRetryAttempts)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd internal/agentd && go test -run 'TestDoneSweeps|TestStopSweeps|TestSweepAfterStop' ./...`
Expected: FAIL，`undefined: m.sweepAfterStop` / `undefined: sweepRetryGap`

- [ ] **Step 3: 实现**

在 `manager.go` 加：

```go
// sweepRetryAttempts / sweepRetryGap 是终态清扫对 ErrExecutorAlive 的重试参数。
//
// 是变量而非常量：测试要把间隔调到毫秒级，否则每条用例都真等 600ms。
var (
	sweepRetryAttempts = 3
	sweepRetryGap      = 200 * time.Millisecond
)

// sweepAfterStop 在停完 executor 之后清扫这个任务留下的逃逸后代。
//
// 参数：taskID 为已停 executor 的任务
//
// 为什么必须在 stopExecutor 之后：prochost.Sweep 在存活锁仍被持有时直接拒绝
// （ErrExecutorAlive）——杀活着的执行者是 Kill 的职责，两者风险模型不同。
//
// 为什么必须重试而不是一次拒绝就算了：存活锁的释放依赖 shim 进程真正退出，
// 它落后于 stopExecutor 返回，中间有一个真实窗口。一次被拒就放弃，这条修复
// 在生产上会静默失效——B93 就是这么错的（宣称「终态即清扫」，实测每次都被
// ErrExecutorAlive 拒掉，直到 B103 排查才发现，中间隔了一整轮验收）。
//
// 注意：重试用尽打 Warn 而不是 Info。它意味着「executor 该死没死、逃逸后代
// 大概率残留」，是需要人看见的事。
func (m *Manager) sweepAfterStop(taskID string) {
	for i := 0; i < sweepRetryAttempts; i++ {
		if err := m.sweep(taskID); !errors.Is(err, prochost.ErrExecutorAlive) {
			return
		}
		if i < sweepRetryAttempts-1 {
			time.Sleep(sweepRetryGap)
		}
	}
	m.log.Warn("终态清扫放弃：存活锁始终未释放，逃逸后代可能残留",
		"task", taskID, "attempts", sweepRetryAttempts, "gap", sweepRetryGap)
}
```

在 `Done` 里，`stopExecutor` 那个 `else` 分支之后、worktree 清理之前插入：

```go
	// 终态清扫（B103）：Kill 只够着 shim 那个进程组，executor 的 Bash 工具
	// setsid 出去的后代（`cmd &` 这类）不在组内——不在这里扫，它们会在 launchd
	// 名下一直活到自然退出。必须在 worktree 清理之前：还活着的进程把 cwd 钉在
	// 工作树里，会让 git worktree remove 失败
	m.sweepAfterStop(taskID)
```

在 `Stop` 里同样位置（`stopExecutor` 之后、追加 failed 事件之前）插入同一行，
注释改为说明 `Stop` 的语义：

```go
	// 终态清扫（B103）：stop 的语义是「别跑了」，那就该包括 Bash 工具 setsid
	// 出去的后代——它们不在 shim 的进程组里，Kill 够不着
	m.sweepAfterStop(taskID)
```

`manager.go` 的 import 若缺 `"errors"` / `"time"` / `prochost` 需补。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd internal/agentd && go test -count=1 ./...`
Expected: PASS

- [ ] **Step 5: 全量回归**

Run: `go build ./... && go vet ./... && go test -count=1 ./...`
Expected: 0 FAIL

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/manager.go internal/agentd/*_test.go
git commit -m "fix(b103): Done/Stop 停完 executor 后补一次清扫，对存活锁竞态做有界重试"
```

---

### Task 5: 采样开销实测与交付说明

**Files:**
- Create: `docs/superpowers/notes/2026-08-15-b103-ledger.md`（ledger，若前面 task 已建则续写）

- [ ] **Step 1: 写一个基准，测单次采样的真实耗时**

在 `internal/prochost/shim_test.go` 追加（用**真实**的 `enumProcs`，不打桩——这条要
测的正是真机上的枚举开销）：

```go
func BenchmarkRosterSampleReal(b *testing.B) {
	dir := b.TempDir()
	s := &rosterSampler{path: rosterPath(filepath.Join(dir, "proc.json"))}
	l := slog.New(slog.NewTextHandler(io.Discard, nil))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.sample(l)
	}
}
```

- [ ] **Step 2: 跑它并把数字记下来**

Run: `cd internal/prochost && go test -bench BenchmarkRosterSampleReal -benchtime 200x -run '^$' ./...`

- [ ] **Step 3: 把结果写进 ledger**

ledger 里必须有这三样，**要有数不要有形容词**：
- `BenchmarkRosterSampleReal` 的 ns/op 与当时机器上的进程总数；
- 该耗时相对 1s 间隔的占比；
- 一句结论：这个开销是否可接受，依据是什么。

- [ ] **Step 4: Commit**

```bash
git add internal/prochost/shim_test.go docs/superpowers/notes/2026-08-15-b103-ledger.md
git commit -m "fix(b103): 补名册采样开销基准与实测数字"
```

---

## 交付

交一条分支，**不合并、不推 main/w4-delivery**。分支上应有：Task 1–5 的提交、
ledger、以及 `go build ./... && go vet ./... && go test -count=1 ./...` 全绿的证据。

**真机复验不由你做**（spec §6 第 6 条）：那需要在执行机上 fork 450 个进程，而你
就跑在执行机上——审核者会另行手工验证。你只需保证单测与基准的结论是真的。
