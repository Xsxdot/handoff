// SecretStore.kt —— 原始配对 bundle 的安全存储抽象。
//
// 职责：整体存/取/清原始 bundle JSON（不解析、不拆字段），使 I2 可在 JVM 用假实现测往返。
// 边界：不选加密算法（由平台实现决定）、不落日志。
package dev.gosuper.handoff.mobile.core

interface SecretStore {
    /** 写入整体 bundle JSON（原始串，不解析）。 */
    fun writeBundle(bundleJSON: String)

    /** 读回整体 bundle JSON；无则返回 null。 */
    fun readBundle(): String?

    /** 清除已存 bundle。 */
    fun clear()
}
