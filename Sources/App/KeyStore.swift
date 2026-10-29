import Foundation
import Security
import os
import ZubiAPI

/// One device's key as Settings shows it.
struct StoredKey: Codable, Identifiable, Equatable {
    let id: String
    var name: String
    var access: KeyAccess
    let created: Date
    var secret: String

    var apiKey: APIKey { APIKey(id: id, name: name, secret: secret, access: access) }
}

/// Every key in one login-keychain item (service: the bundle id, so Zubi Dev never touches the
/// installed app's keys; account `api-keys`), as JSON: one read at launch, and an add or delete can
/// never leave a key half-stored. When a key was last used changes with every request and is not
/// secret, so that lives in UserDefaults instead.
enum KeyStore {
    private static let service = Bundle.main.bundleIdentifier ?? "Zubi"
    private static let account = "api-keys"
    /// The single key from before there were several.
    private static let legacyAccount = "api-key"

    struct KeychainError: Error, CustomStringConvertible {
        let status: OSStatus
        var description: String {
            let message = SecCopyErrorMessageString(status, nil) as String? ?? "error \(status)"
            return "The Keychain refused: \(message)"
        }
    }

    /// The keys. On the first launch with several keys, the single key from before becomes "Default",
    /// with Read & Write as it had, so clients that have it keep working; with no key at all, a new
    /// "Default" is made so the test command works out of the box.
    static func load() throws -> [StoredKey] {
        if let data = try read(account) {
            return try JSONDecoder().decode([StoredKey].self, from: data)
        }
        let legacy = try read(legacyAccount).flatMap { String(data: $0, encoding: .utf8) }
        let keys = [StoredKey(id: UUID().uuidString, name: "Default", access: .readWrite, created: Date(),
                              secret: legacy ?? generateSecret())]
        try save(keys)
        // Only once the new item holds it, so a failed save loses nothing.
        if legacy != nil { SecItemDelete(query(legacyAccount) as CFDictionary) }
        return keys
    }

    static func save(_ keys: [StoredKey]) throws {
        let data = try JSONEncoder().encode(keys)
        let status = SecItemUpdate(query(account) as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        if status == errSecItemNotFound {
            var add = query(account)
            add[kSecValueData as String] = data
            let added = SecItemAdd(add as CFDictionary, nil)
            guard added == errSecSuccess else { throw KeychainError(status: added) }
        } else if status != errSecSuccess {
            throw KeychainError(status: status)
        }
    }

    static func generateSecret() -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        _ = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        return bytes.map { String(format: "%02x", $0) }.joined()
    }

    private static func query(_ account: String) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
    }

    /// Nil when there is no such item. Any other failure throws, so a Keychain that is merely locked
    /// or refusing is never mistaken for "no keys" and overwritten.
    private static func read(_ account: String) throws -> Data? {
        var query = query(account)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = item as? Data else { throw KeychainError(status: status) }
        return data
    }
}

/// The keys as the server reads them: from its own threads, on every request.
final class KeyRing: Sendable {
    private let state = OSAllocatedUnfairLock<[APIKey]>(initialState: [])

    var keys: [APIKey] { state.withLock { $0 } }

    func set(_ keys: [APIKey]) { state.withLock { $0 = keys } }
}

extension KeyAccess {
    var label: String {
        switch self {
        case .read: "Read"
        case .readWrite: "Read & Write"
        }
    }
}
