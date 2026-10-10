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

// coreAPI 是绑定面对连接核的窄消费面（duck typing）。前四方法 = contract §3.2
// 冻结的 Core 导出面；RegisterPushDevice 是 B432 新增的设备登记入口（spec
// §契约面 1 经核通路），`*mobilecore.Core` 直接满足。
type coreAPI interface {
	Pair(ctx context.Context, bundleJSON string) (mobilecore.PairResult, error)
	Origin(machine string) (string, error)
	MachineNames() []string
	RegisterPush(ctx context.Context, machine, deviceID, pushHandle string) error
	Close() error
}

var (
	log = slog.Default()
	// liveCore 是默认运行时**唯一**的真实 Core 实例（B392 §4.1 组装不变量）：
	// 配对视图（core）与会话适配视图（sessions）都从它导出，共享同一机器表、
	// 同一活动会话槽与同一生命周期。禁止再起第二个 Core——两个核会分裂机器表
	// 与活动槽，Pair 成功也保证不了 SwitchMachine 可用。
	liveCore = mobilecore.New(nil, log)
	// core 指向真实连接核（配对面视图）；测试用 swapCore 注入替身。
	core coreAPI = liveCore
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

// RegisterPushDevice 把 APNs device token 上报给指定机器（B432 缝 S1 的最后一跳）。
//
// 参数：machine 已配对机器名；deviceID 壳侧设备 id；pushHandle 是 APNs 的
// device token（十六进制串）。参数刻意命名 pushHandle 而非 token：绑定面
// 门禁禁 Token 字样（export_surface_test），且这里的 token 与 agentd 主令牌
// 是两回事——主令牌只在核手里，壳永远碰不到。
//
// 返回：核侧错误（未配对/离线/HTTP 失败）原样上抛。
// 注意：调用方（壳）不得自行拼 HTTP——SourceGuard 禁壳源码出现 URLSession。
func RegisterPushDevice(machine string, deviceID string, pushHandle string) error {
	log.Debug("绑定面收到推送登记", "machine", machine, "device", deviceID)
	if err := core.RegisterPush(context.Background(), machine, deviceID, pushHandle); err != nil {
		log.Error("绑定面推送登记失败", "machine", machine, "device", deviceID, "cause", err)
		return err
	}
	log.Info("绑定面推送登记完成", "machine", machine, "device", deviceID)
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
