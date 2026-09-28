import UIKit

// 配对屏：粘贴输入框 + 扫码按钮（无摄像头时隐藏，spec 故事 3）。
// 两条入口都经 PairEntry.submit 归一（条 7）。失败显示四类错误之一 + 可重试。
final class PairingViewController: UIViewController {
    var initialError: ShellError?

    private let entry: PairEntry
    private let onPaired: () -> Void
    private let textView = UITextView()
    private let statusLabel = UILabel()

    init(entry: PairEntry, onPaired: @escaping () -> Void) {
        self.entry = entry
        self.onPaired = onPaired
        super.init(nibName: nil, bundle: nil)
    }
    required init?(coder: NSCoder) { fatalError("init(coder:) 未实现") }

    override func viewDidLoad() {
        super.viewDidLoad()
        title = "配对"
        view.backgroundColor = .systemBackground
        buildUI()
        if let e = initialError { render(error: e) }
        else { statusLabel.text = "粘贴 handoff console --bundle 输出，或扫码配对。" }
    }

    private func buildUI() {
        textView.font = .monospacedSystemFont(ofSize: 14, weight: .regular)
        textView.autocorrectionType = .no
        textView.autocapitalizationType = .none
        // 平台差异：UITextView.borderStyle 在 iOS 无 .roundedRect（plan 原稿用 macOS 风格枚举）；
        // 改用 layer 画同等的圆角边框。
        textView.layer.borderColor = UIColor.separator.cgColor
        textView.layer.borderWidth = 1
        textView.layer.cornerRadius = 8
        textView.translatesAutoresizingMaskIntoConstraints = false

        statusLabel.numberOfLines = 0
        statusLabel.textColor = .secondaryLabel
        statusLabel.translatesAutoresizingMaskIntoConstraints = false

        let pairButton = UIButton(type: .system)
        pairButton.setTitle("配对", for: .normal)
        pairButton.addTarget(self, action: #selector(didTapPair), for: .touchUpInside)
        pairButton.translatesAutoresizingMaskIntoConstraints = false

        let scanButton = UIButton(type: .system)
        scanButton.setTitle("扫码", for: .normal)
        scanButton.addTarget(self, action: #selector(didTapScan), for: .touchUpInside)
        scanButton.translatesAutoresizingMaskIntoConstraints = false
        scanButton.isHidden = !QRScannerViewController.isCameraAvailable   // 模拟器无摄像头 → 隐藏

        for v in [textView, statusLabel, pairButton, scanButton] { view.addSubview(v) }
        NSLayoutConstraint.activate([
            textView.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor, constant: 16),
            textView.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 16),
            textView.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -16),
            textView.heightAnchor.constraint(equalToConstant: 120),
            pairButton.topAnchor.constraint(equalTo: textView.bottomAnchor, constant: 12),
            pairButton.leadingAnchor.constraint(equalTo: textView.leadingAnchor),
            scanButton.topAnchor.constraint(equalTo: textView.bottomAnchor, constant: 12),
            scanButton.trailingAnchor.constraint(equalTo: textView.trailingAnchor),
            statusLabel.topAnchor.constraint(equalTo: pairButton.bottomAnchor, constant: 16),
            statusLabel.leadingAnchor.constraint(equalTo: textView.leadingAnchor),
            statusLabel.trailingAnchor.constraint(equalTo: textView.trailingAnchor),
        ])
    }

    @objc private func didTapPair() { submit(textView.text ?? "") }

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
    }
}
