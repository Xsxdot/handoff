package bind

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/mobilecore"
)

// sessionCoreDouble 是 sessionCoreAPI 的可控替身：不重实现协议，只按脚本回话，
// 用来把 coreSessions 适配器自己的契约（编排顺序、失败闭合、互斥锁）逼成
// 可判 pass/fail 的断言。真实 Core 竖切归 implement（B392 contract §8）。
type sessionCoreDouble struct {
	mu            sync.Mutex
	active        string
	activateErr   error
	activateEmpty bool
	sessionErr    error
	sessionEmpty  bool
	originErr     error
	origin        string
	activateCalls []string
	originCalls   []string

	sessionEntered chan struct{} // 非 nil：Session() 进入即关闭
	sessionGate    chan struct{} // 非 nil：Session() 等它关闭再取值
	activated      chan struct{} // 非 nil：Activate() 置 active 后关闭
	activateGate   chan struct{} // 非 nil：Activate() 置 active 后等它关闭
}

func newSessionCoreDouble() *sessionCoreDouble {
	return &sessionCoreDouble{origin: "http://127.0.0.1:41000"}
}

func (d *sessionCoreDouble) Activate(_ context.Context, machine string) (mobilecore.SessionCookie, error) {
	d.mu.Lock()
	d.activateCalls = append(d.activateCalls, machine)
	err := d.activateErr
	if err == nil {
		d.active = machine
	}
	value := "sess-" + machine
	if d.activateEmpty {
		value = ""
	}
	ck := mobilecore.SessionCookie{Name: "handoff_session", Value: value, Path: "/"}
	d.mu.Unlock()
	if err != nil {
		return mobilecore.SessionCookie{}, err
	}
	if d.activated != nil {
		close(d.activated)
	}
	if d.activateGate != nil {
		<-d.activateGate
	}
	return ck, nil
}

func (d *sessionCoreDouble) Session() (mobilecore.SessionCookie, error) {
	if d.sessionEntered != nil {
		close(d.sessionEntered)
	}
	if d.sessionGate != nil {
		<-d.sessionGate
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sessionErr != nil {
		return mobilecore.SessionCookie{}, d.sessionErr
	}
	if d.active == "" {
		return mobilecore.SessionCookie{}, errors.New("无活动会话")
	}
	value := "sess-" + d.active
	if d.sessionEmpty {
		value = ""
	}
	return mobilecore.SessionCookie{Name: "handoff_session", Value: value, Path: "/"}, nil
}

func (d *sessionCoreDouble) ActiveMachine() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.active
}

func (d *sessionCoreDouble) Origin(machine string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.originCalls = append(d.originCalls, machine)
	if d.originErr != nil {
		return "", d.originErr
	}
	return d.origin, nil
}

// TestCoreSessionsSwitchMachineSequence：切机必须先 Activate 再 Origin，返回核侧 origin。
func TestCoreSessionsSwitchMachineSequence(t *testing.T) {
	d := newSessionCoreDouble()
	a := newCoreSessions(d)
	origin, err := a.SwitchMachine("A")
	if err != nil {
		t.Fatalf("切机 A: %v", err)
	}
	if origin != d.origin {
		t.Fatalf("切机应返回核侧 origin: got=%q want=%q", origin, d.origin)
	}
	if len(d.activateCalls) != 1 || d.activateCalls[0] != "A" {
		t.Fatalf("应先 Activate(A): %v", d.activateCalls)
	}
	if len(d.originCalls) != 1 || d.originCalls[0] != "A" {
		t.Fatalf("应再 Origin(A): %v", d.originCalls)
	}
}

// TestCoreSessionsSwitchMachineFailsClosed：Activate 失败 / 兑换空值 / Origin 失败，
// 一律返回 error 且不吐 origin；Activate 失败时根本不该走到 Origin。
func TestCoreSessionsSwitchMachineFailsClosed(t *testing.T) {
	t.Run("activate 失败", func(t *testing.T) {
		d := newSessionCoreDouble()
		d.activateErr = errBoom
		origin, err := newCoreSessions(d).SwitchMachine("A")
		if err == nil || origin != "" {
			t.Fatalf("Activate 失败必须返回 (\"\", err): origin=%q err=%v", origin, err)
		}
		if len(d.originCalls) != 0 {
			t.Fatalf("Activate 失败后不得调用 Origin: %v", d.originCalls)
		}
	})
	t.Run("兑换空 cookie", func(t *testing.T) {
		d := newSessionCoreDouble()
		d.activateEmpty = true
		origin, err := newCoreSessions(d).SwitchMachine("A")
		if err == nil || origin != "" {
			t.Fatalf("兑换空值必须返回 (\"\", err): origin=%q err=%v", origin, err)
		}
		if len(d.originCalls) != 0 {
			t.Fatalf("空 cookie 后不得调用 Origin: %v", d.originCalls)
		}
	})
	t.Run("origin 失败", func(t *testing.T) {
		d := newSessionCoreDouble()
		d.originErr = errBoom
		origin, err := newCoreSessions(d).SwitchMachine("A")
		if err == nil || origin != "" {
			t.Fatalf("Origin 失败必须返回 (\"\", err): origin=%q err=%v", origin, err)
		}
	})
}

