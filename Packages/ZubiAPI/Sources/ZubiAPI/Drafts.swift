import Foundation

// The write routes' request bodies, decoded as strictly as openapi.json describes them: an unknown
// field (`additionalProperties: false`), a wrong type, `null` where the spec allows none, an empty
// title or a bad date fails the decode, and the route answers 400 with the reason.
//
// A list, calendar or folder arrives by ID or title. The route resolves it and puts the ID back
// before a source sees the value, so to a source these fields are always IDs.

/// A field of a partial update: left out, sent as `null`, or sent with a value. An Optional cannot
/// tell the first two apart, and here they differ: keep what is there, or clear it.
public enum PatchField<Value: Sendable>: Sendable {
    case unchanged
    case cleared
    case set(Value)

    func map<T>(_ transform: (Value) throws -> T) rethrows -> PatchField<T> {
        switch self {
        case .unchanged: .unchanged
        case .cleared: .cleared
        case .set(let value): .set(try transform(value))
        }
    }
}

extension PatchField: Equatable where Value: Equatable {}

extension PatchField: Decodable where Value: Decodable {
    /// Only reached for a key that is present; the container overload below reads a missing one.
    public init(from decoder: any Decoder) throws {
        let container = try decoder.singleValueContainer()
        self = container.decodeNil() ? .cleared : .set(try container.decode(Value.self))
    }
}

extension KeyedDecodingContainer {
    /// `decodeIfPresent` would read `null` as missing, so presence is checked first.
    func decode<Value: Decodable>(
        _ type: PatchField<Value>.Type, forKey key: Key
    ) throws -> PatchField<Value> {
        guard contains(key) else { return .unchanged }
        return try decodeNil(forKey: key) ? .cleared : .set(decode(Value.self, forKey: key))
    }
}

/// The body of a note: plain text, whose line breaks are kept, or HTML as Notes stores it.
/// Either way it is what follows the title, which Notes always shows as the first line.
public enum NoteBody: Sendable, Equatable {
    case text(String)
    case html(String)
}

// MARK: Reminders

public struct ReminderDraft: Decodable, Sendable {
    public var title: String
    public var list: String
    public var notes: String?
    public var due: RequestDate?
    /// 0 when left out.
    public var priority: Int

    enum CodingKeys: String, CodingKey, CaseIterable { case title, list, notes, due, priority }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.strictContainer(keyedBy: CodingKeys.self)
        title = try container.decodeTitle(forKey: .title)
        list = try container.decode(String.self, forKey: .list)
        notes = try container.decodeIfSent(String.self, forKey: .notes)
        due = try container.decodeIfSent(String.self, forKey: .due).map { try parseDate($0, field: "due") }
        priority = try container.decodePriority(forKey: .priority) ?? 0
    }
}

public struct ReminderChanges: Decodable, Sendable {
    public var title: String?
    public var list: String?
    public var notes: PatchField<String>
    public var due: PatchField<RequestDate>
    public var priority: Int?
    public var isCompleted: Bool?

    enum CodingKeys: String, CodingKey, CaseIterable { case title, list, notes, due, priority, isCompleted }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.strictContainer(keyedBy: CodingKeys.self)
        title = try container.decodeTitleIfSent(forKey: .title)
        list = try container.decodeIfSent(String.self, forKey: .list)
        notes = try container.decode(PatchField<String>.self, forKey: .notes)
        due = try container.decode(PatchField<String>.self, forKey: .due)
            .map { try parseDate($0, field: "due") }
        priority = try container.decodePriority(forKey: .priority)
        isCompleted = try container.decodeIfSent(Bool.self, forKey: .isCompleted)
    }
}

// MARK: Events

public struct EventDraft: Decodable, Sendable {
    public var title: String
    public var calendar: String
    public var start: RequestDate
    /// For an all-day event, the last day, inclusive.
    public var end: RequestDate
    public var isAllDay: Bool
    public var location: String?
    public var notes: String?
    public var url: URL?

    enum CodingKeys: String, CodingKey, CaseIterable {
        case title, calendar, start, end, isAllDay, location, notes, url
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.strictContainer(keyedBy: CodingKeys.self)
        title = try container.decodeTitle(forKey: .title)
        calendar = try container.decode(String.self, forKey: .calendar)
        start = try parseDate(container.decode(String.self, forKey: .start), field: "start")
        end = try parseDate(container.decode(String.self, forKey: .end), field: "end")
        isAllDay = try container.decodeIfSent(Bool.self, forKey: .isAllDay) ?? false
        location = try container.decodeIfSent(String.self, forKey: .location)
        notes = try container.decodeIfSent(String.self, forKey: .notes)
        url = try container.decodeIfSent(String.self, forKey: .url).map { try parseURL($0, field: "url") }
        try EventTiming.check(start: start, end: end, isAllDay: isAllDay, sent: [start, end])
    }
}

public struct EventChanges: Decodable, Sendable {
    public var title: String?
    public var calendar: String?
    public var start: RequestDate?
    public var end: RequestDate?
    public var isAllDay: Bool?
    public var location: PatchField<String>
    public var notes: PatchField<String>
    public var url: PatchField<URL>

