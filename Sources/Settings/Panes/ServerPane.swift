import SwiftUI

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
}
