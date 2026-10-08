import UIKit

// 机器列表屏：名字 + 在线/离线；离线可见不可进入（I5 条 32）。
// 进入动作调用 MachineListModel.enter → CookieBridge 的 I3 序列。
final class MachineListViewController: UIViewController, UITableViewDataSource, UITableViewDelegate {
    private let composition: AppComposition
    private var machines: [MachineView] = []
    private let tableView = UITableView()

    init(composition: AppComposition) {
        self.composition = composition
        super.init(nibName: nil, bundle: nil)
    }
    required init?(coder: NSCoder) { fatalError("init(coder:) 未实现") }

    override func viewDidLoad() {
        super.viewDidLoad()
        title = "机器"
        view.backgroundColor = .systemBackground
        tableView.frame = view.bounds
        tableView.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        tableView.dataSource = self
        tableView.delegate = self
        tableView.register(UITableViewCell.self, forCellReuseIdentifier: "cell")
        view.addSubview(tableView)
    }

    override func viewWillAppear(_ animated: Bool) {
        super.viewWillAppear(animated)
        // 控制台会藏起导航条。回到本页时放回来，不依赖弹出动画是否走完。
        navigationController?.setNavigationBarHidden(false, animated: animated)
        reload()
    }

    private func reload() {
        machines = composition.machineList.machines()
        Log.shell.info("机器列表刷新 count=\(self.machines.count, privacy: .public)")
        tableView.reloadData()
    }

    func tableView(_ tableView: UITableView, numberOfRowsInSection section: Int) -> Int { machines.count }

    func tableView(_ tableView: UITableView, cellForRowAt indexPath: IndexPath) -> UITableViewCell {
        let cell = tableView.dequeueReusableCell(withIdentifier: "cell", for: indexPath)
        let m = machines[indexPath.row]
        cell.textLabel?.text = m.name
        cell.detailTextLabel?.text = m.online ? "在线" : "离线"
        cell.textLabel?.textColor = m.online ? .label : .secondaryLabel
        cell.accessoryType = m.online ? .disclosureIndicator : .none
        cell.selectionStyle = m.online ? .default : .none
        return cell
    }

    // 离线行不可选中（I5 条 32）：置 nil 让 UIKit 直接吞掉点击，不到 didSelectRowAt。
    func tableView(_ tableView: UITableView, willSelectRowAt indexPath: IndexPath) -> IndexPath? {
        guard machines.indices.contains(indexPath.row), machines[indexPath.row].online else {
            Log.shell.error("离线机不可进入 row=\(indexPath.row, privacy: .public)")
            return nil
        }
        return indexPath
    }

    func tableView(_ tableView: UITableView, didSelectRowAt indexPath: IndexPath) {
        tableView.deselectRow(at: indexPath, animated: true)
        enter(index: indexPath.row)
    }

    private func enter(index: Int) {
        composition.machineList.enter(index: index) { [weak self] result in
            DispatchQueue.main.async {
                switch result {
                case .success:
                    guard let self else { return }
                    let console = ConsoleWebViewController(webView: self.composition.cookieBridge.webView)
                    self.navigationController?.pushViewController(console, animated: true)
                case .failure(let e):
                    self?.presentError(e)
                }
            }
        }
    }

    private func presentError(_ e: ShellError) {
        Log.shell.error("进入机器失败：\(e.title, privacy: .public)")
        let alert = UIAlertController(title: e.title, message: e.message, preferredStyle: .alert)
        if e.canRetry {
            alert.addAction(UIAlertAction(title: "重试", style: .default) { [weak self] _ in self?.reload() })
        }
        alert.addAction(UIAlertAction(title: "取消", style: .cancel))
        present(alert, animated: true)
    }
}
