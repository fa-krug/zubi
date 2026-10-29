import AppKit
import SwiftUI

struct MenuView: View {
    @EnvironmentObject var controller: BridgeController
    @Environment(UpdateCoordinator.self) private var updates
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 6) {
                Circle()
                    .fill(statusColor)
                    .frame(width: 8, height: 8)
                Text(statusText)
            }
            if case .failed(let message) = controller.relayStatus {
                Text(message)
                    .font(.caption)
                    .foregroundStyle(Color.red)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let error = controller.lastError, !controller.isRunning {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(Color.red)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if !controller.hasAnySource {
                Text("No data source connected yet. Open Settings.")
                    .font(.caption)
                    .foregroundStyle(Color.orange)
            }
            if controller.isKeepingAwake {
                Label("Keeping this Mac awake", systemImage: "cup.and.saucer")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            if let code = controller.defaultPairingCode {
                Button("Copy Pairing Code") { controller.copyToPasteboard(code) }
                    .help("Paste it into “zubi” on a device to pair it with this Mac")
            }

            Divider()

            HStack {
                if !controller.isRunning {
                    Button("Start") { controller.start() }
                }
                Button("Settings…") {
                    openWindow(id: "settings")
                    NSApp.activate(ignoringOtherApps: true)
                }
                Spacer()
                Button("Quit") { NSApp.terminate(nil) }
            }

            Button("Check for Updates…") { updates.checkForUpdates() }
                .buttonStyle(.link)
                .font(.caption)
        }
        .padding(12)
        .frame(width: 320)
    }

    private var statusText: String {
        if !controller.isRunning { return "Stopped" }
        switch controller.relayStatus {
        case .connected: return "Connected: clients can reach this Mac"
        case .failed: return "Not connected"
        case .connecting, nil: return "Connecting…"
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
