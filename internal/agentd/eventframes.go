// eventframes.go —— 把控制面事件派生成 frames.jsonl 里的 event 引用帧。
//
// 职责：
//   - 提供一个 store.SetEventHook 用的回调：事件落库后往该任务的
//     frames.jsonl 追加一条 event 帧（只存 seq 与类型名）
//
// 边界：
//   - 只写文件，**绝不回调 store**（会自我递归 / 争锁，见 SetEventHook 的约定）
//   - 不复制 payload：payload 的真相在 events 表，复制一份就有两份会漂移的真相
//   - 尽力而为：任务目录不在、写失败，都只 Warn，绝不影响已经成功的事件落库
//
// 为什么 event 帧要存在：帧流要能表达「模型说了这句 → 请求了权限 → 继续」的
// **顺序**。事件与帧由同一进程写、走同一个 append 序，因此单流顺序即真实顺序；
// 若让前端拿事件流和帧流按时间戳归并，两条不同写入路径的时间戳会真的乱序。
//
// 为什么用 turn.WriterFor 而不是 turn.NewFrameWriter：adapter 在 Start 时已持有
// 该任务的 FrameWriter（r.frames）。若事件钩子每次自己 new 一个 writer，两个
// 实例各持一份内存 seq 写同一个 frames.jsonl，帧号会互相覆盖（落盘 1 2 3 3）。
// WriterFor 按任务目录去重返回**同一个**实例，保证「一个任务目录一个 seq 分配者」。
package agentd

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/executor/turn"
	"github.com/Xsxdot/handoff/internal/proto"
)

// eventFrameHook 返回一个「事件落库后写 event 引用帧」的回调。
//
// 参数：
//   - dataDir: agentd 数据目录（任务目录在 dataDir/tasks/<id> 下）
//   - log:     日志入口
//
// 注意：任务目录不存在时静默跳过——事件可能属于一个目录已被清掉的任务，
// 那不是错误，只是没有帧文件可写。
func eventFrameHook(dataDir string, log *slog.Logger) func(proto.Event) {
	return func(e proto.Event) {
		taskDir := filepath.Join(dataDir, "tasks", e.TaskID)
		if _, err := os.Stat(taskDir); err != nil {
			return // 目录不在：没有帧文件可写，不是错误
		}
		w, err := turn.WriterFor(taskDir, log)
		if err != nil {
			log.Warn("事件帧：创建帧写入器失败", "task", e.TaskID, "seq", e.Seq, "cause", err)
			return
		}
		if err := w.EventRef(e.Seq, string(e.Type)); err != nil {
			log.Warn("事件帧：写入失败", "task", e.TaskID, "seq", e.Seq,
				"type", e.Type, "cause", err)
			return
		}
		log.Debug("事件帧已写入", "task", e.TaskID, "seq", e.Seq, "type", e.Type)
	}
}

// registerEventFrameHook 在装配期把事件帧钩子挂到 store 上。
//
// 为什么是一个注册点而不是改 20 个 AppendEvent 调用点：调用点散落在
// manager.go / reconcile.go / watchdog.go，逐点补一行既啰嗦，又留下
// 「以后新增调用点忘了补」的失效模式。钩子自动覆盖现有与未来的全部调用点。
func (s *Server) registerEventFrameHook() {
	s.st.SetEventHook(eventFrameHook(s.cfg.DataDir, s.log))
	s.log.Info("事件帧钩子已注册", "datadir", s.cfg.DataDir)
}
