// SystemBarsInsets.kt —— edge-to-edge 适配（B421）。
//
// 职责：把系统条 insets 落到 android.R.id.content 的 padding 上，让内容避开
//       状态栏/导航栏。targetSdk 36 在 Android 15+ 被强制 edge-to-edge，窗口
//       默认延伸到系统条底下，不适配则页头与状态栏重叠（真机实拍）。
//       B426 S6 真机走查补充：同一强制下 windowSoftInputMode=adjustResize 被
//       系统忽略（Android 16 真机实测，键盘仍盖住 web 页脚），键盘避让必须由
//       应用消费 IME insets——bottom 取系统条与 IME 的较大者（IME 起量时含
//       导航条高度，两者取 max 不会重复计）。Pairing/MachineList 的输入框同享。
// 边界：只做 padding，不改主题、不动导航条显隐；各 Activity 在 setContentView
//       之后调用一次即可。
package dev.gosuper.handoff.mobile.ui

import android.view.ViewGroup
import androidx.appcompat.app.AppCompatActivity
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat

fun AppCompatActivity.applySystemBarsInsets() {
    // 浅色主题下系统条图标用深色（B421 复验发现：默认白图标在白底上不可见）。
    WindowCompat.getInsetsController(window, window.decorView).isAppearanceLightStatusBars = true
    val content = findViewById<ViewGroup>(android.R.id.content)
    ViewCompat.setOnApplyWindowInsetsListener(content) { v, insets ->
        val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars())
        val ime = insets.getInsets(WindowInsetsCompat.Type.ime())
        v.setPadding(bars.left, bars.top, bars.right, maxOf(bars.bottom, ime.bottom))
        insets
    }
}
