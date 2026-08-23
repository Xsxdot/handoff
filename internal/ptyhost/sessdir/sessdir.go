// Package sessdir 是 PTY 会话在磁盘上的落点：目录布局、元数据、以及跨 agentd
// 重启的三态扫描。
//
// 布局（<root> 通常是 <DataDir>/ptys）：
//
//	<root>/<session-id>/
//	  meta.json     静态事实，agentd 重启后据此重建会话表
//	  sock          unix socket，ptyhost 进程监听
//	  lock          存活锁，ptyhost 全生命周期持有
//	  ptyhost.log   ptyhost 进程自己的日志
//
// 职责：路径拼装、元数据读写、扫描判活、删除。
//
// 边界：
//   - Scan 不删任何东西，只报告三态。删由调用方做——这样扫描能被表驱动测试，
//     而删除这个不可逆动作留在能打日志、能被审计的那层
//   - 不起进程、不连 socket、不认识帧格式
//   - 不打日志：它是叶子层，错误带上下文上抛，由调用方记录
//
// 为什么判活用文件锁不用 pid：pid 会被操作系统复用，「进程存在」不等于「我的那个
// 进程存在」。flock 由内核在进程终止时无条件释放，不存在陈旧锁。
package sessdir

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Xsxdot/handoff/internal/prochost"
)

// MaxSockPath 是 unix socket 路径的保守上限。
//
// macOS 的 sockaddr_un.sun_path 是 104 字节、Linux 是 108，取小的再留 4 字节余量。
//
// 导出是因为它有第二个消费者：internal/ptytestroot 要用同一个上限预算测试根的
// 路径长度。那边曾经自己抄了一份字面量，于是改这里既不会让它编译失败、也不会
// 让任何测试变红，要到真 bind 的时候才炸——那时错误现场离根因已经很远。
const MaxSockPath = 100

// State 是一个会话目录的扫描结论。
type State string

const (
	// StateLive：锁被持有且 meta 可读——那个 ptyhost 进程还活着。
	StateLive State = "live"
	// StateDead：没人持锁——进程已经不在，目录可以清掉。
	StateDead State = "dead"
	// StateBroken：锁被持有但 meta 读不出来——有个进程活着而我们不知道它是什么。
	StateBroken State = "broken"
)

// Meta 是一个会话的静态事实，落在 meta.json 里。
//
// 只放不会变的东西：cols/rows/bytes_out/foreground 都是活事实，存在这里必然是陈的，
// 活事实经 stat 控制帧现问。
type Meta struct {
	ID           string    `json:"id"`
	BasePath     string    `json:"base_path"`
	BaseKind     string    `json:"base_kind"`
	Cwd          string    `json:"cwd"`
	Shell        string    `json:"shell"`
	CreatedAt    time.Time `json:"created_at"`
	PID          int       `json:"pid"`
	ProtoVersion int       `json:"proto_version"`
}

// Entry 是扫描结果里的一条。State 为 StateBroken 时 Err 说明是怎么坏的。
type Entry struct {
	ID    string
	Meta  Meta
	State State
	Err   error
}

// Dir 返回一个会话的目录路径。
func Dir(root, id string) string { return filepath.Join(root, id) }

// MetaPath 返回会话的元数据路径。
func MetaPath(root, id string) string { return filepath.Join(Dir(root, id), "meta.json") }

// SockPath 返回会话的 unix socket 路径。
func SockPath(root, id string) string { return filepath.Join(Dir(root, id), "sock") }

// LockPath 返回会话的存活锁路径。
func LockPath(root, id string) string { return filepath.Join(Dir(root, id), "lock") }

// LogPath 返回会话的 ptyhost 日志路径。
func LogPath(root, id string) string { return filepath.Join(Dir(root, id), "ptyhost.log") }

