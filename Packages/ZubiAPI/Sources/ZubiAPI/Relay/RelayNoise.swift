import CryptoKit
import Foundation

/// Noise_NNpsk0_25519_AESGCM_SHA256 (https://noiseprotocol.org/noise.html), the handshake of every
/// relay session: fresh X25519 keys on both sides, and the device key as the pre-shared key that
/// authenticates both. The same steps as `cli/internal/noise`; both are held to
/// `Tests/Fixtures/relay-noise-vectors.json`.
enum RelayNoise {
    /// Exactly 32 bytes, SHA-256's length, so it is the initial hash as it stands.
    static let protocolName = "Noise_NNpsk0_25519_AESGCM_SHA256"
    static let maxMessage = 65535
    static let tagSize = 16
    /// The most one transport message carries.
    static let maxPlaintext = maxMessage - tagSize
}

struct RelayNoiseError: Error, CustomStringConvertible {
    let description: String

    static let decrypt = RelayNoiseError(description: "A relay message failed to authenticate.")
}

/// One direction of a session. Each is used by one task at a time, which is what makes the
/// unchecked Sendable sound.
final class NoiseCipherState: @unchecked Sendable {
    private let key: SymmetricKey
    private var n: UInt64 = 0

    init(key: Data) {
        self.key = SymmetricKey(data: key)
    }

    /// 32 bits of zeros and the counter, big-endian, as the spec has it for AES-GCM.
    private func nonce() throws -> AES.GCM.Nonce {
        var bytes = Data(count: 4)
        withUnsafeBytes(of: n.bigEndian) { bytes.append(contentsOf: $0) }
        return try AES.GCM.Nonce(data: bytes)
    }

    func encrypt(_ plaintext: Data, ad: Data = Data()) throws -> Data {
        guard n < UInt64.max else { throw RelayNoiseError(description: "Nonces exhausted.") }
        let box = try AES.GCM.seal(plaintext, using: key, nonce: nonce(), authenticating: ad)
        n += 1
        return box.ciphertext + box.tag
    }

    func decrypt(_ ciphertext: Data, ad: Data = Data()) throws -> Data {
        guard ciphertext.count >= RelayNoise.tagSize, ciphertext.count <= RelayNoise.maxMessage, n < UInt64.max
        else { throw RelayNoiseError.decrypt }
        let bytes = Data(ciphertext)
        let split = bytes.count - RelayNoise.tagSize
        let plaintext: Data
        do {
            let box = try AES.GCM.SealedBox(nonce: nonce(), ciphertext: bytes.prefix(split), tag: bytes.suffix(RelayNoise.tagSize))
            plaintext = try AES.GCM.open(box, using: key, authenticating: ad)
        } catch {
            throw RelayNoiseError.decrypt
        }
        n += 1
        return plaintext
    }
}

private struct NoiseSymmetricState {
    var ck: Data
    var h: Data
    var cipher: NoiseCipherState?

    init() {
        h = Data(RelayNoise.protocolName.utf8)
        ck = h
    }

    mutating func mixHash(_ data: Data) {
        h = Data(SHA256.hash(data: h + data))
    }

    /// The spec's HKDF over HMAC-SHA256.
    static func hkdf(_ chainingKey: Data, _ ikm: Data, outputs: Int) -> [Data] {
        let temp = hmac(chainingKey, ikm)
        var out: [Data] = []
        var previous = Data()
        for i in 1...outputs {
            previous = hmac(temp, previous + Data([UInt8(i)]))
            out.append(previous)
        }
        return out
    }

    static func hmac(_ key: Data, _ data: Data) -> Data {
        Data(HMAC<SHA256>.authenticationCode(for: data, using: SymmetricKey(data: key)))
    }

    mutating func mixKey(_ ikm: Data) {
        let o = Self.hkdf(ck, ikm, outputs: 2)
        ck = o[0]
        cipher = NoiseCipherState(key: o[1])
    }

