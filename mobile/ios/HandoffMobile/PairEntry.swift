import Foundation

// 配对入口归一协调器：扫码与粘贴两条 UI 路径都调 submit，
// 保证只有 PairingService.pair 一个归一化点（contract §4.2 条 7）。
final class PairEntry {
    private let service: PairingService
    init(service: PairingService) { self.service = service }

    func submit(raw: String, completion: @escaping (Result<Void, ShellError>) -> Void) {
        do {
            try service.pair(bundleJSON: raw)
            completion(.success(()))
        } catch let e as ShellError {
            completion(.failure(e))
        } catch {
            completion(.failure(.pairing(String(describing: error))))
        }
    }
}
