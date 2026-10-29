import Foundation
import Hummingbird
import HummingbirdTesting
import XCTest
@testable import ZubiAPI

/// openapi.json is the contract. These fail when the server and the spec drift apart in either direction.
final class ContractTests: XCTestCase {
    /// The source file, not the bundle copy, so a stale bundle cannot hide a change.
    static let specURL = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("Sources/ZubiAPI/Resources/openapi.json")

    struct Operation {
        let method: String
        let path: String
        let body: [String: Any]
        let secured: Bool

        var name: String { "\(method.uppercased()) \(path)" }
        var httpMethod: HTTPRequest.Method { HTTPRequest.Method(method.uppercased())! }
        var parameters: [[String: Any]] { body["parameters"] as? [[String: Any]] ?? [] }
        var responses: [String: Any] { body["responses"] as? [String: Any] ?? [:] }

        /// The path with every `{name}` at its parameter's example.
        var examplePath: String {
            parameters.filter { $0["in"] as? String == "path" }.reduce(path) { path, parameter in
                path.replacingOccurrences(
                    of: "{\(parameter["name"] as! String)}", with: ContractTests.string(parameter["example"]!))
            }
        }

        /// The required query parameters, each at its example; every request needs them.
        var requiredQuery: [String: String] {
            Dictionary(uniqueKeysWithValues: parameters.filter {
                $0["in"] as? String == "query" && $0["required"] as? Bool == true
            }.map {
                ($0["name"] as! String, ContractTests.string($0["example"]!))
            })
        }

        /// The JSON request body's schema and example, for an operation that takes one.
        var requestBody: (schema: [String: Any], example: Any)? {
            guard let body = body["requestBody"] as? [String: Any],
                  let media = (body["content"] as? [String: Any])?["application/json"] as? [String: Any],
                  let schema = media["schema"] as? [String: Any], let example = media["example"]
            else { return nil }
            return (schema, example)
        }

        /// The one documented 2xx.
        var success: (status: Int, content: [String: Any]?)? {
            let codes = responses.keys.compactMap(Int.init).filter { (200..<300).contains($0) }
            guard codes.count == 1, let code = codes.first else { return nil }
            return (code, (responses[String(code)] as? [String: Any])?["content"] as? [String: Any])
        }
    }

    var spec: [String: Any] = [:]
    var operations: [Operation] = []
    /// Per operation, the statuses the error tests got from it.
    var provoked: [String: Set<Int>] = [:]