    enum CodingKeys: String, CodingKey, CaseIterable {
        case title, calendar, start, end, isAllDay, location, notes, url
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.strictContainer(keyedBy: CodingKeys.self)
        title = try container.decodeTitleIfSent(forKey: .title)
        calendar = try container.decodeIfSent(String.self, forKey: .calendar)
        start = try container.decodeIfSent(String.self, forKey: .start)
            .map { try parseDate($0, field: "start") }
        end = try container.decodeIfSent(String.self, forKey: .end).map { try parseDate($0, field: "end") }
        isAllDay = try container.decodeIfSent(Bool.self, forKey: .isAllDay)
        location = try container.decode(PatchField<String>.self, forKey: .location)
        notes = try container.decode(PatchField<String>.self, forKey: .notes)
        url = try container.decode(PatchField<String>.self, forKey: .url)
            .map { try parseURL($0, field: "url") }
    }
}

/// An edit can send one end of an event and keep the other, so the routes check the result again.
enum EventTiming {
    static func check(start: RequestDate, end: RequestDate, isAllDay: Bool, sent: [RequestDate]) throws {
        if isAllDay, sent.contains(where: { !$0.isDay }) {
            throw BodyError("An all-day event takes 'start' and 'end' as YYYY-MM-DD")
        }
        guard end.date >= start.date else { throw BodyError("'end' must not be before 'start'") }
    }
}

// MARK: Notes

public struct NoteDraft: Decodable, Sendable {
    public var folder: String
    public var title: String
    /// Nil for a note that is only its title.
    public var body: NoteBody?

    enum CodingKeys: String, CodingKey, CaseIterable { case folder, title, text, html }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.strictContainer(keyedBy: CodingKeys.self)
        folder = try container.decode(String.self, forKey: .folder)
        title = try container.decodeTitle(forKey: .title)
        body = try container.decodeNoteBody(text: .text, html: .html)
    }
}

public struct NoteChanges: Decodable, Sendable {
    public var title: String?
    /// Replaces everything below the title.
    public var body: NoteBody?

    enum CodingKeys: String, CodingKey, CaseIterable { case title, text, html }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.strictContainer(keyedBy: CodingKeys.self)
        title = try container.decodeTitleIfSent(forKey: .title)
        body = try container.decodeNoteBody(text: .text, html: .html)
    }
}

// MARK: Strict decoding

/// A body that decodes as JSON but breaks a rule the spec states in words.
struct BodyError: Error {
    let message: String
    init(_ message: String) { self.message = message }
}

private struct AnyKey: CodingKey {
    let stringValue: String
    var intValue: Int? { nil }
    init(stringValue: String) { self.stringValue = stringValue }
    init?(intValue: Int) { nil }
}

extension Decoder {
    /// A keyed container that refuses keys `Keys` does not list: the spec's `additionalProperties: false`.
    fileprivate func strictContainer<Keys: CodingKey & CaseIterable>(
        keyedBy keys: Keys.Type
    ) throws -> KeyedDecodingContainer<Keys> {
        let known = Set(Keys.allCases.map(\.stringValue))
        let all = try container(keyedBy: AnyKey.self).allKeys.map(\.stringValue)
        if let unknown = all.sorted().first(where: { !known.contains($0) }) {
            throw BodyError("Unknown field '\(unknown)'")
        }
        return try container(keyedBy: keys)
    }
}

extension KeyedDecodingContainer {
    /// Like `decodeIfPresent`, but `null` is refused: the spec does not make these fields nullable.
    fileprivate func decodeIfSent<T: Decodable>(_ type: T.Type, forKey key: Key) throws -> T? {
        guard contains(key) else { return nil }
        if try decodeNil(forKey: key) { throw BodyError("'\(key.stringValue)' must not be null") }
        return try decode(type, forKey: key)
    }

    fileprivate func decodeTitle(forKey key: Key) throws -> String {
        let title = try decode(String.self, forKey: key)
        guard !title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw BodyError("'\(key.stringValue)' must not be empty")
        }
        return title
    }

    fileprivate func decodeTitleIfSent(forKey key: Key) throws -> String? {
        guard contains(key) else { return nil }
        _ = try decodeIfSent(String.self, forKey: key)
        return try decodeTitle(forKey: key)
    }

    fileprivate func decodePriority(forKey key: Key) throws -> Int? {
        guard let priority = try decodeIfSent(Int.self, forKey: key) else { return nil }
        guard (0...9).contains(priority) else { throw BodyError("'\(key.stringValue)' must be 0 to 9") }
        return priority
    }

    fileprivate func decodeNoteBody(text: Key, html: Key) throws -> NoteBody? {
        switch (try decodeIfSent(String.self, forKey: text), try decodeIfSent(String.self, forKey: html)) {
        case (nil, nil): return nil
        case (let text?, nil): return .text(text)
        case (nil, let html?): return .html(html)
        case (_?, _?): throw BodyError("Give '\(text.stringValue)' or '\(html.stringValue)', not both")
        }
    }
}

private func parseDate(_ string: String, field: String) throws -> RequestDate {
    guard let date = DateFormat.parseRequestDate(string) else {
        throw BodyError(
            "'\(field)' must be YYYY-MM-DD or an ISO 8601 date-time with a time zone")
    }
    return date
}

private func parseURL(_ string: String, field: String) throws -> URL {
    guard let url = URL(string: string), url.scheme != nil else {
        throw BodyError("'\(field)' must be a URL")
    }
    return url
}
