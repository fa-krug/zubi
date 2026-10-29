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
            Image(nsImage: controller.isRunning ? MenuBarIcon.running : MenuBarIcon.stopped)
                .accessibilityLabel(controller.isRunning ? "Zubi, running" : "Zubi, stopped")
                #if DEBUG
                .modifier(DebugOpenSettings())
                #endif
        }
        .menuBarExtraStyle(.window)

        Window("Zubi Settings", id: "settings") {
            SettingsView()
                .environmentObject(controller)
                .environment(updates)
        }
        .defaultSize(width: 760, height: 660)
        .windowResizability(.contentMinSize)
    }
}

/// The app icon's bridge as a template; a stopped server fades it rather than changing its shape.
@MainActor
private enum MenuBarIcon {
    private static let stoppedAlpha: CGFloat = 0.35

    static let running = NSImage(named: "MenuBarIcon") ?? NSImage()

    static let stopped: NSImage = {
        let image = NSImage(size: running.size, flipped: false) { rect in
            running.draw(in: rect, from: .zero, operation: .sourceOver, fraction: stoppedAlpha)
            return true
        }
        image.isTemplate = true
        return image
    }()
}

#if DEBUG
/// `--open-settings <tab>` opens Settings at launch, so a pane can be looked at without the menu bar.
private struct DebugOpenSettings: ViewModifier {
    @Environment(\.openWindow) private var openWindow

    func body(content: Content) -> some View {
        content.task {
            guard SettingsTab.debugLaunchTab != nil else { return }
            openWindow(id: "settings")
            NSApp.activate(ignoringOtherApps: true)
        }
    }
}
#endif
