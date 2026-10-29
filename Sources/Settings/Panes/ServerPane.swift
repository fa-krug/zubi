import SwiftUI
import ZubiAPI

struct ServerPane: View {
    @EnvironmentObject private var controller: BridgeController

    var body: some View {
        Form {
            Section("Status") {
                LabeledContent {
                    Button(controller.isRunning ? "Stop" : "Start") {
                        if controller.isRunning { controller.stop() } else { controller.start() }
                    }
                } label: {
                    Label {
                        Text(controller.isRunning
                             ? "Running on \(controller.host):\(String(controller.port))"
                             : "Stopped")
                        if let error = controller.lastError {
                            Text(error).foregroundStyle(.red)
                        }
                    } icon: {
                        Image(systemName: "circle.fill")
                            .foregroundStyle(controller.isRunning ? Color.green : Color.secondary)
                            .imageScale(.small)
                    }
                }
            }

            Section {
                Picker("Bind to", selection: $controller.bindMode) {
                    ForEach(BindMode.allCases) { mode in
                        Text(mode.label).tag(mode)
                    }
                }
                if controller.bindMode == .custom {
                    TextField("IP address", text: $controller.customHost)
                }
                if controller.bindMode == .allInterfaces {
                    Text("Zubi also announces itself on the local network over Bonjour (\(BonjourAdvertiser.serviceType)), "
                         + "so clients can find it by this Mac's name.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                TextField("Port", value: $controller.port, format: .number.grouping(.never))
            } header: {
                Text("Network")
            } footer: {
                HStack {
                    Text("The API is plain HTTP. Off localhost, keep it on a network you trust.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer()
                    Button("Apply and Restart") { controller.applyNetworkSettings() }
                }
            }

            Section {
                Toggle("Connect through a relay", isOn: $controller.relayEnabled)
                if controller.relayEnabled {
                    TextField("Relay", text: $controller.relayURL, prompt: Text("wss://relay.example.com"))
                    LabeledContent("Status") {
                        Text(relayStatus)
                            .foregroundStyle(relayStatusIsError ? Color.red : Color.secondary)
                            .multilineTextAlignment(.trailing)
                    }
                }
            } header: {
                Text("Relay")
            } footer: {
                HStack(alignment: .top) {
                    Text("For devices on other networks. Zubi connects out to a relay you run with “zubi relay”, "
                         + "so nothing here accepts connections from the internet. Each device's session is "
                         + "encrypted end to end with its key: the relay can't read it. Pair a device with the "
                         + "code under Settings › Keys.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer()
                    Button("Apply") { controller.applyRelaySettings() }
                }
            }

            if !controller.log.isEmpty {
                Section("Recent requests") {
                    ForEach(Array(controller.log.suffix(12).reversed().enumerated()), id: \.offset) { _, line in
                        Text(line)
                            .font(.caption.monospaced())
                            .lineLimit(1)
                            .truncationMode(.middle)
                    }
                }
            }
        }
        .formStyle(.grouped)
    }

    private var relayStatus: String {
        switch controller.relayStatus {
        case nil: controller.isRunning ? "Not connected: click Apply" : "Connects while the server runs"
        case .connecting: "Connecting…"
        case .connected: "Connected"
        case .failed(let message): message
        }
    }

    private var relayStatusIsError: Bool {
        if case .failed = controller.relayStatus { return true }
        return false
    }
}
