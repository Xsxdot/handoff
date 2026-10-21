// skilled.go —— 熟练工派发的客户端侧（B452 S5/S8）：按机取任务、等最后一回合的终态、产物下载与安全解包。
//
// 职责：
//   - AttachOn：GET /api/tasks/{id}[?machine=M]（F15），受理机按机转发；
//   - WaitFinal：无 cursor、可重连的「最后一回合终态」等待，供 dispatch --wait 与 wait --final；
//   - Artifacts / ExtractArtifacts：GET /api/tasks/{id}/artifacts（F16）与客户端安全解包。
//
// 边界：不读写 ~/.handoff/cursors（与人手敲的 wait 互不推水位，理由同 StreamEventsOnce）；
// 不改变远端任务状态；超时只结束等待、不停任务。
package client

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// ArtifactsMaxExtractBytes 是客户端解包的总字节上限，与服务端打包上限（512 MiB）同值——
// 防一个压缩炸弹把本机磁盘写满。
const ArtifactsMaxExtractBytes int64 = 512 << 20

// ErrArtifactsUnsupported 表示对端 agentd 不认识 /api/tasks/{id}/artifacts（版本早于 B452）。
//
// 404 判过旧的前提与 ErrBundleUnsupported 相同：调用方必须先用一次成功的 AttachOn 证明
// 任务存在，这里的 404 才只能来自路由缺失。
var ErrArtifactsUnsupported = errors.New("对端 agentd 不支持产物下载（/api/tasks/{id}/artifacts），请升级")

// machineQuery 拼 ?machine=M；空串返回空串（与不带该参数逐字节同形）。
func machineQuery(machine string) string {
	if machine == "" {
		return ""
	}
	return "?machine=" + url.QueryEscape(machine)
}

// AttachOn 是 Attach 的按机变体（F15）：machine 非空时由受理机转发到该机取任务快照。
func (c *Client) AttachOn(ctx context.Context, taskID, machine string) (*AttachInfo, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/tasks/"+url.PathEscape(taskID)+machineQuery(machine), nil)
	if err != nil {
		return nil, fmt.Errorf("任务详情请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("任务详情", resp)
	}
	var info AttachInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("解析任务详情响应: %w", err)
	}
	return &info, nil
}

// FinalOutcome 是一次终态等待的结论。
type FinalOutcome struct {
	// Task 是等待期间最近一次取到的任务快照（artifacts_dir、target 等身份字段取自它）。
	Task proto.Task
	// Event 是判定事件：completed / turn_failed / failed；无回合结果就归档时为 archived（或零值）。
	Event proto.Event
	// Completed 为 true 当且仅当最后一回合以 completed 告终。
	Completed bool
	// FinalText 是 completed.final_text 原文；HasFinalText=false 表示执行器没给（老 adapter）。
	FinalText    string
	HasFinalText bool
	// Reason 是非完成时给人读的原因（fail_reason 原文，或「未完成就已归档」）。
	Reason string
	// Pending 非空表示任务停在需人答的工单上（B460 F1/F4：取自快照 pending_tickets，
	// 已被审批链处理的工单不在其中）。此时 Event/Completed 无意义，调用方以退出码 5 交回人。
	Pending []proto.Ticket
}

// isTurnResult 判定事件是否是一个回合的结果（completed / turn_failed）或任务终结（failed）。
func isTurnResult(t proto.EventType) bool {
	return t == proto.EventTypeCompleted || t == proto.EventTypeTurnFailed || t == proto.EventTypeFailed
}

// turnEnded 判定任务状态是否表示「最后一回合已经结束」：回合结果已落库，答案在历史里。
func turnEnded(s proto.TaskState) bool {
	return s == proto.TaskStateWaitingReview || s == proto.TaskStateCompleted || s == proto.TaskStateFailed
}

