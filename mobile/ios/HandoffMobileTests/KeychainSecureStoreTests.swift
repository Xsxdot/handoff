import XCTest
import Security
@testable import HandoffMobile

final class KeychainSecureStoreTests: XCTestCase {
    private func makeStore(_ suffix: String) -> KeychainSecureStore {
        KeychainSecureStore(service: "dev.handoff.HandoffMobile.tests.\(suffix)", account: "pair_bundle")
    }

    func testRoundTripBytesEqual() throws {
        let store = makeStore(UUID().uuidString)
        defer { try? store.delete() }
        try store.save("pair-{\"v\":1}-value")
        XCTAssertEqual(try store.load(), "pair-{\"v\":1}-value")
    }

    func testDeleteClears() throws {
        let store = makeStore(UUID().uuidString)
        try store.save("x")
        try store.delete()
        XCTAssertNil(try store.load())
    }

    func testAttributeWhenUnlockedThisDeviceOnlyAndNoSynchronizable() throws {
        let suffix = UUID().uuidString
        let store = makeStore(suffix)
        defer { try? store.delete() }
        try store.save("x")

        let q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: "dev.handoff.HandoffMobile.tests.\(suffix)",
            kSecAttrAccount as String: "pair_bundle",
            kSecReturnAttributes as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var out: CFTypeRef?
        XCTAssertEqual(SecItemCopyMatching(q as CFDictionary, &out), errSecSuccess)
        let attrs = out as? [String: Any] ?? [:]
        XCTAssertEqual(attrs[kSecAttrAccessible as String] as? String,
                       kSecAttrAccessibleWhenUnlockedThisDeviceOnly as String)   // 条 11
        // 条 13（修订，2026-09-28）：平台把未设的 kSecAttrSynchronizable 回读为 0（非 nil）。
        // 契约语义是「不得云同步」，故断言「不存在 Synchronizable==true」，而非键为 nil。
        let syncValue = attrs[kSecAttrSynchronizable as String]
        let syncIsTrue = (syncValue as? Bool) ?? (syncValue as? NSNumber)?.boolValue ?? false
        XCTAssertFalse(syncIsTrue, "不得存在 Synchronizable==true 的条目（条 13：不得云同步）")
    }
}
