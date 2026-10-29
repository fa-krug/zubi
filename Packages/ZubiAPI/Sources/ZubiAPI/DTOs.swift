import CoreGraphics
import Foundation
import Hummingbird

// Optionals encode with `encodeIfPresent`, so a nil field is absent from the JSON, never `null`.
// openapi.json relies on that: its optional properties are not nullable.

// MARK: Dates

public enum DateFormat {
    /// yyyy-MM-dd in the Mac's local time zone (for all-day items and query params).
    private static var dayStyle: Date.ISO8601FormatStyle {
        Date.ISO8601FormatStyle(timeZone: .current).year().month().day()
    }

    public static func day(_ date: Date) -> String { date.formatted(dayStyle) }

    /// Full ISO 8601 in UTC.
    public static func dateTime(_ date: Date) -> String { date.formatted(.iso8601) }

    public static func parseDay(_ string: String) throws -> Date {
        guard let date = try? dayStyle.parse(string) else {
            throw HTTPError(.badRequest, message: "Dates must be YYYY-MM-DD")
        }
        return date
    }

    /// A date in a request: `YYYY-MM-DD` for a day in the Mac's time zone, or ISO 8601 with a time
    /// zone. A time without one is refused, since it would quietly mean the Mac's zone.
    public static func parseRequestDate(_ string: String) -> RequestDate? {
        // The round trip refuses what the parser would bend, such as 2026-02-30 or 2026-1-2.
        if let day = try? dayStyle.parse(string), Self.day(day) == string {
            return .day(day)
        }
        if let instant = try? Date.ISO8601FormatStyle().parse(string) {
            return .instant(instant)
        }
        return nil
    }

    /// As `/api/events` and `/api/reminders` write it.
    public static func string(_ date: RequestDate) -> String {
        switch date {
        case .day(let day): Self.day(day)
        case .instant(let instant): dateTime(instant)
        }
    }
}

/// A date from a request body or query.
public enum RequestDate: Sendable, Equatable {
    /// Midnight starting that day, in the Mac's time zone.
    case day(Date)
    case instant(Date)

    public var date: Date {
        switch self {
        case .day(let date), .instant(let date): date
        }
    }

    public var isDay: Bool {
        if case .day = self { return true }
        return false
    }
}

// MARK: Colours

public enum ColorHex {
    /// `#RRGGBB` in sRGB, the colour the list or calendar has in Reminders and Calendar.
    public static func string(_ color: CGColor?) -> String? {
        guard let color,
              let srgb = CGColorSpace(name: CGColorSpace.sRGB),
              let converted = color.converted(to: srgb, intent: .defaultIntent, options: nil),
              let parts = converted.components, parts.count >= 3
        else { return nil }
        let bytes = parts.prefix(3).map { Int((min(max($0, 0), 1) * 255).rounded()) }
        return String(format: "#%02X%02X%02X", bytes[0], bytes[1], bytes[2])
    }
}

// MARK: Reminders

public struct ReminderListDTO: Codable, Sendable, Identifiable {
    public let id: String
    public let title: String
    public let account: String
    /// `#RRGGBB`, as Reminders shows the list.
    public let color: String?
    /// From a source: whether Reminders allows changes. The routes narrow it to what Settings allows.
    public var writable: Bool

    public init(id: String, title: String, account: String, color: String?, writable: Bool) {
        self.id = id
        self.title = title
        self.account = account
        self.color = color
        self.writable = writable
    }
}

public struct ReminderDTO: Codable, Sendable, ResponseEncodable {
    public let id: String
    public let title: String
    public let notes: String?
    public let listID: String
    public let listTitle: String
    public let isCompleted: Bool
    public let priority: Int
    /// ISO 8601 date-time (UTC), or yyyy-MM-dd for all-day reminders.
    public let due: String?
    public let dueAllDay: Bool

    public init(
        id: String, title: String, notes: String?, listID: String, listTitle: String,
        isCompleted: Bool, priority: Int, due: String?, dueAllDay: Bool
    ) {
        self.id = id
        self.title = title
        self.notes = notes
        self.listID = listID
        self.listTitle = listTitle
        self.isCompleted = isCompleted
        self.priority = priority
        self.due = due
        self.dueAllDay = dueAllDay
    }
}

struct ListsResponse: ResponseEncodable { let lists: [ReminderListDTO] }
struct RemindersResponse: ResponseEncodable { let reminders: [ReminderDTO] }

// MARK: Calendar

public struct CalendarDTO: Codable, Sendable, Identifiable {
    public let id: String
    public let title: String
    public let account: String
    /// `#RRGGBB`, as Calendar shows it.
    public let color: String?
    /// From a source: whether Calendar allows changes, which a subscribed or holiday calendar never
    /// does. The routes narrow it to what Settings allows.
    public var writable: Bool

    public init(id: String, title: String, account: String, color: String?, writable: Bool) {
        self.id = id
        self.title = title
        self.account = account
        self.color = color
        self.writable = writable
    }
}

public struct EventDTO: Codable, Sendable, ResponseEncodable {
    public let id: String
    public let title: String
    /// ISO 8601 (UTC), or yyyy-MM-dd for all-day events (end is inclusive).
    public let start: String
    public let end: String
    public let isAllDay: Bool
    public let location: String?
    public let notes: String?
    public let url: String?
    public let calendarID: String
    public let calendarTitle: String

    public init(
        id: String, title: String, start: String, end: String, isAllDay: Bool,
        location: String?, notes: String?, url: String?, calendarID: String, calendarTitle: String
    ) {
        self.id = id
        self.title = title
        self.start = start
        self.end = end
        self.isAllDay = isAllDay
        self.location = location
        self.notes = notes
        self.url = url
        self.calendarID = calendarID
        self.calendarTitle = calendarTitle
    }
}

struct CalendarsResponse: ResponseEncodable { let calendars: [CalendarDTO] }
struct EventsResponse: ResponseEncodable { let events: [EventDTO] }

// MARK: Notes (decoded from the JXA scripts' JSON)

public struct NoteFolderDTO: Codable, Sendable, Identifiable {
    public let id: String
    public let name: String
    public let account: String
    /// The enclosing folder; nil (absent in the JSON) for a folder at the top of its account.
    public let parentID: String?
    /// From a source: whether Notes allows changes. The routes narrow it to what Settings allows.
    public var writable: Bool

    public init(id: String, name: String, account: String, parentID: String?, writable: Bool) {
        self.id = id
        self.name = name
        self.account = account
        self.parentID = parentID
        self.writable = writable
    }
}

public struct NoteSummaryDTO: Codable, Sendable {
    public let id: String
    public let title: String
    public let modified: String   // ISO 8601 UTC, sortable as string
    public let locked: Bool

    public init(id: String, title: String, modified: String, locked: Bool) {
        self.id = id
        self.title = title
        self.modified = modified
        self.locked = locked
    }
}

public struct NoteDTO: Codable, Sendable, ResponseEncodable {
    public let id: String
    public let title: String
    public let folderID: String
    public let created: String
    public let modified: String
    public let locked: Bool
    public let plaintext: String?
    public let html: String?

    public init(
        id: String, title: String, folderID: String, created: String, modified: String,
        locked: Bool, plaintext: String?, html: String?
    ) {
        self.id = id
        self.title = title
        self.folderID = folderID
        self.created = created
        self.modified = modified
        self.locked = locked
        self.plaintext = plaintext
        self.html = html
    }
}

struct NoteFoldersResponse: ResponseEncodable { let folders: [NoteFolderDTO] }
struct NoteSummariesResponse: ResponseEncodable { let notes: [NoteSummaryDTO] }
