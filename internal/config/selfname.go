// selfname.go —— 机器自名（顶层 name:/advertise:）的词法校验与「这个名字
// 是不是我」的唯一判据（B439）。
//
// 职责：
//   - 校验 name: 的词法：非空、无空白、不含 /；local/本机 是保留字
//   - 提供自名判据与目标解析：省略输入或 == 自名 = 本机；无自名时回退地址判据
//
// 边界：
//   - 纯函数 + 只读配置字段与 targets 表；不打开账本、不做网络
//   - 自名判据只有这一处定义：agentd / ledgerstep / ledgermirror / scheduling
//     一律经它或经组装点注入的同一判据，不再各写一套（spec 问题陈述 3）
//
// 为什么门控是「有无 name:」而不是「有无 DSN」：无自名的机器没有可改成的
// 名字，此时把 local/本机/hostname 判成非本机会把人卡死在「设置 name:」之前；
// 有自名的机器，旧别名正是要杀的歧义源（spec §目标架构、plan §4.1）。
package config

import (
	"fmt"
	"strings"
)

// reservedSelfNames 是「本机」输入别名，不可用作自名——把它们当自名会让
// 「省略 = 自名」与「旧别名 = 本机」两套语义撞在一起。
var reservedSelfNames = map[string]bool{"local": true, "本机": true}

// ValidateSelfName 校验自名词法；空串表示未配置，合法。
//
// 返回：非法时返回带可行动指引的错误；合法（含空串）返回 nil。
// 导出供 init 向导（initflow）在写回前校验，与 Load 的启动期校验同源。
func ValidateSelfName(name string) error {
	if name == "" {
		return nil
	}
	if reservedSelfNames[name] {
		return fmt.Errorf("config.yaml 的 name: 不能是保留字 %q（它是「本机」的输入别名）；请改成这台机器的全队唯一名", name)
	}
	if strings.ContainsAny(name, " \t\r\n") {
		return fmt.Errorf("config.yaml 的 name: %q 不能含空白", name)
	}
	if strings.Contains(name, "/") {
		return fmt.Errorf("config.yaml 的 name: %q 不能含 /", name)
	}
	return nil
}

// IsSelfName 报告 target 是否按自名指本机：输入省略（空串）或 == Name 都为真。
//
// 注意：Name 为空（未配置）时没有名字判据，恒返回 false——调用方必须回退
// 地址判据（config.IsSelfTarget），不要把它当成「空串 = 本机」。
func (c *Config) IsSelfName(target string) bool {
	if c == nil || c.Name == "" {
		return false
	}
	return target == "" || target == c.Name
}

// ResolveTarget 把一次目标输入解析成（身份值, 是否本机）。
//
// 身份值进共享账本（有自名时恒为全队名，永不为空串）；路由值由调用方按
// isLocal 折算（本机 → ""，否则 = 身份值）。规则：
//   - 有自名：省略（空串）或 == 自名 → (自名, true)；其余 → (原值, false)。
//   - 无自名：回退地址判据（S5，行为与今天一致）；本机 → ("", true)，
//     否则 → (原值, false)。空串输入按本机处理。
//
// 注意：本函数不校验名字是否已登记——未知名保留原值，由下游（池/设备表）
// 拒绝；这里不做「本机」以外的解释。
func (c *Config) ResolveTarget(target string) (identity string, isLocal bool) {
	if c.Name != "" {
		if target == "" || target == c.Name {
			return c.Name, true
		}
		return target, false
	}
	if target == "" {
		return "", true
	}
	if t, ok := c.Targets[target]; ok && IsSelfTarget(c.Listen, t) {
		return "", true
	}
	return target, false
}
