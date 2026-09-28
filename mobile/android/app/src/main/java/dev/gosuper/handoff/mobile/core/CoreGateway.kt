// CoreGateway.kt —— 壳对 gomobile 绑定面的窄消费口（配对面 + 清单面 + 会话面）。
//
// 职责：把 bind.Bind 的七函数收敛成三个小接口，供 PairEntry / MachineList /
//       WebviewSessionBinder 消费，使 JVM 单测可用假实现替换。
// 边界：不做协议逻辑；不解析 bundle；不出现绑定面之外的方法（Token/Dial 等）。
package dev.gosuper.handoff.mobile.core

/** 一台已登记机器在壳侧的只读视图（镜像 bind.Machine 的 name/origin/online）。 */
data class MachineView(
    val name: String,
    val origin: String,
    val online: Boolean,
)

/** 配对面 + 清单面对绑定面的窄消费口。生产实现 = BindCoreGateway。 */
interface CoreGateway {
    /** 整份 bundle JSON 透传绑定面；失败抛出（壳不解析、不改写）。 */
    fun pair(bundleJSON: String)

    /** 已登记机器数（含离线机）。 */
    fun machineCount(): Int

    /** 按索引取一台机器；越界返回 null（与绑定面 MachineAt 同语义）。 */
    fun machineAt(index: Int): MachineView?
}

/** 会话面对绑定面的窄消费口。 */
interface SessionCore {
    /** 切到目标机并重兑换；返回目标回环源 origin；失败抛出（壳不得导航）。 */
    fun switchMachine(machine: String): String

    /** 取目标机当前会话 cookie 的 value；失败抛出；绝不返回 token。 */
    fun sessionCookie(machine: String): String
}
