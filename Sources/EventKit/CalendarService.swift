import EventKit
import Foundation
import ZubiAPI

/// Separate store from RemindersService; EKEventStore is not thread-safe.
actor CalendarService: CalendarSource {
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
            .map {
                CalendarDTO(
                    id: $0.calendarIdentifier, title: $0.title, account: $0.source?.title ?? "",
                    color: ColorHex.string($0.cgColor), writable: $0.allowsContentModifications)
            }
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

    func event(id: String, occurrence: RequestDate?) -> EventLookup? {
        find(id, occurrence).map { EventLookup(event: EventDTO($0), recurs: $0.hasRecurrenceRules) }
    }

    func createEvent(_ draft: EventDraft) throws -> EventDTO {
        let event = EKEvent(eventStore: store)
        event.calendar = try writableCalendar(draft.calendar)
        event.title = draft.title
        event.location = draft.location
        event.notes = draft.notes
        event.url = draft.url
        Self.setTiming(event, start: draft.start, end: draft.end, isAllDay: draft.isAllDay)
        try save(event)
        return EventDTO(event)
    }

    func updateEvent(id: String, occurrence: RequestDate?, _ changes: EventChanges) throws -> EventDTO {
        guard let event = find(id, occurrence) else {
            throw SourceFailure(.notFound, "Unknown event or occurrence")
        }
        guard event.calendar?.allowsContentModifications == true else {
            throw SourceFailure(.readOnly, "Calendar is read-only in the Calendar app")
        }
        if let calendarID = changes.calendar { event.calendar = try writableCalendar(calendarID) }
        if let title = changes.title { event.title = title }
        if changes.start != nil || changes.end != nil || changes.isAllDay != nil {
            Self.setTiming(
                event, start: changes.start ?? .instant(event.startDate),
                end: changes.end ?? .instant(event.endDate), isAllDay: changes.isAllDay ?? event.isAllDay)
        }
        switch changes.location {
        case .unchanged: break
        case .cleared: event.location = nil
        case .set(let location): event.location = location
        }
        switch changes.notes {
        case .unchanged: break
        case .cleared: event.notes = nil
        case .set(let notes): event.notes = notes
        }
        switch changes.url {
        case .unchanged: break
        case .cleared: event.url = nil
        case .set(let url): event.url = url
        }
        try save(event)
        return EventDTO(event)
    }

    /// The event, or with `occurrence` the occurrence `/api/events` listed with that start. EventKit
    /// hands out only the series for an identifier, so the occurrence is found among the events of
    /// the days around it, compared the way `EventDTO` writes a start.
    private func find(_ id: String, _ occurrence: RequestDate?) -> EKEvent? {
        guard let event = store.event(withIdentifier: id) else { return nil }
        guard let occurrence else { return event }
        let calendar = Calendar.current
        let day = calendar.startOfDay(for: occurrence.date)
        let predicate = store.predicateForEvents(
            withStart: calendar.date(byAdding: .day, value: -1, to: day)!,
            end: calendar.date(byAdding: .day, value: 2, to: day)!,
            calendars: event.calendar.map { [$0] })
        let start = DateFormat.string(occurrence)
        return store.events(matching: predicate).first {
            $0.eventIdentifier == id && EventDTO($0).start == start
        }
    }

    private func writableCalendar(_ id: String) throws -> EKCalendar {
        guard let calendar = store.calendar(withIdentifier: id), calendar.allowedEntityTypes.contains(.event)
        else {
            throw SourceFailure(.notFound, "Unknown calendar")
        }
        guard calendar.allowsContentModifications else {
            throw SourceFailure(.readOnly, "Calendar is read-only in the Calendar app")
        }
        return calendar
    }

    /// An all-day event's `end` is its last day, inclusive; EventKit reads one back as ending a second
    /// before the next midnight, which is what `EventDTO` turns into that same day.
    private static func setTiming(_ event: EKEvent, start: RequestDate, end: RequestDate, isAllDay: Bool) {
        let calendar = Calendar.current
        event.isAllDay = isAllDay
        if isAllDay {
            let lastDay = calendar.startOfDay(for: end.date)
            event.startDate = calendar.startOfDay(for: start.date)
            event.endDate = calendar.date(byAdding: DateComponents(day: 1, second: -1), to: lastDay)!
        } else {
            event.startDate = start.date
            event.endDate = end.date
        }
    }

    /// Only ever this one event or occurrence: an API edit must not rewrite a whole series.
    private func save(_ event: EKEvent) throws {
        do {
            try store.save(event, span: .thisEvent, commit: true)
        } catch {
            // The failed edit would otherwise linger on the store's cached object and show up in reads.
            store.reset()
            throw SourceFailure(.invalid, "Calendar refused the change: \(error.localizedDescription)")
        }
    }
}
