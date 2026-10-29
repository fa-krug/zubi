import Foundation
import Hummingbird

enum CalendarRoutes {
    static func register(
        on api: RouterGroup<ZubiRequestContext>,
        feed: RouterGroup<ZubiRequestContext>,
        service: any CalendarSource,
        isAuthorized: @escaping @Sendable () -> Bool,
        exposedIDs: @escaping @Sendable () -> Set<String>,
        writableIDs: @escaping @Sendable () -> Set<String>,
        writes: WriteHooks,
        now: @escaping @Sendable () -> Date
    ) {
        // GET /api/calendars
        api.get("calendars") { _, context -> CalendarsResponse in
            try requireAccess(isAuthorized)
            let exposed = exposedIDs()
            let writable = writableIDs()
            let calendars = await service.calendars().filter { exposed.contains($0.id) }.map {
                var calendar = $0
                calendar.writable = calendar.writable && writable.contains(calendar.id) && context.keyCanWrite
                return calendar
            }
            return CalendarsResponse(calendars: calendars)
        }

        // GET /api/calendars/{id}/events.ics?from=YYYY-MM-DD&to=YYYY-MM-DD
        // Defaults to 30 days back and a year ahead. The only route that takes `?key=`.
        feed.get("calendars/:id/events.ics") { request, context -> Response in
            try requireAccess(isAuthorized)
            let calendar = try await exposedCalendar(try pathID(context), service, exposedIDs())
            let query = request.query
            let today = Calendar.current.startOfDay(for: now())
            let range = try dayRange(
                from: query.get("from"), to: query.get("to"),
                defaultFrom: Calendar.current.date(byAdding: .day, value: -30, to: today)!,
                defaultTo: Calendar.current.date(byAdding: .year, value: 1, to: today)!,
                maxDays: 2 * 366, limit: "two years")
            let events = await service.events(in: [calendar.id], from: range.from, to: range.to)
            let body = ICalendar.document(name: calendar.title, events: events, stamp: now())
            return Response(
                status: .ok,
                headers: [.contentType: "text/calendar; charset=utf-8"],
                body: ResponseBody(byteBuffer: ByteBuffer(string: body)))
        }

        // GET /api/events?from=YYYY-MM-DD&to=YYYY-MM-DD&calendar=<id or title>&q=<text>
        // Defaults to today plus the next 6 days. "to" is inclusive.
        api.get("events") { request, _ -> EventsResponse in
            try requireAccess(isAuthorized)
            let query = request.query
            let exposed = exposedIDs()
            var calendarIDs = exposed

            if let name = query.get("calendar") {
                calendarIDs = [try await exposedCalendar(name, service, exposed).id]
            }

            let today = Calendar.current.startOfDay(for: now())
            let range = try dayRange(
                from: query.get("from"), to: query.get("to"), defaultFrom: today,
                defaultTo: Calendar.current.date(byAdding: .day, value: 6, to: today)!,
                maxDays: 366, limit: "one year")

            var events = await service.events(in: calendarIDs, from: range.from, to: range.to)
            if let q = searchQuery(request) {
                events = events.filter { matches(q, $0.title, $0.notes) }
            }
            return EventsResponse(events: events)
        }

        // POST /api/events
        api.post("events") { request, context -> EditedResponse<EventDTO> in
            try requireAccess(isAuthorized)
            var draft = try await decodeBody(EventDraft.self, from: request, context: context)
            let calendar = try await exposedCalendar(draft.calendar, service, exposedIDs())
            try requireWritable(calendar, writableIDs())
            draft.calendar = calendar.id
            let event = try await mapSourceErrors { try await service.createEvent(draft) }
            writes.record(.created, .calendar, title: event.title, container: event.calendarTitle, context)
            return EditedResponse(status: .created, response: event)
        }

        // PATCH /api/events/{id}?occurrence=<start as /api/events returned it>
        api.patch("events/:id") { request, context -> EventDTO in
            try requireAccess(isAuthorized)
            let id = try pathID(context)
            let occurrence = try request.query.get("occurrence").map {
                guard let date = DateFormat.parseRequestDate($0) else {
                    throw HTTPError(.badRequest,
                                    message: "occurrence must be a start as /api/events returns it")
                }
                return date
            }
            var changes = try await decodeBody(EventChanges.self, from: request, context: context)
            let exposed = exposedIDs()
            let writable = writableIDs()

            guard let found = await service.event(id: id, occurrence: occurrence) else {
                throw HTTPError(.notFound, message: "Unknown or hidden event")
            }
            // By ID: an event in a hidden calendar must look exactly like no event at all.
            let calendar = try await exposedCalendar(found.event.calendarID, service, exposed, byTitle: false,
                                                     notFound: "Unknown or hidden event")
            try requireWritable(calendar, writable)
            if found.recurs && occurrence == nil {
                throw HTTPError(.badRequest,
                                message: "The event recurs: pass the occurrence's start as occurrence")
            }
            if let name = changes.calendar {
                let target = try await exposedCalendar(name, service, exposed)
                try requireWritable(target, writable)
                changes.calendar = target.id
            }
            try checkTiming(changes, against: found.event)
            let event = try await mapSourceErrors {
                try await service.updateEvent(id: id, occurrence: occurrence, changes)
            }
            writes.record(.edited, .calendar, title: event.title, container: event.calendarTitle, context)
            return event
        }
    }

