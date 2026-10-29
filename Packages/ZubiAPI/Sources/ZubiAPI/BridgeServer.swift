import Foundation
import Hummingbird

public struct ServerConfig: Sendable {
    public let host: String
    public let port: Int

    public init(host: String, port: Int) {
        self.host = host
        self.port = port
    }
}

/// Hummingbird's basic context plus the key the request was made with.
public struct ZubiRequestContext: RequestContext {
    public var coreContext: CoreRequestContextStorage
    /// Set by the key middleware on every `/api` route before a handler runs.
    public internal(set) var key: APIKey?

    public init(source: ApplicationRequestContextSource) {
        coreContext = .init(source: source)
    }
}

/// A change made through the API, for the app's activity log.
public struct WriteRecord: Sendable, Equatable {
    public enum Action: String, Codable, Sendable {
        case created, edited
    }

    public let date: Date
    /// The name of the key that made it, as it was at the time.
    public let keyName: String
    public let action: Action
    public let source: ChangeSource
    public let title: String
    /// The list, calendar or folder the item is in after the change.
    public let container: String
}

public struct BridgeServer: Sendable {
    let config: ServerConfig
    let keys: @Sendable () -> [APIKey]
    let reminders: any RemindersSource
    let calendar: any CalendarSource
    let notes: any NotesSource
    let access: SourceAccess
    let exposed: ExposedIDs
    let changes: ChangeFeed
    /// The event range defaults hang off "today", so the tests pin it.
    let now: @Sendable () -> Date
    let onRequest: @Sendable (String) -> Void
    let onKeyUsed: @Sendable (String) -> Void
    let onWrite: @Sendable (WriteRecord) -> Void
    /// How often `/api/changes` sends a comment. It is also how soon a client that went away
    /// without closing is noticed, since only a failed write tells; the tests shorten it.
    var heartbeat: Duration = .seconds(30)

    /// - Parameters:
    ///   - keys: Every valid key, asked on every request.
    ///   - changes: What `/api/changes` streams; the app also feeds it EventKit's notifications.
    ///   - onKeyUsed: A key's `id`, each time a request authenticates with it.
    ///   - onWrite: Each create or edit that succeeded.
    public init(
        config: ServerConfig,
        keys: @escaping @Sendable () -> [APIKey],
        reminders: any RemindersSource,
        calendar: any CalendarSource,
        notes: any NotesSource,
        access: SourceAccess,
        exposed: ExposedIDs,
        changes: ChangeFeed,
        now: @escaping @Sendable () -> Date = { Date() },
        onRequest: @escaping @Sendable (String) -> Void,
        onKeyUsed: @escaping @Sendable (String) -> Void = { _ in },
        onWrite: @escaping @Sendable (WriteRecord) -> Void = { _ in }
    ) {
        self.config = config
        self.keys = keys
        self.reminders = reminders
        self.calendar = calendar
        self.notes = notes
        self.access = access
        self.exposed = exposed
        self.changes = changes
        self.now = now
        self.onRequest = onRequest
        self.onKeyUsed = onKeyUsed
        self.onWrite = onWrite
    }

    /// Every route, without binding a port. openapi.json must list exactly these.
    public func makeRouter() -> Router<ZubiRequestContext> {
        let router = Router(context: ZubiRequestContext.self)

        // Unauthenticated liveness check
        router.get("health") { _, _ -> String in "ok" }
        DocsRoutes.register(on: router)

        let api = router.group("api").add(middleware: keyMiddleware(acceptsQueryKey: false))
        // The same prefix, for the one route that also takes `?key=`.
        let feed = router.group("api").add(middleware: keyMiddleware(acceptsQueryKey: true))
        let writes = WriteHooks(changes: changes, now: now, onWrite: onWrite)

        ServiceRoutes.register(on: api, changes: changes, heartbeat: heartbeat)
        ReminderRoutes.register(on: api, service: reminders, isAuthorized: access.reminders,
                                exposedIDs: exposed.reminderLists, writableIDs: exposed.writableReminderLists,
                                writes: writes)
        CalendarRoutes.register(on: api, feed: feed, service: calendar, isAuthorized: access.calendar,
                                exposedIDs: exposed.calendars, writableIDs: exposed.writableCalendars,
                                writes: writes, now: now)
        NotesRoutes.register(on: api, service: notes, exposedIDs: exposed.noteFolders,
                             writableIDs: exposed.writableNoteFolders, writes: writes)
        return router
    }

    private func keyMiddleware(acceptsQueryKey: Bool) -> APIKeyMiddleware {
        APIKeyMiddleware(keys: keys, acceptsQueryKey: acceptsQueryKey, onRequest: onRequest,
                         onKeyUsed: onKeyUsed)
    }

    /// Runs until cancelled. `onRunning` is called once the port is bound, so anything that
    /// announces the server (Bonjour) never points at one that failed to start.
    public func run(onRunning: @escaping @Sendable () -> Void = {}) async throws {
        let app = Application(
            router: makeRouter(),
            configuration: .init(
                address: .hostname(config.host, port: config.port),
                serverName: "Zubi"
            ),
            onServerRunning: { _ in onRunning() }
        )
        // No signal handlers: the SwiftUI app owns the lifecycle and stops us via task cancellation.
        // Open change streams end with it, so a stop or rebind never waits on a client.
        try await withTaskCancellationHandler {
            try await app.runService(gracefulShutdownSignals: [])
        } onCancel: {
            changes.closeAll()
        }
    }
}
