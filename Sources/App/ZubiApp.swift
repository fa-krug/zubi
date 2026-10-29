import SwiftUI

@main
struct ZubiApp: App {
    @StateObject private var controller = BridgeController()
    /// Created with the app, not the first view, so the daily check runs without the menu ever opening.
    @State private var updates = UpdateCoordinator()

    var body: some Scene {
        MenuBarExtra {
            MenuView()
                .environmentObject(controller)
                .environment(updates)
        } label: {
            Image(systemName: controller.isRunning ? "checklist.checked" : "checklist")
        }
        .menuBarExtraStyle(.window)

        Window("Zubi Setup", id: "setup") {
            SetupView()
                .environmentObject(controller)
                .environment(updates)
        }
        .windowResizability(.contentSize)
    }
}
