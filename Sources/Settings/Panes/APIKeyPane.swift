import SwiftUI

struct APIKeyPane: View {
    @EnvironmentObject private var controller: BridgeController

    var body: some View {
        Form {
            Section {
                Text(controller.apiKey)
                    .font(.callout.monospaced())
                    .textSelection(.enabled)
                HStack {
                    Spacer()
                    Button("Regenerate") { controller.regenerateAPIKey() }
                    Button("Copy") { controller.copyToPasteboard(controller.apiKey) }
                }
            } header: {
                Text("API key")
            } footer: {
                Text("Clients send it as an Authorization: Bearer header. Regenerating restarts the server and "
                     + "every client needs the new key.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Section("Test the API") {
                Text(controller.curlExample)
                    .font(.caption.monospaced())
                    .textSelection(.enabled)
                HStack {
                    Spacer()
                    Button("Copy Command") { controller.copyToPasteboard(controller.curlExample) }
                }
            }
        }
        .formStyle(.grouped)
    }
}
