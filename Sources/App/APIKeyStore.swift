import Foundation
import Security

enum APIKeyStore {
    /// Per bundle id, so Zubi Dev never reads or regenerates the installed app's key.
    private static let service = Bundle.main.bundleIdentifier ?? "Zubi"
    private static let account = "api-key"

    static func loadOrCreate() -> String {
        if let existing = load() { return existing }
        return regenerate()
    }

    @discardableResult
    static func regenerate() -> String {
        let key = generate()
        save(key)
        return key
    }

    private static func generate() -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        _ = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        return bytes.map { String(format: "%02x", $0) }.joined()
    }

    private static var baseQuery: [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
    }

    private static func load() -> String? {
        var query = baseQuery
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess,
              let data = item as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    private static func save(_ key: String) {
        SecItemDelete(baseQuery as CFDictionary)
        var add = baseQuery
        add[kSecValueData as String] = Data(key.utf8)
        SecItemAdd(add as CFDictionary, nil)
    }
}