// CheckSockPath 在 bind 之前检查 socket 路径长度。
//
// 参数：root 是会话根目录；id 是会话 id。
// 返回：超过 MaxSockPath 时返回可读错误，否则 nil。
//
// 注意：DataDir 可以被配置到任意深的路径下，而 bind 对超长路径只会给一句
// "invalid argument"，所以必须在 bind 之前自己检查。
func CheckSockPath(root, id string) error {
	p := SockPath(root, id)
	if len(p) > MaxSockPath {
		return fmt.Errorf("会话 socket 路径过长（%d 字节，上限 %d）：%s；请把 DataDir 换到更短的路径下", len(p), MaxSockPath, p)
	}
	return nil
}

// Create 建出一个会话目录（0700）。已存在时不报错。
func Create(root, id string) error {
	if err := os.MkdirAll(Dir(root, id), 0o700); err != nil {
		return fmt.Errorf("创建会话目录 %s: %w", Dir(root, id), err)
	}
	return nil
}

// WriteMeta 写入元数据（0600，整体覆盖）。
//
// 参数：root 是会话根目录；m 是元数据，m.ID 决定写到哪个会话目录下。
// 返回：编码或写入失败时报错。
//
// 注意：先写临时文件再 rename。agentd 可能在任何时刻读它；直接覆写会让读者有机会
// 读到半截 JSON，而那会被判成 StateBroken。
func WriteMeta(root string, m Meta) error {
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("编码会话元数据 %s: %w", m.ID, err)
	}
	tmp := MetaPath(root, m.ID) + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("写会话元数据 %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, MetaPath(root, m.ID)); err != nil {
		return fmt.Errorf("落定会话元数据 %s: %w", MetaPath(root, m.ID), err)
	}
	return nil
}

// ReadMeta 读一个会话的元数据。
//
// 返回：解析失败或文件不存在时报错，调用方据此判 StateBroken。
func ReadMeta(root, id string) (Meta, error) {
	body, err := os.ReadFile(MetaPath(root, id))
	if err != nil {
		return Meta{}, fmt.Errorf("读会话元数据 %s: %w", MetaPath(root, id), err)
	}
	var m Meta
	if err := json.Unmarshal(body, &m); err != nil {
		return Meta{}, fmt.Errorf("解析会话元数据 %s: %w", MetaPath(root, id), err)
	}
	return m, nil
}

// Scan 扫描根目录下的全部会话，逐个判活。
//
// 参数：root 是会话根目录；不存在时返回空结果而不报错（首次启动就是这样）。
// 返回：每个会话一条 Entry，含三态结论；只有 IO 故障才返回错误。
//
// 注意：本函数不删任何东西。StateDead 的目录由调用方决定何时清。
func Scan(root string) ([]Entry, error) {
	items, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("读会话根目录 %s: %w", root, err)
	}
	out := make([]Entry, 0, len(items))
	for _, it := range items {
		if !it.IsDir() {
			continue
		}
		id := it.Name()
		held, err := prochost.IsLocked(LockPath(root, id))
		if err != nil {
			// 试锁失败时既不能判活也不能判死，宁可留一个要人看的条目，也不要删掉
			// 一个可能还活着的会话。
			out = append(out, Entry{ID: id, State: StateBroken, Err: fmt.Errorf("试锁失败: %w", err)})
			continue
		}
		if !held {
			out = append(out, Entry{ID: id, State: StateDead})
			continue
		}
		m, err := ReadMeta(root, id)
		if err != nil {
			out = append(out, Entry{ID: id, State: StateBroken, Err: err})
			continue
		}
		out = append(out, Entry{ID: id, Meta: m, State: StateLive})
	}
	return out, nil
}

// Remove 删掉一个会话目录。不存在时不报错（幂等）。
//
// 注意：调用方必须先确认该会话已死（Scan 报 StateDead）。删掉一个还活着的会话目录
// 不会杀死它的进程，只会让它变成谁也找不到的孤儿。
func Remove(root, id string) error {
	if err := os.RemoveAll(Dir(root, id)); err != nil {
		return fmt.Errorf("删除会话目录 %s: %w", Dir(root, id), err)
	}
	return nil
}
