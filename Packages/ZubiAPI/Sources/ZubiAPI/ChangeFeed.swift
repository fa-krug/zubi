import Foundation

public enum ChangeSource: String, Codable, Sendable {
    case reminders, calendar, notes
}

public enum ChangeOrigin: String, Codable, Sendable {
    /// Made by a request through this API.
    case zubi
    /// Made on the Mac, in iCloud or on another device.
    case elsewhere
}

/// One message on `/api/changes`.
public struct Change: Codable, Sendable, Equatable {
    public let source: ChangeSource
    public let origin: ChangeOrigin

    public init(source: ChangeSource, origin: ChangeOrigin) {
        self.source = source
        self.origin = origin
    }
}

/// Fans changes out to every open `/api/changes` stream. One per app, outliving server restarts:
/// the app feeds it EventKit's notifications, the write routes feed it their own changes.
public final class ChangeFeed: @unchecked Sendable {
    enum Message: Sendable, Equatable {
        case change(Change)
        case heartbeat
    }

    /// One open stream. `cancel()` is idempotent; the stream's own termination calls it too.
    final class Subscription: Sendable {
        let messages: AsyncStream<Message>
        private let end: @Sendable () -> Void

        init(messages: AsyncStream<Message>, end: @escaping @Sendable () -> Void) {
            self.messages = messages
            self.end = end
        }

        func cancel() { end() }
    }

    private let lock = NSLock()
    private var subscribers: [UUID: AsyncStream<Message>.Continuation] = [:]
    /// When the API last changed Reminders or Calendar itself.
    private var lastEventKitWrite: Date?
    /// The first EventKit notification of a burst that has not been passed on yet.
    private var pendingSince: Date?

    private let echoWindow: TimeInterval
    private let coalescing: Duration
    private let now: @Sendable () -> Date

    /// - Parameters:
    ///   - echoWindow: How long after one of the API's own writes an EventKit notification is taken
    ///     to be its echo, and dropped: it has already gone out as `zubi`.
    ///   - coalescing: How long to gather a burst of notifications (an iCloud sync sends many) into one.
    public init(
        echoWindow: TimeInterval = 2, coalescing: Duration = .milliseconds(500),
        now: @escaping @Sendable () -> Date = { Date() }
    ) {
        self.echoWindow = echoWindow
        self.coalescing = coalescing
        self.now = now
    }

    /// Call on `EKEventStoreChanged`. It doesn't say what changed, so both kinds go out as `elsewhere`.
    /// The echo check waits for the burst to settle, because the notification can arrive before the
    /// write that caused it has been recorded.
    public func eventStoreChanged() {
        let schedule: Bool = lock.withLock {
            guard pendingSince == nil else { return false }
            pendingSince = now()
            return true
        }
        guard schedule else { return }
        Task {
            try? await Task.sleep(for: coalescing)
            flushEventStoreChange()
        }
    }

    private func flushEventStoreChange() {
        let isEcho: Bool = lock.withLock {
            defer { pendingSince = nil }
            guard let since = pendingSince, let write = lastEventKitWrite else { return false }
            return write > since.addingTimeInterval(-echoWindow)
        }
        guard !isEcho else { return }
        broadcast(Change(source: .reminders, origin: .elsewhere))
        broadcast(Change(source: .calendar, origin: .elsewhere))
    }

    /// A change the API made.
    func wrote(_ source: ChangeSource) {
        if source != .notes {
            lock.withLock { lastEventKitWrite = now() }
        }
        broadcast(Change(source: source, origin: .zubi))
    }

    private func broadcast(_ change: Change) {
        for continuation in lock.withLock({ Array(subscribers.values) }) {
            continuation.yield(.change(change))
        }
    }

    /// A stream of changes with a heartbeat between them. It ends on `cancel()`, on `closeAll()`, or
    /// when its consumer is cancelled; each way removes it here and stops its heartbeat.
    func subscribe(heartbeat: Duration) -> Subscription {
        let id = UUID()
        // Clients re-read on a change rather than replaying each, so a slow one loses only duplicates.
        let (stream, continuation) = AsyncStream.makeStream(
            of: Message.self, bufferingPolicy: .bufferingNewest(32))
        let beat = Task {
            while !Task.isCancelled {
                try await Task.sleep(for: heartbeat)
                continuation.yield(.heartbeat)
            }
        }
        continuation.onTermination = { [weak self] _ in
            beat.cancel()
            self?.lock.withLock { _ = self?.subscribers.removeValue(forKey: id) }
        }
        lock.withLock { subscribers[id] = continuation }
        return Subscription(messages: stream) { continuation.finish() }
    }

    /// Ends every open stream; the feed itself stays usable for the next server.
    func closeAll() {
        for continuation in lock.withLock({ Array(subscribers.values) }) {
            continuation.finish()
        }
    }

    var subscriberCount: Int { lock.withLock { subscribers.count } }
}
