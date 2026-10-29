import CryptoKit
import Foundation
import Network

/// The Mac's side of a relay (`zubi relay`): it holds this Mac's room there, and serves each
/// client's session by answering its Noise handshake with the device key the client names, then
/// piping the decrypted stream into Zubi's own server on this Mac. So everything a client does
/// through the relay goes through the same key check, logging and Settings as a direct request,
/// and the relay sees only ciphertext. docs/features/relay.md describes the protocol;
/// `cli/internal/tunnel` is the client's side.
public final class RelayHost: @unchecked Sendable {
    public enum Status: Equatable, Sendable {
        case connecting
        case connected
        case failed(String)
    }

    /// What the host signs, followed by the relay's nonce, to register its room.
    static let registerContext = "zubi-relay-v1/register"

    /// This Mac's room: the room key's public half, base64url.
    public let room: String
    public let relay: URL

    private let signingKey: Curve25519.Signing.PrivateKey
    private let keys: @Sendable () -> [APIKey]
    private let targetHost: String
    private let targetPort: Int
    private let onStatus: @Sendable (Status) -> Void
    private let session = URLSession(configuration: .ephemeral)
    private let lock = NSLock()
    private var runTask: Task<Void, Never>?

    /// `roomKey` is the raw Ed25519 private key from `newRoomKey()`. `keys` is read for every
    /// session, so a key deleted or regenerated in Settings stops working at once.
    public init(relay: URL, roomKey: Data, keys: @escaping @Sendable () -> [APIKey], targetHost: String,
                targetPort: Int, onStatus: @escaping @Sendable (Status) -> Void) throws {
        signingKey = try Curve25519.Signing.PrivateKey(rawRepresentation: roomKey)
        room = signingKey.publicKey.rawRepresentation.base64URLEncodedString()
        self.relay = relay
        self.keys = keys
        self.targetHost = targetHost
        self.targetPort = targetPort
        self.onStatus = onStatus
    }

    /// A new room key, to keep in the Keychain: the room, and so every pairing code, depends on it.
    public static func newRoomKey() -> Data {
        Curve25519.Signing.PrivateKey().rawRepresentation
    }

    /// Whether `url` is a relay address this host accepts: wss:// only.
    public static func isValidRelay(_ url: URL?) -> Bool {
        guard let url, url.scheme == "wss", let host = url.host, !host.isEmpty else { return false }
        return true
    }

    /// The code a client pastes into `zubi pair` to use `key` through this relay.
    public func pairingCode(for key: APIKey) -> String {
        RelayPairing.code(relay: relay.absoluteString, room: room, key: key)
    }

    /// Connects, and reconnects after every failure, until `stop()`.
    public func start() {
        lock.withLock {
            guard runTask == nil else { return }
            runTask = Task { [self] in await run() }
        }
    }

    /// Disconnects, ending every session.
    public func stop() {
        lock.withLock {
            runTask?.cancel()
            runTask = nil
        }
        session.invalidateAndCancel()
    }

    private func run() async {
        var delay: UInt64 = 1
        while !Task.isCancelled {
            onStatus(.connecting)
            do {
                try await connect { delay = 1 }
                onStatus(.failed("The relay closed the connection."))
            } catch {
                if Task.isCancelled { return }
                // URLSession says only "bad response from the server" when the WebSocket is refused,
                // so ask the address what it is.
                let message = error is URLError ? await diagnose(error) : Self.describe(error)
                if Task.isCancelled { return }
                onStatus(.failed(message))
            }
            try? await Task.sleep(nanoseconds: delay * 1_000_000_000)
            delay = min(delay * 2, 60)
        }
    }

    private static func describe(_ error: Error) -> String {
        if let relayError = error as? RelayNoiseError { return relayError.description }
        return error.localizedDescription
    }