    override func setUpWithError() throws {
        spec = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: Self.specURL)) as? [String: Any])
        let globalSecurity = spec["security"] as? [Any] ?? []
        let paths = try XCTUnwrap(spec["paths"] as? [String: [String: Any]])
        operations = paths.flatMap { path, methods in
            methods.compactMap { method, body -> Operation? in
                guard let body = body as? [String: Any] else { return nil }
                let security = body["security"] as? [Any] ?? globalSecurity
                return Operation(method: method, path: path, body: body, secured: !security.isEmpty)
            }
        }.sorted { $0.name < $1.name }
        XCTAssertFalse(operations.isEmpty)
    }

    // MARK: The spec itself

    /// These tests read operations in a particular way; anything else in the spec would go unchecked.
    func testOperationsUseOnlyWhatTheseTestsUnderstand() throws {
        let known: Set = [
            "operationId", "tags", "summary", "description", "parameters", "requestBody", "responses", "security",
        ]
        for operation in operations {
            XCTAssertEqual(Set(operation.body.keys).subtracting(known), [], "\(operation.name): unknown keys")
            XCTAssertNotNil(operation.success, "\(operation.name): needs exactly one 2xx response")
            for parameter in operation.parameters {
                let name = try XCTUnwrap(parameter["name"] as? String)
                switch parameter["in"] as? String {
                case "query":
                    break
                case "path":
                    XCTAssertEqual(parameter["required"] as? Bool, true, "\(operation.name): '\(name)'")
                    XCTAssertTrue(operation.path.contains("{\(name)}"), "\(operation.name): '\(name)'")
                default:
                    XCTFail("\(operation.name): '\(name)' is neither a query nor a path parameter")
                }
            }
            if let body = operation.body["requestBody"] as? [String: Any] {
                XCTAssertEqual(body["required"] as? Bool, true, "\(operation.name): requestBody")
                let content = try XCTUnwrap(body["content"] as? [String: Any])
                XCTAssertEqual(Array(content.keys), ["application/json"], "\(operation.name): requestBody")
                XCTAssertNotNil(operation.requestBody, "\(operation.name): requestBody needs a schema and example")
            }
        }
    }

    // MARK: Routes

    func testRoutesMatchTheSpecExactly() {
        // Hummingbird prints a `:id` capture as `{id}`, as the spec writes it.
        let routed = Set(Fixture.server().makeRouter().routes.map { "\($0.method.rawValue) \($0.path)" })
        let documented = Set(operations.map(\.name))
        XCTAssertEqual(routed.subtracting(documented), [], "Routes the spec does not document")
        XCTAssertEqual(documented.subtracting(routed), [], "Documented operations with no route")
        XCTAssertEqual(documented.count, 19)
    }

    // MARK: Success shapes

    func testEveryOperationAnswersItsDocumentedShape() async throws {
        let validator = SchemaValidator(spec: spec)
        // Request bodies are checked separately, so they do not count toward the responses' coverage.
        let bodyValidator = SchemaValidator(spec: spec)
        for operation in operations {
            let body = operation.requestBody.map { schema, example in
                XCTAssertEqual(bodyValidator.errors(example, against: schema), [],
                               "\(operation.name): the requestBody example breaks its own schema")
                return example
            }
            // A fresh server each time: a write must not change what the next example finds.
            try await expectSuccess(operation, query: operation.requiredQuery, body: body, validator)

            for parameter in operation.parameters {
                let name = try XCTUnwrap(parameter["name"] as? String)
                let example = try XCTUnwrap(parameter["example"], "\(operation.name): '\(name)' has no example")
                let schema = try XCTUnwrap(parameter["schema"] as? [String: Any])
                XCTAssertEqual(validator.errors(example, against: schema), [],
                               "\(operation.name): example for '\(name)' breaks its own schema")
                guard parameter["in"] as? String == "query" else { continue }
                var query = operation.requiredQuery
                query[name] = Self.string(example)
                try await expectSuccess(operation, query: query, body: body, validator)
            }
        }

        // Shapes no example reaches: a locked note has neither plaintext nor html.
        let note = try XCTUnwrap(operations.first { $0.name == "GET /api/note" })
        try await expectSuccess(note, query: ["id": Fixture.lockedNoteID], body: nil, validator)

        // Every optional property has been seen both ways, so neither shape goes untested.
        for (name, schema) in validator.components where validator.present[name] != nil || validator.absent[name] != nil {
            let required = Set(schema["required"] as? [String] ?? [])
            let optional = Set((schema["properties"] as? [String: Any] ?? [:]).keys).subtracting(required)
            XCTAssertEqual(optional.subtracting(validator.present[name] ?? []), [], "\(name): never seen present")
            XCTAssertEqual(optional.subtracting(validator.absent[name] ?? []), [], "\(name): never seen absent")
        }
    }

    func testWritableIsBothSettingsAndSource() async throws {
        try await withClient(Fixture.server()) { client in
            let lists = try await self.json(client, "GET", "/api/lists")["lists"] as? [[String: Any]]
            XCTAssertEqual(Self.writable(lists, key: "title"), ["Errands": false, "Groceries": true])
            let calendars = try await self.json(client, "GET", "/api/calendars")["calendars"] as? [[String: Any]]
            XCTAssertEqual(Self.writable(calendars, key: "title"),
                           ["Home": false, "Holidays": false, "Work": true])
            let folders = try await self.json(client, "GET", "/api/notes/folders")["folders"] as? [[String: Any]]
            XCTAssertEqual(Self.writable(folders, key: "name"), ["Recipes": true, "Weeknight": false])
        }
    }

    func testWritesAreReadBack() async throws {
        try await withClient(Fixture.server()) { client in
            // Reminders: an offset becomes UTC, and null clears.
            let reminder = try await self.json(client, "POST", "/api/reminders", status: .created, body: [
                "title": "Eggs", "list": "list-groceries", "notes": "Free range",
                "due": "2026-11-05T17:30:00+01:00",
            ])
            XCTAssertEqual(reminder["due"] as? String, "2026-11-05T16:30:00Z")
            XCTAssertEqual(reminder["dueAllDay"] as? Bool, false)
            XCTAssertEqual(reminder["priority"] as? Int, 0)
            let reminderID = try XCTUnwrap(reminder["id"] as? String)
            var listed = try await self.json(client, "GET", "/api/reminders", query: ["list": "Groceries"])
            XCTAssertEqual(Self.item(listed["reminders"], reminderID)?["notes"] as? String, "Free range")

            let cleared = try await self.json(client, "PATCH", "/api/reminders/\(reminderID)", body: [
                "notes": NSNull(), "due": NSNull(), "title": "Brown eggs",
            ])
            XCTAssertNil(cleared["notes"])
            XCTAssertNil(cleared["due"])
            listed = try await self.json(client, "GET", "/api/reminders", query: ["list": "Groceries"])
            let readBack = Self.item(listed["reminders"], reminderID)
            XCTAssertEqual(readBack?["title"] as? String, "Brown eggs")
            XCTAssertNil(readBack?["notes"])
            XCTAssertNil(readBack?["due"])

            // Events: a new one shows up, and an edit to one occurrence leaves the series alone.
            let event = try await self.json(client, "POST", "/api/events", status: .created, body: [
                "title": "Offsite", "calendar": "Work", "start": "2026-11-05", "end": "2026-11-06", "isAllDay": true,
            ])
            XCTAssertEqual(event["start"] as? String, "2026-11-05")
            XCTAssertEqual(event["end"] as? String, "2026-11-06")
            let eventID = try XCTUnwrap(event["id"] as? String)
            var events = try await self.json(client, "GET", "/api/events", query: ["calendar": "Work"])
            XCTAssertEqual(Self.item(events["events"], eventID)?["title"] as? String, "Offsite")

            _ = try await self.json(client, "PATCH", "/api/events/E4", query: ["occurrence": "2026-11-03T10:00:00Z"],
                                    body: ["title": "Sync (moved)", "start": "2026-11-03T11:00:00Z",
                                           "end": "2026-11-03T11:30:00Z"])
            // Escaped, as a client must send an EventKit ID with a reserved character.
            _ = try await self.json(client, "PATCH", "/api/events/E%31", body: ["location": NSNull(), "url": NSNull()])
            events = try await self.json(client, "GET", "/api/events", query: ["calendar": "Work"])
            let all = try XCTUnwrap(events["events"] as? [[String: Any]])
            XCTAssertEqual(all.filter { $0["id"] as? String == "E4" }.map { $0["title"] as? String },
                           ["Sync", "Sync (moved)"])
            XCTAssertNil(Self.item(events["events"], "E1")?["location"])
            XCTAssertNil(Self.item(events["events"], "E1")?["url"])
            XCTAssertEqual(Self.item(events["events"], "E1")?["notes"] as? String, "Bring the numbers")

            // Notes: plain text is escaped, keeps its lines, and follows the title.
            let note = try await self.json(client, "POST", "/api/notes", status: .created, body: [
                "folder": "Recipes", "title": "Fish & chips", "text": "Cod <fresh>\n\nChips",
            ])
            XCTAssertEqual(note["html"] as? String,
                           "<div><h1>Fish &amp; chips</h1></div><div>Cod &lt;fresh&gt;</div><div><br></div><div>Chips</div>")
            let noteID = try XCTUnwrap(note["id"] as? String)
            let notes = try await self.json(client, "GET", "/api/notes", query: ["folder": "Recipes"])
            XCTAssertEqual(Self.item(notes["notes"], noteID)?["title"] as? String, "Fish & chips")

            _ = try await self.json(client, "PATCH", "/api/note", query: ["id": noteID], body: ["title": "Fish"])
            let renamed = try await self.json(client, "GET", "/api/note", query: ["id": noteID])
            XCTAssertEqual(renamed["plaintext"] as? String, "Fish\nCod <fresh>\n\nChips")
        }
    }

    // MARK: Keys, search, activity, feed and stream

    func testSearchMatchesTitleOrNotesIgnoringCase() async throws {
        let searches: [(path: String, q: String, found: [String], why: String)] = [
            ("/api/reminders", "MILK", ["R1"], "title, any case"),
            ("/api/reminders", "two cartons", ["R1"], "notes"),
            ("/api/reminders", "nothing like it", [], "no match"),
            ("/api/reminders", "", ["R1", "R2", "R3"], "an empty search is no search"),
            ("/api/events", "the NUMBERS", ["E1"], "notes"),
            ("/api/events", "sync", ["E4", "E4"], "every occurrence"),
            ("/api/events", "room 4", [], "location is not searched: the spec says title or notes"),
            ("/api/notes", "FLOUR", [Fixture.noteID], "text"),
            ("/api/notes", "family", [Fixture.lockedNoteID], "a locked note by its title, which listings show"),
            ("/api/notes", "diary", [], "hidden folders stay hidden"),
        ]
        try await withClient(Fixture.server()) { client in
            for search in searches {
                let body = try await self.json(client, "GET", search.path, query: ["q": search.q])
                let items = body.values.first as? [[String: Any]] ?? []
                XCTAssertEqual(items.compactMap { $0["id"] as? String }, search.found,
                               "\(search.path)?q=\(search.q): \(search.why)")
            }
        }
    }

    /// Browsers, HTML forms and Go's `url.Values` send a space as `+`. Found live: `zubi reminders --list
    /// "Zubi Test"` answered 404 because the name arrived as "Zubi+Test".
    func testQueryDecodesPlusAsSpace() async throws {
        try await withClient(Fixture.server()) { client in
            for (query, found) in [("q=post+office", ["R3"]), ("q=post%20office", ["R3"]), ("q=a%2Bb", [String]())] {
                let response = try await client.execute(
                    uri: "/api/reminders?" + query, method: .get,
                    headers: [.authorization: "Bearer \(Fixture.apiKey)"])
                XCTAssertEqual(response.status, .ok, query)
                let body = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(response.body.readableBytesView)) as? [String: Any])
                let items = body["reminders"] as? [[String: Any]] ?? []
                XCTAssertEqual(items.compactMap { $0["id"] as? String }, found, query)
            }
        }
        XCTAssertEqual(QueryValues("list=Zubi+Test&q=a%2Bb&empty=&bare").get("list"), "Zubi Test")
        XCTAssertEqual(QueryValues("list=Zubi+Test&q=a%2Bb&empty=&bare").get("q"), "a+b")
        XCTAssertEqual(QueryValues("list=Zubi+Test&q=a%2Bb&empty=&bare").get("empty"), "")
        XCTAssertEqual(QueryValues("list=Zubi+Test&q=a%2Bb&empty=&bare").get("bare"), "")
        XCTAssertNil(QueryValues("list=Zubi+Test").get("missing"))
    }

    func testKeyInfoAndReadOnlyKeys() async throws {
        let validator = SchemaValidator(spec: spec)
        let keyInfo: [String: Any] = ["$ref": "#/components/schemas/KeyInfo"]
        try await withClient(Fixture.server()) { client in
            let readWrite = try await self.json(client, "GET", "/api/key")
            XCTAssertEqual(readWrite as? [String: String], ["name": "Laptop", "access": "readWrite"])
            let read = try await self.json(client, "GET", "/api/key", key: Fixture.readKey)
            XCTAssertEqual(read as? [String: String], ["name": "Pi", "access": "read"])
            XCTAssertEqual(validator.errors(read, against: keyInfo), [])

            // Nothing is writable to a read-only key, whatever Settings allows.
            let key = Fixture.readKey
            let lists = try await self.json(client, "GET", "/api/lists", key: key)["lists"] as? [[String: Any]]
            XCTAssertEqual(Self.writable(lists, key: "title"), ["Errands": false, "Groceries": false])
            let calendars = try await self.json(client, "GET", "/api/calendars", key: key)["calendars"]
                as? [[String: Any]]
            XCTAssertEqual(Self.writable(calendars, key: "title"), ["Home": false, "Holidays": false, "Work": false])
            let folders = try await self.json(client, "GET", "/api/notes/folders", key: key)["folders"]
                as? [[String: Any]]
            XCTAssertEqual(Self.writable(folders, key: "name"), ["Recipes": false, "Weeknight": false])
        }
    }

    /// `queryKey` is for calendar apps, which can't send a header; no other route may take it, and
    /// it takes only a read-only key, since a URL ends up in logs and sync settings.
    func testOnlyTheFeedTakesTheKeyInTheQuery() async throws {
        let schemes = try XCTUnwrap((spec["components"] as? [String: Any])?["securitySchemes"] as? [String: [String: Any]])
        let name = try XCTUnwrap(schemes["queryKey"]?["name"] as? String)
        let global = spec["security"] as? [[String: Any]] ?? []
        let accepted = Locked<[String]>([])
        try await withClient(Fixture.server()) { client in
            for operation in self.operations where operation.secured {
                let security = operation.body["security"] as? [[String: Any]] ?? global
                let takesQueryKey = security.contains { $0["queryKey"] != nil }
                // A read-write key in a URL is refused like a wrong one, even where a read key works.
                for (key, works) in [(Fixture.readKey, takesQueryKey), (Fixture.apiKey, false), ("wrong", false)] {
                    var query = operation.requiredQuery
                    query[name] = key
                    let response = try await client.execute(
                        uri: Self.uri(operation.examplePath, query), method: operation.httpMethod, headers: [:],
                        body: try operation.requestBody.map { try Self.buffer($0.example) })
                    if works {
                        XCTAssertEqual(Int(response.status.code), operation.success?.status, operation.name)
                        accepted.mutate { $0.append(operation.name) }
                    } else {
                        XCTAssertEqual(response.status, .unauthorized, "\(operation.name) ?\(name)=\(key)")
                        XCTAssertEqual(response.body.readableBytes, 0, "\(operation.name): 401 has a body")
                    }
                }
            }
            // In the header, the feed takes either kind, as every route does.
            for key in [Fixture.apiKey, Fixture.readKey] {
                let response = try await client.execute(
                    uri: "/api/calendars/Work/events.ics", method: .get, headers: [.authorization: "Bearer \(key)"])
                XCTAssertEqual(response.status, .ok, key)
            }
        }
        XCTAssertEqual(accepted.value, ["GET /api/calendars/{id}/events.ics"])
    }

    func testWritesAreRecordedWithTheirKey() async throws {
        let records = Locked<[WriteRecord]>([])
        let used = Locked<[String]>([])
        let server = Fixture.server(
            Fixture.Sources(notes: FakeNotes(fails: false)),
            onKeyUsed: { id in used.mutate { $0.append(id) } },
            onWrite: { record in records.mutate { $0.append(record) } })
        try await withClient(server) { client in
            _ = try await self.json(client, "POST", "/api/reminders", status: .created,
                                    body: ["title": "Stamps", "list": "Groceries"])
            _ = try await self.json(client, "PATCH", "/api/reminders/R1", body: ["list": "list-groceries"])
            _ = try await self.json(client, "PATCH", "/api/events/E1", body: ["title": "Standup (moved)"])
            _ = try await self.json(client, "POST", "/api/notes", status: .created,
                                    body: ["folder": "Recipes", "title": "Soup"])
            _ = try await self.json(client, "PATCH", "/api/note", query: ["id": Fixture.noteID], body: ["text": "Eggs"])
            // Refused writes leave no trace.
            try await self.expectError(.forbidden, "POST", "/api/reminders",
                                       body: ["title": "Stamps", "list": "Errands"], client)
            try await self.expectError(.forbidden, "POST", "/api/reminders",
                                       body: ["title": "Stamps", "list": "Groceries"], key: Fixture.readKey, client)
        }
        XCTAssertEqual(records.value.map { "\($0.keyName) \($0.action) \($0.source) \($0.title) in \($0.container)" }, [
            "Laptop created reminders Stamps in Groceries",
            "Laptop edited reminders Milk in Groceries",
            "Laptop edited calendar Standup (moved) in Work",
            "Laptop created notes Soup in Recipes",
            "Laptop edited notes Pancakes in Recipes",
        ])
        XCTAssertEqual(records.value.map(\.date), Array(repeating: Fixture.now, count: 5))
        XCTAssertEqual(Set(used.value), ["key-laptop", "key-pi"])
    }

    func testCalendarFeedIsICalendar() async throws {
        try await withClient(Fixture.server()) { client in
            let response = try await client.execute(
                uri: "/api/calendars/cal-work/events.ics", method: .get,
                headers: [.authorization: "Bearer \(Fixture.apiKey)"])
            XCTAssertEqual(response.status, .ok)
            XCTAssertEqual(response.headers[.contentType], "text/calendar; charset=utf-8")
            let body = String(buffer: response.body)
            XCTAssertTrue(body.hasPrefix("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:"))
            XCTAssertTrue(body.hasSuffix("END:VCALENDAR\r\n"))
            XCTAssertFalse(body.replacingOccurrences(of: "\r\n", with: "").contains("\n"), "bare LF")
            let lines = body.components(separatedBy: "\r\n")
            XCTAssertTrue(lines.contains("X-WR-CALNAME:Work"))
            // Work only, and a recurring event once per occurrence, each with its own UID.
            XCTAssertEqual(lines.filter { $0 == "BEGIN:VEVENT" }.count, 3)
            let uids = lines.filter { $0.hasPrefix("UID:") }
            XCTAssertEqual(Set(uids).count, 3)
            XCTAssertTrue(lines.contains("DTSTART:20261103T100000Z"))
            XCTAssertTrue(lines.contains("URL:https://example.com/meet"))
            // When the feed was made, which the tests pin.
            let stamps = Set(lines.filter { $0.hasPrefix("DTSTAMP:") })
            XCTAssertEqual(stamps.count, 1)
            XCTAssertEqual(stamps.first?.hasSuffix("0000Z"), true)
        }
    }

    /// A client sees a write as it happens, and leaves nothing behind when it goes.
    func testChangeStreamReportsWritesAndLetsGo() async throws {
        let changes = ChangeFeed()
        let server = Fixture.server(Fixture.Sources(notes: FakeNotes(fails: false)), changes: changes)
        let schema: [String: Any] = ["$ref": "#/components/schemas/Change"]
        let validator = SchemaValidator(spec: spec)
        try await withLiveServer(server) { port in
            let (_, bytes) = try await Self.openStream(port: port, uri: "/api/changes")
            var lines = bytes.lines.filter { !$0.isEmpty }.makeAsyncIterator()
            let connected = try await lines.next()
            XCTAssertEqual(connected, ": connected")
            XCTAssertEqual(changes.subscriberCount, 1)

            var post = URLRequest(url: URL(string: "http://localhost:\(port)/api/notes")!)
            post.httpMethod = "POST"
            post.setValue("Bearer \(Fixture.apiKey)", forHTTPHeaderField: "Authorization")
            post.httpBody = try JSONSerialization.data(withJSONObject: ["folder": "Recipes", "title": "Soup"])
            let (_, created) = try await URLSession(configuration: .ephemeral).data(for: post)
            XCTAssertEqual((created as? HTTPURLResponse)?.statusCode, 201)

            var message: [String] = []
            while message.count < 2, let line = try await lines.next() {
                if !line.hasPrefix(":") { message.append(line) }  // heartbeats may come first
            }
            XCTAssertEqual(message.first, "event: change")
            let data = try XCTUnwrap(message.last?.split(separator: " ", maxSplits: 1).last)
            XCTAssertTrue(message.last?.hasPrefix("data: ") == true)
            let change = try JSONSerialization.jsonObject(with: Data(data.utf8))
            XCTAssertEqual(validator.errors(change, against: schema), [])
            XCTAssertEqual(change as? [String: String], ["source": "notes", "origin": "zubi"])

            bytes.task.cancel()
            // The next heartbeat finds the client gone.
            let released = try await Self.eventually { changes.subscriberCount == 0 }
            XCTAssertTrue(released, "the stream outlived its client")
        }
    }

    // MARK: Errors

    func testSecuredOperationsNeedTheKey() async throws {
        try await withClient(Fixture.server()) { client in
            for operation in self.operations where operation.secured {
                for headers: HTTPFields in [[:], [.authorization: "Bearer wrong"]] {
                    let response = try await client.execute(
                        uri: Self.uri(operation.examplePath, operation.requiredQuery), method: operation.httpMethod,
                        headers: headers, body: try operation.requestBody.map { try Self.buffer($0.example) })
                    XCTAssertEqual(response.status, .unauthorized, operation.name)
                    let documented = try XCTUnwrap(operation.responses["401"] as? [String: Any],
                                                   "\(operation.name) does not document 401")
                    XCTAssertNil(documented["content"], "\(operation.name): 401 is documented with a body")
                    XCTAssertEqual(response.body.readableBytes, 0, "\(operation.name): 401 has a body")
                }
            }
        }
    }

    /// Every error any operation documents, provoked at least once, with the body `Error` describes.
    func testEveryDocumentedErrorIsProvoked() async throws {
        try await provokeReadErrors()
        try await provokeAccessDenied()
        try await provokeNotesFailures()
        try await provokeWriteErrors()
        try await provokeFeedErrors()
        try await provokeReadOnlyKey()

        // 401 is every secured operation's, and testSecuredOperationsNeedTheKey covers it.
        for operation in operations {
            let documented = Set(operation.responses.keys.compactMap(Int.init).filter { $0 >= 400 && $0 != 401 })
            XCTAssertEqual(documented.subtracting(provoked[operation.name] ?? []), [],
                           "\(operation.name): documented errors no test provokes")
        }
    }

    private func provokeReadErrors() async throws {
        try await withClient(Fixture.server()) { client in
            try await self.expectError(.badRequest, "GET", "/api/reminders", query: ["due": "tomorrow"], client)
            try await self.expectError(.notFound, "GET", "/api/reminders", query: ["list": "Hidden"], client)
            try await self.expectError(.notFound, "GET", "/api/reminders", query: ["list": "list-hidden"], client)
            try await self.expectError(.badRequest, "GET", "/api/events", query: ["from": "2-11-2026"], client)
            try await self.expectError(.badRequest, "GET", "/api/events",
                                       query: ["from": "2026-11-06", "to": "2026-11-02"], client)
            try await self.expectError(.badRequest, "GET", "/api/events",
                                       query: ["from": "2026-01-01", "to": "2027-06-01"], client)
            try await self.expectError(.notFound, "GET", "/api/events", query: ["calendar": "Hidden"], client)
            try await self.expectError(.notFound, "GET", "/api/notes", query: ["folder": "Hidden"], client)
            try await self.expectError(.badRequest, "GET", "/api/note", client)
            try await self.expectError(.notFound, "GET", "/api/note", query: ["id": Fixture.hiddenNoteID], client)
            try await self.expectError(.notFound, "GET", "/api/note", query: ["id": "x-coredata://nope/ICNote/p9"],
                                       client)
        }
    }

    private func provokeAccessDenied() async throws {
        try await withClient(Fixture.server(remindersGranted: false, calendarGranted: false)) { client in
            for path in ["/api/lists", "/api/reminders", "/api/calendars", "/api/events"] {
                try await self.expectError(.forbidden, "GET", path, client)
            }
            try await self.expectError(.forbidden, "GET", "/api/calendars/{id}/events.ics", path: ["id": "Work"],
                                       client)
        }
    }

    private func provokeNotesFailures() async throws {
        try await withClient(Fixture.server(notesFail: true)) { client in
            try await self.expectError(.serviceUnavailable, "GET", "/api/notes/folders", client)
            try await self.expectError(.serviceUnavailable, "GET", "/api/notes", client)
            try await self.expectError(.serviceUnavailable, "GET", "/api/note", query: ["id": Fixture.noteID], client)
        }
    }

    /// A body that should fail for anything but its shape is first checked against the schema, so it
    /// fails for the intended reason.
    private func provokeWriteErrors() async throws {
        let reminder: [String: Any] = ["title": "Stamps", "list": "Groceries"]
        let event: [String: Any] = [
            "title": "Lunch", "calendar": "Work", "start": "2026-11-03T12:00:00Z", "end": "2026-11-03T13:00:00Z",
        ]
        let note: [String: Any] = ["folder": "Recipes", "title": "Soup"]

        try await withClient(Fixture.server()) { client in
            // POST /api/reminders
            let post = { (status: HTTPResponse.Status, body: Any) in
                try await self.expectError(status, "POST", "/api/reminders", body: body, client)
            }
            try await post(.forbidden, Self.with(reminder, ["list": "Errands"]))
            try await post(.notFound, Self.with(reminder, ["list": "Hidden"]))
            try await post(.notFound, Self.with(reminder, ["list": "Nowhere"]))
            try await post(.badRequest, Self.with(reminder, ["title": ""]))
            try await post(.badRequest, Self.with(reminder, ["title": "  "]))
            try await post(.badRequest, ["list": "Groceries"])
            try await post(.badRequest, Self.with(reminder, ["colour": "red"]))
            try await post(.badRequest, Self.with(reminder, ["notes": NSNull()]))
            try await post(.badRequest, Self.with(reminder, ["due": "2026-11-02T09:00:00"]))
            try await post(.badRequest, Self.with(reminder, ["due": "2026-02-30"]))
            try await post(.badRequest, Self.with(reminder, ["due": "tomorrow"]))
            try await post(.badRequest, Self.with(reminder, ["priority": 10]))
            try await post(.badRequest, Self.with(reminder, ["priority": "high"]))
            try await post(.badRequest, Data("{\"title\":".utf8))
            try await post(.badRequest, ["title", "Stamps"])

            // PATCH /api/reminders/{id}
            let patch = { (status: HTTPResponse.Status, id: String, body: Any) in
                try await self.expectError(status, "PATCH", "/api/reminders/{id}", path: ["id": id], body: body,
                                           client)
            }
            try await patch(.forbidden, "R3", ["title": "Parcel"])
            try await patch(.notFound, "R4", ["title": "Parcel"])
            try await patch(.notFound, "R99", ["title": "Parcel"])
            try await patch(.forbidden, "R1", ["list": "Errands"])
            try await patch(.notFound, "R1", ["list": "Hidden"])
            try await patch(.badRequest, "R1", ["title": ""])
            try await patch(.badRequest, "R1", ["title": NSNull()])
            try await patch(.badRequest, "R1", ["priority": NSNull()])
            try await patch(.badRequest, "R1", ["done": true])
            try await patch(.badRequest, "R1", ["due": "02/11/2026"])

            // POST /api/events
            let postEvent = { (status: HTTPResponse.Status, body: Any) in
                try await self.expectError(status, "POST", "/api/events", body: body, client)
            }
            try await postEvent(.forbidden, Self.with(event, ["calendar": "Home"]))
            try await postEvent(.forbidden, Self.with(event, ["calendar": "Holidays"]))
            try await postEvent(.notFound, Self.with(event, ["calendar": "Hidden"]))
            try await postEvent(.badRequest, Self.with(event, ["end": "2026-11-03T11:00:00Z"]))
            try await postEvent(.badRequest, Self.with(event, ["start": "2026-11-04", "end": "2026-11-03", "isAllDay": true]))
            try await postEvent(.badRequest, Self.with(event, ["isAllDay": true]))
            try await postEvent(.badRequest, Self.with(event, ["start": "2026-11-03 12:00"]))
            try await postEvent(.badRequest, Self.with(event, ["title": ""]))
            try await postEvent(.badRequest, Self.with(event, ["url": "not a url"]))
            try await postEvent(.badRequest, Self.with(event, ["attendees": []]))

            // PATCH /api/events/{id}
            let patchEvent = { (status: HTTPResponse.Status, id: String, query: [String: String], body: Any) in
                try await self.expectError(status, "PATCH", "/api/events/{id}", path: ["id": id], query: query,
                                           body: body, client)
            }
            try await patchEvent(.badRequest, "E4", [:], ["title": "Sync"])
            try await patchEvent(.badRequest, "E4", ["occurrence": "next week"], ["title": "Sync"])
            try await patchEvent(.notFound, "E4", ["occurrence": "2026-11-05T10:00:00Z"], ["title": "Sync"])
            try await patchEvent(.forbidden, "E2", [:], ["title": "Holiday"])
            try await patchEvent(.notFound, "E3", [:], ["title": "Secret"])
            try await patchEvent(.notFound, "E99", [:], ["title": "Nothing"])
            try await patchEvent(.badRequest, "E1", [:], ["end": "2026-11-02T08:00:00Z"])
            try await patchEvent(.badRequest, "E1", [:], ["isAllDay": true, "start": "2026-11-02T09:00:00Z"])
            try await patchEvent(.forbidden, "E1", [:], ["calendar": "Holidays"])
            try await patchEvent(.notFound, "E1", [:], ["calendar": "Hidden"])
            try await patchEvent(.badRequest, "E1", [:], ["title": NSNull()])
            try await patchEvent(.badRequest, "E1", [:], ["room": "4"])

            // POST /api/notes
            let postNote = { (status: HTTPResponse.Status, body: Any) in
                try await self.expectError(status, "POST", "/api/notes", body: body, client)
            }
            try await postNote(.badRequest, Self.with(note, ["text": "Leeks", "html": "<div>Leeks</div>"]))
            try await postNote(.badRequest, Self.with(note, ["title": ""]))
            try await postNote(.badRequest, Self.with(note, ["tags": ["soup"]]))
            try await postNote(.forbidden, Self.with(note, ["folder": "Weeknight"]))
            try await postNote(.notFound, Self.with(note, ["folder": "Hidden"]))

            // PATCH /api/note
            let patchNote = { (status: HTTPResponse.Status, query: [String: String], body: Any) in
                try await self.expectError(status, "PATCH", "/api/note", query: query, body: body, client)
            }
            try await patchNote(.badRequest, [:], ["text": "Leeks"])
            try await patchNote(.badRequest, ["id": Fixture.noteID], ["text": "Leeks", "html": "<div>Leeks</div>"])
            try await patchNote(.badRequest, ["id": Fixture.noteID], ["title": ""])
            try await patchNote(.badRequest, ["id": Fixture.noteID], ["folder": "Weeknight"])
            try await patchNote(.conflict, ["id": Fixture.lockedNoteID], ["text": "Leeks"])
            try await patchNote(.forbidden, ["id": Fixture.readOnlyNoteID], ["text": "Leeks"])
            try await patchNote(.notFound, ["id": Fixture.hiddenNoteID], ["text": "Leeks"])
            try await patchNote(.notFound, ["id": "x-coredata://nope/ICNote/p9"], ["text": "Leeks"])
        }

        try await withClient(Fixture.server(remindersGranted: false, calendarGranted: false)) { client in
            try await self.expectError(.forbidden, "POST", "/api/reminders", body: reminder, client)
            try await self.expectError(.forbidden, "PATCH", "/api/reminders/{id}", path: ["id": "R1"],
                                       body: ["title": "Oat milk"], client)
            try await self.expectError(.forbidden, "POST", "/api/events", body: event, client)
            try await self.expectError(.forbidden, "PATCH", "/api/events/{id}", path: ["id": "E1"],
                                       body: ["title": "Stand-up"], client)
        }

        try await withClient(Fixture.server(notesFail: true)) { client in
            try await self.expectError(.serviceUnavailable, "POST", "/api/notes", body: note, client)
            try await self.expectError(.serviceUnavailable, "PATCH", "/api/note", query: ["id": Fixture.noteID],
                                       body: ["text": "Leeks"], client)
        }
    }


    private func provokeFeedErrors() async throws {
        try await withClient(Fixture.server()) { client in
            let feed = { (status: HTTPResponse.Status, id: String, query: [String: String]) in
                try await self.expectError(status, "GET", "/api/calendars/{id}/events.ics", path: ["id": id],
                                           query: query, client)
            }
            try await feed(.notFound, "Hidden", [:])
            try await feed(.notFound, "cal-hidden", [:])
            try await feed(.notFound, "Nowhere", [:])
            try await feed(.badRequest, "Work", ["from": "2026-13-01"])
            try await feed(.badRequest, "Work", ["from": "2026-11-06", "to": "2026-11-02"])
            try await feed(.badRequest, "Work", ["from": "2026-01-01", "to": "2028-06-01"])
            // Two years is fine.
            let response = try await client.execute(
                uri: Self.uri("/api/calendars/Work/events.ics", ["from": "2026-01-01", "to": "2027-12-31"]),
                method: .get, headers: [.authorization: "Bearer \(Fixture.apiKey)"])
            XCTAssertEqual(response.status, .ok)
        }
    }

    /// A read-only key may read everything a read-write key may, and change nothing, even where the
    /// item itself is writable.
    private func provokeReadOnlyKey() async throws {
        try await withClient(Fixture.server()) { client in
            for operation in self.operations where operation.requestBody != nil {
                let path = operation.parameters.filter { $0["in"] as? String == "path" }.reduce(into: [:]) {
                    $0[$1["name"] as! String] = Self.string($1["example"]!)
                }
                try await self.expectError(.forbidden, operation.method.uppercased(), operation.path, path: path,
                                           query: operation.requiredQuery, body: operation.requestBody!.example,
                                           key: Fixture.readKey, client)
            }
        }
    }

    // MARK: Request parsing

    func testRequestDates() {
        let day = DateFormat.parseRequestDate("2026-11-02")
        XCTAssertEqual(day, .day(try! DateFormat.parseDay("2026-11-02")))
        XCTAssertEqual(DateFormat.parseRequestDate("2026-11-02T10:00:00+01:00"),
                       DateFormat.parseRequestDate("2026-11-02T09:00:00Z"))
        XCTAssertEqual(DateFormat.parseRequestDate("2026-11-02T09:00:00.250Z").map(DateFormat.string),
                       "2026-11-02T09:00:00Z")
        for bad in ["2026-11-02T09:00:00", "2026-02-30", "2026-1-2", "02.11.2026", "", "today"] {
            XCTAssertNil(DateFormat.parseRequestDate(bad), bad)
        }
    }

    func testNoteHTMLEscapesAndKeepsLines() {
        XCTAssertEqual(NoteHTML.body(title: "<Title>", nil), "<div><h1>&lt;Title&gt;</h1></div>")
        XCTAssertEqual(NoteHTML.body(title: "T", .text("a & \"b\"\r\n'c'\n")),
                       "<div><h1>T</h1></div><div>a &amp; &quot;b&quot;</div><div>&#39;c&#39;</div><div><br></div>")
        XCTAssertEqual(NoteHTML.body(title: "T", .html("<div><b>x</b></div>")), "<div><h1>T</h1></div><div><b>x</b></div>")
    }

    // MARK: Docs

    func testOpenAPIServesTheBundledSpec() async throws {
        let expected = try Data(contentsOf: Self.specURL)
        try await withClient(Fixture.server()) { client in
            let response = try await client.execute(uri: "/openapi.json", method: .get)
            XCTAssertEqual(response.status, .ok)
            XCTAssertEqual(response.headers[.contentType], "application/json")
            XCTAssertEqual(Data(response.body.readableBytesView), expected)
        }
    }

    func testDocsPagePointsAtTheSpec() async throws {
        try await withClient(Fixture.server()) { client in
            let response = try await client.execute(uri: "/docs", method: .get)
            XCTAssertEqual(response.status, .ok)
            XCTAssertTrue(response.headers[.contentType]?.hasPrefix("text/html") == true)
            let html = String(buffer: response.body)
            XCTAssertTrue(html.contains("url: \"/openapi.json\""))
            XCTAssertTrue(html.contains("https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.17.14/swagger-ui.css"))
            XCTAssertTrue(html.contains("https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.17.14/swagger-ui-bundle.js"))
            XCTAssertTrue(html.contains("persistAuthorization: true"))
        }
    }

    // MARK: Helpers

    private func withClient(
        _ server: BridgeServer, _ test: @escaping @Sendable (any TestClientProtocol) async throws -> Void
    ) async throws {
        try await Application(router: server.makeRouter()).test(.router, test)
    }

    /// A real server on a free port, for what the in-memory client can't do: read a response that
    /// never ends.
    private func withLiveServer(
        _ server: BridgeServer, _ test: @escaping @Sendable (Int) async throws -> Void
    ) async throws {
        // The live framework binds localhost on a free port itself.
        try await Application(router: server.makeRouter()).test(.live) { client in try await test(try XCTUnwrap(client.port)) }
    }

    /// Opens a stream with the key. The idle timeout turns a stream that never delivers into a
    /// failure rather than a hung test.
    static func openStream(port: Int, uri: String) async throws -> (HTTPURLResponse, URLSession.AsyncBytes) {
        var request = URLRequest(url: URL(string: "http://localhost:\(port)\(uri)")!)
        request.setValue("Bearer \(Fixture.apiKey)", forHTTPHeaderField: "Authorization")
        request.timeoutInterval = 5
        let (bytes, response) = try await URLSession(configuration: .ephemeral).bytes(for: request)
        return (try XCTUnwrap(response as? HTTPURLResponse), bytes)
    }

    /// Polls, for what settles on another task.
    static func eventually(_ condition: @escaping @Sendable () -> Bool, within seconds: Double = 3) async throws -> Bool {
        let deadline = Date().addingTimeInterval(seconds)
        while Date() < deadline {
            if condition() { return true }
            try await Task.sleep(for: .milliseconds(20))
        }
        return condition()
    }

    /// Calls `operation` on a fresh server and checks it answers its documented 2xx, with a body
    /// that validates against the schema.
    private func expectSuccess(
        _ operation: Operation, query: [String: String], body: Any?, _ validator: SchemaValidator
    ) async throws {
        let uri = Self.uri(operation.examplePath, query)
        let headers: HTTPFields = operation.secured ? [.authorization: "Bearer \(Fixture.apiKey)"] : [:]
        let (status, documented) = try XCTUnwrap(operation.success, operation.name)
        let content = try XCTUnwrap(documented, "\(operation.name): \(status) documents no body")
        XCTAssertEqual(content.count, 1, "\(operation.name): one media type per response")
        let (mediaType, media) = try XCTUnwrap(content.first)
        let schema = try XCTUnwrap((media as? [String: Any])?["schema"] as? [String: Any])

        if mediaType == "text/event-stream" {
            // It never ends by itself: check what arrives first, then go away as a client would.
            try await withLiveServer(Fixture.server()) { port in
                let (response, bytes) = try await Self.openStream(port: port, uri: uri)
                defer { bytes.task.cancel() }
                XCTAssertEqual(response.statusCode, status, "\(operation.name) \(uri)")
                XCTAssertTrue(response.value(forHTTPHeaderField: "Content-Type")?.hasPrefix(mediaType) == true, uri)
                let first = try await bytes.lines.first { _ in true }
                XCTAssertEqual(validator.errors(first ?? "", against: schema), [], "\(operation.name) \(uri)")
            }
            return
        }

        let request = try body.map(Self.buffer)
        try await withClient(Fixture.server()) { client in
            let response = try await client.execute(
                uri: uri, method: operation.httpMethod, headers: headers, body: request)
            XCTAssertEqual(Int(response.status.code), status, "\(operation.name) \(uri): \(String(buffer: response.body))")
            guard Int(response.status.code) == status else { return }

            XCTAssertTrue(response.headers[.contentType]?.hasPrefix(mediaType) == true,
                          "\(uri): Content-Type \(response.headers[.contentType] ?? "none"), documented \(mediaType)")

            let body: Any = mediaType == "application/json"
                ? try JSONSerialization.jsonObject(with: Data(response.body.readableBytesView), options: .fragmentsAllowed)
                : String(buffer: response.body)
            XCTAssertEqual(validator.errors(body, against: schema), [], "\(operation.name) \(uri)")
        }
    }

    /// Sends a request with the key, asserts `status`, and returns the JSON body.
    private func json(
        _ client: any TestClientProtocol, _ method: String, _ path: String, query: [String: String] = [:],
        status: HTTPResponse.Status = .ok, body: Any? = nil, key: String = Fixture.apiKey
    ) async throws -> [String: Any] {
        let response = try await client.execute(
            uri: Self.uri(path, query), method: HTTPRequest.Method(method)!,
            headers: [.authorization: "Bearer \(key)"], body: try body.map(Self.buffer))
        XCTAssertEqual(response.status, status, "\(method) \(path): \(String(buffer: response.body))")
        return try XCTUnwrap(JSONSerialization.jsonObject(with: Data(response.body.readableBytesView)) as? [String: Any])
    }

    /// Asserts `status` is documented for the operation, with the body `Error` describes. `path` is the
    /// spec's, with `{name}`s filled from `pathValues`.
    private func expectError(
        _ status: HTTPResponse.Status, _ method: String, _ path: String, path pathValues: [String: String] = [:],
        query: [String: String] = [:], body: Any? = nil, key: String = Fixture.apiKey,
        _ client: any TestClientProtocol
    ) async throws {
        let operation = try XCTUnwrap(operations.first { $0.path == path && $0.method == method.lowercased() },
                                      "\(method) \(path)")
        if status != .badRequest, let body, let schema = operation.requestBody?.schema {
            XCTAssertEqual(SchemaValidator(spec: spec).errors(body, against: schema), [],
                           "\(operation.name): a body meant to fail with \(status.code) breaks the schema instead")
        }
        let filled = pathValues.reduce(path) { $0.replacingOccurrences(of: "{\($1.key)}", with: $1.value) }
        let uri = Self.uri(filled, query)
        let response = try await client.execute(
            uri: uri, method: operation.httpMethod, headers: [.authorization: "Bearer \(key)"],
            body: try body.map(Self.buffer))
        XCTAssertEqual(response.status, status, "\(method) \(uri): \(String(buffer: response.body))")
        provoked[operation.name, default: []].insert(Int(response.status.code))

        let documented = try XCTUnwrap(operation.responses[String(response.status.code)] as? [String: Any],
                                       "\(uri) answered \(response.status.code), which \(operation.name) does not document")
        let schema = try XCTUnwrap(
            ((documented["content"] as? [String: Any])?["application/json"] as? [String: Any])?["schema"]
                as? [String: Any], "\(operation.name) \(response.status.code) documents no JSON body")
        XCTAssertEqual(schema["$ref"] as? String, "#/components/schemas/Error", uri)
        let validator = SchemaValidator(spec: spec)
        let errorBody = try JSONSerialization.jsonObject(with: Data(response.body.readableBytesView))
        XCTAssertEqual(validator.errors(errorBody, against: schema), [], uri)
    }

    static func uri(_ path: String, _ query: [String: String]) -> String {
        guard !query.isEmpty else { return path }
        // Strict: note IDs carry ':' and '/', which must arrive intact.
        let allowed = CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "-._~"))
        let pairs = query.sorted { $0.key < $1.key }.map {
            "\($0.key)=\($0.value.addingPercentEncoding(withAllowedCharacters: allowed)!)"
        }
        return "\(path)?\(pairs.joined(separator: "&"))"
    }

    /// A JSON body; `Data` goes out as it is, to send something that is not JSON.
    static func buffer(_ body: Any) throws -> ByteBuffer {
        let data = try (body as? Data) ?? JSONSerialization.data(withJSONObject: body, options: .fragmentsAllowed)
        return ByteBuffer(bytes: data)
    }

    static func with(_ base: [String: Any], _ changes: [String: Any]) -> [String: Any] {
        base.merging(changes) { $1 }
    }

    static func string(_ example: Any) -> String {
        (example as? String) ?? "\(example)"
    }

    static func item(_ items: Any?, _ id: String) -> [String: Any]? {
        (items as? [[String: Any]])?.first { $0["id"] as? String == id }
    }

    static func writable(_ items: [[String: Any]]?, key: String) -> [String: Bool] {
        Dictionary(uniqueKeysWithValues: (items ?? []).map { ($0[key] as! String, $0["writable"] as! Bool) })
    }
}
