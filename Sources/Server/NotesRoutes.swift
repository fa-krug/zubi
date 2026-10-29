import Hummingbird

enum NotesRoutes {
    static func register(on api: RouterGroup<BasicRequestContext>, service: NotesService) {
        // GET /api/notes/folders
        api.get("notes/folders") { _, _ -> NoteFoldersResponse in
            let folders = try await exposedFolders(service)
            return NoteFoldersResponse(folders: folders)
        }

        // GET /api/notes?folder=<id or name>&limit=50
        // Metadata only, newest first. Fetch content via /api/note.
        api.get("notes") { request, _ -> NoteSummariesResponse in
            let query = request.uri.queryParameters
            let limit = min(max(Int(query.get("limit") ?? "") ?? 50, 1), 500)
            let folders = try await exposedFolders(service)

            var targets = folders
            if let name = query.get("folder") {
                guard let match = folders.first(where: { $0.id == name || $0.name == name }) else {
                    throw HTTPError(.notFound, message: "Unknown or hidden folder")
                }
                targets = [match]
            }

            var notes: [NoteSummaryDTO] = []
            for folder in targets {
                notes += try await mapErrors { try await service.notes(inFolder: folder.id, limit: limit) }
            }
            notes.sort { $0.modified > $1.modified }
            return NoteSummariesResponse(notes: Array(notes.prefix(limit)))
        }

        // GET /api/note?id=<note id>
        // Query parameter because note IDs are x-coredata:// URLs containing slashes.
        api.get("note") { request, _ -> NoteDTO in
            guard let id = request.uri.queryParameters.get("id") else {
                throw HTTPError(.badRequest, message: "Missing id")
            }
            let note = try await mapErrors { try await service.note(id: id) }
            guard Exposure.noteFolders.ids().contains(note.folderID) else {
                throw HTTPError(.notFound, message: "Unknown or hidden note")
            }
            return note
        }
    }

    private static func exposedFolders(_ service: NotesService) async throws -> [NoteFolderDTO] {
        let exposed = Exposure.noteFolders.ids()
        let all = try await mapErrors { try await service.folders() }
        return all.filter { exposed.contains($0.id) }
    }

    private static func mapErrors<T>(_ operation: () async throws -> T) async throws -> T {
        do {
            return try await operation()
        } catch let error as NotesError {
            throw HTTPError(error.isNotFound ? .notFound : .serviceUnavailable, message: error.description)
        }
    }
}
