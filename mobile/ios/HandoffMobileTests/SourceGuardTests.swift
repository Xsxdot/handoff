import XCTest

// 源码 guard（contract §4.1 条 3/4、§4.5 条 31、§4.7 条 36/37）：静态扫描壳源码。
// 能变红：引入 JSONDecoder / URLSession / 非导出 Bind 符号 / 改 cookie 名常量即失败。
final class SourceGuardTests: XCTestCase {
    private static let allowedBindSymbols: Set<String> = [
        "BindPair", "BindMachineCount", "BindMachineAt", "BindOrigin",
        "BindSessionCookie", "BindSwitchMachine", "BindClose", "BindMachine",
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
