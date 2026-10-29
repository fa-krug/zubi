import SwiftUI

struct NotesPane: View {
    @EnvironmentObject private var controller: BridgeController

    private static let indent: CGFloat = 18

    var body: some View {
        Form {
            Section {
                AccessRow(
                    title: "Notes",
                    granted: controller.notesConnected,
                    denied: notesError != nil,
                    detail: notesError,
                    grant: { await controller.connectNotes() },
                    openSettings: { controller.openPrivacySettings("Privacy_Automation") }
                )
                if controller.notesState == .connecting {
                    ProgressView().controlSize(.small)
                }
            } footer: {
                Text("Notes has no public API. Zubi reads it through Notes.app scripting, and locked "
                     + "notes stay private.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            if controller.notesConnected {
                if controller.noteFolders.isEmpty {
                    Section { Text("No folders found.").foregroundStyle(.secondary) }
                }
                ForEach(groupedByAccount(controller.noteFolders, account: \.account), id: \.account) { group in
                    Section(group.account) {
                        ForEach(group.items) { folder in
                            ExposureToggle(id: folder.id, title: folder.name, selection: $controller.exposedNoteFolders) {
                                Image(systemName: "folder.fill")
                                    .foregroundStyle(.yellow)
                            }
                            .padding(.leading, CGFloat(depth(of: folder)) * Self.indent)
                        }
                    }
                }
                ReloadFooter()
            }
        }
        .formStyle(.grouped)
    }

    /// Folders arrive depth first, so a parent is always already listed; the cap stops a cycle.
    private func depth(of folder: NoteFolderDTO) -> Int {
        let parents = Dictionary(controller.noteFolders.map { ($0.id, $0.parentID) }, uniquingKeysWith: { a, _ in a })
        var depth = 0
        var next = folder.parentID
        while let id = next, depth < 16 {
            depth += 1
            next = parents[id] ?? nil
        }
        return depth
    }

    private var notesError: String? {
        if case .failed(let message) = controller.notesState { return message }
        return nil
    }
}
