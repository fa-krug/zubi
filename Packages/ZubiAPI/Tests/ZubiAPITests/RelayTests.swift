import CryptoKit
import XCTest
@testable import ZubiAPI

/// The app's half of the relay protocol, held to the vectors the CLI's tests use too. They come from
/// an independent Noise implementation (github.com/flynn/noise), so neither side only agrees with
/// itself.
final class RelayTests: XCTestCase {
    private struct Fixture: Decodable {
        struct Message: Decodable {
            let payload: String
            let ciphertext: String
        }

        struct Vector: Decodable {
            let name: String
            let prologue: String
            let psk: String
            let initiator_ephemeral: String
            let responder_ephemeral: String
            let handshake_hash: String
            let messages: [Message]
        }

        struct Derivation: Decodable {
            let room: String
            let key_id: String
            let secret: String
            let prologue: String
            let psk: String
        }

        struct Pairing: Decodable {
            let code: String
            let relay: String
            let room: String
            let key_id: String
            let secret: String
            let name: String
        }

        let `protocol`: String
        let derivation: Derivation
        let pairing: Pairing
        let vectors: [Vector]
    }

    private func fixture() throws -> Fixture {
        let url = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent()
            .appendingPathComponent("Fixtures/relay-noise-vectors.json")
        return try JSONDecoder().decode(Fixture.self, from: Data(contentsOf: url))
    }

    func testVectors() throws {
        let file = try fixture()
        XCTAssertEqual(file.protocol, RelayNoise.protocolName)
        XCTAssertFalse(file.vectors.isEmpty)
        for v in file.vectors {
            let initiator = try NoiseHandshake(
                initiator: true, prologue: hex(v.prologue), psk: hex(v.psk),
                ephemeral: Curve25519.KeyAgreement.PrivateKey(rawRepresentation: hex(v.initiator_ephemeral)))
            let responder = try NoiseHandshake(
                initiator: false, prologue: hex(v.prologue), psk: hex(v.psk),
                ephemeral: Curve25519.KeyAgreement.PrivateKey(rawRepresentation: hex(v.responder_ephemeral)))
            let m = v.messages

            let first = try initiator.writeMessage(hex(m[0].payload))
            XCTAssertEqual(first, hex(m[0].ciphertext), "\(v.name): message 1")
            XCTAssertEqual(try responder.readMessage(first), hex(m[0].payload), "\(v.name): payload 1")
            let second = try responder.writeMessage(hex(m[1].payload))
            XCTAssertEqual(second, hex(m[1].ciphertext), "\(v.name): message 2")
            XCTAssertEqual(try initiator.readMessage(second), hex(m[1].payload), "\(v.name): payload 2")

            let (iSend, iReceive, iHash) = try initiator.split()
            let (rSend, rReceive, rHash) = try responder.split()
            XCTAssertEqual(iHash, hex(v.handshake_hash), "\(v.name): handshake hash")
            XCTAssertEqual(rHash, iHash)
            for (i, message) in m.dropFirst(2).enumerated() {
                let (send, receive) = i % 2 == 0 ? (iSend, rReceive) : (rSend, iReceive)
                let sealed = try send.encrypt(hex(message.payload))
                XCTAssertEqual(sealed, hex(message.ciphertext), "\(v.name): transport \(i)")
                XCTAssertEqual(try receive.decrypt(sealed), hex(message.payload), "\(v.name): transport \(i) payload")
            }
        }
    }

    func testDerivation() throws {
        let d = try fixture().derivation
        XCTAssertEqual(RelayPairing.prologue(room: d.room, keyID: d.key_id), hex(d.prologue))
        XCTAssertEqual(RelayPairing.psk(secret: d.secret), hex(d.psk))
    }

    func testPairingCode() throws {
        let p = try fixture().pairing
        // The CLI's code reads here...
        let decoded = try XCTUnwrap(RelayPairing.decode(p.code))
        XCTAssertEqual(decoded.relay, p.relay)
        XCTAssertEqual(decoded.room, p.room)
        XCTAssertEqual(decoded.keyID, p.key_id)
        XCTAssertEqual(decoded.secret, p.secret)
        XCTAssertEqual(decoded.name, p.name)
        // ...and the app's own code carries the same fields; the CLI's tests read this form.
        let key = APIKey(id: p.key_id, name: p.name, secret: p.secret, access: .read)
        let code = RelayPairing.code(relay: p.relay, room: p.room, key: key)
        XCTAssertTrue(code.hasPrefix("zubi1:"))
        XCTAssertFalse(code.contains("="))
        let own = try XCTUnwrap(RelayPairing.decode(code))
        XCTAssertEqual(own.relay, p.relay)
        XCTAssertEqual(own.keyID, p.key_id)
        XCTAssertNil(RelayPairing.decode("zubi2:abc"))
    }

    func testWrongKeyAndTampering() throws {
        let initiator = try NoiseHandshake(initiator: true, prologue: Data("p".utf8), psk: Data(repeating: 1, count: 32))
        let stranger = try NoiseHandshake(initiator: false, prologue: Data("p".utf8), psk: Data(repeating: 2, count: 32))
        XCTAssertThrowsError(try stranger.readMessage(initiator.writeMessage()))

        let a = try NoiseHandshake(initiator: true, prologue: Data(), psk: Data(repeating: 3, count: 32))
        let b = try NoiseHandshake(initiator: false, prologue: Data(), psk: Data(repeating: 3, count: 32))
        _ = try b.readMessage(a.writeMessage())
        _ = try a.readMessage(b.writeMessage())
        let (send, _, _) = try a.split()
        let (_, receive, _) = try b.split()
        var sealed = try send.encrypt(Data("hello".utf8))
        sealed[sealed.startIndex] ^= 1
        XCTAssertThrowsError(try receive.decrypt(sealed))
        XCTAssertThrowsError(try NoiseHandshake(initiator: true, prologue: Data(), psk: Data(count: 5)))
    }

    func testRoomAndRelayAddress() throws {
        let key = RelayHost.newRoomKey()
        let host = try RelayHost(relay: URL(string: "wss://203.0.113.7")!, roomKey: key, keys: { [] },
                                 targetHost: "127.0.0.1", targetPort: 8787, onStatus: { _ in })
        let publicKey = try Curve25519.Signing.PrivateKey(rawRepresentation: key).publicKey.rawRepresentation
        XCTAssertEqual(host.room, publicKey.base64URLEncodedString())
        XCTAssertEqual(host.room.count, 43)
        XCTAssertTrue(RelayHost.isValidRelay(URL(string: "wss://relay.example.com")))
        XCTAssertFalse(RelayHost.isValidRelay(URL(string: "ws://relay.example.com")))
        XCTAssertFalse(RelayHost.isValidRelay(URL(string: "https://relay.example.com")))
        XCTAssertFalse(RelayHost.isValidRelay(nil))
    }

    private func hex(_ string: String) -> Data {
        var data = Data()
        var index = string.startIndex
        while index < string.endIndex {
            let next = string.index(index, offsetBy: 2)
            data.append(UInt8(string[index..<next], radix: 16)!)
            index = next
        }
        return data
    }
}
