// quota_rerun.go —— B453 S6/S7 门面侧：撞额度回调（orchestration.QuotaHandler 的实现）。
//
// 职责：
//   - MarkTaskQuota：把任务所用账号标无额度，并从标记前的读数摘出「撞到的窗口 / 预计恢复」
//   - 只读任务派发成功后留存原始输入（任务目录私有文件，不是契约）
//   - RerunReadonly：用留存的同一份输入、以小队为接收者在进程内重走派发链（选人 → 本机开工或
//     冻结身份转发他机），带上重跑链上已用的账号（F14）——选人谓词据此排除，每账号一次
//   - 重跑链排除谓词与链耗尽文案
//
// 边界：编排只经 QuotaHandler 调这里；选人/准入/转发全复用 handleDispatch 的既有链路，不另写一份。
// 不重试（同 forwardDispatch：重试会把「已建成但回执丢了」变成重复派发）。
package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// rerunInputFile 是任务目录里留存原始派发输入的私有文件名（plan Q9：任务私有，不是契约）。
const rerunInputFile = "rerun-input.json"

// quotaMarkCategory 是任务回合撞额度标记的类别（与检测链撞额度同一类别）。
const quotaMarkCategory = "quota_exhausted"

// rerunStripKeys 是留存输入时去掉的键：冻结身份与落点（重跑要重新选人）、接收者（重跑固定为
// 原小队）、链参数（重跑时按链重写）。计划原文、项目、形态、名称、纪律、基线、亲和原样保留。
var rerunStripKeys = []string{"carrier", "squad", "home_dir", "frozen_target", "target", "executor",
	"receiver", "rerun_of", "exclude_accounts"}

// 编译期断言：门面实现编排的撞额度回调接口。
var _ orchestration.QuotaHandler = (*Server)(nil)

// MarkTaskQuota 实现 orchestration.QuotaHandler：标任务所用账号无额度。task.Account 是派发时
// 快照的账号 id（B464 起，plan Q23）。未选账号登录、账号已删或账号域未装配 → 不标（零值 Account），
// 编排把原因写进任务失败原因。QuotaHit.Account 给人看，取账号名。
func (s *Server) MarkTaskQuota(task *proto.Task, f orchestration.QuotaFailure) orchestration.QuotaHit {
	id := strings.TrimSpace(task.Account)
	if id == "" || s.accounts == nil {
		s.log.Info("任务撞额度：载体未选账号登录（或账号域未装配），不标账号", "task", task.ID,
			"carrier", task.Carrier, "signature", f.Signature)
		return orchestration.QuotaHit{}
	}
	now := time.Now()
	var hit orchestration.QuotaHit
	name := id
	if v, err := s.accounts.Get(id); err != nil {
		s.log.Warn("任务撞额度：读账号失败，窗口与预计恢复按未知", "task", task.ID, "account", id, "cause", err)
	} else {
		name = v.Account.Name
		ws, recoverAt := accounts.QuotaHitWindows(v.Account, v.Record, now)
		for _, w := range ws {
			hit.Windows = append(hit.Windows, accounts.WindowLabel(w)+" 已用 "+
				strconv.FormatFloat(math.Round(w.UsedPercent), 'f', 0, 64)+"%")
		}
		hit.RecoverAt = recoverAt
	}
	msg := f.Text
	if r := []rune(msg); len(r) > 300 {
		msg = string(r[:300]) + "…"
	}
	if err := s.accounts.MarkQuota(id, accounts.QuotaMark{At: now, Task: task.ID, Carrier: task.Carrier,
		Machine: s.conf().Name, Category: quotaMarkCategory, Message: msg}); err != nil {
		s.log.Error("任务撞额度：标记账号失败（账号状态待下次探测纠正）", "task", task.ID, "account", id, "cause", err)
		return hit
	}
	hit.Account = name
	s.log.Info("任务撞额度已标账号无额度", "task", task.ID, "account", id, "name", name, "carrier", task.Carrier,
		"category", quotaMarkCategory, "signature", f.Signature, "windows", len(hit.Windows))
	return hit
}

