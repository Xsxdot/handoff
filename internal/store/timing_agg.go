// timing_agg.go —— 耗时账本的聚合（账目行 → 三分法结果）。
//
// 职责：
//   - Store.TaskTiming：从 task_timing_ledger 取行，交给纯函数聚合
//   - aggregateTiming：纯函数，账目集合 → proto.TaskTiming
//
// 边界：
//   - **SQL 只负责取行**。求和、区间并集、排行全在纯函数里——把区间并集写进
//     SQL 会让这段逻辑失去穷举测试的可能（拆解 T6 的硬要求）
//   - **不做截断**：Detail 的 200 rune 上限由采集侧负责（UpsertTiming 的注释
//     已写死这一点，两处都以为对方管了是这类字段最常见的失守方式）
//   - 不认识任何具体 executor：四家喂进来的账目在这里已经同构
package store

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Xsxdot/handoff/internal/proto"
)

// subLabelRunes 是下钻层标签的 rune 上限。
//
// 命令首词按 P4=(b) 取前两段，但「两段」在极端命令上仍可能很长
// （`docker run --rm -v <长路径>` 的第二段就是个长路径）。排行是给人扫一眼
// 用的，一格标签撑满整行就失去了排行的意义。
const subLabelRunes = 40

// unknownToolLabel 是工具名缺席时的桶名。
//
// 不拿空串当桶名：空串在界面上是一格看不见的行，读者会以为渲染坏了。
const unknownToolLabel = "(未知工具)"

// timingRow 是账本里的一行，聚合纯函数的输入。
//
// 刻意不复用 proto.TimingEntry：那个类型带着 Key（幂等键），而聚合根本不看
// Key——把它摆进入参会让读者以为聚合对 Key 有要求。
type timingRow struct {
	Kind     string
	Turn     int
	DurMS    int64
	OffsetMS int64
	Label    string
	Detail   string
}

// span 是一次工具调用在回合内占用的区间（左闭右开，单位毫秒）。
type span struct{ start, end int64 }

// turnAcc 是单个回合的累加中间态。
type turnAcc struct {
	turnMS  int64
	apiMS   int64
	hasTurn bool
	hasAPI  bool
	spans   []span
}

// bucketAcc 是排行的累加中间态。
//
// 下钻只许一层，这条由结构保证而非靠自律：下钻层的 bucketAcc 一律以
// subs=nil 构造，于是 rankBuckets 递归到第二层自然终止。
type bucketAcc struct {
	durMS int64
	count int
	subs  map[string]*bucketAcc
}

