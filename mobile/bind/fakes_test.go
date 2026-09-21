package bind

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Xsxdot/handoff/internal/mobilecore"
)

// fakeCore 是 coreAPI 的替身：只记录「绑定层如实转发」，不重实现协议。
type fakeCore struct {
	mu              sync.Mutex
	pairErr         error
	originErr       error
	closeErr        error
	pairCalls       int
	lastBundleBytes int
	lastMachine     string
}

func newFakeCore() *fakeCore { return &fakeCore{} }

func (f *fakeCore) Pair(_ context.Context, bundleJSON string) (mobilecore.PairResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pairCalls++
	f.lastBundleBytes = len(bundleJSON)
	if f.pairErr != nil {
		return mobilecore.PairResult{}, f.pairErr
	}
	return mobilecore.PairResult{Machines: []mobilecore.PairedMachine{
		{Name: "devbox", Origin: "http://127.0.0.1:41001", Online: true},
	}}, nil
}

func (f *fakeCore) Origin(machine string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastMachine = machine
	if f.originErr != nil {
		return "", f.originErr
	}
	return "http://127.0.0.1:41001", nil
}

func (f *fakeCore) MachineNames() []string { return []string{"devbox"} }

func (f *fakeCore) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeErr
}

// fakeSessions 是 sessionAPI 的替身：记录切机/取 cookie 的调用序与当前罐内容，
// 用于锁「切机清罐、一罐只装一机、失败上抛」三条语义（不重实现协议）。
type fakeSessions struct {
	mu           sync.Mutex
	current      string // 当前罐里装着的机器名；空=罐空
	cookieValue  string
	cookieErr    error
	switchErr    error
	cookieCalls  int
	switchCalls  int
	lastMachine  string
	switchedFrom string
	port         int
}

func newFakeSessions() *fakeSessions { return &fakeSessions{} }

func (f *fakeSessions) SessionCookie(machine string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cookieCalls++
	f.lastMachine = machine
	if f.cookieErr != nil {
		return "", f.cookieErr
	}
	if f.current != machine {
		// 一罐只装一机：未切到该机前不得返回它的 cookie。
		return "", errors.New("会话罐里不是该机（需先切机）")
	}
	return f.cookieValue, nil
}

func (f *fakeSessions) SwitchMachine(machine string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.switchCalls++
	f.lastMachine = machine
	if f.switchErr != nil {
		return "", f.switchErr
	}
	f.switchedFrom = f.current
	f.current = machine               // 清旧罐并重兑换
	f.cookieValue = "sess-" + machine // 每机独立 cookie
	f.port++
	return fmt.Sprintf("http://127.0.0.1:%d", 42000+f.port), nil
}

func swapCore(c coreAPI) (restore func()) {
	old := core
	core = c
	return func() { core = old }
}

func swapSessions(s sessionAPI) (restore func()) {
	old := sessions
	sessions = s
	return func() { sessions = old }
}

var errBoom = errors.New("核侧故障")
