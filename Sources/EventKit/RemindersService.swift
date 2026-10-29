import EventKit
import Foundation
import ZubiAPI

/// All EventKit access is serialized through this actor, because EKEventStore is not thread-safe.
actor RemindersService: RemindersSource {
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
                    color: ColorHex.string($0.cgColor), writable: $0.allowsContentModifications)
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

    func reminder(id: String) -> ReminderDTO? {
        (store.calendarItem(withIdentifier: id) as? EKReminder).map(ReminderDTO.init)
    }

    func createReminder(_ draft: ReminderDraft) throws -> ReminderDTO {
        let reminder = EKReminder(eventStore: store)
        reminder.calendar = try writableList(draft.list)
        reminder.title = draft.title
        reminder.notes = draft.notes
        reminder.priority = draft.priority
        reminder.dueDateComponents = draft.due.map(Self.dueComponents)
        try save(reminder)
        return ReminderDTO(reminder)
    }

    func updateReminder(id: String, _ changes: ReminderChanges) throws -> ReminderDTO {
        guard let reminder = store.calendarItem(withIdentifier: id) as? EKReminder else {
            throw SourceFailure(.notFound, "Unknown reminder")
        }
        guard reminder.calendar?.allowsContentModifications == true else {
            throw SourceFailure(.readOnly, "List is read-only in Reminders")
        }
        if let listID = changes.list { reminder.calendar = try writableList(listID) }
        if let title = changes.title { reminder.title = title }
        if let priority = changes.priority { reminder.priority = priority }
        if let isCompleted = changes.isCompleted { reminder.isCompleted = isCompleted }
        switch changes.notes {
        case .unchanged: break
        case .cleared: reminder.notes = nil
        case .set(let notes): reminder.notes = notes
        }
        switch changes.due {
        case .unchanged: break
        case .cleared: reminder.dueDateComponents = nil
        case .set(let due): reminder.dueDateComponents = Self.dueComponents(due)
        }
        try save(reminder)
        return ReminderDTO(reminder)
    }

    private func writableList(_ id: String) throws -> EKCalendar {
        guard let list = store.calendar(withIdentifier: id), list.allowedEntityTypes.contains(.reminder)
        else {
            throw SourceFailure(.notFound, "Unknown list")
        }
        guard list.allowsContentModifications else {
            throw SourceFailure(.readOnly, "List is read-only in Reminders")
        }
        return list
    }

    private func save(_ reminder: EKReminder) throws {
        do {
            try store.save(reminder, commit: true)
        } catch {
            // The failed edit would otherwise linger on the store's cached object and show up in reads.
            store.reset()
            throw SourceFailure(.invalid, "Reminders refused the change: \(error.localizedDescription)")
        }
    }

    /// The shape `ReminderDTO.init` reads back: a day has no hour, so it stays all-day.
    private static func dueComponents(_ due: RequestDate) -> DateComponents {
        let calendar = Calendar.current
        switch due {
        case .day(let day):
            return calendar.dateComponents([.year, .month, .day], from: day)
        case .instant(let instant):
            var components = calendar.dateComponents(
                [.year, .month, .day, .hour, .minute, .second], from: instant)
            components.timeZone = calendar.timeZone
            return components
        }
    }
}