    /// `from` up to the start of the day after `to`, both `YYYY-MM-DD` in the Mac's time zone.
    private static func dayRange(
        from: String?, to: String?, defaultFrom: Date, defaultTo: Date, maxDays: Int, limit: String
    ) throws -> (from: Date, to: Date) {
        let start = try from.map(DateFormat.parseDay) ?? defaultFrom
        let lastDay = try to.map(DateFormat.parseDay) ?? defaultTo
        let end = Calendar.current.date(byAdding: .day, value: 1, to: lastDay)!
        guard end > start else {
            throw HTTPError(.badRequest, message: "'to' must not be before 'from'")
        }
        guard end.timeIntervalSince(start) <= Double(maxDays) * 24 * 3600 else {
            throw HTTPError(.badRequest, message: "Range is limited to \(limit)")
        }
        return (start, end)
    }

    /// The event as it would be after `changes`, which may move only one end, checked like a new one.
    private static func checkTiming(_ changes: EventChanges, against event: EventDTO) throws {
        guard let currentStart = DateFormat.parseRequestDate(event.start),
              let currentEnd = DateFormat.parseRequestDate(event.end)
        else { return }
        do {
            try EventTiming.check(
                start: changes.start ?? currentStart, end: changes.end ?? currentEnd,
                isAllDay: changes.isAllDay ?? event.isAllDay,
                sent: [changes.start, changes.end].compactMap { $0 })
        } catch let error as BodyError {
            throw HTTPError(.badRequest, message: error.message)
        }
    }

    private static func exposedCalendar(
        _ name: String, _ service: any CalendarSource, _ exposed: Set<String>, byTitle: Bool = true,
        notFound: String = "Unknown or hidden calendar"
    ) async throws -> CalendarDTO {
        let match = await service.calendars().first {
            ($0.id == name || (byTitle && $0.title == name)) && exposed.contains($0.id)
        }
        guard let match else { throw HTTPError(.notFound, message: notFound) }
        return match
    }

    private static func requireWritable(_ calendar: CalendarDTO, _ writable: Set<String>) throws {
        try ZubiAPI.requireWritable(calendar.id, sourceAllows: calendar.writable, writableIDs: writable,
                                    noun: "Calendar", source: "the Calendar app")
    }

    private static func requireAccess(_ isAuthorized: () -> Bool) throws {
        guard isAuthorized() else {
            throw HTTPError(.forbidden, message: "Calendar access not granted on the Mac")
        }
    }
}
