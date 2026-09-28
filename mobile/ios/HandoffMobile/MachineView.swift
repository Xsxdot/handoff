import Foundation

// 壳侧机器只读视图：把 gomobile 的 BindMachine 隔离在 LiveConnectCore 内，
// 使上层与单测不依赖 Go 框架类型。字段与 contract §3.1 Machine 逐字对齐。
struct MachineView: Equatable {
    let name: String
    let origin: String
    let online: Bool
}