    mutating func mixKeyAndHash(_ ikm: Data) {
        let o = Self.hkdf(ck, ikm, outputs: 3)
        ck = o[0]
        mixHash(o[1])
        cipher = NoiseCipherState(key: o[2])
    }

    mutating func encryptAndHash(_ plaintext: Data) throws -> Data {
        let ciphertext = try cipher?.encrypt(plaintext, ad: h) ?? plaintext
        mixHash(ciphertext)
        return ciphertext
    }

    mutating func decryptAndHash(_ ciphertext: Data) throws -> Data {
        let plaintext = try cipher?.decrypt(ciphertext, ad: h) ?? ciphertext
        mixHash(ciphertext)
        return plaintext
    }
}

/// One side of NNpsk0: `-> psk, e` then `<- e, ee`. The initiator writes first.
final class NoiseHandshake {
    private var state = NoiseSymmetricState()
    private let initiator: Bool
    private let psk: Data
    private let ephemeral: Curve25519.KeyAgreement.PrivateKey
    private var remoteEphemeral: Data?
    private var step = 0

    /// `ephemeral` is left out except in tests, which fix it to reproduce known messages.
    init(initiator: Bool, prologue: Data, psk: Data,
         ephemeral: Curve25519.KeyAgreement.PrivateKey = Curve25519.KeyAgreement.PrivateKey()) throws {
        guard psk.count == 32 else { throw RelayNoiseError(description: "The pre-shared key must be 32 bytes.") }
        self.initiator = initiator
        self.psk = psk
        self.ephemeral = ephemeral
        state.mixHash(prologue)
    }

    private var myStep: Int { initiator ? 0 : 1 }

    var isComplete: Bool { step == 2 }

    func writeMessage(_ payload: Data = Data()) throws -> Data {
        guard step == myStep else { throw RelayNoiseError(description: "Not this side's turn to write.") }
        if step == 0 { state.mixKeyAndHash(psk) }
        let publicKey = ephemeral.publicKey.rawRepresentation
        state.mixHash(publicKey)
        state.mixKey(publicKey)
        if step == 1 {
            guard let remoteEphemeral else { throw RelayNoiseError.decrypt }
            try mixEE(remoteEphemeral)
        }
        let sealed = try state.encryptAndHash(payload)
        step += 1
        return publicKey + sealed
    }

    func readMessage(_ message: Data) throws -> Data {
        guard step != myStep, step < 2 else { throw RelayNoiseError(description: "Not this side's turn to read.") }
        let bytes = Data(message)
        guard bytes.count >= 32 + RelayNoise.tagSize, bytes.count <= RelayNoise.maxMessage else {
            throw RelayNoiseError.decrypt
        }
        if step == 0 { state.mixKeyAndHash(psk) }
        let remote = Data(bytes.prefix(32))
        state.mixHash(remote)
        state.mixKey(remote)
        if step == 1 {
            try mixEE(remote)
        } else {
            remoteEphemeral = remote
        }
        let payload = try state.decryptAndHash(Data(bytes.dropFirst(32)))
        step += 1
        return payload
    }

    private func mixEE(_ remote: Data) throws {
        let shared: SharedSecret
        do {
            let publicKey = try Curve25519.KeyAgreement.PublicKey(rawRepresentation: remote)
            shared = try ephemeral.sharedSecretFromKeyAgreement(with: publicKey)
        } catch {
            throw RelayNoiseError.decrypt
        }
        state.mixKey(shared.withUnsafeBytes { Data($0) })
    }

    /// The send and receive cipher states for this side, and the handshake hash both sides share.
    func split() throws -> (send: NoiseCipherState, receive: NoiseCipherState, hash: Data) {
        guard isComplete else { throw RelayNoiseError(description: "The handshake isn't complete.") }
        let o = NoiseSymmetricState.hkdf(state.ck, Data(), outputs: 2)
        let first = NoiseCipherState(key: o[0]), second = NoiseCipherState(key: o[1])
        return initiator ? (first, second, state.h) : (second, first, state.h)
    }
}
