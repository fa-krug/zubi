import Foundation

/// Apple Notes has no public API, so this goes through Notes.app scripting.
/// Locked notes return metadata only.
actor NotesService {
    func folders() async throws -> [NoteFolderDTO] {
        try decode(await JXARunner.run(Scripts.folders))
    }

    func notes(inFolder folderID: String, limit: Int) async throws -> [NoteSummaryDTO] {
        try decode(await JXARunner.run(Scripts.notesInFolder, args: [folderID, String(limit)]))
    }

    func note(id: String) async throws -> NoteDTO {
        try decode(await JXARunner.run(Scripts.note, args: [id]))
    }

    private func decode<T: Decodable>(_ data: Data) throws -> T {
        do {
            return try JSONDecoder().decode(T.self, from: data)
        } catch {
            throw NotesError.badOutput
        }
    }
}

/// Bulk property reads (folders.id(), notes.name(), ...) cost one Apple Event each
/// instead of one per item, which keeps large folders fast.
private enum Scripts {
    static let folders = """
    function run(argv) {
      const Notes = Application('Notes');
      const out = [];
      Notes.accounts().forEach(account => {
        const accountName = account.name();
        const ids = account.folders.id();
        const names = account.folders.name();
        ids.forEach((id, i) => out.push({ id: id, name: names[i], account: accountName }));
      });
      return JSON.stringify(out);
    }
    """

    static let notesInFolder = """
    function run(argv) {
      const Notes = Application('Notes');
      const folder = Notes.folders.byId(argv[0]);
      const ids = folder.notes.id();
      const names = folder.notes.name();
      const modified = folder.notes.modificationDate();
      const locked = folder.notes.passwordProtected();
      const out = ids.map((id, i) => ({ id: id, title: names[i], modified: modified[i], locked: locked[i] }));
      out.sort((a, b) => b.modified - a.modified);
      return JSON.stringify(out.slice(0, parseInt(argv[1], 10)));
    }
    """

    static let note = """
    function run(argv) {
      const Notes = Application('Notes');
      const note = Notes.notes.byId(argv[0]);
      const locked = note.passwordProtected();
      return JSON.stringify({
        id: note.id(),
        title: note.name(),
        folderID: note.container().id(),
        created: note.creationDate(),
        modified: note.modificationDate(),
        locked: locked,
        plaintext: locked ? null : note.plaintext(),
        html: locked ? null : note.body()
      });
    }
    """
}
