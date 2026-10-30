import UIKit

// Machine choice is a screen-local pending selection. CurrentMachineStore is committed only
// by ConsoleWebViewController after an authenticated same-origin main document completes.
final class MachineListViewController: UIViewController, UITableViewDataSource, UITableViewDelegate {
    private let composition: AppComposition
    private let machineList: MachineListModel
    private var machines: [MachineView] = []
    private var pendingSelection: String?
    private let autoResume: Bool
    private var didAttemptResume = false
    private var unavailableView: UIView?
    private weak var unavailableDetailLabel: UILabel?
    private let tableView = UITableView()
    private var listContentHeight: NSLayoutConstraint?
    private let enterButton = UIButton(type: .system)
    private let footnoteLabel = UILabel()

    init(composition: AppComposition, autoResume: Bool = false) {
        self.composition = composition
        self.machineList = composition.machineList
        self.autoResume = autoResume
        super.init(nibName: nil, bundle: nil)
    }
    required init?(coder: NSCoder) { fatalError("init(coder:) 未实现") }

    override func viewDidLoad() {
        super.viewDidLoad()
        overrideUserInterfaceStyle = .light
        title = "机器"
        view.backgroundColor = .white
        tableView.accessibilityIdentifier = "machine-list.table"
        let paired = UILabel()
        paired.text = "● 配对成功"
        paired.textColor = .systemGreen
        paired.font = .preferredFont(forTextStyle: .subheadline)
        paired.translatesAutoresizingMaskIntoConstraints = false
        let heading = UILabel()
        heading.text = "选择一台机器"
        heading.font = .systemFont(ofSize: 26, weight: .bold)
        heading.translatesAutoresizingMaskIntoConstraints = false
        let subtitle = UILabel()
        subtitle.text = "以后打开 App，直接进入这台机器。"
        subtitle.textColor = .secondaryLabel
        subtitle.font = .preferredFont(forTextStyle: .subheadline)
        subtitle.translatesAutoresizingMaskIntoConstraints = false
        footnoteLabel.text = "稍后可在设置中更换"
        footnoteLabel.textColor = .secondaryLabel
        footnoteLabel.font = .preferredFont(forTextStyle: .footnote)
        footnoteLabel.translatesAutoresizingMaskIntoConstraints = false

        tableView.translatesAutoresizingMaskIntoConstraints = false
        tableView.dataSource = self
        tableView.delegate = self
        tableView.backgroundColor = .white
        tableView.rowHeight = 72
        tableView.layer.cornerRadius = 12
        tableView.layer.borderWidth = 1
        tableView.layer.borderColor = UIColor.separator.cgColor
        enterButton.setTitle("进入工作台", for: .normal)
        enterButton.accessibilityIdentifier = "machine-list.enter"
        enterButton.setTitleColor(.white, for: .normal)
        enterButton.backgroundColor = UIColor(red: 0.31, green: 0.28, blue: 0.93, alpha: 1)
        enterButton.layer.cornerRadius = 12
        enterButton.titleLabel?.font = .systemFont(ofSize: 17, weight: .semibold)
        enterButton.translatesAutoresizingMaskIntoConstraints = false
        enterButton.addTarget(self, action: #selector(didTapEnter), for: .touchUpInside)
        [paired, heading, subtitle, tableView, footnoteLabel, enterButton].forEach(view.addSubview)
        // UITableView has no intrinsic height. Prefer its rows' height, capped by the
        // space above the footer so longer lists scroll and short lists stay compact.
        let contentHeight = tableView.heightAnchor.constraint(equalToConstant: tableView.rowHeight)
        contentHeight.priority = .defaultHigh
        listContentHeight = contentHeight
        NSLayoutConstraint.activate([
            paired.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor, constant: 20),
            paired.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 24),
            heading.topAnchor.constraint(equalTo: paired.bottomAnchor, constant: 14),
            heading.leadingAnchor.constraint(equalTo: paired.leadingAnchor),
            heading.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -24),
            subtitle.topAnchor.constraint(equalTo: heading.bottomAnchor, constant: 6),
            subtitle.leadingAnchor.constraint(equalTo: heading.leadingAnchor),
            subtitle.trailingAnchor.constraint(equalTo: heading.trailingAnchor),
            tableView.topAnchor.constraint(equalTo: subtitle.bottomAnchor, constant: 20),
            tableView.leadingAnchor.constraint(equalTo: heading.leadingAnchor),
            tableView.trailingAnchor.constraint(equalTo: heading.trailingAnchor),
            tableView.bottomAnchor.constraint(lessThanOrEqualTo: footnoteLabel.topAnchor, constant: -16),
            contentHeight,
            footnoteLabel.leadingAnchor.constraint(equalTo: heading.leadingAnchor),
            footnoteLabel.trailingAnchor.constraint(equalTo: heading.trailingAnchor),
            enterButton.topAnchor.constraint(equalTo: footnoteLabel.bottomAnchor, constant: 12),
            enterButton.leadingAnchor.constraint(equalTo: heading.leadingAnchor),
            enterButton.trailingAnchor.constraint(equalTo: heading.trailingAnchor),
            enterButton.heightAnchor.constraint(equalToConstant: 56),
            enterButton.bottomAnchor.constraint(equalTo: view.safeAreaLayoutGuide.bottomAnchor, constant: -12),
        ])
    }

    override func viewWillAppear(_ animated: Bool) {
        super.viewWillAppear(animated)
        navigationController?.overrideUserInterfaceStyle = .light
        navigationController?.setNavigationBarHidden(false, animated: animated)
        reload()
        if autoResume && !didAttemptResume {
            didAttemptResume = true
            if let selected = machineList.currentMachine,
               machineList.resumeIndex() != nil {
                Log.shell.info("恢复已保存机器 machine=\(selected, privacy: .public)")
                enter(machine: selected)
            } else if let selected = machineList.currentMachine {
                Log.shell.warning("当前机器离线或不可用，保留选择 machine=\(selected, privacy: .public)")
                showUnavailable(selected)
            }
        }
    }

    private func reload() {
        machines = machineList.machines()
        listContentHeight?.constant = CGFloat(max(1, machines.count)) * tableView.rowHeight
        if let saved = machineList.currentMachine,
           let selected = machines.first(where: { $0.name == saved && $0.online })?.name {
            pendingSelection = selected
        } else if pendingSelection == nil || !machines.contains(where: { $0.name == pendingSelection && $0.online }) {
            pendingSelection = machines.first(where: \.online)?.name
        }
        tableView.reloadData()
        enterButton.isEnabled = pendingSelection != nil
        Log.shell.info("机器列表刷新 count=\(self.machines.count, privacy: .public)")
    }

    private func showUnavailable(_ machine: String) {
        tableView.isHidden = true
        enterButton.isHidden = true
        footnoteLabel.isHidden = true
        let panel = UIStackView()
        panel.axis = .vertical
        panel.alignment = .fill
        panel.spacing = 16
        panel.translatesAutoresizingMaskIntoConstraints = false
        let heading = UILabel()
        heading.text = "无法连接到 \(machine)"
        heading.font = .preferredFont(forTextStyle: .title2)
        heading.textAlignment = .center
        let detail = UILabel()
        detail.text = "当前机器选择已保留。恢复网络后重试，或前往设置更换机器。"
        detail.numberOfLines = 0
        detail.textColor = .secondaryLabel
        detail.textAlignment = .center
        unavailableDetailLabel = detail
        let retry = UIButton(type: .system)
        retry.setTitle("重试连接", for: .normal)
        retry.addAction(UIAction { [weak self] _ in
            guard let self else { return }
            let selected = self.machineList.currentMachine
            let result = self.machineList.refreshSavedMachine {
                try self.composition.pairingService.pairFromStoredBundle()
            }
            if case .success(let machine) = result, machine.name == selected {
                self.reload()
                // Keep the unavailable panel as the navigation return landing while Console
                // authenticates A. Successful main-document completion collapses this screen;
                // a preflight/load failure pops back to a usable retry state.
                self.enter(machine: machine.name)
            } else {
                self.unavailableDetailLabel?.text =
                    "重新校验后仍无法连接到这台机器。原选择已保留，请稍后重试或前往设置更换。"
            }
        }, for: .touchUpInside)
        let change = UIButton(type: .system)
        change.setTitle("前往设置更换机器", for: .normal)
        change.addAction(UIAction { [weak self] _ in
            guard let self else { return }
            self.unavailableView?.removeFromSuperview()
            self.unavailableView = nil
            self.tableView.isHidden = false
            self.enterButton.isHidden = false
            self.footnoteLabel.isHidden = false
            self.pendingSelection = nil
            self.reload()
        }, for: .touchUpInside)
        [heading, detail, retry, change].forEach(panel.addArrangedSubview)
        view.addSubview(panel)
        NSLayoutConstraint.activate([
            panel.leadingAnchor.constraint(equalTo: view.safeAreaLayoutGuide.leadingAnchor, constant: 24),
            panel.trailingAnchor.constraint(equalTo: view.safeAreaLayoutGuide.trailingAnchor, constant: -24),
            panel.centerYAnchor.constraint(equalTo: view.safeAreaLayoutGuide.centerYAnchor),
        ])
        unavailableView = panel
    }

    func tableView(_ tableView: UITableView, numberOfRowsInSection section: Int) -> Int { machines.count }

    func tableView(_ tableView: UITableView, cellForRowAt indexPath: IndexPath) -> UITableViewCell {
        let cell = tableView.dequeueReusableCell(withIdentifier: "machine")
            ?? UITableViewCell(style: .subtitle, reuseIdentifier: "machine")
        let machine = machines[indexPath.row]
        let selected = machine.name == pendingSelection
        cell.textLabel?.text = machine.name
        cell.detailTextLabel?.text = machine.online ? "● 在线" : "● 离线"
        cell.detailTextLabel?.textColor = machine.online ? .systemGreen : .secondaryLabel
        cell.imageView?.image = UIImage(systemName: machine.name.localizedCaseInsensitiveContains("linux") ? "server.rack" : "laptopcomputer")
        cell.imageView?.tintColor = .label
        cell.textLabel?.textColor = machine.online ? .label : .secondaryLabel
        cell.accessoryType = selected ? .checkmark : .none
        cell.tintColor = UIColor(red: 0.31, green: 0.28, blue: 0.93, alpha: 1)
        cell.backgroundColor = selected ? UIColor(red: 0.95, green: 0.95, blue: 1, alpha: 1) : .white
        cell.selectionStyle = machine.online ? .default : .none
        return cell
    }

    func tableView(_ tableView: UITableView, willSelectRowAt indexPath: IndexPath) -> IndexPath? {
        guard machines.indices.contains(indexPath.row), machines[indexPath.row].online else { return nil }
        return indexPath
    }

    func tableView(_ tableView: UITableView, didSelectRowAt indexPath: IndexPath) {
        tableView.deselectRow(at: indexPath, animated: true)
        guard machines.indices.contains(indexPath.row) else { return }
        pendingSelection = machines[indexPath.row].name
        tableView.reloadData()
        enterButton.isEnabled = true
    }

    @objc private func didTapEnter() {
        guard let selected = pendingSelection else { return }
        enter(machine: selected)
    }

    private func enter(machine selectedMachine: String) {
        guard let machine = machineList.machines().first(where: { $0.name == selectedMachine && $0.online }) else {
            presentError(.offline(selectedMachine))
            return
        }
        let navigation = navigationController
        let composition = self.composition
        let console = ConsoleWebViewController(
            webView: composition.cookieBridge.webView,
            machine: selectedMachine,
            origin: machine.origin,
            selection: composition.currentMachine,
            onRequestSwitch: { [weak navigation, composition] in
                navigation?.pushViewController(MachineListViewController(composition: composition), animated: true)
            },
            onRestore: { [composition] completion in
                composition.machineList.enter(machine: selectedMachine) { result in
                    if case .failure(let error) = result { completion(error) }
                    else { completion(nil) }
                }
            },
            retryCurrentMachine: { completion in
                composition.machineList.enter(machine: selectedMachine) { result in
                    if case .failure(let error) = result { completion(error) }
                    else { completion(nil) }
                }
            },
            onAuthenticatedMainDocument: { [weak navigation] console in
                navigation?.setViewControllers([console], animated: true)
            },
        )
        navigationController?.pushViewController(console, animated: true)
        composition.machineList.enter(machine: selectedMachine) { [weak self] result in
            DispatchQueue.main.async {
                if case .failure(let error) = result {
                    self?.navigationController?.popViewController(animated: true)
                    self?.presentError(error)
                }
            }
        }
    }

    private func presentError(_ error: ShellError) {
        Log.shell.error("进入机器失败：\(error.title, privacy: .public)")
        let alert = UIAlertController(title: error.title, message: error.message, preferredStyle: .alert)
        alert.addAction(UIAlertAction(title: "确定", style: .default))
        present(alert, animated: true)
    }
}
