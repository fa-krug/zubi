import SwiftUI
import ZubiAPI

/// The Connection tab: whether clients can reach this Mac, the code to pair one, and the relay.
struct ServerPane: View {
    @EnvironmentObject private var controller: BridgeController
    @State private var usesOwnRelay = false

    var body: some View {
        Form {
            Section {
                LabeledContent {
                    if !controller.isRunning {
                        Button("Start") { controller.start() }
                    }
                } label: {
                    Label {
                        Text(statusTitle)
                        Text(statusDetail)
                    } icon: {
                        Image(systemName: "circle.fill")
                            .foregroundStyle(statusColor)
                            .imageScale(.small)
                    }
                }
                if nothingShared {
                    Label("Nothing is shared yet: choose lists, calendars and folders under Reminders, Calendars "
                          + "and Notes.", systemImage: "exclamationmark.circle")
                        .foregroundStyle(.orange)
                }
            } header: {
                Text("Status")
            }

            Section {
                if let key = controller.keys.first, let code = controller.pairingCode(for: key) {
                    PairingCodeRow(code: code)
                    Text("On the device: install the client (zubi.fa-krug.de shows how), run “zubi” and paste this "
                         + "code. It is the client “\(key.name)”; give each device its own under Clients.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                } else {
                    Text("The code appears once Zubi is connected.")
                        .foregroundStyle(.secondary)
                }
            } header: {
                Text("Pairing code")
            }

            Section {
                Toggle("Use my own relay", isOn: $usesOwnRelay)
                if usesOwnRelay {
                    TextField("Relay", text: $controller.ownRelay, prompt: Text("wss://relay.example.com"))
                } else {
                    LabeledContent("Relay", value: BridgeController.defaultRelay)
                }
            } header: {
                Text("Relay")
            } footer: {
                HStack(alignment: .top) {
                    Text("Clients reach this Mac through a relay, so nothing here accepts connections from the "
                         + "internet. Each client's connection is encrypted end to end with its own code: the relay "
                         + "can't read it. After switching relays, pair your clients again.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer()
                    Button("Apply") {
                        if !usesOwnRelay { controller.ownRelay = "" }
                        controller.applyRelaySettings()
                    }
                }
            }
        }
        .formStyle(.grouped)
        .onAppear { usesOwnRelay = !controller.ownRelay.isEmpty }
    }

    private var nothingShared: Bool {
        controller.exposedReminderLists.isEmpty && controller.exposedCalendars.isEmpty
            && controller.exposedNoteFolders.isEmpty
    }

    private var statusTitle: String {
        if !controller.isRunning { return "Stopped" }
        switch controller.relayStatus {
        case .connected: return "Connected"
        case .failed: return "Not connected"
        case .connecting, nil: return "Connecting…"
        }
    }

    private var statusDetail: String {
        if !controller.isRunning {
            return controller.lastError ?? "Zubi isn't serving. Click Start."
        }
        let host = URL(string: controller.relayAddress)?.host ?? controller.relayAddress
        switch controller.relayStatus {
        case .connected: return "Clients can reach this Mac through \(host)."
        case .failed(let message): return message
        case .connecting, nil: return "Connecting to \(host)."
        }
    }

    private var statusColor: Color {
        if !controller.isRunning { return .secondary }
        switch controller.relayStatus {
        case .connected: return .green
        case .failed: return .red
        case .connecting, nil: return .orange
        }
    }
}

/// A pairing code, selectable, with a button to copy it.
struct PairingCodeRow: View {
    @EnvironmentObject private var controller: BridgeController
    let code: String

    var body: some View {
        HStack(alignment: .top) {
            Text(code)
                .font(.callout.monospaced())
                .textSelection(.enabled)
                .lineLimit(3)
                .truncationMode(.middle)
                .frame(maxWidth: .infinity, alignment: .leading)
            Button("Copy") { controller.copyToPasteboard(code) }
        }
    }
}
