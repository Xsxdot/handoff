import Foundation
import Security

// 安全存储缝（I2）。生产 = KeychainSecureStore；测试可注入假实现。
protocol SecureStore: AnyObject {
    func save(_ value: String) throws
    func load() throws -> String?
    func delete() throws
}

struct KeychainError: Error { let status: OSStatus }

// Keychain 实现（I2 / contract §4.3 条 11/13）：
// - kSecClassGenericPassword；service/account 固定；
// - kSecAttrAccessible = kSecAttrAccessibleWhenUnlockedThisDeviceOnly；
// - 不设 kSecAttrSynchronizable（不跨设备同步，条 13 负向）。
// 存的是**原始 bundle 字符串**（条 10/11），不解析、不改写。
final class KeychainSecureStore: SecureStore {
    private let service: String
    private let account: String

    init(service: String = "dev.handoff.HandoffMobile.pairbundle",
         account: String = "pair_bundle") {
        self.service = service
        self.account = account
    }

    private func baseQuery() -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: service,
         kSecAttrAccount as String: account]
    }

    func save(_ value: String) throws {
        let data = Data(value.utf8)
        try delete()   // upsert：先删旧值
        var q = baseQuery()
        q[kSecValueData as String] = data
        q[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        // 负向（条 13）：不写 kSecAttrSynchronizable。
        let status = SecItemAdd(q as CFDictionary, nil)
        guard status == errSecSuccess else {
            Log.shell.error("Keychain 写入失败 status=\(status, privacy: .public)")
            throw KeychainError(status: status)
        }
        Log.shell.info("Keychain 写入配对串成功 bytes=\(data.count, privacy: .public)")
    }

    func load() throws -> String? {
        var q = baseQuery()
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        let status = SecItemCopyMatching(q as CFDictionary, &out)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = out as? Data else {
            Log.shell.error("Keychain 读取失败 status=\(status, privacy: .public)")
            throw KeychainError(status: status)
        }
        Log.shell.debug("Keychain 读回配对串 bytes=\(data.count, privacy: .public)")
        return String(data: data, encoding: .utf8)
    }

    func delete() throws {
        let status = SecItemDelete(baseQuery() as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainError(status: status)
        }
    }
}