// saveRerunInput 留存只读任务的原始派发输入。失败只记 Error：任务照常跑，只是撞额度时不能自动重跑
// （RerunReadonly 会如实报「没有留存输入」）。
func (s *Server) saveRerunInput(taskID string, raw []byte, req dispatchRequest, binding scheduling.Binding) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		s.log.Error("留存重跑输入失败：请求体不是 JSON 对象", "task", taskID, "cause", err)
		return
	}
	for _, k := range rerunStripKeys {
		delete(obj, k)
	}
	// 转发来的冻结请求里 model 是「有效模型」（点名值或原载体缺省）——与原载体缺省相同按未点名
	// 处理，否则重跑会把 A 的缺省模型硬塞给执行器不同的 B。
	if m, ok := obj["model"]; ok {
		var model string
		_ = json.Unmarshal(m, &model)
		if model == "" || model == s.carrierDefaultModel(binding.Carrier) {
			delete(obj, "model")
		}
	}
	if len(req.ExcludeAccounts) > 0 {
		enc, _ := json.Marshal(req.ExcludeAccounts)
		obj["exclude_accounts"] = enc
	}
	body, err := json.Marshal(obj)
	if err != nil {
		s.log.Error("留存重跑输入失败：编码", "task", taskID, "cause", err)
		return
	}
	path := filepath.Join(s.conf().DataDir, "tasks", taskID, rerunInputFile)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		s.log.Error("留存重跑输入失败：写文件", "task", taskID, "path", path, "cause", err)
		return
	}
	s.log.Info("只读任务已留存重跑输入", "task", taskID, "bytes", len(body), "chain_accounts", len(req.ExcludeAccounts))
}

// carrierDefaultModel 读载体登记的缺省模型；读不到为空串。
func (s *Server) carrierDefaultModel(carrier string) string {
	if carrier == "" || s.scheduling == nil {
		return ""
	}
	c, err := s.scheduling.Carrier(carrier)
	if err != nil {
		return ""
	}
	return c.Model
}

// RerunReadonly 实现 orchestration.QuotaHandler：用原任务留存的同一份输入，以原小队为接收者在
// 进程内重走派发链，排除重跑链上已用的账号（本任务账号；未选账号的以载体名计）。
func (s *Server) RerunReadonly(ctx context.Context, task *proto.Task, f orchestration.QuotaFailure) (orchestration.RerunOutcome, error) {
	if strings.TrimSpace(task.Squad) == "" {
		return orchestration.RerunOutcome{}, fmt.Errorf("原任务点名载体 %s 派发、不在小队里，没有可换的成员", task.Carrier)
	}
	raw, err := os.ReadFile(filepath.Join(s.conf().DataDir, "tasks", task.ID, rerunInputFile))
	if err != nil {
		s.log.Warn("撞额度重跑：读留存输入失败", "task", task.ID, "cause", err)
		return orchestration.RerunOutcome{}, fmt.Errorf("原任务没有留存派发输入（可能派发于升级前），无法自动重跑：%w", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return orchestration.RerunOutcome{}, fmt.Errorf("原任务留存的派发输入损坏，无法自动重跑：%w", err)
	}
	var prior []string
	if v, ok := obj["exclude_accounts"]; ok {
		_ = json.Unmarshal(v, &prior)
	}
	used := strings.TrimSpace(task.Account)
	if used == "" {
		used = task.Carrier
	}
	excl := dedupeStrings(append(prior, used))
	set := func(k string, v any) {
		enc, _ := json.Marshal(v)
		obj[k] = enc
	}
	set("receiver", task.Squad)
	set("exclude_accounts", excl)
	set("rerun_of", proto.TaskRef{Task: task.ID, Machine: s.conf().Name})
	body, err := json.Marshal(obj)
	if err != nil {
		return orchestration.RerunOutcome{}, fmt.Errorf("编码重跑请求: %w", err)
	}
	status, payload := s.dispatchInProcess(ctx, body)
	if status != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(payload))
		if json.Unmarshal(payload, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		s.log.Warn("撞额度重跑派发被拒", "task", task.ID, "squad", task.Squad, "status", status,
			"chain_accounts", len(excl), "cause", msg)
		return orchestration.RerunOutcome{}, errors.New(msg)
	}
	var nt proto.Task
	if err := json.Unmarshal(payload, &nt); err != nil {
		s.log.Error("撞额度重跑已派出但应答解码失败", "task", task.ID, "cause", err)
		return orchestration.RerunOutcome{}, fmt.Errorf("重跑已派出但应答解码失败（请到小队 %s 的成员机核对）：%w", task.Squad, err)
	}
	machine := nt.Target
	if self := s.conf().Name; self != "" && s.IsSelfMachine(machine) {
		machine = self
	}
	s.log.Info("只读任务撞额度重跑已派出", "task", task.ID, "new_task", nt.ID, "machine", machine,
		"carrier", nt.Carrier, "squad", task.Squad, "chain_accounts", len(excl))
	return orchestration.RerunOutcome{Ref: proto.TaskRef{Task: nt.ID, Machine: machine}, Carrier: nt.Carrier}, nil
}