    /// Why connecting failed, in words someone setting up a relay can act on. A relay answers its
    /// root with a line starting "Zubi relay", so anything else at the address isn't one: typically
    /// a tunnel or proxy pointing at the wrong port.
    private func diagnose(_ error: Error) async -> String {
        let host = relay.host ?? relay.absoluteString
        guard var parts = URLComponents(url: relay, resolvingAgainstBaseURL: false) else {
            return Self.describe(error)
        }
        parts.scheme = parts.scheme == "ws" ? "http" : "https"
        parts.path = "/"
        guard let root = parts.url else { return Self.describe(error) }

        let status: Int
        let body: String
        do {
            let (data, response) = try await session.data(for: URLRequest(url: root, timeoutInterval: 10))
            status = (response as? HTTPURLResponse)?.statusCode ?? 0
            body = String(decoding: data.prefix(300), as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
        } catch {
            return "Can't reach \(host): \(error.localizedDescription)"
        }

        if body.hasPrefix("Zubi relay") {
            // The relay is there; ask why it refused the WebSocket.
            let probe = (try? await session.data(for: URLRequest(url: root.appendingPathComponent("v1/host"),
                                                                    timeoutInterval: 10)))?.1
            switch (probe as? HTTPURLResponse)?.statusCode {
            case 403:
                return "The relay at \(host) refused the connection: its proxy didn't say the connection came over "
                    + "HTTPS. The proxy must pass the X-Forwarded-Proto header on."
            case 429:
                return "The relay at \(host) is limiting connections from this network for a moment. Zubi tries again."
            case 426:
                return Self.describe(error) // the relay would take a WebSocket: something on the way dropped it
            default:
                return "The relay at \(host) answers, but the WebSocket didn't get through. Make sure the proxy "
                    + "passes WebSocket connections on."
            }
        }

        switch status {
        case 502, 503, 504, 520...530:
            return "\(host) is a proxy or tunnel, but nothing answers behind it (\(status)). Check that "
                + "“zubi relay” runs, and that the tunnel points at its address, such as http://127.0.0.1:8080."
        default:
            let said = body.isEmpty ? "" : ": “\(body.prefix(60))”"
            return "\(host) answers, but it isn't a Zubi relay (\(status)\(said)). Point the tunnel or proxy at the "
                + "relay's address, such as http://127.0.0.1:8080 rather than localhost, and check that "
                + "“zubi relay” runs there."
        }
    }

    /// One control connection: the challenge, the signed registration, then sessions as they come.
    private func connect(registered: () -> Void) async throws {
        let ws = session.webSocketTask(with: relay.appendingPathComponent("v1").appendingPathComponent("host"))
        ws.resume()
        try await withTaskCancellationHandler {
            defer { ws.cancel(with: .goingAway, reason: nil) }
            let challenge = try await Self.receiveJSON(ws)
            guard challenge["type"] as? String == "challenge", let text = challenge["nonce"] as? String,
                  let nonce = Data(base64URLEncoded: text)
            else { throw RelayNoiseError(description: "The relay didn't send a challenge.") }
            let signature = try signingKey.signature(for: Data(Self.registerContext.utf8) + nonce)
            try await Self.sendJSON(ws, ["type": "register", "key": room,
                                         "signature": signature.base64URLEncodedString()])
            let answer = try await Self.receiveJSON(ws)
            guard answer["type"] as? String == "registered" else {
                throw RelayNoiseError(description: answer["message"] as? String ?? "The relay refused this Mac.")
            }
            registered()
            onStatus(.connected)

            // The relay drops a host silent for 90 seconds; the pings also keep platforms that
            // sleep idle services awake.
            let pinger = Task {
                while !Task.isCancelled {
                    try? await Task.sleep(nanoseconds: 30_000_000_000)
                    try? await Self.sendJSON(ws, ["type": "ping"])
                }
            }
            defer { pinger.cancel() }
            while true {
                let message = try await Self.receiveJSON(ws)
                switch message["type"] as? String ?? "" {
                case "session":
                    if let id = message["id"] as? String {
                        Task { await self.serve(sessionID: id) }
                    }
                case "error":
                    throw RelayNoiseError(description: message["message"] as? String ?? "The relay closed the connection.")
                default:
                    break // pong, and anything a newer relay sends
                }
            }
        } onCancel: {
            ws.cancel(with: .goingAway, reason: nil)
        }
    }

    /// One client's session, from the handshake to the end of both directions.
    private func serve(sessionID: String) async {
        let ws = session.webSocketTask(with: relay.appendingPathComponent("v1").appendingPathComponent("accept")
            .appendingPathComponent(sessionID))
        ws.resume()
        defer { ws.cancel(with: .normalClosure, reason: nil) }
        // The client learns its key was refused from the close code, 1008, before any data flows.
        let refuse = { ws.cancel(with: .policyViolation, reason: Data("the key was refused".utf8)) }
        do {
            guard case .data(let first) = try await ws.receive() else { return refuse() }
            let bytes = [UInt8](first)
            guard bytes.count >= 2, bytes[0] == 1, bytes.count >= 2 + Int(bytes[1]) else { return refuse() }
            let keyEnd = 2 + Int(bytes[1])
            let keyID = String(decoding: bytes[2..<keyEnd], as: UTF8.self)
            guard let key = keys().first(where: { $0.id == keyID }) else { return refuse() }
            let handshake = try NoiseHandshake(initiator: false, prologue: RelayPairing.prologue(room: room, keyID: keyID),
                                               psk: RelayPairing.psk(secret: key.secret))
            do {
                _ = try handshake.readMessage(Data(bytes[keyEnd...]))
            } catch {
                return refuse()
            }
            try await ws.send(.data(handshake.writeMessage()))
            let (send, receive, _) = try handshake.split()

            let local = try await LocalConnection.open(host: targetHost, port: targetPort)
            defer { local.cancel() }
            await withTaskGroup(of: Bool.self) { group in
                group.addTask { await Self.relayToLocal(ws, receive, local) }
                group.addTask { await Self.localToRelay(local, send, ws) }
                // A failure in either direction ends both; a clean end of one waits for the other.
                for await finished in group where !finished {
                    ws.cancel(with: .goingAway, reason: nil)
                    local.cancel()
                }
            }
        } catch {
            return
        }
    }

    /// Decrypts what the client sends into Zubi's server. An empty message is the client's end of
    /// stream, passed on as the end of the TCP stream.
    private static func relayToLocal(_ ws: URLSessionWebSocketTask, _ cipher: NoiseCipherState,
                                     _ local: LocalConnection) async -> Bool {
        do {
            while true {
                guard case .data(let sealed) = try await ws.receive() else { return false }
                let plaintext = try cipher.decrypt(sealed)
                if plaintext.isEmpty {
                    try await local.finish()
                    return true
                }
                try await local.send(plaintext)
            }
        } catch {
            return false
        }
    }

    /// Encrypts what Zubi's server answers back to the client, ending with an empty message.
    private static func localToRelay(_ local: LocalConnection, _ cipher: NoiseCipherState,
                                     _ ws: URLSessionWebSocketTask) async -> Bool {
        do {
            while true {
                let (data, isEnd) = try await local.receive(maximumLength: RelayNoise.maxPlaintext)
                if let data, !data.isEmpty {
                    try await ws.send(.data(cipher.encrypt(data)))
                }
                if isEnd {
                    try await ws.send(.data(cipher.encrypt(Data())))
                    return true
                }
            }
        } catch {
            return false
        }
    }

    private static func receiveJSON(_ ws: URLSessionWebSocketTask) async throws -> [String: Any] {
        guard case .string(let text) = try await ws.receive(),
              let object = try? JSONSerialization.jsonObject(with: Data(text.utf8)) as? [String: Any]
        else { throw RelayNoiseError(description: "The relay sent something that isn't a control message.") }
        return object
    }

    private static func sendJSON(_ ws: URLSessionWebSocketTask, _ object: [String: String]) async throws {
        let data = try JSONSerialization.data(withJSONObject: object)
        try await ws.send(.string(String(decoding: data, as: UTF8.self)))
    }
}

/// A TCP connection to Zubi's own server, with async sends and receives.
final class LocalConnection: @unchecked Sendable {
    private let connection: NWConnection
    private let queue = DispatchQueue(label: "de.fa-krug.zubi.relay.local")

