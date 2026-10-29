import Hummingbird

enum NotesRoutes {
    static func register(
        on api: RouterGroup<ZubiRequestContext>,
        service: any NotesSource,
        exposedIDs: @escaping @Sendable () -> Set<String>,
        writableIDs: @escaping @Sendable () -> Set<String>,
        writes: WriteHooks
    ) {
        // GET /api/notes/folders
        api.get("notes/folders") { _, context -> NoteFoldersResponse in
            let writable = writableIDs()
            let folders = try await exposedFolders(service, exposedIDs()).map {
                var folder = $0
                folder.writable = folder.writable && writable.contains(folder.id) && context.keyCanWrite
                return folder
            }
            return NoteFoldersResponse(folders: folders)
        }

        // GET /api/notes?folder=<id or name>&limit=50&q=<text>
        // Metadata only, newest first. Fetch content via /api/note.
        api.get("notes") { request, _ -> NoteSummariesResponse in
            let query = request.query
            let limit = min(max(Int(query.get("limit") ?? "") ?? 50, 1), 500)
            let q = searchQuery(request)
            let folders = try await exposedFolders(service, exposedIDs())

            var targets = folders
            if let name = query.get("folder") {
                targets = [try folder(name, in: folders)]
            }

            var notes: [NoteSummaryDTO] = []
            for folder in targets {
                notes += try await mapSourceErrors {
                    try await service.notes(inFolder: folder.id, limit: limit, matching: q)
                }
            }
            notes.sort { $0.modified > $1.modified }
            return NoteSummariesResponse(notes: Array(notes.prefix(limit)))
        }

        // GET /api/note?id=<note id>
        // Query parameter because note IDs are x-coredata:// URLs containing slashes.
        api.get("note") { request, _ -> NoteDTO in
            let id = try noteID(request)
            let note = try await mapSourceErrors { try await service.note(id: id) }
            guard exposedIDs().contains(note.folderID) else {
                throw HTTPError(.notFound, message: "Unknown or hidden note")
            }
            return note
        }

        // POST /api/notes
        api.post("notes") { request, context -> EditedResponse<NoteDTO> in
            var draft = try await decodeBody(NoteDraft.self, from: request, context: context)
            let target = try folder(draft.folder, in: try await exposedFolders(service, exposedIDs()))
            try requireWritable(target, writableIDs())
            draft.folder = target.id
            let note = try await mapSourceErrors { try await service.createNote(draft) }
            writes.record(.created, .notes, title: note.title, container: target.name, context)
            return EditedResponse(status: .created, response: note)
        }

        // PATCH /api/note?id=<note id>
        api.patch("note") { request, context -> NoteDTO in
            let id = try noteID(request)
            let changes = try await decodeBody(NoteChanges.self, from: request, context: context)
            let note = try await mapSourceErrors { try await service.note(id: id) }
            let folders = try await exposedFolders(service, exposedIDs())
            guard let home = folders.first(where: { $0.id == note.folderID }) else {
                throw HTTPError(.notFound, message: "Unknown or hidden note")
            }
            try requireWritable(home, writableIDs())
            guard !note.locked else {
                throw HTTPError(.conflict,
                                message: "The note is locked, and Zubi never changes a locked note")
            }
            let updated = try await mapSourceErrors { try await service.updateNote(id: id, changes) }
            writes.record(.edited, .notes, title: updated.title, container: home.name, context)
            return updated
        }
    }

    private static func noteID(_ request: Request) throws -> String {
        guard let id = request.query.get("id") else {
            throw HTTPError(.badRequest, message: "Missing id")
        }
        return id
    }

    private static func exposedFolders(
        _ service: any NotesSource, _ exposed: Set<String>
    ) async throws -> [NoteFolderDTO] {
        let all = try await mapSourceErrors { try await service.folders() }
        return all.filter { exposed.contains($0.id) }
    }

    private static func folder(_ name: String, in folders: [NoteFolderDTO]) throws -> NoteFolderDTO {
        guard let match = folders.first(where: { $0.id == name || $0.name == name }) else {
            throw HTTPError(.notFound, message: "Unknown or hidden folder")
        }
        return match
    }

    private static func requireWritable(_ folder: NoteFolderDTO, _ writable: Set<String>) throws {
        try ZubiAPI.requireWritable(folder.id, sourceAllows: folder.writable, writableIDs: writable,
                                    noun: "Folder", source: "Notes")
    }
}
