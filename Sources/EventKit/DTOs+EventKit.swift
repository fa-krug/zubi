import EventKit
import Foundation
import ZubiAPI

// The EventKit side of the API's DTOs, kept here so the ZubiAPI package never links EventKit.

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
            notes: reminder.notes.nonEmpty,
            listID: reminder.calendar?.calendarIdentifier ?? "",
            listTitle: reminder.calendar?.title ?? "",
            isCompleted: reminder.isCompleted,
            priority: reminder.priority,
            due: dueString,
            dueAllDay: allDay
        )
    }
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
            location: event.location.nonEmpty,
            notes: event.notes.nonEmpty,
            url: event.url?.absoluteString,
            calendarID: event.calendar?.calendarIdentifier ?? "",
            calendarTitle: event.calendar?.title ?? ""
        )
    }
}

private extension Optional where Wrapped == String {
    /// EventKit stores a cleared field as "" rather than nil, and the API leaves out a field with no
    /// value, so an empty one is left out too rather than sent as "".
    var nonEmpty: String? { self?.isEmpty == false ? self : nil }
}
