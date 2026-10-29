import Foundation
import Hummingbird
@testable import ZubiAPI

// In-memory sources that keep what is written to them, so a create shows up in the next read. Titles
// and IDs match the `example`s in openapi.json, so calling an operation with a documented example
// finds something. Every optional field is set on one item and nil on another, so both shapes go
// through the schema check.
//
// Exposure, per kind: one item at Read & Write, one at Read, one Off. Off items are listed as writable
// too, to show that writable never outranks hidden; one item is writable in Settings but read-only
// at its source.

enum Fixture {
    static let apiKey = "test-key"
    static let readKey = "read-key"
    static let keys = [
        APIKey(id: "key-laptop", name: "Laptop", secret: apiKey, access: .readWrite),
        APIKey(id: "key-pi", name: "Pi", secret: readKey, access: .read),
    ]
    static let noteID = "x-coredata://00000000-0000-0000-0000-000000000000/ICNote/p1"
    static let lockedNoteID = "x-coredata://00000000-0000-0000-0000-000000000000/ICNote/p2"
    static let hiddenNoteID = "x-coredata://00000000-0000-0000-0000-000000000000/ICNote/p3"
    static let readOnlyNoteID = "x-coredata://00000000-0000-0000-0000-000000000000/ICNote/p4"
    /// Every fake timestamp a write sets; the reads' examples are all earlier.
    static let writtenAt = "2026-11-02T12:00:00Z"
    /// Midday on the `from` example, so both event-range examples fall inside the default window.
    static let now = try! DateFormat.parseDay("2026-11-02").addingTimeInterval(12 * 3600)

    static let exposed = ExposedIDs(
        reminderLists: { ["list-groceries", "list-errands"] },
        calendars: { ["cal-work", "cal-home", "cal-holidays"] },
        noteFolders: { ["folder-recipes", "folder-weeknight"] },
        writableReminderLists: { ["list-groceries", "list-hidden"] },
        writableCalendars: { ["cal-work", "cal-holidays", "cal-hidden"] },
        writableNoteFolders: { ["folder-recipes", "folder-hidden"] }
    )

    struct Sources {
        let reminders = FakeReminders()
        let calendar = FakeCalendar()
        let notes: FakeNotes
    }

    static func server(
        remindersGranted: Bool = true, calendarGranted: Bool = true, notesFail: Bool = false
    ) -> BridgeServer {
        server(Sources(notes: FakeNotes(fails: notesFail)),
               remindersGranted: remindersGranted, calendarGranted: calendarGranted)
    }

    static func server(
        _ sources: Sources, remindersGranted: Bool = true, calendarGranted: Bool = true,
        changes: ChangeFeed = ChangeFeed(), onKeyUsed: @escaping @Sendable (String) -> Void = { _ in },
        onWrite: @escaping @Sendable (WriteRecord) -> Void = { _ in }
    ) -> BridgeServer {
        var server = BridgeServer(
            config: ServerConfig(host: "127.0.0.1", port: 0),
            keys: { keys },
            reminders: sources.reminders,
            calendar: sources.calendar,
            notes: sources.notes,
            access: SourceAccess(reminders: { remindersGranted }, calendar: { calendarGranted }),
            exposed: exposed,
            changes: changes,
            now: { now },
            onRequest: { _ in },
            onKeyUsed: onKeyUsed,
            onWrite: onWrite
        )
        // Short, so a client that went away is noticed within a test.
        server.heartbeat = .milliseconds(100)
        return server
    }

    static func string(_ date: RequestDate?) -> String? { date.map(DateFormat.string) }
}

extension PatchField {
    func apply(to value: Value?) -> Value? {
        switch self {
        case .unchanged: value
        case .cleared: nil
        case .set(let new): new
        }
    }
}

