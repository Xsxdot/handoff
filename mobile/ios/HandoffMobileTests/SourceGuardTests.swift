import XCTest

// 源码 guard（contract §4.1 条 3/4、§4.5 条 31、§4.7 条 36/37）：静态扫描壳源码。
// 能变红：引入 JSONDecoder / URLSession / 非导出 Bind 符号 / 改 cookie 名常量即失败。
final class SourceGuardTests: XCTestCase {
    private static let allowedBindSymbols: Set<String> = [
        "BindPair", "BindMachineCount", "BindMachineAt", "BindOrigin",
        "BindSessionCookie", "BindSwitchMachine", "BindClose", "BindMachine",
        // B432：壳上报 APNs device 的唯一导出面。
        "BindRegisterPushDevice",
    ]

    private var appSources: [String] {
        let dir = URL(fileURLWithPath: #filePath)          // …/HandoffMobileTests/SourceGuardTests.swift
            .deletingLastPathComponent()                   // HandoffMobileTests
            .deletingLastPathComponent()                   // mobile/ios
            .appendingPathComponent("HandoffMobile")
        let urls = (try? FileManager.default.contentsOfDirectory(at: dir, includingPropertiesForKeys: nil)) ?? []
        return urls.filter { $0.pathExtension == "swift" }
                   .compactMap { try? String(contentsOf: $0, encoding: .utf8) }
    }

    func testNoNonExportedBindSymbols() {
        let text = appSources.joined(separator: "\n")
        let regex = try! NSRegularExpression(pattern: "\\bBind[A-Z][A-Za-z0-9]*")
        let range = NSRange(text.startIndex..., in: text)
        let found = Set(regex.matches(in: text, range: range).compactMap { m -> String? in
            guard let r = Range(m.range, in: text) else { return nil }
            return String(text[r])
        })
        let illegal = found.subtracting(Self.allowedBindSymbols)
        XCTAssertTrue(illegal.isEmpty, "壳源码出现非导出面绑定符号：\(illegal)")   // 条 3
    }

    // 条 3 负向（B432 修订）：壳不得出现绑定面绕过符号——Dial / Credential 一律禁；
    // Token 语境只放行 APNs device（`deviceToken` 与系统回调名 `…WithDeviceToken`），
    // 其余含 Token 的标识（BindToken / AccessToken / agentdToken …）一律红。
    //
    // 为什么必须放行 APNs device：`application(_:didRegisterForRemoteNotificationsWithDeviceToken:)`
    // 是系统签名，写不出第二个名字；APNs device 经 `pushHandle` 参数名交给 Go 核，
    // 与 agentd 主令牌是两回事（主令牌只在核手里）。正则前界 `\b` 保证
    // `…WithDeviceToken` 之外的 `xxxToken` 不会被误放进白名单。
    func testNoCookieBypassSymbols() {
        let text = appSources.joined(separator: "\n")
        for forbidden in ["Dial", "Credential"] {
            XCTAssertFalse(text.contains(forbidden),
                           "壳源码出现绕过 cookie 闸的符号（条 3 负向）：\(forbidden)")
        }
        XCTAssertTrue(Self.tokenBypassHits(in: text).isEmpty,
                      "壳源码出现主令牌语境标识（条 3 负向）：\(Self.tokenBypassHits(in: text))")

        // 负向自证：守卫必须真能抓——否则正则退化成永假时本测试自己也不会红。
        let poisoned = text + "\nlet a = BindToken; let b = AccessToken; let c = agentdToken\n"
        XCTAssertFalse(Self.tokenBypassHits(in: poisoned).isEmpty,
                       "守卫必须抓得到 BindToken/AccessToken/agentdToken（负向自证）")
        // APNs 语境必须仍然放行（否则真机回调一写就误伤）。
        let apns = "func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data)"
        XCTAssertTrue(Self.tokenBypassHits(in: apns).isEmpty, "APNs 语境不得误伤")
    }

    // tokenBypassHits 摘出「主令牌语境」的标识：含 Token 的词，除 APNs 白名单外全算。
    static func tokenBypassHits(in text: String) -> [String] {
        let regex = try! NSRegularExpression(pattern: "\\b[A-Za-z0-9_]*Token\\b", options: [])
        let range = NSRange(text.startIndex..., in: text)
        return regex.matches(in: text, range: range).compactMap { m -> String? in
            guard let r = Range(m.range, in: text) else { return nil }
            let word = String(text[r])
            if word.lowercased() == "devicetoken" || word.hasSuffix("WithDeviceToken") {
                return nil   // APNs 语境
            }
            return word
        }
    }

    func testNoBundleJSONParsing() {
        for src in appSources {
            XCTAssertFalse(src.contains("JSONDecoder"), "壳不得解析 bundle（条 4 负向）")
            XCTAssertFalse(src.contains("JSONSerialization"), "壳不得解析 bundle（条 4 负向）")
        }
    }

    func testNoOwnHTTPClient() {
        for src in appSources {
            XCTAssertFalse(src.contains("URLSession"), "壳不得自建 HTTP 客户端（条 36）")
        }
    }

    func testCookieNameConstant() {
        XCTAssertTrue(appSources.joined(separator: "\n").contains("\"handoff_session\""),
                      "cookie 名常量必须逐字为 handoff_session（条 31）")
    }
}
