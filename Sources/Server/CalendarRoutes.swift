import Foundation
import Hummingbird

enum CalendarRoutes {
    static func register(on api: RouterGroup<BasicRequestContext>, service: CalendarService) {
        // GET /api/calendars
        api.get("calendars") { _, _ -> CalendarsResponse in
            try requireAccess()
            let exposed = Exposure.calendars.ids()
            let calendars = await service.calendars().filter { exposed.contains($0.id) }
            return CalendarsResponse(calendars: calendars)
        }

        // GET /api/events?from=YYYY-MM-DD&to=YYYY-MM-DD&calendar=<id or title>
        // Defaults to today plus the next 6 days. "to" is inclusive.
        api.get("events") { request, _ -> EventsResponse in
            try requireAccess()
            let query = request.uri.queryParameters
            let exposed = Exposure.calendars.ids()
            var calendarIDs = exposed

            if let name = query.get("calendar") {
                let match = await service.calendars().first {
                    ($0.id == name || $0.title == name) && exposed.contains($0.id)
                }
                guard let match else { throw HTTPError(.notFound, message: "Unknown or hidden calendar") }
                calendarIDs = [match.id]
            }

            let calendar = Calendar.current
            let today = calendar.startOfDay(for: Date())
            let from = try query.get("from").map(DateFormat.parseDay) ?? today
            let lastDay = try query.get("to").map(DateFormat.parseDay)
                ?? calendar.date(byAdding: .day, value: 6, to: today)!
            let to = calendar.date(byAdding: .day, value: 1, to: lastDay)!

            guard to > from else {
                throw HTTPError(.badRequest, message: "'to' must not be before 'from'")
            }
            guard to.timeIntervalSince(from) <= 366 * 24 * 3600 else {
                throw HTTPError(.badRequest, message: "Range is limited to one year")
            }

            let events = await service.events(in: calendarIDs, from: from, to: to)
            return EventsResponse(events: events)
        }
    }

    private static func requireAccess() throws {
        guard CalendarService.authorizationStatus == .fullAccess else {
            throw HTTPError(.forbidden, message: "Calendar access not granted on the Mac")
        }
    }
}
