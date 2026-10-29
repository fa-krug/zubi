import Foundation

/// RFC 5545 output for the calendar feed: one VEVENT per occurrence, so a subscribing app shows
/// exactly what `/api/events` lists and never has to expand a recurrence rule itself.
enum ICalendar {
    static let productID = "-//Zubi//Zubi API//EN"

    static func document(name: String, events: [EventDTO], stamp: Date) -> String {
        var lines = [
            "BEGIN:VCALENDAR",
            "VERSION:2.0",
            "PRODID:\(productID)",
            "CALSCALE:GREGORIAN",
            "METHOD:PUBLISH",
            // Not in RFC 5545, but what Apple Calendar, Google and Outlook name a subscription by.
            "X-WR-CALNAME:\(escape(name))",
        ]
        let dtstamp = utcStamp(stamp)
        for event in events {
            lines += vevent(event, dtstamp: dtstamp)
        }
        lines.append("END:VCALENDAR")
        return lines.map(fold).joined(separator: "\r\n") + "\r\n"
    }

    private static func vevent(_ event: EventDTO, dtstamp: String) -> [String] {
        let start: String
        let end: String
        let kind: String
        if event.isAllDay {
            // DTEND is exclusive in iCalendar, while the API's all-day `end` is the last day.
            kind = ";VALUE=DATE"
            start = compactDay(event.start)
            end = compactDay(event.end, addingDays: 1)
        } else {
            kind = ""
            start = instant(event.start)
            end = instant(event.end)
        }
        var lines = [
            "BEGIN:VEVENT",
            // Occurrences of a series share the event's ID, so the start tells them apart.
            "UID:\(escape(event.id + "-" + start))",
            "DTSTAMP:\(dtstamp)",
            "DTSTART\(kind):\(start)",
            "DTEND\(kind):\(end)",
            "SUMMARY:\(escape(event.title))",
        ]
        if let location = event.location { lines.append("LOCATION:\(escape(location))") }
        if let notes = event.notes { lines.append("DESCRIPTION:\(escape(notes))") }
        // A URI is not TEXT, so it is not escaped; a line break would end the property, though.
        if let url = event.url { lines.append("URL:\(url.filter { $0 != "\r" && $0 != "\n" })") }
        lines.append("END:VEVENT")
        return lines
    }

    /// TEXT escaping (RFC 5545 §3.3.11): backslash, semicolon, comma, and every line break as `\n`.
    static func escape(_ text: String) -> String {
        var out = ""
        // CRLF first, so it makes one break rather than two; a lone CR is a break too.
        let lines = text.replacingOccurrences(of: "\r\n", with: "\n").replacingOccurrences(of: "\r", with: "\n")
        for scalar in lines.unicodeScalars {
            switch scalar {
            case "\\": out += "\\\\"
            case ";": out += "\\;"
            case ",": out += "\\,"
            case "\n": out += "\\n"
            default: out.unicodeScalars.append(scalar)
            }
        }
        return out
    }

    /// Folds a content line at 75 octets (§3.1), each continuation starting with a space, and never
    /// inside a UTF-8 sequence.
    static func fold(_ line: String) -> String {
        guard line.utf8.count > 75 else { return line }
        var out = ""
        var width = 0
        for scalar in line.unicodeScalars {
            let size = String(scalar).utf8.count
            if width + size > 75 {
                out += "\r\n "
                width = 1
            }
            out.unicodeScalars.append(scalar)
            width += size
        }
        return out
    }

    private static let utc: Calendar = {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "UTC")!
        return calendar
    }()

    /// `yyyyMMddTHHmmssZ`.
    private static func utcStamp(_ date: Date) -> String {
        let c = utc.dateComponents([.year, .month, .day, .hour, .minute, .second], from: date)
        return String(format: "%04d%02d%02dT%02d%02d%02dZ", c.year!, c.month!, c.day!, c.hour!, c.minute!, c.second!)
    }

    /// An event's ISO 8601 `start` or `end` in UTC form.
    private static func instant(_ string: String) -> String {
        guard let date = DateFormat.parseRequestDate(string)?.date else { return string }
        return utcStamp(date)
    }

    /// `YYYY-MM-DD` as `yyyyMMdd`, optionally moved on. A floating date, so it is counted in UTC,
    /// where no day is ever 23 or 25 hours long.
    static func compactDay(_ day: String, addingDays days: Int = 0) -> String {
        let parts = day.split(separator: "-").compactMap { Int($0) }
        guard parts.count == 3,
              let date = utc.date(from: DateComponents(year: parts[0], month: parts[1], day: parts[2])),
              let moved = utc.date(byAdding: .day, value: days, to: date)
        else { return day.replacingOccurrences(of: "-", with: "") }
        let c = utc.dateComponents([.year, .month, .day], from: moved)
        return String(format: "%04d%02d%02d", c.year!, c.month!, c.day!)
    }
}