// TaskTiming 对该任务的全部耗时账目求和，得到三分法聚合。
//
// 参数：taskID —— 任务 ID
//
// 返回：
//   - 没有任何账目行时返回 (nil, nil)。**不返回零值结构**——0 会被读成
//     「一共没花时间」，而真相是「还不知道」（与 TaskCumulative 同款纪律）
//   - 取行/扫描出错时返回 (nil, err)。**出错绝不返回 (nil, nil)**：那与
//     「没有账目」形状相同，会把一次读库失败伪装成一个正常的空账本
//
// 注意：**本方法读路径高频，成功不打日志**（与 TaskCumulative 同款）。
func (s *Store) TaskTiming(taskID string) (*proto.TaskTiming, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT kind, turn, dur_ms, offset_ms, label, detail
   FROM task_timing_ledger WHERE task_id = ?`, taskID)
	if err != nil {
		return nil, fmt.Errorf("读任务 %s 耗时账本: %w", taskID, err)
	}
	defer rows.Close()

	var acc []timingRow
	for rows.Next() {
		var r timingRow
		if err := rows.Scan(&r.Kind, &r.Turn, &r.DurMS, &r.OffsetMS, &r.Label, &r.Detail); err != nil {
			return nil, fmt.Errorf("扫描任务 %s 耗时账目: %w", taskID, err)
		}
		acc = append(acc, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历任务 %s 耗时账目: %w", taskID, err)
	}
	return aggregateTiming(acc), nil
}

// aggregateTiming 把账目行聚合成三分法结果。
//
// **纯函数**：无 I/O、无时钟、结果不依赖 map 迭代顺序（次序由显式排序决定）。
//
// 参数：rows —— 同一个任务的全部账目行，顺序无关
// 返回：聚合结果；rows 为空返回 nil（不是零值结构，理由见 TaskTiming）
//
// 三条口径（契约 §2.1，改之前先读那一节）：
//   - TotalMS = Σ kind=turn。**turn 不是段**，是 other 的分母；加进三分会让
//     总时长被计两遍且不报错
//   - ToolSpanMS 分回合求区间并集再相加：OffsetMS 是回合内相对偏移，跨回合的
//     区间不可比，混在一起并集会把两个回合的 [0,500) 并成一个 500
//   - OtherMS = max(0, Total − API − ToolSpan)。取 max 是**防御不是语义**，
//     真出现负数说明采集有 bug，此时 Partial 必为真
//
// 关于 Partial 的一个反直觉推论：**运行中的任务几乎总是 Partial**。回合开始时
// 就写了 turn 行，而模型段要到第一次 ToolStart 或 EndTurn 才关闭——所以在跑的
// 那个回合没有 api 行。这不是 bug，正是「other 此刻偏大」的诚实表达。
func aggregateTiming(rows []timingRow) *proto.TaskTiming {
	if len(rows) == 0 {
		return nil
	}

	turns := map[int]*turnAcc{}
	tools := map[string]*bucketAcc{}
	var out proto.TaskTiming

	for _, r := range rows {
		acc := turns[r.Turn]
		if acc == nil {
			acc = &turnAcc{}
			turns[r.Turn] = acc
		}
		switch proto.TimingKind(r.Kind) {
		case proto.TimingKindTurn:
			// 同一回合库里只有一行 turn（键是 turn/<turn>，按同键覆盖），
			// 所以这里赋值而不是累加——累加会把「反复刷新」读成「多个回合」
			acc.turnMS, acc.hasTurn = r.DurMS, true
		case proto.TimingKindAPI:
			acc.apiMS += r.DurMS
			acc.hasAPI = true
		case proto.TimingKindTool:
			acc.spans = append(acc.spans, span{r.OffsetMS, r.OffsetMS + r.DurMS})
			out.ToolMS += r.DurMS
			addToolRow(tools, r)
		default:
			// 未知 kind：既不 panic 也不静默当 0。库里出现本版不认识的 kind
			// 是常态（部署顺序不保证），唯一诚实的处置是「不计入任何一档，
			// 并把账目标成不全」
			out.Partial = true
		}
	}

	for _, acc := range turns {
		out.TotalMS += acc.turnMS
		out.APIMS += acc.apiMS
		out.ToolSpanMS += unionMS(acc.spans)
		// 有回合墙钟却一条模型段都没有、或有段却没有回合墙钟，两者都说明这个
		// 回合的账缺了一块，OtherMS 会因此偏大
		if !acc.hasTurn || !acc.hasAPI {
			out.Partial = true
		}
	}

	if residual := out.TotalMS - out.APIMS - out.ToolSpanMS; residual < 0 {
		out.OtherMS, out.Partial = 0, true
	} else {
		out.OtherMS = residual
	}

	out.Buckets = rankBuckets(tools)
	return &out
}

// unionMS 求区间并集的总长度。
//
// 它存在的唯一理由是并发工具：一个回合里同时发出的多个工具调用，Σdur_ms 会
// 大于它们实际占用的墙钟。拿 Σdur_ms 当墙钟用，OtherMS 会被系统性地吃成 0，
// 而且不报错。
//
// 边界：end <= start 的区间（0ms 调用、时钟回拨、脏数据）跳过而不是当成负
// 长度——负长度会把并集算小，进而把 OtherMS 算大，是一个静默错误。
func unionMS(spans []span) int64 {
	valid := make([]span, 0, len(spans))
	for _, s := range spans {
		if s.end > s.start {
			valid = append(valid, s)
		}
	}
	if len(valid) == 0 {
		return 0
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].start < valid[j].start })
	var total int64
	cur := valid[0]
	for _, s := range valid[1:] {
		if s.start > cur.end {
			total += cur.end - cur.start
			cur = s
			continue
		}
		if s.end > cur.end {
			cur.end = s.end
		}
	}
	return total + cur.end - cur.start
}

// addToolRow 把一条工具账目累加进排行（工具名一格 + 命令首词下钻一层）。
func addToolRow(tools map[string]*bucketAcc, r timingRow) {
	label := r.Label
	if label == "" {
		label = unknownToolLabel
	}
	b := tools[label]
	if b == nil {
		b = &bucketAcc{subs: map[string]*bucketAcc{}}
		tools[label] = b
	}
	b.durMS += r.DurMS
	b.count++

	head := commandHead(r.Detail)
	if head == "" {
		return // 入参不是命令：编一格 `{"path":` 出来对读者毫无价值
	}
	s := b.subs[head]
	if s == nil {
		s = &bucketAcc{} // subs 留 nil：下钻只许一层，由结构保证
		b.subs[head] = s
	}
	s.durMS += r.DurMS
	s.count++
}

// rankBuckets 把累加态排成降序排行并截断到 proto.TimingBucketCap。
//
// 排序键是 (DurMS 降序, Label 升序)。第二关键字不是美观，是**确定性**：
// Go 的 map 迭代顺序随机，只按 DurMS 排会让同耗时的两格在两次调用间换位，
// 于是契约夹具与断言会偶发翻红——而偶发红会被当噪音忽略，判据就此失效。
func rankBuckets(m map[string]*bucketAcc) []proto.TimingBucket {
	if len(m) == 0 {
		return nil
	}
	out := make([]proto.TimingBucket, 0, len(m))
	for label, acc := range m {
		out = append(out, proto.TimingBucket{
			Label: label, DurMS: acc.durMS, Count: acc.count,
			Sub: rankBuckets(acc.subs),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DurMS != out[j].DurMS {
			return out[i].DurMS > out[j].DurMS
		}
		return out[i].Label < out[j].Label
	})
	if len(out) > proto.TimingBucketCap {
		out = out[:proto.TimingBucketCap]
	}
	return out
}

// commandHead 从 Detail 取「命令首词」，作为排行下钻层的标签。
// 返回 "" 表示这条 Detail 不适合下钻（调用方跳过，不建空格子）。
//
// 三条规则（P4=(b) 的落地）：
//  1. 以 { 或 [ 开头 → ""。那是入参 JSON（非 Bash 工具的 Detail 回落成
//     compactJSON），它的「首词」是 `{"path":` 之类，对「哪条命令慢」无价值
//  2. 跳过前导的 VAR=value 环境赋值：它们不是命令，`TOKEN=… go test` 应当与
//     `go test` 落进同一格；顺带避免把赋值右边的凭据抬进排行标签
//  3. 取剩下的前两段（`go test ./...` → `go test`，把 go build/vet/test 分开），
//     再按 subLabelRunes 截断
func commandHead(detail string) string {
	s := strings.TrimSpace(detail)
	if s == "" || s[0] == '{' || s[0] == '[' {
		return ""
	}
	fields := strings.Fields(s)
	for len(fields) > 0 && isEnvAssign(fields[0]) {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return ""
	}
	head := fields[0]
	if len(fields) > 1 {
		head += " " + fields[1]
	}
	if r := []rune(head); len(r) > subLabelRunes {
		return string(r[:subLabelRunes])
	}
	return head
}

// isEnvAssign 判断一段是不是 VAR=value 形式的环境赋值。
//
// 只认 [A-Za-z_][A-Za-z0-9_]*= 这一种形状：宽一点会把 `--flag=v` 这类真正的
// 命令参数误判成赋值，从而把命令首词吃掉一段。
func isEnvAssign(f string) bool {
	eq := strings.IndexByte(f, '=')
	if eq <= 0 {
		return false
	}
	for i, c := range f[:eq] {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '_':
		case i > 0 && c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return true
}