// outcomeFromEvent 把一条回合结果事件换算成结论；payload 解不开时按缺字段处理，不报错——
// 判定只依赖事件类型这一封闭取值，payload 只是附带的人读信息。
func outcomeFromEvent(task proto.Task, ev proto.Event) *FinalOutcome {
	out := &FinalOutcome{Task: task, Event: ev}
	switch ev.Type {
	case proto.EventTypeCompleted:
		var p struct {
			FinalText *string `json:"final_text"`
		}
		_ = json.Unmarshal(ev.Payload, &p)
		out.Completed = true
		if p.FinalText != nil {
			out.FinalText, out.HasFinalText = *p.FinalText, true
		}
	default:
		var p struct {
			FailReason string `json:"fail_reason"`
		}
		_ = json.Unmarshal(ev.Payload, &p)
		out.Reason = p.FailReason
		if out.Reason == "" {
			out.Reason = string(ev.Type)
		}
	}
	return out
}

// archivedWithoutResult 是「没有任何回合结果就归档/终结」的结论。
func archivedWithoutResult(task proto.Task, ev proto.Event) *FinalOutcome {
	return &FinalOutcome{Task: task, Event: ev, Reason: "任务未完成就已归档（没有任何回合结果）"}
}

// lastResultIn 自后向前找最后一条回合结果；没有返回 nil。
func lastResultIn(events []proto.Event) *proto.Event {
	for i := len(events) - 1; i >= 0; i-- {
		if isTurnResult(events[i].Type) {
			ev := events[i]
			return &ev
		}
	}
	return nil
}

// lastArchivedIn 自后向前找 archived 事件；没有返回零值。
func lastArchivedIn(events []proto.Event) proto.Event {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == proto.EventTypeArchived {
			return events[i]
		}
	}
	return proto.Event{}
}

