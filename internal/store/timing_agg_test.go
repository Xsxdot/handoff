// timing_agg_test.go —— 耗时聚合纯函数的穷举测试 + 接线的真实 SQLite 验证。
//
// 为什么纯函数与接线分开测：聚合的分支（并发、缺段、负差、截断）在 SQL 之上
// 穷举成本极低；而接线只有两条断言（GetTask 填 / ListTasks 不填），却必须
// 走真库才算数——两者混在一起会让穷举那部分被建库开销拖慢几十倍。
package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// tr 造一条账目行，省掉每个用例重复写字段名。
func tr(kind string, turn int, dur, off int64, label, detail string) timingRow {
	return timingRow{Kind: kind, Turn: turn, DurMS: dur, OffsetMS: off, Label: label, Detail: detail}
}

func TestAggregateTimingEmpty(t *testing.T) {
	if got := aggregateTiming(nil); got != nil {
		t.Fatalf("空账目应返回 nil（「还不知道」），实际 %+v", got)
	}
	if got := aggregateTiming([]timingRow{}); got != nil {
		t.Fatalf("空切片应返回 nil，实际 %+v", got)
	}
}

func TestAggregateTimingSingleTurnNoConcurrency(t *testing.T) {
	got := aggregateTiming([]timingRow{
		tr("turn", 1, 1000, 0, "", ""),
		tr("api", 1, 400, 0, "", ""),
		tr("api", 1, 300, 0, "", ""),
		tr("tool", 1, 200, 400, "Bash", "go test ./..."),
	})
	if got == nil {
		t.Fatal("有账目却返回 nil")
	}
	if got.TotalMS != 1000 || got.APIMS != 700 || got.ToolMS != 200 || got.ToolSpanMS != 200 {
		t.Fatalf("三分法算错: %+v", got)
	}
	if got.OtherMS != 100 {
		t.Fatalf("OtherMS 应为 1000-700-200=100，实际 %d", got.OtherMS)
	}
	if got.Partial {
		t.Fatalf("账目齐全不应 Partial: %+v", got)
	}
}

// 并发工具：Σtool 大于墙钟跨度。这是 OffsetMS 存在的唯一理由，
// 也是「取其一冒充另一个」会静默算错的那个分支。
func TestAggregateTimingConcurrentTools(t *testing.T) {
	got := aggregateTiming([]timingRow{
		tr("turn", 1, 1000, 0, "", ""),
		tr("api", 1, 100, 0, "", ""),
		// [100,600) 与 [300,700) 重叠 → 并集 [100,700) = 600
		tr("tool", 1, 500, 100, "Bash", "go build ./..."),
		tr("tool", 1, 400, 300, "Bash", "go vet ./..."),
	})
	if got.ToolMS != 900 {
		t.Fatalf("ToolMS 应为时长之和 900，实际 %d", got.ToolMS)
	}
	if got.ToolSpanMS != 600 {
		t.Fatalf("ToolSpanMS 应为区间并集 600，实际 %d", got.ToolSpanMS)
	}
	if got.OtherMS != 300 {
		t.Fatalf("OtherMS 应为 1000-100-600=300（用 ToolMS 算会得 0），实际 %d", got.OtherMS)
	}
}

// 跨回合：offset 是回合内相对量，两个回合的 [0,500) 不能并成一个 500。
func TestAggregateTimingAcrossTurns(t *testing.T) {
	got := aggregateTiming([]timingRow{
		tr("turn", 1, 800, 0, "", ""), tr("api", 1, 300, 0, "", ""),
		tr("tool", 1, 500, 0, "Bash", "ls"),
		tr("turn", 2, 900, 0, "", ""), tr("api", 2, 400, 0, "", ""),
		tr("tool", 2, 500, 0, "Bash", "ls"),
	})
	if got.TotalMS != 1700 || got.APIMS != 700 {
		t.Fatalf("跨回合求和错: %+v", got)
	}
	if got.ToolSpanMS != 1000 {
		t.Fatalf("两个回合各 500，并集必须分回合算，应为 1000，实际 %d", got.ToolSpanMS)
	}
}

