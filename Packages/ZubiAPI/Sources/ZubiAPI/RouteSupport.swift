import Foundation
import Hummingbird

// What the write routes share: strict body decoding, the writability check, and turning a source's
// errors into the statuses openapi.json documents.

/// A request body, or 400 saying what is wrong with it.
func decodeBody<T: Decodable>(
    _ type: T.Type, from request: Request, context: some RequestContext
) async throws -> T {
    let buffer = try await request.body.collect(upTo: context.maxUploadSize)
    do {
        return try JSONDecoder().decode(T.self, from: Data(buffer.readableBytesView))
    } catch let error as BodyError {
        throw HTTPError(.badRequest, message: error.message)
    } catch let error as DecodingError {
        throw HTTPError(.badRequest, message: describe(error))
    }
}

private func describe(_ error: DecodingError) -> String {
    switch error {
    case .keyNotFound(let key, _):
        return "Missing field '\(key.stringValue)'"
    case .valueNotFound(_, let context):
        return context.codingPath.last.map { "'\($0.stringValue)' must not be null" } ?? "The body is empty"
    case .typeMismatch(_, let context):
        return context.codingPath.last.map { "'\($0.stringValue)' has the wrong type" }
            ?? "The body must be a JSON object"
    default:
        return "The body is not valid JSON"
    }
}

/// An ID from the path. Hummingbird matches routes on the raw path and hands captures over still
/// escaped, so an EventKit ID with a reserved character, which a client must escape, is unescaped here.
func pathID(_ context: ZubiRequestContext) throws -> String {
    let id = try context.parameters.require("id")
    return id.removingPercentEncoding ?? id
}

/// Query values as browsers, HTML forms and Go's `url.Values` encode them, where `+` is a space.
/// Hummingbird percent-decodes but keeps `+`, so `?list=Zubi+Test` would look for "Zubi+Test" and 404.
/// A literal plus arrives as `%2B`.
struct QueryValues: Sendable {
    private let pairs: [(name: String, value: String)]

    init(_ query: String?) {
        pairs = (query ?? "").split(separator: "&").map { field in
            let parts = field.split(separator: "=", maxSplits: 1, omittingEmptySubsequences: false)
            func decode(_ text: Substring) -> String {
                let spaced = text.replacingOccurrences(of: "+", with: " ")
                return spaced.removingPercentEncoding ?? spaced
            }
            return (decode(parts[0]), parts.count > 1 ? decode(parts[1]) : "")
        }
    }

    /// The first value given for `name`, as Hummingbird's own lookup answers.
    func get(_ name: String) -> String? {
        pairs.first { $0.name == name }?.value
    }
}

extension Request {
    /// Every route reads its query through this, never `uri.queryParameters`; see `QueryValues`.
    var query: QueryValues { QueryValues(uri.query) }
}

/// `?q=`, or nil when it is missing or empty: an empty search matches everything anyway.
func searchQuery(_ request: Request) -> String? {
    guard let q = request.query.get("q"), !q.isEmpty else { return nil }
    return q
}

/// Whether any of `fields` contains `query`, ignoring case, as the spec's `q` describes.
func matches(_ query: String, _ fields: String?...) -> Bool {
    fields.contains { $0?.range(of: query, options: .caseInsensitive) != nil }
}

/// What every successful write does besides answering: tell `/api/changes`, and the activity log.
struct WriteHooks: Sendable {
    let changes: ChangeFeed
    let now: @Sendable () -> Date
    let onWrite: @Sendable (WriteRecord) -> Void

    func record(
        _ action: WriteRecord.Action, _ source: ChangeSource, title: String, container: String,
        _ context: ZubiRequestContext
    ) {
        changes.wrote(source)
        onWrite(WriteRecord(date: now(), keyName: context.key?.name ?? "", action: action, source: source,
                            title: title, container: container))
    }
}

/// 403 unless Settings has the item at Read & Write and the source itself allows changes. Exposure
/// is checked before this, so anything hidden has already answered 404.
func requireWritable(
    _ id: String, sourceAllows: Bool, writableIDs: Set<String>, noun: String, source: String
) throws {
    guard writableIDs.contains(id) else {
        throw HTTPError(.forbidden, message: "\(noun) is shared read-only")
    }
    guard sourceAllows else {
        throw HTTPError(.forbidden, message: "\(noun) is read-only in \(source)")
    }
}

/// Runs a source call, answering a `SourceError` with its status.
func mapSourceErrors<T>(_ operation: () async throws -> T) async throws -> T {
    do {
        return try await operation()
    } catch let error as any SourceError {
        let status: HTTPResponse.Status = switch error.kind {
        case .notFound: .notFound
        case .readOnly: .forbidden
        case .locked: .conflict
        case .invalid: .badRequest
        case .unavailable: .serviceUnavailable
        }
        throw HTTPError(status, message: error.description)
    }
}
