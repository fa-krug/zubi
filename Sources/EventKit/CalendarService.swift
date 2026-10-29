import EventKit
import Foundation

/// Separate store from RemindersService; EKEventStore is not thread-safe.
actor CalendarService {
    private var store = EKEventStore()

    static var authorizationStatus: EKAuthorizationStatus {
        EKEventStore.authorizationStatus(for: .event)
    }

    func requestAccess() async throws -> Bool {
        let granted = try await store.requestFullAccessToEvents()
        if granted { store = EKEventStore() }
        return granted
    }

    func calendars() -> [CalendarDTO] {
        store.calendars(for: .event)
            .map { CalendarDTO(id: $0.calendarIdentifier, title: $0.title, account: $0.source?.title ?? "") }
            .sorted { ($0.account, $0.title) < ($1.account, $1.title) }
    }

    /// Recurring events are expanded into individual occurrences by EventKit.
    func events(in calendarIDs: Set<String>, from start: Date, to end: Date) -> [EventDTO] {
        let calendars = store.calendars(for: .event).filter { calendarIDs.contains($0.calendarIdentifier) }
        guard !calendars.isEmpty else { return [] }
        let predicate = store.predicateForEvents(withStart: start, end: end, calendars: calendars)
        return store.events(matching: predicate)
            .sorted { $0.startDate < $1.startDate }
            .map(EventDTO.init)
    }
}