// WaitFinal 等待任务**最后一回合**的终态并返回结论（S5：dispatch --wait 与 wait --final 共用）。
//
// 判定（「先读状态再定起点」）：
//   - 每轮先取权威快照 S，水位 W = S 最近事件的最大 seq；
//   - S 表示回合已结束（waiting_review / completed / failed）：答案在历史里——取最近窗口里最后
//     一条回合结果；窗口里没有就从 0 回放到 W 补找（窗口只有最近 100 条）；waiting_review 下
//     还须证明该结果属于本回合（判据见循环内注释），证明不了就退避重取快照、不下结论；
//     completed / failed 下没有本回合结果才判「未完成就归档」；
//   - 否则回合进行中：从 W 起跟事件流，seq > W 的第一条回合结果就是本回合答案。
//
// 断线（含正常关闭）一律回到「重取快照」：快照是权威，断线期间落库的结果不会漏。
//
// 待答工单（B460 F4/F5）：判据优先级 回合已结束 > 快照 pending_tickets 非空 > 继续等。进入时
// 就有待答工单 → 首轮快照即返回（不拨事件流）；跟流时 question / permission_request 事件**只作
// 唤醒**——收流、立即（不退避）重取快照，回到判据。工单是否已被审批链处理不在这里判：进入
// 待答列表的就是要人答的（审批链批准工单建单即已答，见 store.CreateAnsweredTicket）。
//
// 参数：machine 非空时快照与事件流都按机转发（F14/F15）。
// 返回：结论（Pending 非空 = 有待答工单）；ctx 到期返回 ctx.Err()（调用方转 124，任务照跑）；
// 任务不存在等永久错误原样返回。
func (c *Client) WaitFinal(ctx context.Context, taskID, machine string) (*FinalOutcome, error) {
	backoff := c.wsInitialBackoff
	emptyRechecks := 0
	// sawProgress/progressW：本次等待亲见任务「进行中」时的最大水位——确定性的回合起点。
	// candidate：上一轮 waiting_review 快照里待确认的结果（见下方判据 ③）。
	sawProgress, progressW := false, int64(0)
	var candidate *proto.Event
	// wakeFloor：上次唤醒事件的 seq。续跟起点取 max(快照水位, wakeFloor)，保证同一条唤醒事件
	// 不会因快照窗口滞后被重放回来、形成无退避的紧循环。
	wakeFloor := int64(0)
	c.log().Info("终态等待开始", "addr", c.baseURL, "task", taskID, "machine", machine)
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		snap, err := c.AttachOn(ctx, taskID, machine)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if isPermanent(err) {
				c.log().Error("终态等待失败：快照永久错误", "task", taskID, "machine", machine, "cause", err)
				return nil, err
			}
			c.log().Debug("终态等待快照暂时失败，退避重试", "task", taskID, "attempt", attempt, "cause", err)
			if err := waitArchivedRetry(ctx, &backoff, c.wsInitialBackoff, c.wsMaxBackoff, false); err != nil {
				return nil, err
			}
			continue
		}
		task := snap.Task.Task
		var w int64
		if n := len(snap.RecentEvents); n > 0 {
			w = snap.RecentEvents[n-1].Seq
		}
		if turnEnded(task.State) {
			res, rerr := c.lastResultFor(ctx, taskID, machine, snap.RecentEvents, w)
			if rerr != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if isPermanent(rerr) {
					return nil, rerr
				}
				c.log().Debug("终态等待回放暂时失败，退避重试", "task", taskID, "cause", rerr)
				if err := waitArchivedRetry(ctx, &backoff, c.wsInitialBackoff, c.wsMaxBackoff, false); err != nil {
					return nil, err
				}
				continue
			}
			// 亲见过「进行中」时的水位是确定性的回合起点：不晚于它的结果只能是上一回合的
			if res != nil && sawProgress && res.Seq <= progressW {
				res = nil
			}
			if task.State != proto.TaskStateWaitingReview {
				// completed / failed 是终态：没有本回合结果就是未完成就归档/终结
				if res == nil {
					c.log().Warn("终态等待：任务已终结但没有本回合结果", "task", taskID, "state", task.State, "watermark", w)
					return archivedWithoutResult(task, lastArchivedIn(snap.RecentEvents)), nil
				}
				out := outcomeFromEvent(task, *res)
				c.log().Info("终态等待完成：快照已有回合结果", "task", taskID, "seq", res.Seq,
					"type", res.Type, "completed", out.Completed)
				return out, nil
			}
			// waiting_review：handleResult 先迁状态、后落回合结果，快照可能落在两步之间（首回合时看不到
			// 结果；续接过的任务看到的是上一回合的结果）。只在能证明结果属于本回合时下结论：
			//   ① 本次等待亲见过进行中（上面已滤掉水位以内的）；
			//   ② 结果落库时刻不早于任务最近一次写入（迁入 waiting_review 必在结果之前写，故结果晚于它
			//      即为本回合；上一回合的结果必早于续接与本次迁入）；
			//   ③ 前后两次快照（间隔一次退避）都是 waiting_review 且最后一条结果相同——覆盖结果落库后
			//      又有写入顶高 updated_at（如清扫作废工单）的情形；两步之间的窗口是同一 goroutine 里
			//      相邻的两次写库，一次退避足以越过。
			if res != nil && (sawProgress || !res.CreatedAt.Before(task.UpdatedAt) || (candidate != nil && candidate.Seq == res.Seq)) {
				out := outcomeFromEvent(task, *res)
				c.log().Info("终态等待完成：本回合结果已确认", "task", taskID, "seq", res.Seq,
					"type", res.Type, "completed", out.Completed, "confirmed_by_resnapshot", candidate != nil && !sawProgress)
				return out, nil
			}
			candidate = res
			c.log().Info("终态等待：waiting_review 下尚无可证明属于本回合的结果，退避重取快照", "task", taskID,
				"has_candidate", res != nil, "updated_at", task.UpdatedAt, "watermark", w)
			if err := waitArchivedRetry(ctx, &backoff, c.wsInitialBackoff, c.wsMaxBackoff, false); err != nil {
				return nil, err
			}
			continue
		}
		if len(snap.PendingTickets) > 0 {
			c.log().Info("终态等待结束：任务有待答工单，交回人", "task", taskID, "machine", machine,
				"tickets", len(snap.PendingTickets), "first_ticket", snap.PendingTickets[0].ID, "state", task.State)
			return &FinalOutcome{Task: task, Pending: snap.PendingTickets}, nil
		}
		candidate = nil
		sawProgress = true
		if w > progressW {
			progressW = w
		}

		var out *FinalOutcome
		woke := false
		start := w
		if wakeFloor > start {
			start = wakeFloor
		}
		from := start
		started := time.Now()
		streamErr := c.streamOnceOn(ctx, taskID, machine, start, nil, func(ev proto.Event) error {
			// B460 修复轮 1 N1：起点及以前的事件已由快照覆盖（或是本次已处理过的唤醒），服务端
			// 若重放回来一律忽略——旧唤醒事件会造成无退避的重取快照紧循环，旧回合结果会冒充
			// 本回合结论。
			if ev.Seq <= start {
				c.log().Debug("终态等待忽略起点以前的重放事件", "task", taskID, "seq", ev.Seq,
					"type", ev.Type, "start", start)
				return nil
			}
			// 只在内存推进水位：绝不 writeCursor（见文件头边界）
			from = ev.Seq
			switch {
			case isTurnResult(ev.Type):
				out = outcomeFromEvent(task, ev)
				return errStopStream
			case ev.Type == proto.EventTypeArchived:
				out = archivedWithoutResult(task, ev)
				return errStopStream
			case ev.Type == proto.EventTypeQuestion || ev.Type == proto.EventTypePermissionRequest:
				// 只作唤醒：工单是否仍待答以快照为准（可能已被审批链或人答掉）
				woke = true
				wakeFloor = ev.Seq
				return errStopStream
			}
			return nil
		})
		lived := time.Since(started)
		if out != nil {
			c.log().Info("终态等待完成：事件流收到回合结果", "task", taskID, "machine", machine,
				"seq", out.Event.Seq, "type", out.Event.Type, "completed", out.Completed)
			return out, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if woke {
			c.log().Debug("终态等待被工单事件唤醒，立即重取快照", "task", taskID, "machine", machine, "seq", wakeFloor)
			backoff = c.wsInitialBackoff
			continue
		}
		switch {
		case errors.Is(streamErr, errArchived):
			// 正常关闭只是线索：回查快照。首次立刻复查，之后按退避（防「一帧不收反复正常关闭」的紧循环）
			if from != w {
				emptyRechecks = 0
			}
			emptyRechecks++
			if emptyRechecks == 1 {
				continue
			}
		case streamErr == nil:
			// 回调没收手却正常返回：协议异常，回查快照
		case isPermanent(streamErr):
			c.log().Error("终态等待失败：事件流永久错误", "task", taskID, "machine", machine, "cause", streamErr)
			return nil, streamErr
		default:
			c.log().Debug("终态等待事件流断开，重取快照", "task", taskID, "from_seq", from, "cause", streamErr)
		}
		if err := waitArchivedRetry(ctx, &backoff, c.wsInitialBackoff, c.wsMaxBackoff,
			lived >= c.wsStableAfter); err != nil {
			return nil, err
		}
	}
}

