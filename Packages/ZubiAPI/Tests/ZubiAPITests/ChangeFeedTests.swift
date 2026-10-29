import XCTest
@testable import ZubiAPI

final class ChangeFeedTests: XCTestCase {
    private let settle = Duration.milliseconds(200)

    func testEventKitBurstsCoalesceAndEchoesAreDropped() async throws {
        let feed = ChangeFeed(echoWindow: 2, coalescing: .milliseconds(50))
        let subscription = feed.subscribe(heartbeat: .seconds(60))
        var messages = subscription.messages.makeAsyncIterator()

        // A burst from elsewhere: both kinds once, since the notification doesn't say which.
        feed.eventStoreChanged()
        feed.eventStoreChanged()
        feed.eventStoreChanged()
        let first = await messages.next()
        let second = await messages.next()
        XCTAssertEqual(first, .change(Change(source: .reminders, origin: .elsewhere)))
        XCTAssertEqual(second, .change(Change(source: .calendar, origin: .elsewhere)))

        // The API's own write goes out as zubi, and EventKit's echo of it does not go out again.
        feed.wrote(.reminders)
        let own = await messages.next()
        XCTAssertEqual(own, .change(Change(source: .reminders, origin: .zubi)))
        feed.eventStoreChanged()
        try await Task.sleep(for: settle)

        // Nor when the notification beats the write it echoes.
        feed.eventStoreChanged()
        feed.wrote(.calendar)
        let calendar = await messages.next()
        XCTAssertEqual(calendar, .change(Change(source: .calendar, origin: .zubi)))
        try await Task.sleep(for: settle)

        // Nothing else was queued: the next message is the next change.
        feed.wrote(.notes)
        let notes = await messages.next()
        XCTAssertEqual(notes, .change(Change(source: .notes, origin: .zubi)))

        subscription.cancel()
        XCTAssertEqual(feed.subscriberCount, 0)
    }

    func testEchoWindowCloses() async throws {
        let feed = ChangeFeed(echoWindow: 0.1, coalescing: .milliseconds(10))
        let subscription = feed.subscribe(heartbeat: .seconds(60))
        defer { subscription.cancel() }
        var messages = subscription.messages.makeAsyncIterator()
        feed.wrote(.reminders)
        _ = await messages.next()
        try await Task.sleep(for: settle)
        feed.eventStoreChanged()
        let later = await messages.next()
        XCTAssertEqual(later, .change(Change(source: .reminders, origin: .elsewhere)))
    }

    func testHeartbeatsAndEndings() async throws {
        let feed = ChangeFeed()
        let subscription = feed.subscribe(heartbeat: .milliseconds(20))
        var messages = subscription.messages.makeAsyncIterator()
        let beat = await messages.next()
        XCTAssertEqual(beat, .heartbeat)

        // closeAll ends every stream, and the feed takes new ones afterwards.
        let other = feed.subscribe(heartbeat: .seconds(60))
        XCTAssertEqual(feed.subscriberCount, 2)
        feed.closeAll()
        var drained = 0
        while await messages.next() != nil { drained += 1 }
        XCTAssertLessThanOrEqual(drained, 1)
        var otherMessages = other.messages.makeAsyncIterator()
        let ended = await otherMessages.next()
        XCTAssertNil(ended)
        XCTAssertEqual(feed.subscriberCount, 0)
        let again = feed.subscribe(heartbeat: .seconds(60))
        XCTAssertEqual(feed.subscriberCount, 1)

        // A consumer that is cancelled unsubscribes too.
        let task = Task { for await _ in again.messages {} }
        task.cancel()
        await task.value
        XCTAssertEqual(feed.subscriberCount, 0)
    }

    func testMessagesAreServerSentEvents() {
        XCTAssertEqual(ServerSentEvents.encode(.heartbeat), ": keep-alive\n\n")
        let encoded = ServerSentEvents.encode(.change(Change(source: .calendar, origin: .elsewhere)))
        XCTAssertTrue(encoded.hasPrefix("event: change\ndata: {"))
        XCTAssertTrue(encoded.hasSuffix("}\n\n"))
        XCTAssertEqual(encoded.components(separatedBy: "\n").count, 4)
    }
}
