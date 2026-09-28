// AppGraph.kt —— 壳的组装点：进程内唯一持有端口实现与接缝实例。
//
// 职责：构造 CoreGateway/SecretStore 与 PairEntry/StartupController；Activity 从这里取。
// 边界：不解析 bundle、不做协议；由 HandoffApp 持有，不随 Activity 重建。
package dev.gosuper.handoff.mobile

import android.content.Context
import dev.gosuper.handoff.mobile.core.BindCoreGateway
import dev.gosuper.handoff.mobile.core.EncryptedSecretStore
import dev.gosuper.handoff.mobile.core.SecretStore
import dev.gosuper.handoff.mobile.pair.PairEntry
import dev.gosuper.handoff.mobile.pair.StartupController

class AppGraph(context: Context) {
    val core = BindCoreGateway()
    private val store: SecretStore = EncryptedSecretStore(context.applicationContext)

    val pairEntry = PairEntry(core, store)
    val startup = StartupController(store, core)
}
