// HandoffApp.kt —— Application：持有进程级 AppGraph（Go 核单例的唯一入口）。
//
// 职责：进程创建时构造 AppGraph 并跑一次启动决策；进程终止时 Close 核。
// 边界：不随 Activity 重建；不在别处再 Pair 或新建第二个核。
package dev.gosuper.handoff.mobile

import android.app.Application
import android.util.Log
import bind.Bind

private const val TAG = "HandoffApp"

class HandoffApp : Application() {
    lateinit var graph: AppGraph
        private set

    override fun onCreate() {
        super.onCreate()
        graph = AppGraph(this)
        graph.startup.ensureStarted()
        Log.i(TAG, "App 启动 state=${graph.startup.state::class.simpleName}")
    }

    override fun onTerminate() {
        // 系统回收/模拟器终止：收掉核（幂等）。真实设备进程被杀时不一定回调，
        // 前台重启走启动路径重建（contract §4.8 条 39）。
        runCatching { Bind.close() }
        super.onTerminate()
    }
}