func TestAggregateTimingPartialMissingAPI(t *testing.T) {
	got := aggregateTiming([]timingRow{
		tr("turn", 1, 1000, 0, "", ""),
		tr("tool", 1, 200, 0, "Bash", "ls"),
	})
	if !got.Partial {
		t.Fatalf("回合缺 api 条目必须 Partial: %+v", got)
	}
	if got.OtherMS != 800 {
		t.Fatalf("缺段时 other 偏大是预期行为，应为 800，实际 %d", got.OtherMS)
	}
}

func TestAggregateTimingPartialMissingTurn(t *testing.T) {
	got := aggregateTiming([]timingRow{tr("api", 3, 500, 0, "", "")})
	if !got.Partial {
		t.Fatalf("有段却无回合墙钟必须 Partial: %+v", got)
	}
}

func TestAggregateTimingPartialUnknownKind(t *testing.T) {
	got := aggregateTiming([]timingRow{
		tr("turn", 1, 1000, 0, "", ""), tr("api", 1, 900, 0, "", ""),
		tr("wormhole", 1, 5000, 0, "", ""),
	})
	if !got.Partial {
		t.Fatalf("未知 kind 必须计入 Partial: %+v", got)
	}
	if got.TotalMS != 1000 || got.APIMS != 900 || got.ToolMS != 0 {
		t.Fatalf("未知 kind 不得计进任何一档: %+v", got)
	}
}

func TestAggregateTimingNegativeResidual(t *testing.T) {
	got := aggregateTiming([]timingRow{
		tr("turn", 1, 100, 0, "", ""),
		tr("api", 1, 900, 0, "", ""),
		tr("tool", 1, 300, 0, "Bash", "ls"),
	})
	if got.OtherMS != 0 {
		t.Fatalf("负差必须夹到 0，实际 %d", got.OtherMS)
	}
	if !got.Partial {
		t.Fatalf("负差说明采集有 bug，必须 Partial: %+v", got)
	}
}

// 截断：反向断言「第 21 名不在结果里」，并配一条正面断言锁住留下的是最大的那些。
func TestAggregateTimingBucketCap(t *testing.T) {
	var rows []timingRow
	rows = append(rows, tr("turn", 1, 1_000_000, 0, "", ""), tr("api", 1, 1, 0, "", ""))
	// 造 25 个工具名，耗时 100..2500 递增；截断后应只剩最大的 20 个（600..2500）
	for i := 1; i <= 25; i++ {
		rows = append(rows, tr("tool", 1, int64(i)*100, 0, fmt.Sprintf("T%02d", i), ""))
	}
	got := aggregateTiming(rows)
	if len(got.Buckets) != proto.TimingBucketCap {
		t.Fatalf("应截断到 %d 格，实际 %d", proto.TimingBucketCap, len(got.Buckets))
	}
	if got.Buckets[0].Label != "T25" || got.Buckets[0].DurMS != 2500 {
		t.Fatalf("排行第一应是最大的 T25/2500，实际 %+v", got.Buckets[0])
	}
	if last := got.Buckets[len(got.Buckets)-1]; last.Label != "T06" {
		t.Fatalf("留下的应是最大的 20 个（末位 T06），实际末位 %s", last.Label)
	}
	for _, b := range got.Buckets {
		if b.Label == "T01" || b.Label == "T05" {
			t.Fatalf("被截断的应是最小的那些，%s 不该出现", b.Label)
		}
	}
}