// lastResultFor 取快照里（窗口没有时从 0 回放到水位 w）最后一条回合结果；没有返回 nil。
// 回放在到达 w 前被正常关闭时用已收到的。是否属于本回合由调用方判。
func (c *Client) lastResultFor(ctx context.Context, taskID, machine string, recent []proto.Event, w int64) (*proto.Event, error) {
	if ev := lastResultIn(recent); ev != nil {
		return ev, nil
	}
	var last *proto.Event
	err := c.streamOnceOn(ctx, taskID, machine, 0, nil, func(ev proto.Event) error {
		if ev.Seq > w {
			return errStopStream
		}
		if isTurnResult(ev.Type) {
			cp := ev
			last = &cp
		}
		if ev.Seq >= w {
			return errStopStream
		}
		return nil
	})
	if err != nil && !errors.Is(err, errArchived) {
		return nil, err
	}
	if last != nil {
		c.log().Info("终态等待：回放补找到回合结果", "task", taskID, "seq", last.Seq, "type", last.Type)
	}
	return last, nil
}

// Artifacts 取任务产物包（F16）。
//
// 返回：200 → tar.gz 字节流（调用方负责 Close）；204 → (nil, nil) 表示无产物；
// 404 → ErrArtifactsUnsupported（调用方须已证明任务存在）；其余为故障。
func (c *Client) Artifacts(ctx context.Context, taskID, machine string) (io.ReadCloser, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/tasks/"+url.PathEscape(taskID)+"/artifacts"+machineQuery(machine), nil)
	if err != nil {
		return nil, fmt.Errorf("产物下载请求: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		c.log().Info("产物包响应就绪", "task", taskID, "machine", machine, "content_length", resp.ContentLength)
		return resp.Body, nil
	case http.StatusNoContent:
		resp.Body.Close()
		c.log().Info("任务无产物", "task", taskID, "machine", machine)
		return nil, nil
	case http.StatusNotFound:
		resp.Body.Close()
		c.log().Warn("对端不支持产物下载，按版本过旧处理", "task", taskID, "machine", machine)
		return nil, ErrArtifactsUnsupported
	default:
		defer resp.Body.Close()
		return nil, c.httpError("产物下载", resp)
	}
}

// ExtractArtifacts 把产物 tar.gz 安全解到 dest（调用方新建的空目录）。
//
// 规则：条目只收普通文件与目录；名字带 ..、绝对路径或其他类型（符号链接、设备…）一律拒收；
// 总字节超 ArtifactsMaxExtractBytes 拒收。任何一条不满足都**整次失败并删除 dest**——
// 零文件落地，不留半截（流不可倒带，故以「失败即整体删除」兑现）。
func ExtractArtifacts(r io.Reader, dest string) (err error) {
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dest)
		}
	}()
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("产物包不是 gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("读产物包: %w", err)
		}
		rel, err := safeArtifactPath(hdr.Name)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("建产物目录 %s: %w", rel, err)
			}
		case tar.TypeReg:
			total += hdr.Size
			if total > ArtifactsMaxExtractBytes {
				return fmt.Errorf("产物包解出总量超过上限 %d 字节，拒收", ArtifactsMaxExtractBytes)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return fmt.Errorf("建产物目录 %s: %w", rel, err)
			}
			if err := writeArtifactFile(target, tr, hdr.Size); err != nil {
				return fmt.Errorf("写产物文件 %s: %w", rel, err)
			}
		default:
			return fmt.Errorf("产物包含非普通条目 %q（类型 %q），拒收", hdr.Name, string(hdr.Typeflag))
		}
	}
}

// safeArtifactPath 校验 tar 条目名：相对、不含上跳；返回清洗后的 / 分隔相对路径。
func safeArtifactPath(name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "/") || filepath.IsAbs(filepath.FromSlash(name)) {
		return "", fmt.Errorf("产物包含绝对路径条目 %q，拒收", name)
	}
	clean := path.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return "", fmt.Errorf("产物包含上跳条目 %q，拒收", name)
	}
	if clean == "." {
		return "", fmt.Errorf("产物包含空条目名 %q，拒收", name)
	}
	return clean, nil
}

// writeArtifactFile 以 0600 新建文件并写入恰好 size 字节。
func writeArtifactFile(target string, r io.Reader, size int64) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.CopyN(f, r, size); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
