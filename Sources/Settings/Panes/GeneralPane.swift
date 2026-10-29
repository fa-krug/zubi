import SwiftUI

struct GeneralPane: View {
    @EnvironmentObject private var controller: BridgeController
    @Environment(UpdateCoordinator.self) private var updates

    var body: some View {
        Form {
            Section("Startup") {
                Toggle(isOn: Binding(
                    get: { controller.launchAtLogin },
                    set: { controller.setLaunchAtLogin($0) }
                )) {
                    Text("Launch at login")
                    Text("Zubi has to be running, in a logged-in session, for the API to answer.")
                }
            }

            Section("Availability") {
                Toggle(isOn: $controller.keepAwake) {
                    Text("Keep this Mac awake while Zubi serves")
                    Text("Stops idle sleep while the server runs, so devices can always reach it. The display "
                         + "still sleeps, and closing the lid still sleeps the Mac.")
                }
            }

            Section("Updates") {
                Toggle(isOn: Binding(
                    get: { updates.automaticallyCheckForUpdates },
                    set: { updates.automaticallyCheckForUpdates = $0 }
                )) {
                    Text("Automatically check for updates")
                    Text("Check for Updates still works when off.")
                }
                .disabled(!updates.channel.updatesItself)
                LabeledContent {
                    Button("Check for Updates…") { updates.checkForUpdates() }
                } label: {
                    Text("Version \(updates.runningVersion)")
                    if !updates.channel.updatesItself {
                        Text("A local build never updates itself.")
                    }
                }
            }
        }
        .formStyle(.grouped)
    }
}
