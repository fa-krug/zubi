import XCTest
@testable import ZubiAPI

final class ICalendarTests: XCTestCase {
    func testTextIsEscaped() {
        XCTAssertEqual(ICalendar.escape("a\\b;c,d"), "a\\\\b\\;c\\,d")
        // CRLF is one break, and a lone CR or LF is one too.
        XCTAssertEqual(ICalendar.escape("one\r\ntwo\rthree\nfour"), "one\\ntwo\\nthree\\nfour")
        XCTAssertEqual(ICalendar.escape("Café: 10–12"), "Café: 10–12")
    }

    func testLongLinesFoldAt75Octets() {
        XCTAssertEqual(ICalendar.fold(String(repeating: "x", count: 75)), String(repeating: "x", count: 75))

        let line = "DESCRIPTION:" + String(repeating: "abcdefghij", count: 20)
        let folded = ICalendar.fold(line)
        let parts = folded.components(separatedBy: "\r\n")
        XCTAssertGreaterThan(parts.count, 1)
        XCTAssertTrue(parts.allSatisfy { $0.utf8.count <= 75 })
        XCTAssertTrue(parts.dropFirst().allSatisfy { $0.hasPrefix(" ") })
        XCTAssertEqual(parts[0].utf8.count, 75)
        // Unfolding gives the line back.
        XCTAssertEqual(folded.replacingOccurrences(of: "\r\n ", with: ""), line)
    }

    func testFoldingNeverSplitsACharacter() {
        // Two-, three- and four-byte scalars, so a fold point lands inside one sooner or later.
        let line = "SUMMARY:" + String(repeating: "äカ😀", count: 30)
        let folded = ICalendar.fold(line)
        for part in folded.components(separatedBy: "\r\n") {
            XCTAssertLessThanOrEqual(part.utf8.count, 75)
            // Each physical line is whole UTF-8 on its own.
            XCTAssertNotNil(String(bytes: Array(part.utf8), encoding: .utf8))
        }
        XCTAssertEqual(folded.replacingOccurrences(of: "\r\n ", with: ""), line)
    }

    func testAllDayEventsEndTheDayAfter() {
        XCTAssertEqual(ICalendar.compactDay("2026-11-03"), "20261103")
        XCTAssertEqual(ICalendar.compactDay("2026-11-30", addingDays: 1), "20261201")
        XCTAssertEqual(ICalendar.compactDay("2026-12-31", addingDays: 1), "20270101")
        XCTAssertEqual(ICalendar.compactDay("2028-02-28", addingDays: 1), "20280229")

        let event = EventDTO(
            id: "E9", title: "Trip, finally", start: "2026-12-30", end: "2026-12-31", isAllDay: true,
            location: nil, notes: "Pack;\nleave early", url: nil, calendarID: "c", calendarTitle: "Home")
        let lines = ICalendar.document(name: "Home", events: [event], stamp: Date(timeIntervalSince1970: 0))
            .components(separatedBy: "\r\n")
        XCTAssertTrue(lines.contains("DTSTART;VALUE=DATE:20261230"))
        XCTAssertTrue(lines.contains("DTEND;VALUE=DATE:20270101"))
        XCTAssertTrue(lines.contains("UID:E9-20261230"))
        XCTAssertTrue(lines.contains("DTSTAMP:19700101T000000Z"))
        XCTAssertTrue(lines.contains("SUMMARY:Trip\\, finally"))
        XCTAssertTrue(lines.contains("DESCRIPTION:Pack\\;\\nleave early"))
        XCTAssertFalse(lines.contains { $0.hasPrefix("LOCATION") || $0.hasPrefix("URL") })
    }

    func testTimedEventsAreUTC() {
        let event = EventDTO(
            id: "AB:CD", title: "Call", start: "2026-11-02T09:00:00Z", end: "2026-11-02T09:30:00Z",
            isAllDay: false, location: "Room 1", notes: nil, url: "https://example.com/a,b",
            calendarID: "c", calendarTitle: "Work")
        let document = ICalendar.document(name: "Work", events: [event], stamp: Date(timeIntervalSince1970: 0))
        let lines = document.components(separatedBy: "\r\n")
        XCTAssertEqual(lines.first, "BEGIN:VCALENDAR")
        XCTAssertEqual(lines.suffix(2), ["END:VCALENDAR", ""])
        XCTAssertTrue(lines.contains("DTSTART:20261102T090000Z"))
        XCTAssertTrue(lines.contains("DTEND:20261102T093000Z"))
        XCTAssertTrue(lines.contains("UID:AB:CD-20261102T090000Z"))
        XCTAssertTrue(lines.contains("LOCATION:Room 1"))
        // A URI is not TEXT: its comma stays.
        XCTAssertTrue(lines.contains("URL:https://example.com/a,b"))
        XCTAssertFalse(lines.contains { $0.hasPrefix("DESCRIPTION") })
    }
}