actor FakeReminders: RemindersSource {
    let all = [
        ReminderListDTO(id: "list-errands", title: "Errands", account: "iCloud", color: nil, writable: true),
        ReminderListDTO(id: "list-groceries", title: "Groceries", account: "iCloud", color: "#34C759",
                        writable: true),
        ReminderListDTO(id: "list-hidden", title: "Hidden", account: "iCloud", color: "#FF9500", writable: true),
    ]

    private(set) var items = [
        ReminderDTO(
            id: "R1", title: "Milk", notes: "Oat, two cartons", listID: "list-groceries",
            listTitle: "Groceries", isCompleted: false, priority: 1, due: "2026-11-02T08:00:00Z",
            dueAllDay: false),
        ReminderDTO(
            id: "R2", title: "Bread", notes: nil, listID: "list-groceries", listTitle: "Groceries",
            isCompleted: true, priority: 0, due: nil, dueAllDay: false),
        ReminderDTO(
            id: "R3", title: "Post office", notes: nil, listID: "list-errands", listTitle: "Errands",
            isCompleted: false, priority: 9, due: "2026-11-02", dueAllDay: true),
        ReminderDTO(
            id: "R4", title: "Secret", notes: nil, listID: "list-hidden", listTitle: "Hidden",
            isCompleted: false, priority: 5, due: nil, dueAllDay: false),
    ]

    func lists() -> [ReminderListDTO] { all }

    func reminders(in listIDs: Set<String>, filter: ReminderFilter, includeCompleted: Bool) -> [ReminderDTO] {
        items.filter { listIDs.contains($0.listID) }
    }

    func reminder(id: String) -> ReminderDTO? {
        items.first { $0.id == id }
    }

    func createReminder(_ draft: ReminderDraft) -> ReminderDTO {
        let reminder = ReminderDTO(
            id: "R\(items.count + 1)", title: draft.title, notes: draft.notes, listID: draft.list,
            listTitle: title(of: draft.list), isCompleted: false, priority: draft.priority,
            due: Fixture.string(draft.due), dueAllDay: draft.due?.isDay ?? false)
        items.append(reminder)
        return reminder
    }

    func updateReminder(id: String, _ changes: ReminderChanges) throws -> ReminderDTO {
        guard let index = items.firstIndex(where: { $0.id == id }) else {
            throw SourceFailure(.notFound, "Unknown reminder")
        }
        let old = items[index]
        let listID = changes.list ?? old.listID
        let due: (string: String?, allDay: Bool) = switch changes.due {
        case .unchanged: (old.due, old.dueAllDay)
        case .cleared: (nil, false)
        case .set(let date): (Fixture.string(date), date.isDay)
        }
        items[index] = ReminderDTO(
            id: id, title: changes.title ?? old.title, notes: changes.notes.apply(to: old.notes),
            listID: listID, listTitle: title(of: listID), isCompleted: changes.isCompleted ?? old.isCompleted,
            priority: changes.priority ?? old.priority, due: due.string, dueAllDay: due.allDay)
        return items[index]
    }

    private func title(of listID: String) -> String {
        all.first { $0.id == listID }?.title ?? ""
    }
}

