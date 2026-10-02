// PairingActivity.kt —— 配对屏：扫码 / 粘贴两条入口都归一到 PairEntry.pair。
//
// 职责：无存储凭据时展示配对 UI；两条入口把载荷交给同一 pair 入口；成功进机器列表。
// 边界：不解析 bundle；失败展示四类错误之一并可重试；不加载 webview。
package dev.gosuper.handoff.mobile.ui

import android.content.Intent
import android.os.Bundle
import android.util.Log
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import dev.gosuper.handoff.mobile.HandoffApp
import dev.gosuper.handoff.mobile.R
import dev.gosuper.handoff.mobile.pair.ErrorClassifier
import dev.gosuper.handoff.mobile.pair.ShellError
import dev.gosuper.handoff.mobile.pair.StartupState

private const val TAG = "PairingActivity"

class PairingActivity : AppCompatActivity() {

    private val scanLauncher = registerForActivityResult(ScanContract()) { result ->
        result.contents?.let { onBundle(it, source = "scan") }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_pairing)
        // B421：targetSdk 36 强制 edge-to-edge，内容避开系统条。
        applySystemBarsInsets()

        val bundleInput = findViewById<EditText>(R.id.bundleInput)

        findViewById<Button>(R.id.scanButton).setOnClickListener {
            val opts = ScanOptions()
                .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                .setPrompt("扫描 handoff console --qr 的二维码")
            scanLauncher.launch(opts)
        }
        findViewById<Button>(R.id.submitButton).setOnClickListener {
            onBundle(bundleInput.text.toString(), source = "paste")
        }

        when (val s = (application as HandoffApp).graph.startup.state) {
            is StartupState.MachineList -> goToMachineList()
            is StartupState.Failed -> showError(s.error)
            else -> Unit
        }
    }

    /** 扫码与粘贴共享的唯一归一点。 */
    private fun onBundle(raw: String, source: String) {
        Log.i(TAG, "配对入口 source=$source")
        val r = (application as HandoffApp).graph.pairEntry.pair(raw)
        if (r.isSuccess) goToMachineList() else showError(ErrorClassifier.fromPairFailure())
    }

    private fun goToMachineList() {
        startActivity(Intent(this, MachineListActivity::class.java))
        finish()
    }

    private fun showError(error: ShellError) {
        val msg = when (error) {
            ShellError.PAIRING -> getString(R.string.error_pairing)
            ShellError.OFFLINE -> getString(R.string.error_offline)
            ShellError.EXCHANGE -> getString(R.string.error_exchange)
            ShellError.EXPIRED -> getString(R.string.error_expired)
        }
        findViewById<TextView>(R.id.errorText).apply {
            text = msg
            visibility = View.VISIBLE
        }
    }
}
