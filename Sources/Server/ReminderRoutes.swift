import Hummingbird

enum ReminderRoutes {
    static func register(on api: RouterGroup<BasicRequestContext>, service: RemindersService) {
        // GET /api/lists
        api.get("lists") { _, _ -> ListsResponse in
            try requireAccess()
            let exposed = Exposure.reminderLists.ids()
            let lists = await service.lists().filter { exposed.contains($0.id) }
            return ListsResponse(lists: lists)
        }

        // GET /api/reminders?list=<id or title>&due=today|overdue&completed=true
        api.get("reminders") { request, _ -> RemindersResponse in
            try requireAccess()
            let query = request.uri.queryParameters
            let exposed = Exposure.reminderLists.ids()
            var listIDs = exposed

            if let list = query.get("list") {
                let match = await service.lists().first {
                    ($0.id == list || $0.title == list) && exposed.contains($0.id)
                }
                guard let match else { throw HTTPError(.notFound, message: "Unknown or hidden list") }
                listIDs = [match.id]
            }

            let filter: ReminderFilter
            switch query.get("due") {
            case nil: filter = .all
            case "today": filter = .dueToday
            case "overdue": filter = .overdue
            default: throw HTTPError(.badRequest, message: "due must be 'today' or 'overdue'")
            }

            let reminders = await service.reminders(
                in: listIDs, filter: filter, includeCompleted: query.get("completed") == "true")
            return RemindersResponse(reminders: reminders)
        }
    }

    private static func requireAccess() throws {
        guard RemindersService.authorizationStatus == .fullAccess else {
            throw HTTPError(.forbidden, message: "Reminders access not granted on the Mac")
        }
    }
}
