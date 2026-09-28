import Foundation
import WebKit
@testable import HandoffMobile

final class FakeCookieJar: CookieJar {
    var cookies: [HTTPCookie] = []
    var lastSet: HTTPCookie?
    var deleteCompletionHangs = false   // 模拟「清罐未完成」
    private let recorder: CallRecorder?
    init(recorder: CallRecorder? = nil) { self.recorder = recorder }

    func allCookies(_ completion: @escaping ([HTTPCookie]) -> Void) {
        recorder?.record("allCookies")
        completion(cookies)
    }
    func delete(_ cookie: HTTPCookie, completion: @escaping () -> Void) {
        recorder?.record("delete")
        cookies.removeAll { $0 == cookie }
        if deleteCompletionHangs { return }   // 不回调 → 不得注入
        completion()
    }
    func set(_ cookie: HTTPCookie, completion: @escaping () -> Void) {
        recorder?.record("set"); lastSet = cookie; cookies.append(cookie); completion()
    }
}

final class FakeOriginLoader: OriginLoader {
    private let recorder: CallRecorder?
    init(recorder: CallRecorder? = nil) { self.recorder = recorder }
    func load(origin: String) { recorder?.record("load:\(origin)") }
}
