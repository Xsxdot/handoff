package bind

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Xsxdot/handoff/internal/mobilecore"
)

// B392：会话入口的生产适配。绑定面导出的 SessionCookie / SwitchMachine 是稳定的
// 壳契约（B386 后七函数），本文件把它们接到 bind.go 的 liveCore——唯一真实
// *mobilecore.Core。适配器只编排核侧既有能力，不复制 S2 协议。

// sessionCoreAPI 是会话适配器消费的核面（窄消费，duck typing）：切机、活动机、
// 当前会话与回环源。*mobilecore.Core 直接满足。
type sessionCoreAPI interface {
	Activate(ctx context.Context, machine string) (mobilecore.SessionCookie, error)
	Session() (mobilecore.SessionCookie, error)
	ActiveMachine() string
	Origin(machine string) (string, error)
}

// coreSessions 是 sessionAPI 的生产适配器。
//
// 一把私有锁让「切机」与「校验活动机 + 取 cookie」互不穿插：切换期间的并发
// SessionCookie 不会读到另一台机器的 cookie（B392 §4.2/§4.3）。
type coreSessions struct {
	mu   sync.Mutex
	core sessionCoreAPI
}

// newCoreSessions 把唯一真实 Core 导成会话适配视图。
func newCoreSessions(c sessionCoreAPI) *coreSessions { return &coreSessions{core: c} }

// SessionCookie 实现 sessionAPI：先校验当前活动机就是所请求机器，再取核内会话值，
// 只返回 cookie value（string-only 导出面，绝不返回 token / origin / 内部 DTO）。
func (a *coreSessions) SessionCookie(machine string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.core.ActiveMachine() != machine {
		return "", fmt.Errorf("当前活动机器不是 %q，拒绝返回其会话 cookie", machine)
	}
	ck, err := a.core.Session()
	if err != nil {
		return "", err
	}
	if ck.Value == "" {
		return "", errors.New("核内会话 cookie 值为空")
	}
	return ck.Value, nil
}

// SwitchMachine 实现 sessionAPI：Activate（切机清旧槽并重兑换；同机命中缓存）→
// 校验兑换非空 → Origin。只有两步都成功才返回目标 loopback 源；任一步失败都不
// 返回 origin（壳不得导航），也不伪造回滚。
func (a *coreSessions) SwitchMachine(machine string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ck, err := a.core.Activate(context.Background(), machine)
	if err != nil {
		return "", err
	}
	if ck.Value == "" {
		return "", errors.New("切机兑换返回的会话 cookie 值为空")
	}
	origin, err := a.core.Origin(machine)
	if err != nil {
		return "", err
	}
	return origin, nil
}
