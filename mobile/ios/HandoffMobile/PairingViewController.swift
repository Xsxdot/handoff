import UIKit

// 配对屏：品牌欢迎与扫码/粘贴入口；两条路径都经 PairEntry.submit 归一。
// 两条入口都经 PairEntry.submit 归一（条 7）。失败显示四类错误之一 + 可重试。
final class PairingViewController: UIViewController {
    var initialError: ShellError?

    private let entry: PairEntry
    private let onPaired: () -> Void
    private let statusLabel = UILabel()
    private let pasteButton = UIButton(type: .system)

    init(entry: PairEntry, onPaired: @escaping () -> Void) {
        self.entry = entry
        self.onPaired = onPaired
        super.init(nibName: nil, bundle: nil)
    }
    required init?(coder: NSCoder) { fatalError("init(coder:) 未实现") }

    override func viewDidLoad() {
        super.viewDidLoad()
        title = "配对"
        view.backgroundColor = .white
        buildUI()
        if let e = initialError { render(error: e) }
        else { statusLabel.isHidden = true }
    }

    override func viewWillAppear(_ animated: Bool) {
        super.viewWillAppear(animated)
        navigationController?.setNavigationBarHidden(true, animated: animated)
    }

    private func buildUI() {
        let scroll = UIScrollView()
        scroll.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(scroll)
        NSLayoutConstraint.activate([
            scroll.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor),
            scroll.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            scroll.bottomAnchor.constraint(equalTo: view.bottomAnchor),
        ])
        let content = UIStackView()
        content.axis = .vertical
        content.alignment = .fill
        content.spacing = 10
        content.distribution = .equalSpacing
        content.translatesAutoresizingMaskIntoConstraints = false
        scroll.addSubview(content)
        NSLayoutConstraint.activate([
            content.topAnchor.constraint(equalTo: scroll.contentLayoutGuide.topAnchor, constant: 20),
            content.leadingAnchor.constraint(equalTo: scroll.frameLayoutGuide.leadingAnchor, constant: 24),
            content.trailingAnchor.constraint(equalTo: scroll.frameLayoutGuide.trailingAnchor, constant: -24),
            content.bottomAnchor.constraint(equalTo: scroll.contentLayoutGuide.bottomAnchor, constant: -24),
            content.heightAnchor.constraint(greaterThanOrEqualTo: scroll.frameLayoutGuide.heightAnchor, constant: -44),
        ])

        let brand = UIStackView()
        brand.axis = .horizontal
        brand.alignment = .center
        brand.spacing = 8
        content.addArrangedSubview(brand)
        let logo = UIImageView(image: UIImage(named: "HandoffLogo"))
        logo.contentMode = .scaleAspectFit
        logo.heightAnchor.constraint(equalToConstant: 44).isActive = true
        logo.widthAnchor.constraint(equalToConstant: 44).isActive = true
        brand.addArrangedSubview(logo)
        let wordmark = UILabel()
        wordmark.text = "handoff"
        wordmark.font = .systemFont(ofSize: 20, weight: .bold)
        wordmark.textColor = UIColor(red: 0.08, green: 0.09, blue: 0.17, alpha: 1)
        brand.addArrangedSubview(wordmark)

        let header = UIStackView()
        header.axis = .vertical
        header.alignment = .fill
        header.spacing = 8
        content.addArrangedSubview(header)

        let titleLabel = UILabel()
        titleLabel.text = "把工作台带在身边"
        titleLabel.font = .systemFont(ofSize: 32, weight: .bold)
        titleLabel.textColor = UIColor(red: 0.08, green: 0.09, blue: 0.17, alpha: 1)
        titleLabel.textAlignment = .left
        header.addArrangedSubview(titleLabel)

        let subtitle = UILabel()
        subtitle.text = "连接电脑上的 Handoff，\n随时查看会话、推进任务。"
        subtitle.font = .systemFont(ofSize: 17)
        subtitle.textColor = .secondaryLabel
        subtitle.textAlignment = .left
        subtitle.numberOfLines = 0
        header.addArrangedSubview(subtitle)

        let illustration = UIImageView(image: UIImage(named: "PairingIllustration"))
        illustration.contentMode = .scaleAspectFit
        illustration.accessibilityLabel = "设备与工作台连接示意图"
        illustration.heightAnchor.constraint(equalToConstant: 260).isActive = true
        illustration.transform = CGAffineTransform(scaleX: 1.4, y: 1.4)
        content.addArrangedSubview(illustration)

        statusLabel.numberOfLines = 0
        statusLabel.textColor = .secondaryLabel
        statusLabel.translatesAutoresizingMaskIntoConstraints = false

