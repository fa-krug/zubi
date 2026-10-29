import Foundation

/// A value the server's hooks write from its own threads and a test reads afterwards.
final class Locked<Value>: @unchecked Sendable {
    private let lock = NSLock()
    private var stored: Value

    init(_ value: Value) { stored = value }

    var value: Value { lock.withLock { stored } }

    func mutate(_ change: (inout Value) -> Void) {
        lock.withLock { change(&stored) }
    }
}