actor FakeCalendar: CalendarSource {
    let all = [
        CalendarDTO(id: "cal-home", title: "Home", account: "iCloud", color: nil, writable: true),
        CalendarDTO(id: "cal-hidden", title: "Hidden", account: "iCloud", color: "#FF2D55", writable: true),
        CalendarDTO(id: "cal-holidays", title: "Holidays", account: "Subscribed", color: "#8E8E93",
                    writable: false),
        CalendarDTO(id: "cal-work", title: "Work", account: "Exchange", color: "#1BADF8", writable: true),
    ]

    /// Occurrences, as EventKit expands them: a recurring event is one entry per occurrence, sharing
    /// an ID. E1 stands alone, E4 recurs.
    private(set) var items: [(event: EventDTO, recurs: Bool)] = [
        (EventDTO(
            id: "E1", title: "Standup", start: "2026-11-02T09:00:00Z", end: "2026-11-02T09:15:00Z",
            isAllDay: false, location: "Room 4", notes: "Bring the numbers",
            url: "https://example.com/meet", calendarID: "cal-work", calendarTitle: "Work"), false),
        (EventDTO(
            id: "E2", title: "Holiday", start: "2026-11-03", end: "2026-11-03", isAllDay: true,
            location: nil, notes: nil, url: nil, calendarID: "cal-home", calendarTitle: "Home"), false),
        (EventDTO(
            id: "E3", title: "Secret", start: "2026-11-04", end: "2026-11-04", isAllDay: true,
            location: nil, notes: nil, url: nil, calendarID: "cal-hidden", calendarTitle: "Hidden"), false),
        (EventDTO(
            id: "E4", title: "Sync", start: "2026-11-02T10:00:00Z", end: "2026-11-02T10:30:00Z",
            isAllDay: false, location: nil, notes: nil, url: nil, calendarID: "cal-work",
            calendarTitle: "Work"), true),
        (EventDTO(
            id: "E4", title: "Sync", start: "2026-11-03T10:00:00Z", end: "2026-11-03T10:30:00Z",
            isAllDay: false, location: nil, notes: nil, url: nil, calendarID: "cal-work",
            calendarTitle: "Work"), true),
    ]

    func calendars() -> [CalendarDTO] { all }

    func events(in calendarIDs: Set<String>, from start: Date, to end: Date) -> [EventDTO] {
        items.map(\.event).filter { calendarIDs.contains($0.calendarID) }
    }

    func event(id: String, occurrence: RequestDate?) -> EventLookup? {
        index(id, occurrence).map { EventLookup(event: items[$0].event, recurs: items[$0].recurs) }
    }

    func createEvent(_ draft: EventDraft) -> EventDTO {
        let event = EventDTO(
            id: "E\(items.count + 1)", title: draft.title, start: DateFormat.string(draft.start),
            end: DateFormat.string(draft.end), isAllDay: draft.isAllDay, location: draft.location,
            notes: draft.notes, url: draft.url?.absoluteString, calendarID: draft.calendar,
            calendarTitle: title(of: draft.calendar))
        items.append((event, false))
        return event
    }

    func updateEvent(id: String, occurrence: RequestDate?, _ changes: EventChanges) throws -> EventDTO {
        guard let index = index(id, occurrence) else { throw SourceFailure(.notFound, "Unknown event") }
        let old = items[index].event
        let calendarID = changes.calendar ?? old.calendarID
        let event = EventDTO(
            id: id, title: changes.title ?? old.title, start: Fixture.string(changes.start) ?? old.start,
            end: Fixture.string(changes.end) ?? old.end, isAllDay: changes.isAllDay ?? old.isAllDay,
            location: changes.location.apply(to: old.location), notes: changes.notes.apply(to: old.notes),
            url: changes.url.apply(to: old.url.flatMap(URL.init(string:)))?.absoluteString,
            calendarID: calendarID, calendarTitle: title(of: calendarID))
        items[index].event = event
        return event
    }

    private func index(_ id: String, _ occurrence: RequestDate?) -> Int? {
        items.firstIndex { item in
            item.event.id == id && (occurrence.map { DateFormat.string($0) == item.event.start } ?? true)
        }
    }

    private func title(of calendarID: String) -> String {
        all.first { $0.id == calendarID }?.title ?? ""
    }
}

struct FakeNotesError: SourceError {
    let kind: SourceErrorKind
    var description: String { kind == .notFound ? "Note or folder not found." : "Notes did not respond in time." }
}

