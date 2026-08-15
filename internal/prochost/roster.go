// roster.go —— 出生登记：后代名册的闭包、落盘与读取。
//
// 职责：
//   - 在进程树**还活着**的时候，沿 ppid 链闭包出 shim 的全部后代
//   - 把名册（pid + 启动时刻）原子落盘，供 executor 死后点名回收
//   - 读回名册，容忍缺失与损坏
//
// 边界：
//   - 不发任何信号、不做存活判定——点名与回收是 footprint.go 的 Sweep 的事
//   - 名册是**累积**的：每轮把「上一轮里仍存活的条目」与「本轮 ppid 闭包」取并集，
//     只在进程消失或 pid 被复用时删除条目（B103）。不能整份覆盖——Bash 工具 setsid
//     出来的后代在工具壳退出后 ppid 链就断了，覆盖会把仍活着的它们抹掉
//   - 仍有残余盲区：活得比采样间隔（rosterInterval）还短、且存活窗口内一次都没被
//     采到的工具壳，它的后代仍然漏记。1s 采样把窗口收小，不等于消除
//   - 不碰 proc.json（那是 adapter 独占的文件），名册是独立文件
package prochost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// RosterFileName 是后代名册的文件名（与 proc.json 同目录）。
const RosterFileName = "roster.json"

// rosterEntry 是名册里的一条：一个后代进程的 pid 与它的出生时刻。
//
// 为什么必须带 StartedAt：pid 会被内核复用。清扫发生在 executor 死后，名册
// 落盘与点名之间隔着不确定的时间，期间该 pid 完全可能已经属于另一个无关进程
// （极端情况下是 agentd 或用户的登录 shell）。出生时刻是这条记录的身份凭据，
// 对不上就是另一个进程——B47 误杀 114 次的教训，这里宁漏勿错。
type rosterEntry struct {
	PID       int   `json:"pid"`
	StartedAt int64 `json:"started_at"`
}

// rosterPath 由 proc.json 的路径推出名册路径（同目录，固定文件名）。
//
// 为什么不让调用方各自拼：shim 写、Start 记、Sweep 读，三处必须完全一致，
// 拼错一个字符的表现是「名册永远为空」——一个不报错、只是悄悄不干活的故障。
func rosterPath(infoPath string) string {
	if infoPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(infoPath), RosterFileName)
}

// descendantsOf 从进程快照里闭包出 root 的全部后代（不含 root 自己）。
//
// 参数：
//   - root: 起点 pid（生产里是 shim 自己）
//   - procs: 一次进程快照（当前 uid 的全部进程）
//
// 返回：后代的 pid 与启动时刻；root 不在快照里或没有后代时返回空切片
//
// 为什么按 ppid 而不是 pgid：executor 经 Bash 工具拉起的子进程会 setsid 自成
// 会话与进程组（2026-08-12 devbox 实证：`33365 92657 33365 (zsh)`，父进程是
// opencode serve 但 pgid 是它自己），pgid 判据看不到它们。ppid 不受 setsid 影响。
//
// 注意：
//   - **本函数只在树活着时有意义**。executor 一死，后代被 reparent 给
//     init/launchd，ppid 链当场断在最需要它的地方——所以名册必须在活着的时候
//     周期落盘，而不是清扫时现算
//   - visited 集合是必需的：真实快照里 pid 1 的 ppid 是 0 或 1（自环），且快照
//     是非原子的，两条记录之间可能出现看起来成环的形态。没有它会死循环
//   - 本函数刻意不打日志：它每 15s 被调用一次、且是纯函数，日志放在调用方
//     （shim 的周期落盘）边界上记一次入参与结论即可，这里再记等于同一件事
//     写两遍并按周期刷屏
func descendantsOf(root int, procs []procEntry) []rosterEntry {
	if root <= 0 || len(procs) == 0 {
		return nil
	}
	// 先按 ppid 建反向索引，避免每一层都全表扫描（进程表可达数千条）
	children := make(map[int][]procEntry, len(procs))
	for _, p := range procs {
		if p.PID == p.PPID {
			continue // 自环：pid 1 的常见形态，不可能是别人的后代链的一环
		}
		children[p.PPID] = append(children[p.PPID], p)
	}
	visited := map[int]bool{root: true}
	queue := []int{root}
	out := make([]rosterEntry, 0, 8)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range children[cur] {
			if visited[c.PID] {
				continue
			}
			visited[c.PID] = true
			out = append(out, rosterEntry{PID: c.PID, StartedAt: c.StartedAt})
			queue = append(queue, c.PID)
		}
	}
	return out
}

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

// writeRoster 把名册原子写到 path（临时文件 + rename）。
//
// 参数：
//   - path: 名册路径（rosterPath 的结果）
//   - entries: 本次快照的全部后代；空切片是合法输入（表示这一刻没有后代）
//
// 返回：临时文件写失败或 rename 失败时返回错误
//
// 为什么必须原子：读者是另一个进程（agentd 的 Sweep），它随时可能在 shim
// 正在写的瞬间读。直接覆盖写会让读者拿到半截 JSON——而半截 JSON 解析失败会
// 被当成「名册损坏」，于是一次正常的周期写入就变成了一条错误日志。
//
// 为什么临时文件放同目录：rename 只有在同一文件系统内才是原子的。
//
// 注意：本函数不打日志。它每 15s 被调用一次，成功路径打日志就是按周期刷屏；
// 失败由调用方（shim 的周期落盘）统一记一条 Warn 并继续——名册写不出去只
// 意味着这一轮没有第二段清扫，不值得中断任务。
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

// readRoster 读回名册。
//
// 参数：path 为名册路径；空串等同于「没有名册」
//
// 返回：
//   - entries: 名册内容；没有名册时为 nil
//   - err: 文件存在但读不动或解析失败
//
// 注意：**文件不存在返回 (nil, nil) 而不是错误**。三种正常形态都会走到这里：
// 任务刚起来还没到第一次落盘、升级前建的老任务、adapter 不带 InfoPath。
// 把它们当错误会让 Sweep 每次都记一条假故障，真故障就淹没了。
// 但**解析失败必须报错**：那是「有名册却读不出来」，与「没有名册」是两回事。
func readRoster(path string) ([]rosterEntry, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读名册 %s: %w", path, err)
	}
	var entries []rosterEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("解析名册 %s: %w", path, err)
	}
	return entries, nil
}
