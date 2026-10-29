import EventKit
import Foundation

enum ReminderFilter: Sendable {
    case all
    case dueToday
    case overdue
}

/// All EventKit access is serialized through this actor, because EKEventStore is not thread-safe.
actor RemindersService {
    private var store = EKEventStore()

    static var authorizationStatus: EKAuthorizationStatus {
        EKEventStore.authorizationStatus(for: .reminder)
    }

    func requestAccess() async throws -> Bool {
        let granted = try await store.requestFullAccessToReminders()
        // A store created before the grant can return empty results, so start fresh.
        if granted { store = EKEventStore() }
        return granted
    }

    func lists() -> [ReminderListDTO] {
        store.calendars(for: .reminder)
            .map {
                ReminderListDTO(
                    id: $0.calendarIdentifier, title: $0.title, account: $0.source?.title ?? "",
                    color: ColorHex.string($0.cgColor))
            }
            .sorted { ($0.account, $0.title.lowercased()) < ($1.account, $1.title.lowercased()) }
    }

    func reminders(in listIDs: Set<String>, filter: ReminderFilter, includeCompleted: Bool) async -> [ReminderDTO] {
        let calendars = store.calendars(for: .reminder).filter { listIDs.contains($0.calendarIdentifier) }
        guard !calendars.isEmpty else { return [] }

        let calendar = Calendar.current
        let startOfToday = calendar.startOfDay(for: Date())
        let startOfTomorrow = calendar.date(byAdding: .day, value: 1, to: startOfToday)!

        let predicate: NSPredicate
        switch filter {
        case .all where includeCompleted:
            predicate = store.predicateForReminders(in: calendars)
        case .all:
            predicate = store.predicateForIncompleteReminders(
                withDueDateStarting: nil, ending: nil, calendars: calendars)
        case .dueToday:
            predicate = store.predicateForIncompleteReminders(
                withDueDateStarting: startOfToday, ending: startOfTomorrow, calendars: calendars)
        case .overdue:
            predicate = store.predicateForIncompleteReminders(
                withDueDateStarting: nil, ending: startOfToday, calendars: calendars)
        }

        let store = self.store
        let dtos: [ReminderDTO] = await withCheckedContinuation { continuation in
            store.fetchReminders(matching: predicate) { reminders in
                // Convert inside the callback; EKReminder must not leave this queue.
                continuation.resume(returning: (reminders ?? []).map(ReminderDTO.init))
            }
        }

        return filter == .all ? dtos : dtos.filter { $0.due != nil }
    }
}
