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
                    .fill(controller.isRunning ? Color.green : Color.secondary)
                    .frame(width: 8, height: 8)
                Text(controller.isRunning
                     ? "Running on \(controller.host):\(String(controller.port))"
                     : "Stopped")
            }

            if !controller.hasAnySource {
                Text("No data source connected yet. Open Setup.")
                    .font(.caption)
                    .foregroundStyle(Color.orange)
            }

            if let error = controller.lastError {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(Color.red)
            }

            if !controller.log.isEmpty {
                Divider()
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(Array(controller.log.suffix(8).enumerated()), id: \.offset) { _, line in
                        Text(line)
                            .font(.caption.monospaced())
                            .lineLimit(1)
                    }
                }
            }

            Divider()

            HStack {
                Button(controller.isRunning ? "Stop" : "Start") {
                    if controller.isRunning { controller.stop() } else { controller.start() }
                }

                Button("Setup…") {
                    openWindow(id: "setup")
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
}