// inprocWriter 是进程内调用 handleDispatch 的最小 ResponseWriter（只收状态码与体）。
type inprocWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *inprocWriter) Header() http.Header { return w.header }
func (w *inprocWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(p)
}
func (w *inprocWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

// dispatchInProcess 在进程内走一遍 POST /api/tasks（不带转发头：落点在他机时照常冻结转发）。
func (s *Server) dispatchInProcess(ctx context.Context, body []byte) (int, []byte) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/tasks", bytes.NewReader(body))
	if err != nil {
		return http.StatusInternalServerError, []byte(`{"error":"构造重跑请求失败"}`)
	}
	req.Header.Set("Content-Type", "application/json")
	w := &inprocWriter{header: http.Header{}}
	s.handleDispatch(w, req)
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.status, w.body.Bytes()
}

// rerunExcl 是重跑链排除集：账号 id（B464 起，plan Q23/W3），及无账号成员的载体名。account 把
// 载体解析成其所选登录的账号 id（空 = 未选/读不到，按载体名计）。
type rerunExcl struct {
	set     map[string]bool
	list    []string
	account func(scheduling.Carrier) string
	accept  func(scheduling.Carrier) bool // nil = 不排除
}

func (s *Server) rerunExclusion(list []string) rerunExcl {
	e := rerunExcl{set: map[string]bool{}, account: func(c scheduling.Carrier) string { return s.loginOfCarrier(c).Account }}
	for _, v := range list {
		if v = strings.TrimSpace(v); v != "" && !e.set[v] {
			e.set[v] = true
			e.list = append(e.list, v)
		}
	}
	if len(e.list) > 0 {
		e.accept = func(c scheduling.Carrier) bool { return !e.excludes(c) }
	}
	return e
}

func (e rerunExcl) active() bool { return len(e.list) > 0 }

// excludes：选了账号登录的按账号 id 判，未选的按载体名判。混版本期旧机存的是账号名，与 id 对不上
// → 最多多换回同一账号一次（plan §11 W3，接受）。
func (e rerunExcl) excludes(c scheduling.Carrier) bool {
	if a := e.account(c); a != "" {
		return e.set[a]
	}
	return e.set[c.Name]
}

// andAccept 组合两个选人谓词（nil = 不限）。
func andAccept(a, b func(scheduling.Carrier) bool) func(scheduling.Carrier) bool {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	return func(c scheduling.Carrier) bool { return a(c) && b(c) }
}

// rerunExhaustedError 是重跑链耗尽：errors.Is(ErrNoHealthy) 成立（HTTP 400 映射不变），文案点名
// 被链排除的成员。
type rerunExhaustedError struct{ msg string }

func (e *rerunExhaustedError) Error() string        { return e.msg }
func (e *rerunExhaustedError) Is(target error) bool { return target == scheduling.ErrNoHealthy }

// rerunNoMemberError 把「排除后无人可选」写成点名文案：被链排除的成员逐个列出；其余成员因准入
// 被跳过的原因（S5 文案）原样接在后面。
func (s *Server) rerunNoMemberError(err error, squad string, e rerunExcl) error {
	var names []string
	remaining := 0
	if q, qerr := s.scheduling.Squad(squad); qerr == nil {
		for _, m := range q.Members {
			c, cerr := s.scheduling.Carrier(m.Carrier)
			if cerr != nil {
				continue
			}
			if !e.excludes(c) {
				remaining++
				continue
			}
			if a := s.loginOfCarrier(c); a.Account != "" {
				label := a.Account
				if v, err := s.accounts.Get(a.Account); err == nil {
					label = v.Account.Name
				}
				names = append(names, c.Name+"（账号 "+label+"）")
			} else {
				names = append(names, c.Name+"（未选账号）")
			}
		}
	}
	excluded := "重跑链已排除：" + strings.Join(names, "、") + "（已用：" + strings.Join(e.list, "、") +
		"；每个账号在一次重跑链里只用一次）"
	s.log.Info("重跑链排除后小队无人可选", "squad", squad, "excluded", len(names), "remaining", remaining, "cause", err)
	if remaining == 0 {
		return &rerunExhaustedError{msg: "小队 " + squad + " 没有可换的成员——" + excluded}
	}
	return &rerunExhaustedError{msg: err.Error() + "；" + excluded}
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
