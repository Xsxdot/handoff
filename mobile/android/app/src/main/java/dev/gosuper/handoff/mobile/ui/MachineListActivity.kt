// MachineListActivity.kt —— 机器列表屏：在线/离线展示 + 点击进入（离线置灰）。
//
// 职责：读清单；在线机点击启动 WebviewActivity（带 machine/online）。
// 边界：不触碰 cookie/webview；离线机仅展示不可进入。
package dev.gosuper.handoff.mobile.ui

import android.content.Intent
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.BaseAdapter
import android.widget.ListView
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import dev.gosuper.handoff.mobile.HandoffApp
import dev.gosuper.handoff.mobile.R
import dev.gosuper.handoff.mobile.core.MachineView
import dev.gosuper.handoff.mobile.list.MachineList

class MachineListActivity : AppCompatActivity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_machine_list)

        val machines = MachineList.load((application as HandoffApp).graph.core)
        val listView = findViewById<ListView>(R.id.machineListView)
        listView.adapter = MachineAdapter(machines)
        listView.setOnItemClickListener { _, _, position, _ ->
            val m = machines[position]
            if (!m.online) return@setOnItemClickListener
            startActivity(
                Intent(this, WebviewActivity::class.java)
                    .putExtra(WebviewActivity.EXTRA_MACHINE, m.name)
                    .putExtra(WebviewActivity.EXTRA_ONLINE, m.online),
            )
        }
    }
}

private class MachineAdapter(private val items: List<MachineView>) : BaseAdapter() {
    override fun getCount(): Int = items.size
    override fun getItem(position: Int): Any = items[position]
    override fun getItemId(position: Int): Long = position.toLong()

    override fun getView(position: Int, convertView: View?, parent: ViewGroup): View {
        val view = convertView ?: LayoutInflater.from(parent.context).inflate(R.layout.item_machine, parent, false)
        val m = items[position]
        view.findViewById<TextView>(R.id.machineName).text = m.name
        view.findViewById<TextView>(R.id.machineStatus).text =
            view.context.getString(if (m.online) R.string.machine_online else R.string.machine_offline)
        view.isEnabled = m.online
        view.alpha = if (m.online) 1f else 0.4f
        return view
    }
}
