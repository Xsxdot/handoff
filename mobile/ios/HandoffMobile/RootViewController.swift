import UIKit

// 启动路径分发（I2 条 15/16）：有已存配对串 → 机器列表；无或 Pair(stored) 失败 → 配对屏 + 可重试。
final class RootViewController: UIViewController {
    private let composition: AppComposition

    init(composition: AppComposition) {
        self.composition = composition
        super.init(nibName: nil, bundle: nil)
    }
    required init?(coder: NSCoder) { fatalError("init(coder:) 未实现") }

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .systemBackground
        do {
            if try composition.pairingService.pairFromStoredBundle() {
                Log.shell.info("启动路径：已有配对串 → 机器列表")
                showMachineList()
            } else {
                showPairing(nil)
            }
        } catch let e as ShellError {
            Log.shell.error("启动路径配对失败：\(e.title, privacy: .public)")
            showPairing(e)
        } catch {
            showPairing(.pairing(String(describing: error)))
        }
    }

    private func showPairing(_ error: ShellError?) {
        let vc = PairingViewController(entry: PairEntry(service: composition.pairingService)) { [weak self] in
            self?.showMachineList()
        }
        vc.initialError = error
        setRootContent(vc)
    }

    private func showMachineList() {
        setRootContent(MachineListViewController(composition: composition))
    }

    private func setRootContent(_ vc: UIViewController) {
        children.forEach { $0.willMove(toParent: nil); $0.view.removeFromSuperview(); $0.removeFromParent() }
        addChild(vc)
        vc.view.frame = view.bounds
        vc.view.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        view.addSubview(vc.view)
        vc.didMove(toParent: self)
    }
}
