// 本文件实现 relay 隧道的后台预热。
//
// 职责：周期性地对每台 relay 机器主动建隧道，让探活拿到的是一条已经就绪的通道
//
// 边界：
//   - **预热只保证隧道，不代表可达**：隧道通了但对端 agentd 没起，机器照样是
//     「已断开」。两个判据不合并——合并会让「网络不通」和「服务没起」这两种
//     完全不同的故障显示成同一句话
//   - 不碰直连机器：它们没有隧道可预热
//   - 不占探活预算：探活只有 3s，而首次建隧道要 WSS 拨号 + CONNECT + E2E 握手
package targetclient

import (
	"context"
	"time"
)

// 预热节奏。tick 与 mirrorDiscoveryTick 同量级：预热是补漏，不是心跳。
const (
	warmTick           = 30 * time.Second
	warmBackoffInitial = 1 * time.Second
	warmBackoffMax     = 60 * time.Second
)

// warmState 是单台机器的退避状态。
//
// 退避**各算各的**：一台长期离线的机器不能把其余机器的重试节奏一起拖慢。
type warmState struct {
	backoff time.Duration
	nextAt  time.Time
}

// Warm 跑预热循环，阻塞直到 ctx 取消。
//
// 参数：
//   - ctx: 生命周期；取消即返回
//
// 注意：
//   - 只对 relay 形态的 target 生效
//   - 单台失败按 1s→60s 指数退避，退避期内跳过该台，不影响其余
//   - 新增的机器由下一轮扫到；删除的机器自然不再出现在 Names() 里
func (p *Pool) Warm(ctx context.Context) {
	p.log.Info("relay 隧道预热循环启动", "tick", p.warmTick.String())
	states := make(map[string]*warmState)
	t := time.NewTicker(p.warmTick)
	defer t.Stop()

	p.warmOnce(ctx, states)
	for {
		select {
		case <-ctx.Done():
			p.log.Info("relay 隧道预热循环退出", "reason", "上下文取消")
			return
		case <-t.C:
			p.warmOnce(ctx, states)
		}
	}
}

// warmOnce 跑一轮预热：对每台处于「可以重试」状态的 relay 机器建一次隧道。
func (p *Pool) warmOnce(ctx context.Context, states map[string]*warmState) {
	targets := p.conf().Targets
	now := time.Now()
	warmed, skipped, failed := 0, 0, 0

	for _, name := range p.Names() {
		t := targets[name]
		if !t.IsRelay() {
			continue // 直连没有隧道可预热
		}
		if st, ok := states[name]; ok && now.Before(st.nextAt) {
			skipped++
			continue
		}
		// 每台独立限时：一台黑洞机器不能把整轮拖住
		callCtx, cancel := context.WithTimeout(ctx, p.warmTick)
		err := p.ensureTunnel(callCtx, name)
		cancel()
		if err != nil {
			failed++
			st := states[name]
			if st == nil {
				st = &warmState{backoff: p.warmBackoffInitial}
				states[name] = st
			} else {
				st.backoff *= 2
				if st.backoff > p.warmBackoffMax {
					st.backoff = p.warmBackoffMax
				}
			}
			st.nextAt = now.Add(st.backoff)
			p.log.Warn("relay 隧道预热失败，等待后重试", "target", name, "node", t.Node,
				"backoff_ms", st.backoff.Milliseconds(), "cause", err)
			continue
		}
		warmed++
		if _, had := states[name]; had {
			// 恢复了：清掉退避，下一轮回到正常节奏
			delete(states, name)
			p.log.Info("relay 隧道预热恢复", "target", name, "node", t.Node)
		}
	}
	p.log.Debug("relay 隧道预热完成一轮", "warmed", warmed, "skipped", skipped, "failed", failed)
}

// ensureTunnel 对一台机器建隧道；测试可用 p.ensure 替换掉真实拨号。
func (p *Pool) ensureTunnel(ctx context.Context, name string) error {
	if p.ensure != nil {
		return p.ensure(ctx, name)
	}
	return p.realEnsure(ctx, name)
}

// realEnsure 是生产实现：取出（必要时构造）该机器的 Dialer 并建隧道。
func (p *Pool) realEnsure(ctx context.Context, name string) error {
	if _, err := p.For(name); err != nil {
		return err
	}
	p.mu.Lock()
	e, ok := p.entries[name]
	p.mu.Unlock()
	if !ok || e.dialer == nil {
		// 直连条目没有 dialer；warmOnce 已过滤过，走到这里说明配置刚变形态，
		// 下一轮自然纠正，不当失败处理
		return nil
	}
	return e.dialer.Ensure(ctx)
}
