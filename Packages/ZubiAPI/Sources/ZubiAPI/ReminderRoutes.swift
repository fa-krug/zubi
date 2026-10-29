import Hummingbird

enum ReminderRoutes {
    static func register(
        on api: RouterGroup<ZubiRequestContext>,
        service: any RemindersSource,
        isAuthorized: @escaping @Sendable () -> Bool,
        exposedIDs: @escaping @Sendable () -> Set<String>,
        writableIDs: @escaping @Sendable () -> Set<String>,
        writes: WriteHooks
    ) {
        // GET /api/lists
        api.get("lists") { _, context -> ListsResponse in
            try requireAccess(isAuthorized)
            let exposed = exposedIDs()
            let writable = writableIDs()
            let lists = await service.lists().filter { exposed.contains($0.id) }.map {
                var list = $0
                list.writable = list.writable && writable.contains(list.id) && context.keyCanWrite
                return list
            }
            return ListsResponse(lists: lists)
        }

        // GET /api/reminders?list=<id or title>&due=today|overdue&completed=true&q=<text>
        api.get("reminders") { request, _ -> RemindersResponse in
            try requireAccess(isAuthorized)
            let query = request.query
            let exposed = exposedIDs()
            var listIDs = exposed

            if let list = query.get("list") {
                listIDs = [try await exposedList(list, service, exposed).id]
            }

            let filter: ReminderFilter
            switch query.get("due") {
            case nil: filter = .all
            case "today": filter = .dueToday
            case "overdue": filter = .overdue
            default: throw HTTPError(.badRequest, message: "due must be 'today' or 'overdue'")
            }

            var reminders = await service.reminders(
                in: listIDs, filter: filter, includeCompleted: query.get("completed") == "true")
            if let q = searchQuery(request) {
                reminders = reminders.filter { matches(q, $0.title, $0.notes) }
            }
            return RemindersResponse(reminders: reminders)
        }

        // POST /api/reminders
        api.post("reminders") { request, context -> EditedResponse<ReminderDTO> in
            try requireAccess(isAuthorized)
            var draft = try await decodeBody(ReminderDraft.self, from: request, context: context)
            let list = try await exposedList(draft.list, service, exposedIDs())
            try requireWritable(list, writableIDs())
            draft.list = list.id
            let reminder = try await mapSourceErrors { try await service.createReminder(draft) }
            writes.record(.created, .reminders, title: reminder.title, container: reminder.listTitle, context)
            return EditedResponse(status: .created, response: reminder)
        }

        // PATCH /api/reminders/{id}
        api.patch("reminders/:id") { request, context -> ReminderDTO in
            try requireAccess(isAuthorized)
            let id = try pathID(context)
            var changes = try await decodeBody(ReminderChanges.self, from: request, context: context)
            let exposed = exposedIDs()
            let writable = writableIDs()

            guard let current = await service.reminder(id: id) else {
                throw HTTPError(.notFound, message: "Unknown or hidden reminder")
            }
            // Its list, by ID: a reminder in a hidden list must look exactly like no reminder at all.
            let list = try await exposedList(current.listID, service, exposed, byTitle: false,
                                             notFound: "Unknown or hidden reminder")
            try requireWritable(list, writable)
            if let name = changes.list {
                let target = try await exposedList(name, service, exposed)
                try requireWritable(target, writable)
                changes.list = target.id
            }
            let reminder = try await mapSourceErrors { try await service.updateReminder(id: id, changes) }
            writes.record(.edited, .reminders, title: reminder.title, container: reminder.listTitle, context)
            return reminder
        }
    }

    private static func exposedList(
        _ name: String, _ service: any RemindersSource, _ exposed: Set<String>, byTitle: Bool = true,
        notFound: String = "Unknown or hidden list"
    ) async throws -> ReminderListDTO {
        let match = await service.lists().first {
            ($0.id == name || (byTitle && $0.title == name)) && exposed.contains($0.id)
        }
        guard let match else { throw HTTPError(.notFound, message: notFound) }
        return match
    }

    private static func requireWritable(_ list: ReminderListDTO, _ writable: Set<String>) throws {
        try ZubiAPI.requireWritable(list.id, sourceAllows: list.writable, writableIDs: writable,
                                    noun: "List", source: "Reminders")
    }

    private static func requireAccess(_ isAuthorized: () -> Bool) throws {
        guard isAuthorized() else {
            throw HTTPError(.forbidden, message: "Reminders access not granted on the Mac")
        }
    }
}
