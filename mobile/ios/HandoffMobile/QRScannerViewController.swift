import AVFoundation
import UIKit

// 扫码入口（spec 故事 2）。模拟器无摄像头 → isCameraAvailable=false，配对屏隐藏该入口。
// 扫到的原始串经 onPayload 交给 PairEntry.submit，与粘贴同一归一入口（条 7）。
final class QRScannerViewController: UIViewController, AVCaptureMetadataOutputObjectsDelegate {
    static var isCameraAvailable: Bool { AVCaptureDevice.default(for: .video) != nil }

    private let onPayload: (String) -> Void
    private let session = AVCaptureSession()

    init(onPayload: @escaping (String) -> Void) {
        self.onPayload = onPayload
        super.init(nibName: nil, bundle: nil)
    }
    required init?(coder: NSCoder) { fatalError("init(coder:) 未实现") }

    override func viewDidLoad() {
        super.viewDidLoad()
        title = "扫码"
        view.backgroundColor = .black
        guard let device = AVCaptureDevice.default(for: .video),
              let input = try? AVCaptureDeviceInput(device: device),
              session.canAddInput(input) else {
            Log.shell.error("无可用摄像头，扫码不可用")
            return
        }
        session.addInput(input)
        let output = AVCaptureMetadataOutput()
        if session.canAddOutput(output) {
            session.addOutput(output)
            output.setMetadataObjectsDelegate(self, queue: .main)
            output.metadataObjectTypes = [.qr]
        }
        let layer = AVCaptureVideoPreviewLayer(session: session)
        layer.frame = view.bounds
        layer.videoGravity = .resizeAspectFill
        view.layer.addSublayer(layer)
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in self?.session.startRunning() }
    }

    override func viewWillDisappear(_ animated: Bool) {
        super.viewWillDisappear(animated)
        session.stopRunning()
    }

    func metadataOutput(_ output: AVCaptureMetadataOutput, didOutput metadataObjects: [AVMetadataObject],
                        from connection: AVCaptureConnection) {
        guard let obj = metadataObjects.first as? AVMetadataMachineReadableCodeObject,
              obj.type == .qr, let payload = obj.stringValue else { return }
        Log.shell.info("扫码得到载荷 bytes=\(payload.utf8.count, privacy: .public)")
        session.stopRunning()
        onPayload(payload)
    }
}
