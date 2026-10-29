import Foundation
import Hummingbird

struct BridgeServer: Sendable {
    let config: ServerConfig
    let reminders: RemindersService
    let calendar: CalendarService
    let notes: NotesService
    let onRequest: @Sendable (String) -> Void

    func run() async throws {
        let router = Router()

        // Unauthenticated liveness check
        router.get("health") { _, _ -> String in "ok" }

        let api = router.group("api")
            .add(middleware: APIKeyMiddleware<BasicRequestContext>(apiKey: config.apiKey, onRequest: onRequest))

        ReminderRoutes.register(on: api, service: reminders)
        CalendarRoutes.register(on: api, service: calendar)
        NotesRoutes.register(on: api, service: notes)

        let app = Application(
            router: router,
            configuration: .init(
                address: .hostname(config.host, port: config.port),
                serverName: "Zubi"
            )
        )
        // No signal handlers: the SwiftUI app owns the lifecycle and stops us via task cancellation.
        try await app.runService(gracefulShutdownSignals: [])
    }
}