// TestCoreSessionsSessionCookieRejectsWrongMachine：只返回所请求机器当前有效会话；
// 错机、无活动会话、空值、核侧报错均 error，绝不吐当前另一台机器的 cookie。
func TestCoreSessionsSessionCookieRejectsWrongMachine(t *testing.T) {
	d := newSessionCoreDouble()
	a := newCoreSessions(d)

	if _, err := a.SessionCookie("A"); err == nil {
		t.Fatal("无活动会话时必须报错")
	}
	d.active = "B"
	if v, err := a.SessionCookie("A"); err == nil || v != "" {
		t.Fatalf("错机必须返回 (\"\", err): value=%q err=%v", v, err)
	}
	d.active = "A"
	if v, err := a.SessionCookie("A"); err != nil || v != "sess-A" {
		t.Fatalf("活动机 A 应返回其 cookie: value=%q err=%v", v, err)
	}
	d.sessionEmpty = true
	if v, err := a.SessionCookie("A"); err == nil || v != "" {
		t.Fatalf("空 cookie 必须返回 (\"\", err): value=%q err=%v", v, err)
	}
	d.sessionEmpty = false
	d.sessionErr = errBoom
	if v, err := a.SessionCookie("A"); err == nil || v != "" {
		t.Fatalf("核侧报错必须上抛: value=%q err=%v", v, err)
	}
}

// TestCoreSessionsLockSerializesSwitchAndRead 证明适配器锁让「校验活动机 + 读 cookie」
// 与「切机」互不穿插：去掉锁时，读 A 会在阻塞期间被切到 B 并读到 B 的 cookie。
func TestCoreSessionsLockSerializesSwitchAndRead(t *testing.T) {
	d := newSessionCoreDouble()
	d.active = "A"
	d.sessionEntered = make(chan struct{})
	d.sessionGate = make(chan struct{})
	d.activated = make(chan struct{})
	d.activateGate = make(chan struct{})
	a := newCoreSessions(d)

	type readResult struct {
		value string
		err   error
	}
	readDone := make(chan readResult, 1)
	go func() {
		v, err := a.SessionCookie("A")
		readDone <- readResult{v, err}
	}()

	select {
	case <-d.sessionEntered:
	case <-time.After(time.Second):
		t.Fatal("读请求未进入 Session()")
	}

	switchDone := make(chan error, 1)
	go func() {
		_, err := a.SwitchMachine("B")
		switchDone <- err
	}()

	select {
	case <-d.activated:
		// 无锁时切机会在读完成前就改活动机；下面读到的将是 B 的 cookie。
		t.Log("切机竞态进入 Activate（无锁则复现串机）")
	case <-time.After(100 * time.Millisecond):
		// 有锁：切机堵在适配器锁上，符合预期。
	}

	close(d.sessionGate)
	r := <-readDone
	if r.err != nil {
		t.Fatalf("读 A 会话失败: %v（若无锁，切机已改活动机导致错机拒绝）", r.err)
	}
	if r.value != "sess-A" {
		t.Fatalf("读到非 A 的 cookie: %q（适配器锁未串行化切机与读）", r.value)
	}
	close(d.activateGate)
	if err := <-switchDone; err != nil {
		t.Fatalf("切机 B 失败: %v", err)
	}
}

// TestDefaultRuntimeSharesOneCore：生产默认装配必须让配对面、会话面与 Close 观察
// 同一个真实 *mobilecore.Core（B392 §4.1 组装不变量）。另起第二个 Core 或恢复占位即红。
func TestDefaultRuntimeSharesOneCore(t *testing.T) {
	if core != coreAPI(liveCore) {
		t.Fatal("配对面未指向唯一真实 Core")
	}
	cs, ok := sessions.(*coreSessions)
	if !ok {
		t.Fatalf("会话面生产装配不是 coreSessions: %T", sessions)
	}
	if cs.core != sessionCoreAPI(liveCore) {
		t.Fatal("会话适配器未指向唯一真实 Core（第二个 Core 或占位）")
	}
}
