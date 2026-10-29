import EventKit
import Foundation
import Hummingbird

// MARK: Dates

enum DateFormat {
    /// yyyy-MM-dd in the Mac's local time zone (for all-day items and query params).
    private static var dayStyle: Date.ISO8601FormatStyle {
        Date.ISO8601FormatStyle(timeZone: .current).year().month().day()
    }

    static func day(_ date: Date) -> String { date.formatted(dayStyle) }

    /// Full ISO 8601 in UTC.
    static func dateTime(_ date: Date) -> String { date.formatted(.iso8601) }

    static func parseDay(_ string: String) throws -> Date {
        guard let date = try? dayStyle.parse(string) else {
            throw HTTPError(.badRequest, message: "Dates must be YYYY-MM-DD")
        }
        return date
    }
}

// MARK: Reminders

struct ReminderListDTO: Codable, Sendable, Identifiable {
    let id: String
    let title: String
}

struct ReminderDTO: Codable, Sendable {
    let id: String
    let title: String
    let notes: String?
    let listID: String
    let listTitle: String
    let isCompleted: Bool
    let priority: Int
    /// ISO 8601 date-time (UTC), or yyyy-MM-dd for all-day reminders.
    let due: String?
    let dueAllDay: Bool
}

extension ReminderDTO {
    init(_ reminder: EKReminder) {
        let components = reminder.dueDateComponents
        let allDay = components != nil && components?.hour == nil
        var dueString: String?
        if let components, let date = Calendar.current.date(from: components) {
            dueString = allDay ? DateFormat.day(date) : DateFormat.dateTime(date)
        }
        self.init(
            id: reminder.calendarItemIdentifier,
            title: reminder.title ?? "",
            notes: reminder.notes,
            listID: reminder.calendar?.calendarIdentifier ?? "",
            listTitle: reminder.calendar?.title ?? "",
            isCompleted: reminder.isCompleted,
            priority: reminder.priority,
            due: dueString,
            dueAllDay: allDay
        )
    }
}

struct ListsResponse: ResponseEncodable { let lists: [ReminderListDTO] }
struct RemindersResponse: ResponseEncodable { let reminders: [ReminderDTO] }

// MARK: Calendar

struct CalendarDTO: Codable, Sendable, Identifiable {
    let id: String
    let title: String
    let account: String
}

struct EventDTO: Codable, Sendable {
    let id: String
    let title: String
    /// ISO 8601 (UTC), or yyyy-MM-dd for all-day events (end is inclusive).
    let start: String
    let end: String
    let isAllDay: Bool
    let location: String?
    let notes: String?
    let url: String?
    let calendarID: String
    let calendarTitle: String
}

extension EventDTO {
    init(_ event: EKEvent) {
        let format: (Date) -> String = event.isAllDay ? DateFormat.day : DateFormat.dateTime
        self.init(
            id: event.eventIdentifier ?? "",
            title: event.title ?? "",
            start: format(event.startDate),
            end: format(event.endDate),
            isAllDay: event.isAllDay,
            location: event.location,
            notes: event.notes,
            url: event.url?.absoluteString,
            calendarID: event.calendar?.calendarIdentifier ?? "",
            calendarTitle: event.calendar?.title ?? ""
        )
    }
}

struct CalendarsResponse: ResponseEncodable { let calendars: [CalendarDTO] }
struct EventsResponse: ResponseEncodable { let events: [EventDTO] }

// MARK: Notes (decoded from the JXA scripts' JSON)

struct NoteFolderDTO: Codable, Sendable, Identifiable {
    let id: String
    let name: String
    let account: String
}

struct NoteSummaryDTO: Codable, Sendable {
    let id: String
    let title: String
    let modified: String   // ISO 8601 UTC, sortable as string
    let locked: Bool
}

struct NoteDTO: Codable, Sendable, ResponseEncodable {
    let id: String
    let title: String
    let folderID: String
    let created: String
    let modified: String
    let locked: Bool
    let plaintext: String?
    let html: String?
}

struct NoteFoldersResponse: ResponseEncodable { let folders: [NoteFolderDTO] }
struct NoteSummariesResponse: ResponseEncodable { let notes: [NoteSummaryDTO] }
