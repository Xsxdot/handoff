// types.go —— 绑定面回给壳的只读 DTO（B369 移动端）。
//
// 为什么单独定义而不复用 internal/mobilecore 的结构体：gomobile 的
// `bind/gen.go#isSupported` 只放行「正在绑定集合内」的包里的命名类型
// （`validPkg`），跨包类型（如 mobilecore.PairResult）一律判为
// unsupported 并**静默跳过整个函数**（产物里只留一行
// `// skipped function …`）。故壳要读的结构必须在本包定义。
//
// 字段类型也被同一条规则约束：只许 gomobile 支持的类型（本包用 string/bool）。
// 列表不能用 Go slice（`isSupported` 对 slice 只放行 []byte），改用
// `MachineCount()` + `MachineAt(i)` 的计数/按索引取指针形状。
package bind

// Machine 是一台已配对机器在壳侧的只读视图。
//
// 语义与核侧 mobilecore.PairedMachine 逐字段对齐：
// 在线机有 Origin（形如 http://127.0.0.1:<port>/），离线机 Online=false 且 Origin 为空。
type Machine struct {
	Name   string
	Origin string
	Online bool
}
