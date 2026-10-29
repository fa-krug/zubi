import SwiftUI
import ZubiAPI

struct KeysPane: View {
    @EnvironmentObject private var controller: BridgeController
    @Environment(\.openURL) private var openURL

    @State private var sheet: KeySheet?
    @State private var renaming: StoredKey?
    @State private var newName = ""
    @State private var deleting: StoredKey?
    @State private var regenerating: StoredKey?

    var body: some View {
        Form {
            Section {
                if let error = controller.keyError {
                    LabeledContent {
                        Button("Try Again") { controller.reloadKeys() }
                    } label: {
                        Label {
                            Text("The keys could not be read or saved")
                            Text(error)
                        } icon: {
                            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.red)
                        }
                    }
                } else if controller.keys.isEmpty {
                    Text("No keys. Every request is refused until you add one.")
                        .foregroundStyle(.secondary)
                }
                ForEach(controller.keys) { key in
                    row(key)
                }
            } header: {
                Text("Keys")
            } footer: {
                HStack(alignment: .top) {
                    Text("Give each device its own key, so you can tell them apart in Activity and revoke "
                         + "one without the others. A Read key never changes anything; Read & Write "
                         + "writes only where a list, calendar or folder allows it. Changes apply at once.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer()
                    Button("Add Key…") { sheet = .add }
                }
            }

            Section {
                if let key = controller.testKey {
                    Text(controller.curlCommand(for: key))
                        .font(.caption.monospaced())
                        .textSelection(.enabled)
                }
                HStack {
                    Spacer()
                    Button("Open API Docs") {
                        if let url = controller.docsURL { openURL(url) }
                    }
                    .disabled(!controller.isRunning || controller.docsURL == nil)
                    if let key = controller.testKey {
                        Button("Copy Command") { controller.copyToPasteboard(controller.curlCommand(for: key)) }
                    }
                }
            } header: {
                Text("Test the API")
            } footer: {
                Text(testFooter)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .sheet(item: $sheet) { sheet in
            KeySheetView(sheet: sheet)
                .environmentObject(controller)
        }
        .alert("Rename Key", isPresented: isPresented($renaming)) {
            TextField("Name", text: $newName)
            Button("Rename") {
                if let key = renaming { controller.renameKey(key.id, to: newName) }
            }
            Button("Cancel", role: .cancel) {}
        }
        .confirmationDialog(
            "Delete “\(deleting?.name ?? "")”?", isPresented: isPresented($deleting), presenting: deleting
        ) { key in
            Button("Delete", role: .destructive) { controller.deleteKey(key.id) }
        } message: { _ in
            Text("Every device using it is refused from then on. This can't be undone.")
        }
        .confirmationDialog(
            "Regenerate “\(regenerating?.name ?? "")”?", isPresented: isPresented($regenerating),
            presenting: regenerating
        ) { key in
            Button("Regenerate") {
                if controller.regenerateKey(key.id) != nil { sheet = .reveal(key.id) }
            }
        } message: { _ in
            Text("The current secret stops working at once, and every device using it needs the new one.")
        }
    }

    private var testFooter: String {
        guard let key = controller.testKey else { return "Add a key to get a command to try." }
        return "The command uses “\(key.name)”. The docs page lists every endpoint and can call them from "
            + "the browser: paste a key under Authorize."
    }

    private func row(_ key: StoredKey) -> some View {
        LabeledContent {
            HStack {
                Picker("Access", selection: Binding(
                    get: { key.access },
                    set: { controller.setAccess($0, of: key.id) }
                )) {
                    ForEach(KeyAccess.allCases, id: \.self) { access in
                        Text(access.label).tag(access)
                    }
                }
                .labelsHidden()
                .pickerStyle(.menu)
                .fixedSize()

                Menu {
                    Button("Copy Key") { controller.copyToPasteboard(key.secret) }
                    Button("Copy curl Command") { controller.copyToPasteboard(controller.curlCommand(for: key)) }
                    feedMenu(key)
                    Divider()
                    Button("Rename…") {
                        newName = key.name
                        renaming = key
                    }
                    Button("Regenerate Secret…") { regenerating = key }
                    Divider()
                    Button("Delete…", role: .destructive) { deleting = key }
                } label: {
                    Image(systemName: "ellipsis.circle")
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("Copy, rename, regenerate or delete this key")
            }
        } label: {
            Label {
                Text(key.name)
                Text(detail(key))
            } icon: {
                Image(systemName: key.access == .readWrite ? "key.fill" : "key")
                    .foregroundStyle(.purple)
            }
        }
    }

    /// Feed URLs only for a Read key: the API refuses one that can write in a URL, which ends up in
    /// logs and sync settings.
    @ViewBuilder
    private func feedMenu(_ key: StoredKey) -> some View {
        if key.access != .read {
            Button("Calendar Feeds Need a Read Key") {}
                .disabled(true)
        } else if controller.sharedCalendars.isEmpty {
            Button("No Shared Calendar to Subscribe To") {}
                .disabled(true)
        } else {
            Menu("Copy Calendar Feed URL") {
                ForEach(controller.sharedCalendars) { calendar in
                    Button(calendar.title) {
                        controller.copyToPasteboard(controller.feedURL(for: key, calendar: calendar))
                    }
                }
            }
        }
    }

    private func detail(_ key: StoredKey) -> String {
        let created = "Created \(key.created.formatted(date: .abbreviated, time: .omitted))"
        guard let used = controller.keyLastUsed[key.id] else { return "\(created) · never used" }
        return "\(created) · last used \(used.formatted(.relative(presentation: .named)))"
    }

    private func isPresented(_ item: Binding<StoredKey?>) -> Binding<Bool> {
        Binding(get: { item.wrappedValue != nil }, set: { if !$0 { item.wrappedValue = nil } })
    }
}

/// What the key sheet shows: the form for a new key, or a key's secret right after it was made.
enum KeySheet: Identifiable {
    case add
    case reveal(String)

    var id: String {
        switch self {
        case .add: "add"
        case .reveal(let id): id
        }
    }
}

/// One sheet for both steps, because macOS won't present a second sheet while the first one is
/// still closing: adding a key turns this one into the reveal.
private struct KeySheetView: View {
    @EnvironmentObject private var controller: BridgeController
    @Environment(\.dismiss) private var dismiss

    @State var sheet: KeySheet
    @State private var name = ""
    @State private var access: KeyAccess = .read

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            switch sheet {
            case .add: form
            case .reveal(let id): reveal(controller.keys.first { $0.id == id })
            }
        }
        .padding(20)
        .frame(width: 480)
    }

    private var form: some View {
        Group {
            Text("New Key").font(.headline)
            Form {
                TextField("Name", text: $name, prompt: Text("Kitchen Pi"))
                Picker("Access", selection: $access) {
                    ForEach(KeyAccess.allCases, id: \.self) { access in
                        Text(access.label).tag(access)
                    }
                }
            }
            Text("Name it after the device that will use it. Read is enough for a display, and a calendar app "
                 + "subscribing to a feed needs Read: a feed URL refuses a key that can write.")
                .font(.caption)
                .foregroundStyle(.secondary)
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Add Key") {
                    if let key = controller.addKey(name: name, access: access) { sheet = .reveal(key.id) }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
    }

    @ViewBuilder
    private func reveal(_ key: StoredKey?) -> some View {
        if let key {
            Text("“\(key.name)”").font(.headline)
            Text(key.access == .read
                 ? "Give this key to the device. Clients send it as an Authorization: Bearer header; a calendar "
                   + "app can take it in a feed URL instead (Copy Calendar Feed URL in the key's menu)."
                 : "Give this key to the device. Clients send it as an Authorization: Bearer header. For a "
                   + "calendar app, add a Read key: a feed URL refuses a key that can write.")
                .font(.callout)
                .fixedSize(horizontal: false, vertical: true)
            secretRow(key.secret, button: "Copy Key")
            secretRow(controller.curlCommand(for: key), button: "Copy Command")
        } else {
            Text("The key could not be saved.").font(.headline)
        }
        HStack {
            Spacer()
            Button("Done") { dismiss() }
                .keyboardShortcut(.defaultAction)
        }
    }

    private func secretRow(_ text: String, button: String) -> some View {
        HStack(alignment: .top) {
            Text(text)
                .font(.callout.monospaced())
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
                .fixedSize(horizontal: false, vertical: true)
            Button(button) { controller.copyToPasteboard(text) }
        }
        .padding(10)
        .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 8))
    }
}