        let startTitle = UILabel()
        startTitle.text = "开始配对"
        startTitle.font = .systemFont(ofSize: 18, weight: .semibold)
        startTitle.textColor = UIColor(red: 0.08, green: 0.09, blue: 0.17, alpha: 1)

        let instruction = UILabel()
        instruction.text = QRScannerViewController.isCameraAvailable
            ? "在电脑端打开配对二维码，用手机扫描。"
            : "此设备暂不支持相机扫描，可粘贴电脑端的配对信息。"
        instruction.font = .systemFont(ofSize: 17)
        instruction.textColor = .secondaryLabel
        instruction.numberOfLines = 0
        let instructions = UIStackView(arrangedSubviews: [startTitle, instruction])
        instructions.axis = .vertical
        instructions.alignment = .fill
        instructions.spacing = 8
        content.addArrangedSubview(instructions)

        let scanButton = UIButton(type: .system)
        scanButton.setTitle("扫描配对二维码", for: .normal)
        scanButton.addTarget(self, action: #selector(didTapScan), for: .touchUpInside)
        scanButton.translatesAutoresizingMaskIntoConstraints = false
        scanButton.isHidden = !QRScannerViewController.isCameraAvailable
        scanButton.backgroundColor = UIColor(red: 0.31, green: 0.28, blue: 0.93, alpha: 1)
        scanButton.setTitleColor(.white, for: .normal)
        scanButton.layer.cornerRadius = 12
        scanButton.clipsToBounds = true
        scanButton.titleLabel?.font = .systemFont(ofSize: 17, weight: .semibold)
        scanButton.heightAnchor.constraint(equalToConstant: 54).isActive = true

        pasteButton.setTitle("粘贴配对信息", for: .normal)
        pasteButton.addTarget(self, action: #selector(didTapPaste), for: .touchUpInside)
        pasteButton.titleLabel?.font = .systemFont(ofSize: 17, weight: .semibold)
        pasteButton.heightAnchor.constraint(
            equalToConstant: QRScannerViewController.isCameraAvailable ? 48 : 54
        ).isActive = true
        if !QRScannerViewController.isCameraAvailable {
            pasteButton.backgroundColor = UIColor(red: 0.31, green: 0.28, blue: 0.93, alpha: 1)
            pasteButton.setTitleColor(.white, for: .normal)
            pasteButton.layer.cornerRadius = 12
        } else {
            pasteButton.setTitleColor(UIColor(red: 0.31, green: 0.28, blue: 0.93, alpha: 1), for: .normal)
        }

        let footnote = UILabel()
        footnote.text = "配对一次，下次直接进入"
        footnote.font = .preferredFont(forTextStyle: .footnote)
        footnote.textColor = .tertiaryLabel
        footnote.textAlignment = .center
        statusLabel.numberOfLines = 0
        content.addArrangedSubview(scanButton)
        content.addArrangedSubview(pasteButton)
        content.addArrangedSubview(footnote)
        content.addArrangedSubview(statusLabel)
    }

    @objc private func didTapPaste() {
        let alert = UIAlertController(title: "粘贴配对串", message: "粘贴 handoff console --bundle 输出", preferredStyle: .alert)
        alert.addTextField { field in
            field.placeholder = "配对串"
            field.autocorrectionType = .no
            field.autocapitalizationType = .none
            field.clearButtonMode = .whileEditing
        }
        alert.addAction(UIAlertAction(title: "取消", style: .cancel))
        alert.addAction(UIAlertAction(title: "配对", style: .default) { [weak self, weak alert] _ in
            self?.submit(alert?.textFields?.first?.text ?? "")
        })
        present(alert, animated: true)
    }

    @objc private func didTapScan() {
        let scanner = QRScannerViewController { [weak self] payload in
            self?.dismiss(animated: true) { self?.submit(payload) }
        }
        present(scanner, animated: true)
    }

    private func submit(_ raw: String) {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        Log.shell.info("提交配对载荷 bytes=\(trimmed.utf8.count, privacy: .public)")
        entry.submit(raw: raw) { [weak self] result in
            DispatchQueue.main.async {
                switch result {
                case .success: self?.onPaired()
                case .failure(let e): self?.render(error: e)
                }
            }
        }
    }

    private func render(error: ShellError) {
        Log.shell.error("配对/启动错误：\(error.title, privacy: .public)")
        statusLabel.text = "\(error.title)：\(error.message)"
        statusLabel.textColor = .systemRed
        statusLabel.isHidden = false
    }
}
