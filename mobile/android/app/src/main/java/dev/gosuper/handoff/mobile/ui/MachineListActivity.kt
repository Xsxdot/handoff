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
import android.widget.Button
import android.widget.ListView
import android.widget.RadioButton
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
        // B421：targetSdk 36 强制 edge-to-edge，内容避开系统条。
        applySystemBarsInsets()

        val graph = (application as HandoffApp).graph
        var machines = MachineList.load(graph.core)
        val selected = graph.currentMachine.read()
        val listView = findViewById<ListView>(R.id.machineListView)
        var pendingSelection = selected?.takeIf { name -> machines.any { it.name == name && it.online } }
            ?: machines.firstOrNull { it.online }?.name
        val adapter = MachineAdapter(machines, pendingSelection)
        listView.adapter = adapter
        val enterButton = findViewById<Button>(R.id.enterMachineButton)
        enterButton.isEnabled = pendingSelection != null
        val unavailable = intent.getStringExtra(EXTRA_SELECTED_UNAVAILABLE)
        val unavailablePanel = findViewById<View>(R.id.unavailablePanel)
        if (!unavailable.isNullOrEmpty()) {
            findViewById<TextView>(R.id.unavailableTitle).text = "无法连接到 $unavailable"
            unavailablePanel.visibility = View.VISIBLE
            listView.visibility = View.GONE
            findViewById<View>(R.id.selectionFootnote).visibility = View.GONE
            enterButton.visibility = View.GONE
            findViewById<View>(R.id.retryMachineButton).setOnClickListener {
                val retry = graph.pairEntry.retrySelectedMachine()
                if (retry.isSuccess && retry.getOrNull()?.name == unavailable) {
                    startActivity(Intent(this, WebviewActivity::class.java)
                        .putExtra(WebviewActivity.EXTRA_MACHINE, unavailable)
                        .putExtra(WebviewActivity.EXTRA_ONLINE, true))
                    finish()
                } else {
                    findViewById<TextView>(R.id.unavailableDetail).text =
                        "重新校验后仍无法连接到这台机器。原选择已保留，请稍后重试或前往设置更换。"
                }
            }
            findViewById<View>(R.id.changeMachineButton).setOnClickListener {
                // Manual re-pair may have refreshed availability; rebuild the explicit picker
                // from the live Core snapshot instead of reopening the onCreate snapshot.
                machines = MachineList.load(graph.core)
                unavailablePanel.visibility = View.GONE
                listView.visibility = View.VISIBLE
                findViewById<View>(R.id.selectionFootnote).visibility = View.VISIBLE
                enterButton.visibility = View.VISIBLE
                pendingSelection = machines.firstOrNull { it.online }?.name
                adapter.replaceItems(machines, pendingSelection)
                enterButton.isEnabled = pendingSelection != null
            }
        }
        listView.setOnItemClickListener { _, _, position, _ ->
            val m = machines[position]
            if (!m.online) return@setOnItemClickListener
            pendingSelection = m.name
            adapter.select(pendingSelection)
            enterButton.isEnabled = true
        }
        enterButton.setOnClickListener {
            val machine = pendingSelection ?: return@setOnClickListener
            if (intent.getBooleanExtra(EXTRA_CHANGE_MACHINE, false)) {
                setResult(RESULT_OK, Intent().putExtra(EXTRA_SELECTED_MACHINE, machine))
                finish()
                return@setOnClickListener
            }
            startActivity(
                Intent(this, WebviewActivity::class.java)
                    .putExtra(WebviewActivity.EXTRA_MACHINE, machine)
                    .putExtra(WebviewActivity.EXTRA_ONLINE, true),
            )
            // Initial selection is a one-time choice; returning from the workbench must not
            // expose the chooser as an alternate machine-switch path.
            finish()
        }
    }

    companion object {
        const val EXTRA_SELECTED_UNAVAILABLE = "selected_machine_unavailable"
        const val EXTRA_CHANGE_MACHINE = "change_machine"
        const val EXTRA_SELECTED_MACHINE = "selected_machine"
    }
}

private class MachineAdapter(private var items: List<MachineView>, private var selected: String?) : BaseAdapter() {
    fun select(name: String?) { selected = name; notifyDataSetChanged() }
    fun replaceItems(next: List<MachineView>, selection: String?) {
        items = next
        selected = selection
        notifyDataSetChanged()
    }
    override fun getCount(): Int = items.size
    override fun getItem(position: Int): Any = items[position]
    override fun getItemId(position: Int): Long = position.toLong()

    override fun getView(position: Int, convertView: View?, parent: ViewGroup): View {
        val view = convertView ?: LayoutInflater.from(parent.context).inflate(R.layout.item_machine, parent, false)
        val m = items[position]
        view.findViewById<TextView>(R.id.machineName).text = m.name
        view.findViewById<TextView>(R.id.machineStatus).apply {
            text = view.context.getString(if (m.online) R.string.machine_online else R.string.machine_offline)
            setCompoundDrawablesRelativeWithIntrinsicBounds(
                if (m.online) R.drawable.status_dot_online else R.drawable.status_dot_offline, 0, 0, 0,
            )
        }
        view.findViewById<RadioButton>(R.id.machineSelected).apply {
            isChecked = m.name == selected
            isEnabled = m.online
            visibility = if (m.online) View.VISIBLE else View.INVISIBLE
        }
        view.setBackgroundColor(if (m.name == selected) 0xFFEFEFFF.toInt() else android.graphics.Color.TRANSPARENT)
        view.isEnabled = m.online
        view.alpha = if (m.online) 1f else 0.4f
        return view
    }
}
