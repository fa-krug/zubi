import Hummingbird

/// What a key may do. A read-only key never changes anything, whatever an item's own setting.
public enum KeyAccess: String, Codable, Sendable, CaseIterable {
    case read
    case readWrite
}

/// One device's key, as Settings › Keys holds it.
public struct APIKey: Sendable, Equatable {
    /// Stable across renames and regenerated secrets, for the app's own bookkeeping.
    public let id: String
    public let name: String
    public let secret: String
    public let access: KeyAccess

    public init(id: String, name: String, secret: String, access: KeyAccess) {
        self.id = id
        self.name = name
        self.secret = secret
        self.access = access
    }
}

/// Every `/api` route sits behind this. It finds the key the request carries, puts it in the context,
/// and answers 403 to any write made with a read-only key, so no route can forget to.
struct APIKeyMiddleware: RouterMiddleware {
    typealias Context = ZubiRequestContext

    /// Read per request, so a key added, changed or deleted in Settings needs no restart.
    let keys: @Sendable () -> [APIKey]
    /// Only the calendar feed: calendar apps subscribe to a URL and cannot send a header. Only a
    /// read-only key counts there, since a URL ends up in proxy logs and sync settings.
    let acceptsQueryKey: Bool
    let onRequest: @Sendable (String) -> Void
    let onKeyUsed: @Sendable (String) -> Void

    func handle(
        _ request: Request,
        context: Context,
        next: (Request, Context) async throws -> Response
    ) async throws -> Response {
        // The path only: a query key must never reach the log.
        let line = "\(request.method) \(request.uri.path)"

        guard let key = resolve(request) else {
            onRequest("401 \(line)")
            throw HTTPError(.unauthorized)
        }
        onKeyUsed(key.id)
        let logLine = "\(line) · \(key.name)"

        if key.access == .read && request.method != .get && request.method != .head {
            onRequest("403 \(logLine)")
            throw HTTPError(.forbidden, message: "This key is read-only, so it can't create or edit anything")
        }

        var context = context
        context.key = key
        do {
            let response = try await next(request, context)
            onRequest("\(response.status.code) \(logLine)")
            return response
        } catch let error as HTTPError {
            onRequest("\(error.status.code) \(logLine)")
            throw error
        }
    }

    /// The bearer header if there is one, else (feed only) `?key=`. Every key is compared, so the
    /// time taken says nothing about which one, or how much of it, matched.
    private func resolve(_ request: Request) -> APIKey? {
        let provided: String
        let fromQuery: Bool
        if let header = request.headers[.authorization] {
            guard header.hasPrefix("Bearer ") else { return nil }
            provided = String(header.dropFirst("Bearer ".count))
            fromQuery = false
        } else if acceptsQueryKey, let key = request.query.get("key") {
            provided = key
            fromQuery = true
        } else {
            return nil
        }
        guard !provided.isEmpty else { return nil }

        var match: APIKey?
        for key in keys() where !key.secret.isEmpty {
            if constantTimeEquals(provided, key.secret) && match == nil { match = key }
        }
        // A key that can write is no credential in a URL: refused as if wrong, so a leaked feed URL
        // never carries one that works for anything else.
        if fromQuery, match?.access != .read { return nil }
        return match
    }

    private func constantTimeEquals(_ lhs: String, _ rhs: String) -> Bool {
        let a = Array(lhs.utf8)
        let b = Array(rhs.utf8)
        guard a.count == b.count else { return false }
        return zip(a, b).reduce(UInt8(0)) { $0 | ($1.0 ^ $1.1) } == 0
    }
}

extension ZubiRequestContext {
    /// Whether this request may write at all; items narrow it further.
    var keyCanWrite: Bool { key?.access == .readWrite }
}
