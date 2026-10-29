import SwiftUI
import ZubiAPI

/// The Clients tab: one pairing code per client device. What a client may change is decided by
/// each list, calendar and folder's setting, the same for every client.
struct KeysPane: View {
    @EnvironmentObject private var controller: BridgeController

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
                            Text("The clients could not be read or saved")
                            Text(error)
                        } icon: {
                            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.red)
                        }
                    }
                } else if controller.keys.isEmpty {
                    Text("No clients. Add one to get its pairing code.")
                        .foregroundStyle(.secondary)
                }
                ForEach(controller.keys) { key in
                    row(key)
                }
            } header: {
                Text("Clients")
            } footer: {
                HStack(alignment: .top) {
                    Text("Give each device its own code, so you can tell them apart in Activity and remove one "
                         + "without the others. A device pairs by running “zubi” and pasting its code.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer()
                    Button("Add Client…") { sheet = .add }
                }
            }
        }
        .formStyle(.grouped)
        .sheet(item: $sheet) { sheet in
            KeySheetView(sheet: sheet)
                .environmentObject(controller)
        }
        .alert("Rename Client", isPresented: isPresented($renaming)) {
            TextField("Name", text: $newName)
            Button("Rename") {
                if let key = renaming { controller.renameKey(key.id, to: newName) }
            }
            Button("Cancel", role: .cancel) {}
        }
        .confirmationDialog(
            "Remove “\(deleting?.name ?? "")”?", isPresented: isPresented($deleting), presenting: deleting
        ) { key in
            Button("Remove", role: .destructive) { controller.deleteKey(key.id) }
        } message: { _ in
            Text("That device can't reach this Mac any more. This can't be undone.")
        }
        .confirmationDialog(
            "New code for “\(regenerating?.name ?? "")”?", isPresented: isPresented($regenerating),
            presenting: regenerating
        ) { key in
            Button("New Code") {
                if controller.regenerateKey(key.id) != nil { sheet = .reveal(key.id) }
            }
        } message: { _ in
            Text("The current code stops working at once, and the device needs to pair again with the new one.")
        }
    }

    private func row(_ key: StoredKey) -> some View {
        LabeledContent {
            HStack {
                if let code = controller.pairingCode(for: key) {
                    Button("Copy Code") { controller.copyToPasteboard(code) }
                }
                Menu {
                    Button("Rename…") {
                        newName = key.name
                        renaming = key
                    }
                    Button("New Code…") { regenerating = key }
                    Divider()
                    Button("Remove…", role: .destructive) { deleting = key }
                } label: {
                    Image(systemName: "ellipsis.circle")
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("Rename, renew or remove this client")
            }
        } label: {
            Label {
                Text(key.name)
                Text(detail(key))
            } icon: {
                Image(systemName: "laptopcomputer.and.iphone")
                    .foregroundStyle(.purple)
            }
        }
    }

    private func detail(_ key: StoredKey) -> String {
        let created = "Added \(key.created.formatted(date: .abbreviated, time: .omitted))"
        guard let used = controller.keyLastUsed[key.id] else { return "\(created) · not connected yet" }
        return "\(created) · last connected \(used.formatted(.relative(presentation: .named)))"
    }

    private func isPresented(_ item: Binding<StoredKey?>) -> Binding<Bool> {
        Binding(get: { item.wrappedValue != nil }, set: { if !$0 { item.wrappedValue = nil } })
    }
}

/// What the client sheet shows: the form for a new client, or a client's code right after it was made.
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
/// still closing: adding a client turns this one into the reveal.
private struct KeySheetView: View {
    @EnvironmentObject private var controller: BridgeController
    @Environment(\.dismiss) private var dismiss

    @State var sheet: KeySheet
    @State private var name = ""

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
            Text("New Client").font(.headline)
            Form {
                TextField("Name", text: $name, prompt: Text("Kitchen Pi"))
            }
            Text("Name it after the device, so you can tell it apart in Activity.")
                .font(.caption)
                .foregroundStyle(.secondary)
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Add") {
                    if let key = controller.addKey(name: name) { sheet = .reveal(key.id) }
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
            if let code = controller.pairingCode(for: key) {
                Text("On the device, run “zubi” and paste this code. It is as secret as a password.")
                    .font(.callout)
                    .fixedSize(horizontal: false, vertical: true)
                PairingCodeRow(code: code)
                    .padding(10)
                    .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 8))
            } else {
                Text("The code appears here and under Clients once Zubi is connected (Settings › Connection).")
                    .font(.callout)
                    .fixedSize(horizontal: false, vertical: true)
            }
        } else {
            Text("The client could not be saved.").font(.headline)
        }
        HStack {
            Spacer()
            Button("Done") { dismiss() }
                .keyboardShortcut(.defaultAction)
        }
    }
}
