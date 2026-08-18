// 容器成员采样的测试：不依赖真实 Job Object，经 containerSampleFn 缝注入。
package prochost

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testLogger 造一个丢弃输出的日志器。
func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// 有容器能力时，采样必须落盘 members.json，且带推进的 sampled_at。
func TestContainerSamplingWritesSnapshot(t *testing.T) {
	saved := containerSampleFn
	defer func() { containerSampleFn = saved }()
	containerSampleFn = func() ([]int, error) { return []int{11, 22}, nil }

	p := filepath.Join(t.TempDir(), MembersFileName)
	s := &membersSampler{path: p}
	if ok := s.sample(testLogger()); !ok {
		t.Fatal("有容器能力时 sample 应返回 true（继续周期采样）")
	}
	snap, err := readMembers(p)
	if err != nil {
		t.Fatalf("readMembers: %v", err)
	}
	if len(snap.PIDs) != 2 || snap.PIDs[0] != 11 || snap.PIDs[1] != 22 {
		t.Fatalf("pid 表不对: %+v", snap)
	}
	if snap.SampledAt <= 0 {
		t.Fatal("sampled_at 必须有值：agentd 侧靠它说明数据时刻")
	}
}

// 无容器能力（unix）时 sample 返回 false，采样循环就此退出，不刷日志。
func TestContainerSamplingUnsupportedStops(t *testing.T) {
	saved := containerSampleFn
	defer func() { containerSampleFn = saved }()
	containerSampleFn = nil

	s := &membersSampler{path: filepath.Join(t.TempDir(), MembersFileName)}
	if ok := s.sample(testLogger()); ok {
		t.Fatal("无容器能力时 sample 应返回 false，让采样循环退出")
	}
}

// 单次查询失败不该终止采样：下一轮可能就好了。
func TestContainerSamplingTransientErrorKeepsGoing(t *testing.T) {
	saved := containerSampleFn
	defer func() { containerSampleFn = saved }()
	containerSampleFn = func() ([]int, error) { return nil, errors.New("transient") }

	s := &membersSampler{path: filepath.Join(t.TempDir(), MembersFileName)}
	if ok := s.sample(testLogger()); !ok {
		t.Fatal("单次查询失败应返回 true 继续重试，不能就此放弃整个任务的计数能力")
	}
}

// 内容未变则不重复落盘——每秒一次原子写是实打实的 I/O。
func TestContainerSamplingSkipsUnchanged(t *testing.T) {
	saved := containerSampleFn
	defer func() { containerSampleFn = saved }()
	containerSampleFn = func() ([]int, error) { return []int{7}, nil }

	p := filepath.Join(t.TempDir(), MembersFileName)
	s := &membersSampler{path: p}
	if !s.sample(testLogger()) {
		t.Fatal("首次采样应继续")
	}
	first, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if !s.sample(testLogger()) {
		t.Fatal("稳定采样应继续")
	}
	if s.writes != 1 {
		t.Fatalf("内容未变时不该重复落盘，writes=%d", s.writes)
	}
	second, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if !first.ModTime().Equal(second.ModTime()) {
		t.Error("内容未变时文件不该被重写")
	}
}