    private init(_ connection: NWConnection) {
        self.connection = connection
    }

    static func open(host: String, port: Int) async throws -> LocalConnection {
        guard let nwPort = NWEndpoint.Port(rawValue: UInt16(clamping: port)) else {
            throw RelayNoiseError(description: "Port \(port) is not valid.")
        }
        let local = LocalConnection(NWConnection(host: NWEndpoint.Host(host), port: nwPort, using: .tcp))
        try await local.start()
        return local
    }

    private func start() async throws {
        let connection = connection
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            // Each outcome clears the handler first, so the continuation resumes exactly once.
            connection.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    connection.stateUpdateHandler = nil
                    continuation.resume()
                case .failed(let error), .waiting(let error):
                    connection.stateUpdateHandler = nil
                    connection.cancel()
                    continuation.resume(throwing: error)
                case .cancelled:
                    connection.stateUpdateHandler = nil
                    continuation.resume(throwing: CancellationError())
                default:
                    break
                }
            }
            connection.start(queue: queue)
        }
    }

    func send(_ data: Data) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            connection.send(content: data, completion: .contentProcessed { error in
                if let error { continuation.resume(throwing: error) } else { continuation.resume() }
            })
        }
    }

    /// Ends the sending side, as TCP's FIN; receiving goes on.
    func finish() async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            connection.send(content: nil, contentContext: .finalMessage, isComplete: true,
                            completion: .contentProcessed { error in
                                if let error { continuation.resume(throwing: error) } else { continuation.resume() }
                            })
        }
    }

    /// The next bytes, and whether the server has ended its side.
    func receive(maximumLength: Int) async throws -> (Data?, Bool) {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<(Data?, Bool), Error>) in
            connection.receive(minimumIncompleteLength: 1, maximumLength: maximumLength) { data, _, isComplete, error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume(returning: (data, isComplete))
                }
            }
        }
    }

    func cancel() {
        connection.cancel()
    }
}
