// 本包收拢「按 target 形态选路构造 agentd 客户端」这唯一判据。
//
// 职责：
//   - New：一次性工厂，按 Target 是 relay 还是直连造出对应 client（CLI 用）
//   - Pool（见 pool.go）：常驻复用池，一台机器一条 relay 隧道（agentd 用）
//
// 边界：
//   - 不做任何网络请求：New 只构造，隧道由 Dialer 惰性建立或由 Warm 预热
//   - 不碰 client 的上层语义：MarkForwarded / NoRedirect 等一律由调用方链式调用
//   - 不读配置文件：调用方给什么 Target 就按什么造
//
// 为什么要有这个包：选路判据曾经存在两份——CLI 有 relay 分支，agentd 侧六处扇出
// 一处都没有，于是 relay 机器在控制台一律显示「已断开」。判据只留一份，才不会
// 有第二份从来没被写出来。
package targetclient

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/relay"
)

// ErrNoEndpoint 表示这个 target 既没有 addr 也没有 relay，无从构造客户端。
//
// config.Target.Validate 早就写着「direct target addr 不能为空」，这里是同一条
// 不变式在扇出侧的落点——扇出侧过去从没问过它。
var ErrNoEndpoint = errors.New("target 既没有 addr 也没有 relay")

// New 按 Target 形态选路，构造一个一次性的 agentd 客户端。
//
// 参数：
//   - name: target 名，只用于日志与错误文案（会原样显示给用户）
//   - t: target 配置；t.IsRelay() 为真走 relay 隧道，否则直连 t.Addr
//   - log: 日志器；nil 时用 slog.Default()
//
// 返回：
//   - client: 可直接链式 MarkForwarded()/NoRedirect()
//   - cleanup: **恒非 nil**，调用方 defer 它即可（直连形态是 no-op）
//   - err: ErrNoEndpoint（无端点）或 relay token 熵不足
//
// 注意：
//   - 不发任何网络请求；relay 隧道由 Dialer 首次用到时惰性建立
//   - 常驻场景不要用它——每次调用都会新建一条 relay 隧道，用 Pool
func New(name string, t config.Target, log *slog.Logger) (*client.Client, func(), error) {
	if log == nil {
		log = slog.Default()
	}
	noop := func() {}
	if t.IsRelay() {
		// relay 形态下 token 额外充当 E2E 的 PSK 源，弱 token = 隧道没有端到端
		// 保护。这道闸必须在建隧道之前。
		if err := relay.CheckTokenEntropy(t.Token); err != nil {
			log.Error("relay target 的 token 熵不足，拒绝构造", "target", name, "node", t.Node)
			return nil, noop, fmt.Errorf("target %s: %w", name, err)
		}
		d := relay.NewDialer(t.Relay, t.Credential, t.Node, t.Token, "", log)
		log.Info("target 走 relay 传输", "target", name, "node", t.Node, "relay_url", t.Relay)
		return client.NewRelay(d, t.Token), func() { _ = d.Close() }, nil
	}
	if t.Addr == "" {
		log.Error("target 无端点，既没有 addr 也没有 relay", "target", name)
		return nil, noop, fmt.Errorf("target %s: %w", name, ErrNoEndpoint)
	}
	log.Debug("target 走直连传输", "target", name, "addr", t.Addr)
	return client.New("http://"+t.Addr, t.Token), noop, nil
}