actor FakeNotes: NotesSource {
    let fails: Bool

    init(fails: Bool) { self.fails = fails }

    let all = [
        NoteFolderDTO(id: "folder-recipes", name: "Recipes", account: "iCloud", parentID: nil, writable: true),
        NoteFolderDTO(id: "folder-weeknight", name: "Weeknight", account: "iCloud", parentID: "folder-recipes",
                      writable: true),
        NoteFolderDTO(id: "folder-hidden", name: "Hidden", account: "iCloud", parentID: nil, writable: true),
    ]

    private(set) var items = [
        NoteDTO(
            id: Fixture.noteID, title: "Pancakes", folderID: "folder-recipes", created: "2026-01-04T10:00:00Z",
            modified: "2026-10-30T07:12:00Z", locked: false, plaintext: "Pancakes\nFlour, milk, eggs",
            html: "<div><h1>Pancakes</h1></div><div>Flour, milk, eggs</div>"),
        NoteDTO(
            id: Fixture.lockedNoteID, title: "Family secret", folderID: "folder-recipes",
            created: "2025-12-24T10:00:00Z", modified: "2026-10-01T18:00:00Z", locked: true, plaintext: nil,
            html: nil),
        NoteDTO(
            id: Fixture.hiddenNoteID, title: "Diary", folderID: "folder-hidden", created: "2026-01-01T00:00:00Z",
            modified: "2026-10-31T21:00:00Z", locked: false, plaintext: "Dear diary", html: "<div>Dear diary</div>"),
        NoteDTO(
            id: Fixture.readOnlyNoteID, title: "Risotto", folderID: "folder-weeknight",
            created: "2026-02-01T19:00:00Z", modified: "2026-10-20T19:00:00Z", locked: false,
            plaintext: "Risotto", html: "<div><h1>Risotto</h1></div>"),
    ]

    func folders() throws -> [NoteFolderDTO] {
        try failIfAsked()
        return all
    }

    func notes(inFolder folderID: String, limit: Int, matching query: String?) throws -> [NoteSummaryDTO] {
        try failIfAsked()
        let found = items.filter { note in
            guard let query else { return true }
            let text = note.locked ? nil : note.plaintext
            return [note.title, text].contains { $0?.localizedCaseInsensitiveContains(query) == true }
        }
        let summaries = found.filter { $0.folderID == folderID }.map {
            NoteSummaryDTO(id: $0.id, title: $0.title, modified: $0.modified, locked: $0.locked)
        }
        return Array(summaries.sorted { $0.modified > $1.modified }.prefix(limit))
    }

    func note(id: String) throws -> NoteDTO {
        try failIfAsked()
        guard let note = items.first(where: { $0.id == id }) else { throw FakeNotesError(kind: .notFound) }
        return note
    }

    func createNote(_ draft: NoteDraft) throws -> NoteDTO {
        try failIfAsked()
        let note = Self.note(
            id: "x-coredata://00000000-0000-0000-0000-000000000000/ICNote/p\(items.count + 1)",
            folderID: draft.folder, created: Fixture.writtenAt, title: draft.title, body: draft.body)
        items.append(note)
        return note
    }

    func updateNote(id: String, _ changes: NoteChanges) throws -> NoteDTO {
        let old = try note(id: id)
        guard !old.locked else { throw FakeNotesError(kind: .locked) }
        let index = items.firstIndex { $0.id == id }!
        // Notes keeps the body when only the title changes; the fake keeps the plain text below it.
        let body = changes.body ?? old.plaintext.map {
            .text($0.split(separator: "\n", maxSplits: 1).dropFirst().joined())
        }
        items[index] = Self.note(
            id: id, folderID: old.folderID, created: old.created, title: changes.title ?? old.title, body: body)
        return items[index]
    }

    private static func note(id: String, folderID: String, created: String, title: String, body: NoteBody?)
        -> NoteDTO
    {
        let text: String? = switch body {
        case .text(let text): text
        case .html, nil: nil
        }
        return NoteDTO(
            id: id, title: title, folderID: folderID, created: created, modified: Fixture.writtenAt,
            locked: false, plaintext: [title, text].compactMap { $0 }.joined(separator: "\n"),
            html: NoteHTML.body(title: title, body))
    }

    private func failIfAsked() throws {
        if fails { throw FakeNotesError(kind: .unavailable) }
    }
}
