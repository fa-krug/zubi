import Foundation
import ZubiAPI

/// Apple Notes has no public API, so this goes through Notes.app scripting.
/// Locked notes return metadata only, and are never changed.
actor NotesService: NotesSource {
    func folders() async throws -> [NoteFolderDTO] {
        try decode(await JXARunner.run(Scripts.folders))
    }

    func notes(inFolder folderID: String, limit: Int, matching query: String?) async throws -> [NoteSummaryDTO] {
        try decode(await JXARunner.run(Scripts.notesInFolder, args: [folderID, String(limit), query ?? ""]))
    }

    func note(id: String) async throws -> NoteDTO {
        try decode(await JXARunner.run(Scripts.note, args: [id]))
    }

    func createNote(_ draft: NoteDraft) async throws -> NoteDTO {
        let html = NoteHTML.body(title: draft.title, draft.body)
        return try decode(await JXARunner.run(Scripts.createNote, args: [draft.folder, html]))
    }

    func updateNote(id: String, _ changes: NoteChanges) async throws -> NoteDTO {
        var html = ""
        if let body = changes.body {
            // The title is the body's first line, so a new body takes the current title along
            // unless a new one came with it.
            let title: String
            if let new = changes.title { title = new } else { title = try await note(id: id).title }
            html = NoteHTML.body(title: title, body)
        }
        // An empty argument leaves that part alone; a sent title is never empty. The last argument is
        // the title line alone, which replaces the first line when only the title changes.
        let titleLine = changes.title.map { NoteHTML.body(title: $0, nil) } ?? ""
        return try decode(await JXARunner.run(
            Scripts.updateNote, args: [id, changes.title ?? "", html, titleLine]))
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
    /// Depth first, so every folder follows its parent. `account.folders` may or may not list nested
    /// folders itself, so children are read from each folder and a folder is only emitted once.
    static let folders = """
    function run(argv) {
      const Notes = Application('Notes');
      const out = [];
      const seen = {};
      Notes.accounts().forEach(account => {
        const accountName = account.name();
        const ids = account.folders.id();
        const names = account.folders.name();
        const nameOf = {};
        const children = {};
        const nested = {};
        ids.forEach((id, i) => {
          nameOf[id] = names[i];
          children[id] = Notes.folders.byId(id).folders.id();
          children[id].forEach(child => { nested[child] = true; });
        });
        function walk(id, parentID) {
          if (seen[id]) return;
          seen[id] = true;
          const folder = Notes.folders.byId(id);
          const name = nameOf[id] !== undefined ? nameOf[id] : folder.name();
          // Notes in the trash cannot be edited, and scripting has no flag for that folder, so it is
          // known by its name; in another language a write there fails with 503 instead of 403.
          out.push({ id: id, name: name, account: accountName, parentID: parentID,
                     writable: name !== 'Recently Deleted' });
          (children[id] || folder.folders.id()).forEach(child => walk(child, id));
        }
        ids.filter(id => !nested[id]).forEach(id => walk(id, null));
      });
      return JSON.stringify(out);
    }
    """

    /// argv: folder ID, limit, search text or "". The search lowercases both sides itself rather than
    /// trusting `whose` to ignore case; a title match needs no text, and a locked note's text is never
    /// read, so it can only match by its title, which every listing shows anyway.
    static let notesInFolder = """
    function run(argv) {
      const Notes = Application('Notes');
      const folder = Notes.folders.byId(argv[0]);
      const query = argv[2].toLowerCase();
      const ids = folder.notes.id();
      const names = folder.notes.name();
      const modified = folder.notes.modificationDate();
      const locked = folder.notes.passwordProtected();
      let keep = ids.map(() => true);
      if (query !== '') {
        keep = names.map(name => String(name).toLowerCase().includes(query));
        if (keep.some((found, i) => !found && !locked[i])) {
          // One Apple Event for every note's text; one per note only if Notes refuses that.
          let texts = null;
          try { texts = folder.notes.plaintext(); } catch (e) { texts = null; }
          ids.forEach((id, i) => {
            if (keep[i] || locked[i]) return;
            const text = texts ? texts[i] : Notes.notes.byId(id).plaintext();
            keep[i] = String(text).toLowerCase().includes(query);
          });
        }
      }
      const out = [];
      ids.forEach((id, i) => {
        if (keep[i]) out.push({ id: id, title: names[i], modified: modified[i], locked: locked[i] });
      });
      out.sort((a, b) => b.modified - a.modified);
      return JSON.stringify(out.slice(0, parseInt(argv[1], 10)));
    }
    """

    /// A note as `NoteDTO` decodes it. Shared, so a write answers exactly what a read would.
    private static let describeNote = """
    function describe(note) {
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

    static let note = describeNote + """
    function run(argv) {
      return describe(Application('Notes').notes.byId(argv[0]));
    }
    """

    /// argv: folder ID, body HTML with the title as its first line.
    static let createNote = describeNote + """
    function run(argv) {
      const Notes = Application('Notes');
      const folder = Notes.folders.byId(argv[0]);
      const note = Notes.make({ new: 'note', at: folder, withProperties: { body: argv[1] } });
      return describe(note);
    }
    """

    /// argv: note ID, new title or "", new body HTML (which carries the title) or "", and the title line
    /// alone (used when only the title changes).
    /// The API refuses locked notes before this runs; checking again covers one locked in between.
    /// The marker is `NotesError.lockedMarker`, written out so no Swift value enters the source.
    static let updateNote = describeNote + """
    function run(argv) {
      const Notes = Application('Notes');
      const note = Notes.notes.byId(argv[0]);
      if (note.passwordProtected()) throw new Error('ZUBI_LOCKED');
      if (argv[2] !== '') note.body = argv[2];
      // Notes shows the body's first line as the title, so a rename rewrites that line; setting
      // only note.name would leave the old title on screen.
      else if (argv[1] !== '') {
        const body = note.body();
        const end = body.indexOf('</div>');
        note.body = argv[3] + (end >= 0 ? body.slice(end + '</div>'.length) : body);
      }
      return describe(note);
    }
    """
}
