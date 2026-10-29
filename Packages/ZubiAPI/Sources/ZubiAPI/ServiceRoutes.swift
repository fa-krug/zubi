import Foundation
import Hummingbird

struct KeyInfo: ResponseEncodable {
    let name: String
    let access: KeyAccess
}

enum ServiceRoutes {
    static func register(
        on api: RouterGroup<ZubiRequestContext>, changes: ChangeFeed, heartbeat: Duration
    ) {
        // GET /api/key
        api.get("key") { _, context -> KeyInfo in
            guard let key = context.key else { throw HTTPError(.unauthorized) }
            return KeyInfo(name: key.name, access: key.access)
        }

        // GET /api/changes (server-sent events)
        api.get("changes") { _, _ -> Response in
            Response(
                status: .ok,
                headers: [
                    .contentType: "text/event-stream",
                    .cacheControl: "no-cache",
                    // Without it some clients hold the first bytes back to sniff the type.
                    HTTPFields.Element.Name("X-Content-Type-Options")!: "nosniff",
                ],
                body: ResponseBody { writer in
                    // Subscribed only once the body is written, so a response that is never sent
                    // leaves nothing behind.
                    let subscription = changes.subscribe(heartbeat: heartbeat)
                    defer { subscription.cancel() }
                    // At once, so the client knows the stream is open before anything changes.
                    try await writer.write(ByteBuffer(string: ": connected\n\n"))
                    // A failed write (the client is gone) throws out of the loop, and the defer
                    // unsubscribes; a heartbeat makes sure one is attempted every so often.
                    for await message in subscription.messages {
                        try await writer.write(ByteBuffer(string: ServerSentEvents.encode(message)))
                    }
                    try await writer.finish(nil)
                }
            )
        }
    }
}

enum ServerSentEvents {
    static func encode(_ message: ChangeFeed.Message) -> String {
        switch message {
        case .heartbeat:
            return ": keep-alive\n\n"
        case .change(let change):
            let data = (try? JSONEncoder().encode(change)).map { String(decoding: $0, as: UTF8.self) } ?? "{}"
            return "event: change\ndata: \(data)\n\n"
        }
    }
}