// 下钻层：命令首词按前两段取，JSON 入参不下钻，环境赋值前缀跳过。
func TestAggregateTimingSubBuckets(t *testing.T) {
	got := aggregateTiming([]timingRow{
		tr("turn", 1, 10_000, 0, "", ""), tr("api", 1, 1, 0, "", ""),
		tr("tool", 1, 300, 0, "Bash", "go test ./internal/store/"),
		tr("tool", 1, 200, 0, "Bash", "go test ./internal/proto/"),
		tr("tool", 1, 100, 0, "Bash", "go build ./..."),
		tr("tool", 1, 50, 0, "Bash", "TOKEN=s3cret go test ./..."),
		tr("tool", 1, 900, 0, "Read", `{"file_path":"/a/b.go"}`),
	})
	var bash, read *proto.TimingBucket
	for i := range got.Buckets {
		switch got.Buckets[i].Label {
		case "Bash":
			bash = &got.Buckets[i]
		case "Read":
			read = &got.Buckets[i]
		}
	}
	if bash == nil || read == nil {
		t.Fatalf("两个工具名都该有一格: %+v", got.Buckets)
	}
	if read.Sub != nil {
		t.Fatalf("入参是 JSON 不下钻，实际 %+v", read.Sub)
	}
	if len(bash.Sub) != 2 {
		t.Fatalf("go test / go build 两格（TOKEN= 前缀应并进 go test），实际 %+v", bash.Sub)
	}
	if bash.Sub[0].Label != "go test" || bash.Sub[0].DurMS != 550 || bash.Sub[0].Count != 3 {
		t.Fatalf("go test 应聚成 300+200+50=550 / 3 次，实际 %+v", bash.Sub[0])
	}
	for _, s := range bash.Sub {
		if s.Label == "TOKEN=s3cret go" {
			t.Fatalf("环境赋值必须跳过，不得成为标签: %+v", bash.Sub)
		}
	}
	for _, s := range bash.Sub {
		if s.Sub != nil {
			t.Fatalf("下钻只许一层，实际 %+v", s)
		}
	}
}

// 排序确定性：同耗时的两格按 Label 升序，不随 map 迭代顺序漂。
func TestAggregateTimingDeterministicOrder(t *testing.T) {
	rows := []timingRow{tr("turn", 1, 5000, 0, "", ""), tr("api", 1, 1, 0, "", "")}
	for _, name := range []string{"Zeta", "Alpha", "Mike"} {
		rows = append(rows, tr("tool", 1, 100, 0, name, ""))
	}
	want := []string{"Alpha", "Mike", "Zeta"}
	for i := 0; i < 50; i++ {
		got := aggregateTiming(rows)
		for j, b := range got.Buckets {
			if b.Label != want[j] {
				t.Fatalf("第 %d 次调用次序漂了: %v", i, got.Buckets)
			}
		}
	}
}

// 接线：GetTask 填 Timing，ListTasks 不填。真实 SQLite 上跑。
func TestTaskTimingWiring(t *testing.T) {
	s := openTestStore(t)
	t0 := time.Now().UTC().Truncate(time.Second)
	id := "11111111-2222-3333-4444-555555555555"
	if err := s.CreateTask(&proto.Task{
		ID: id, RepoPath: "/r", State: proto.TaskStatePending, CreatedAt: t0, UpdatedAt: t0,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := s.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Timing != nil {
		t.Fatalf("无账目时 Timing 应为 nil（「还不知道」），实际 %+v", got.Timing)
	}

	for _, e := range []proto.TimingEntry{
		{Key: "turn/1", Kind: proto.TimingKindTurn, Turn: 1, DurMS: 1000},
		{Key: "api/1/0", Kind: proto.TimingKindAPI, Turn: 1, DurMS: 700},
		{Key: "tool/1/p1", Kind: proto.TimingKindTool, Turn: 1, DurMS: 200,
			OffsetMS: 700, Label: "Bash", Detail: "go test ./..."},
	} {
		if err := s.UpsertTiming(id, e); err != nil {
			t.Fatalf("UpsertTiming %s: %v", e.Key, err)
		}
	}

	got, err = s.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Timing == nil {
		t.Fatal("有账目时 GetTask 必须填 Timing —— 只接线不实现会让前端显示「—」，看起来完全正常")
	}
	if got.Timing.TotalMS != 1000 || got.Timing.APIMS != 700 || got.Timing.OtherMS != 100 {
		t.Fatalf("接线后的聚合值不对: %+v", got.Timing)
	}

	list, err := s.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应有 1 个任务，实际 %d", len(list))
	}
	if list[0].Timing != nil {
		t.Fatalf("ListTasks 不得填 Timing（每行做一次 SUM 是纯浪费），实际 %+v", list[0].Timing)
	}

	if err := s.UpsertTiming(id, proto.TimingEntry{
		Key: "turn/1", Kind: proto.TimingKindTurn, Turn: 1, DurMS: 2000,
	}); err != nil {
		t.Fatalf("UpsertTiming 覆盖: %v", err)
	}
	got, err = s.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Timing.TotalMS != 2000 {
		t.Fatalf("同键重报应覆盖成 2000（不是累加成 3000），实际 %d", got.Timing.TotalMS)
	}
}
