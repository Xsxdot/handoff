// Package bind 是移动端 App 的 gomobile 绑定面（B369）。
//
// 职责：把 internal/mobilecore 的连接核能力收敛成 gomobile 可导出的最小面
// （配对、回环源、配对清单、会话 cookie 桥接、切机、关），编为 Android AAR /
// iOS XCFramework。壳（Kotlin/Swift）只经这里调用，协议零重实现。
//
// 边界：本包不含任何协议逻辑；一切拨号/选路/兑换复用主模块 internal/mobilecore。
// 导出面最小 = 绑定面积最小，且**不含 Token/Dial**（回环门禁承重属性：绑定面
// 不得暴露绕过 cookie 闸的方法）。
//
// 形状约束（B386）：导出函数的参数与返回值只许 gomobile 支持的形状——
// 基本类型（string/bool/int）、error、以及**本包内定义**的结构体指针。
// 不许 []T（除 []byte）、不许跨包类型（会被 gomobile 静默跳过而不报错）；
// 列表用「计数 + 按索引取指针」表达。`mobile/bind/gobind_surface_test.go`
// 拿真 gobind 产物钉住这条：产物里出现 skipped function/field 即红。
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

// Pair 解析一份配对 bundle 载荷并登记其中的机器（含离线机，不整单失败）。
// 配对结果经 MachineCount / MachineAt 读取。
func Pair(bundleJSON string) error {
	log.Info("绑定面收到配对请求", "payload_bytes", len(bundleJSON))
	res, err := core.Pair(context.Background(), bundleJSON)
	if err != nil {
		log.Error("绑定面配对失败", "cause", err)
		return err
	}
	log.Info("绑定面配对完成", "machines", len(res.Machines))
	return nil
}

// MachineCount 返回已登记机器数（含离线机），供设置页配对清单。
func MachineCount() int {
	names := core.MachineNames()
	log.Debug("绑定面取配对清单", "count", len(names))
	return len(names)
}

// MachineAt 按索引返回一台已登记机器；越界返回 nil。
//
// Online 取核侧 Origin 是否成功——这是**真值不是猜**：
// mobilecore 对未配对机器报「未配对」、对离线机报「当前离线」，
// 只有在线机才返回回环源。
func MachineAt(index int) *Machine {
	names := core.MachineNames()
	if index < 0 || index >= len(names) {
		return nil
	}
	m := &Machine{Name: names[index]}
	if origin, err := core.Origin(m.Name); err == nil {
		m.Origin = origin
		m.Online = true
	}
	return m
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

// Close 收掉全部回环反代与客户端（幂等）。
func Close() error {
	if err := core.Close(); err != nil {
		log.Error("绑定面关闭失败", "cause", err)
		return err
	}
	log.Info("绑定面已关闭")
	return nil
}
