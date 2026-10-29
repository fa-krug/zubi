import CryptoKit
import Foundation

/// What a client needs to reach this Mac through a relay, as one code to paste into `zubi pair`.
/// It holds the key's secret, so it is as secret as the key. The same format as
/// `cli/internal/tunnel`'s Pairing.
public enum RelayPairing {
    static let prefix = "zubi1:"

    private struct Payload: Codable {
        let relay: String
        let room: String
        let keyID: String
        let secret: String
        let name: String

        enum CodingKeys: String, CodingKey {
            case relay, room, secret, name
            case keyID = "key_id"
        }
    }

    /// "zubi1:" and the JSON, base64url-encoded.
    public static func code(relay: String, room: String, key: APIKey) -> String {
        let payload = Payload(relay: relay, room: room, keyID: key.id, secret: key.secret, name: key.name)
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        let data = (try? encoder.encode(payload)) ?? Data()
        return prefix + data.base64URLEncodedString()
    }

    /// The fields of a code, or nil when it isn't one.
    static func decode(_ code: String) -> (relay: String, room: String, keyID: String, secret: String, name: String)? {
        guard code.hasPrefix(prefix), let data = Data(base64URLEncoded: String(code.dropFirst(prefix.count))),
              let p = try? JSONDecoder().decode(Payload.self, from: data)
        else { return nil }
        return (p.relay, p.room, p.keyID, p.secret, p.name)
    }

    /// Binds a session to its room and key; both sides hash it into the handshake.
    static func prologue(room: String, keyID: String) -> Data {
        Data("zubi-relay-v1\u{0}\(room)\u{0}\(keyID)".utf8)
    }

    /// The handshake's pre-shared key, derived from the device key's secret.
    static func psk(secret: String) -> Data {
        Data(SHA256.hash(data: Data("zubi-relay-v1 psk\u{0}\(secret)".utf8)))
    }
}

extension Data {
    /// RFC 4648 §5, without padding.
    func base64URLEncodedString() -> String {
        base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }

    init?(base64URLEncoded string: String) {
        var base64 = string.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        base64 += String(repeating: "=", count: (4 - base64.count % 4) % 4)
        self.init(base64Encoded: base64)
    }
}
