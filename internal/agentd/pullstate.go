// pullstate.go —— 自拉换版的内存状态：并发锁 + 阶段流转 + 快照。
//
// 职责：
//   - 保证同一时刻只有一个自拉在跑（begin 抢锁）
//   - 记录阶段与失败原文，供 /api/status 回报
//
// 边界：
//   - **只在内存，不落盘**：成功路径的终点是进程重启，状态随之消失，而那时
//     版本号已经变了、调用方靠它就能确认；一个落盘的 done 会在下次启动时
//     变成误导性的陈旧数据。失败时进程不重启，内存态正好可查
//   - 不做下载、不碰文件：它只记「现在到哪一步了」，动作在 update.go
//   - 不做超时：自拉的总时限由 Installer 的 HTTP 超时兜底
//   - 本文件不打日志：它是状态容器，日志由动作侧（update.go）打
package agentd

import (
	"sync"
	"time"

	"github.com/xushixin/handoff/internal/proto"
)

// pullTracker 持有自拉换版的并发锁与状态。
//
// 为什么锁与状态放在一起：它们的不变量是同一条——「running 为真当且仅当
// 有一个自拉正在推进」。拆成两个对象后，任何一处忘记同步都会造出
// 「状态说在下载、锁却是空闲」的幽灵态。
type pullTracker struct {
	mu      sync.Mutex
	running bool
	st      *proto.PullState
}

func newPullTracker() *pullTracker { return &pullTracker{} }

// begin 尝试开始一次自拉。
//
// 返回：
//   - true 表示抢到了，调用方可以起后台 goroutine；false 表示已有一个在跑，
//     调用方应当回 409 + proto.UpdateReasonPullInProgress
//
// 注意：
//   - 抢到锁的一方**必须**最终调用 fail 或让进程重启，否则锁永不释放。
//     成功路径不需要显式释放：换版成功即触发重启，进程整个换掉
func (p *pullTracker) begin(tag string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return false
	}
	now := time.Now()
	p.running = true
	p.st = &proto.PullState{
		Tag: tag, Stage: proto.PullStageDownloading,
		StartedAt: now, UpdatedAt: now,
	}
	return true
}

// stage 推进阶段。没有进行中的自拉时是空操作。
func (p *pullTracker) stage(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.st == nil {
		return
	}
	p.st.Stage = s
	p.st.UpdatedAt = time.Now()
}

// fail 记录失败并释放锁。
//
// 注意：
//   - 失败状态**保留**在内存里（不清空 st）：进程不会重启，操作者要靠
//     /api/status 拿到这条原文才知道该改代理还是改网络
func (p *pullTracker) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running = false
	if p.st == nil {
		return
	}
	p.st.Stage = proto.PullStageFailed
	p.st.Error = err.Error()
	p.st.UpdatedAt = time.Now()
}

// snapshot 返回状态副本，供 status 装配。没跑过时返回 nil。
//
// 返回副本而不是内部指针：status 的装配与后台 goroutine 的阶段推进并发，
// 直接外露指针会让 json.Marshal 撞上数据竞争。
func (p *pullTracker) snapshot() *proto.PullState {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.st == nil {
		return nil
	}
	cp := *p.st
	return &cp
}
