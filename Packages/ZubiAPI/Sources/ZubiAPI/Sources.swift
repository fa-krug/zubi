import Foundation

// What the routes read from and write to. The app backs these with EventKit and Notes.app; the tests
// with fakes. The routes check exposure and writability before a write reaches a source, and resolve a
// list, calendar or folder named by title to its ID, so a source only ever sees IDs it may change.

public enum ReminderFilter: Sendable {
    case all
    case dueToday
    case overdue
}

public protocol RemindersSource: Sendable {
    /// By account, then title. `writable` says whether the list itself allows changes.
    func lists() async -> [ReminderListDTO]
    func reminders(in listIDs: Set<String>, filter: ReminderFilter, includeCompleted: Bool) async -> [ReminderDTO]
    /// Any reminder, in any list; nil when there is none.
    func reminder(id: String) async -> ReminderDTO?
    func createReminder(_ draft: ReminderDraft) async throws -> ReminderDTO
    func updateReminder(id: String, _ changes: ReminderChanges) async throws -> ReminderDTO
}

public protocol CalendarSource: Sendable {
    /// By account, then title. `writable` says whether the calendar itself allows changes.
    func calendars() async -> [CalendarDTO]
    /// Occurrences starting in `start..<end`, sorted by start.
    func events(in calendarIDs: Set<String>, from start: Date, to end: Date) async -> [EventDTO]
    /// The event, or with `occurrence` the occurrence whose `start` is that date; nil when there is none.
    func event(id: String, occurrence: RequestDate?) async -> EventLookup?
    func createEvent(_ draft: EventDraft) async throws -> EventDTO
    /// Changes only the one event or occurrence `event(id:occurrence:)` finds, never the rest of a series.
    func updateEvent(id: String, occurrence: RequestDate?, _ changes: EventChanges) async throws -> EventDTO
}

/// An event as found for an edit.
public struct EventLookup: Sendable {
    public let event: EventDTO
    /// Part of a series, so an edit has to name the occurrence.
    public let recurs: Bool

    public init(event: EventDTO, recurs: Bool) {
        self.event = event
        self.recurs = recurs
    }
}

public protocol NotesSource: Sendable {
    /// Depth first, so a folder follows its parent. `writable` says whether Notes allows changes there.
    func folders() async throws -> [NoteFolderDTO]
    /// Newest first. With `query`, only notes whose title or text contains it, ignoring case; a
    /// locked note's text is never searched.
    func notes(inFolder folderID: String, limit: Int, matching query: String?) async throws -> [NoteSummaryDTO]
    func note(id: String) async throws -> NoteDTO
    /// The note as `note(id:)` would return it.
    func createNote(_ draft: NoteDraft) async throws -> NoteDTO
    /// Throws `.locked` for a locked note, which is never changed.
    func updateNote(id: String, _ changes: NoteChanges) async throws -> NoteDTO
}

/// Why a source could not do what was asked; the routes answer with the matching status.
public enum SourceErrorKind: Sendable {
    /// 404
    case notFound
    /// 403: the source itself does not allow the change.
    case readOnly
    /// 409: a locked note.
    case locked
    /// 400: the source refused the values, for example EventKit's own validation.
    case invalid
    /// 503: the source did not answer, or not usefully, such as Notes without Automation access.
    case unavailable
}

/// What a source throws. Its `description` becomes the error message, so it must hold nothing private.
public protocol SourceError: Error, CustomStringConvertible {
    var kind: SourceErrorKind { get }
}

/// A `SourceError` for sources with nothing more to say than a kind and a message.
public struct SourceFailure: SourceError, Sendable {
    public let kind: SourceErrorKind
    public let description: String

    public init(_ kind: SourceErrorKind, _ description: String) {
        self.kind = kind
        self.description = description
    }
}

/// Whether the Mac granted access. Asked on every request, since it can be revoked while running.
public struct SourceAccess: Sendable {
    public var reminders: @Sendable () -> Bool
    public var calendar: @Sendable () -> Bool

    public init(reminders: @escaping @Sendable () -> Bool, calendar: @escaping @Sendable () -> Bool) {
        self.reminders = reminders
        self.calendar = calendar
    }
}

/// The IDs set to Read or Read & Write in Settings, and those set to Read & Write. Read on every
/// request, so a change needs no restart. Anything not exposed answers 404 for reads and writes
/// alike, even when it is also listed as writable.
public struct ExposedIDs: Sendable {
    public var reminderLists: @Sendable () -> Set<String>
    public var calendars: @Sendable () -> Set<String>
    public var noteFolders: @Sendable () -> Set<String>
    public var writableReminderLists: @Sendable () -> Set<String>
    public var writableCalendars: @Sendable () -> Set<String>
    public var writableNoteFolders: @Sendable () -> Set<String>

    public init(
        reminderLists: @escaping @Sendable () -> Set<String>,
        calendars: @escaping @Sendable () -> Set<String>,
        noteFolders: @escaping @Sendable () -> Set<String>,
        writableReminderLists: @escaping @Sendable () -> Set<String>,
        writableCalendars: @escaping @Sendable () -> Set<String>,
        writableNoteFolders: @escaping @Sendable () -> Set<String>
    ) {
        self.reminderLists = reminderLists
        self.calendars = calendars
        self.noteFolders = noteFolders
        self.writableReminderLists = writableReminderLists
        self.writableCalendars = writableCalendars
        self.writableNoteFolders = writableNoteFolders
    }
}
