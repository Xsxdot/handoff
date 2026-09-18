// Package bind 是移动端 App 的 gomobile 绑定面（B369）。
//
// 职责：把 internal/mobilecore 的连接核能力收敛成 gomobile 可导出的最小面
// （配对、回环源、配对清单、会话 cookie 桥接、切机、关），编为 Android AAR /
// iOS XCFramework。壳（Kotlin/Swift）只经这里调用，协议零重实现。
//
// 边界：本包不含任何协议逻辑；一切拨号/选路/兑换复用主模块 internal/mobilecore。
// 导出面最小 = 绑定面积最小，且**不含 Token/Dial**（回环门禁承重属性：绑定面
// 不得暴露绕过 cookie 闸的方法）。
package bind

import (
	"context"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/mobilecore"
)

// coreAPI 是绑定面对连接核的窄消费面（duck typing）。四方法 = contract §3.2
// 已冻结的 Core 导出面，`*mobilecore.Core` 直接满足。
type coreAPI interface {
	Pair(ctx context.Context, bundleJSON string) (mobilecore.PairResult, error)
	Origin(machine string) (string, error)
	MachineNames() []string
	Close() error
}

var (
	log = slog.Default()
	// core 指向真实连接核；测试用 swapCore 注入替身。
	core coreAPI = mobilecore.New(nil, log)
)

// Pair 解析一份配对 bundle 载荷并登记其中的机器，返回各机器的 webview 源。
// 离线机器标记 online=false（部分 bundle，不整单失败）。
func Pair(bundleJSON string) (mobilecore.PairResult, error) {
	log.Info("绑定面收到配对请求", "payload_bytes", len(bundleJSON))
	res, err := core.Pair(context.Background(), bundleJSON)
	if err != nil {
		log.Error("绑定面配对失败", "cause", err)
		return mobilecore.PairResult{}, err
	}
	log.Info("绑定面配对完成", "machines", len(res.Machines))
	return res, nil
}

// Origin 返回某台机器 webview 应加载的 loopback 源（形如 http://127.0.0.1:<port>/）。
// 未配对机器返回 error，不返回空串冒充成功。
func Origin(machine string) (string, error) {
	origin, err := core.Origin(machine)
	if err != nil {
		log.Error("绑定面取回环源失败", "machine", machine, "cause", err)
		return "", err
	}
	return origin, nil
}

// MachineNames 返回已登记机器名（设置页配对清单）。
func MachineNames() []string {
	names := core.MachineNames()
	log.Debug("绑定面取配对清单", "count", len(names))
	return names
}

// Close 收掉全部回环反代与客户端（幂等）。
func Close() error {
	if err := core.Close(); err != nil {
		log.Error("绑定面关闭失败", "cause", err)
		return err
	}
	log.Info("绑定面已关闭")
	return nil
}
