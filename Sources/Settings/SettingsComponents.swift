import AppKit
import SwiftUI

/// A tinted glyph on a faint tile of the same tint, as Blitz's sidebar draws them.
struct SettingsTabIcon: View {
    let systemImage: String
    let tint: Color

    var body: some View {
        Image(systemName: systemImage)
            .resizable()
            .scaledToFit()
            .frame(width: 14, height: 14)
            .foregroundStyle(tint)
            .padding(4)
            .background(tint.opacity(0.12), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
    }
}

/// One source's permission: granted, not yet asked, or refused with a way to System Settings.
struct AccessRow: View {
    let title: String
    let granted: Bool
    let denied: Bool
    var detail: String? = nil
    let grant: @MainActor () async -> Void
    let openSettings: () -> Void

    @Environment(\.openWindow) private var openWindow

    var body: some View {
        LabeledContent {
            if denied {
                HStack {
                    Button("Open System Settings", action: openSettings)
                    Button("Retry", action: grantThenReturn)
                }
            } else if !granted {
                Button("Grant Access", action: grantThenReturn)
            }
        } label: {
            Label {
                Text(granted ? "\(title) access granted" : "\(title) access needed")
                if let detail {
                    Text(detail)
                }
            } icon: {
                Image(systemName: granted ? "checkmark.circle.fill" : "exclamationmark.circle.fill")
                    .foregroundStyle(granted ? Color.green : Color.orange)
            }
        }
    }

    /// The permission dialog takes focus, and a menu bar app has no Dock icon to get it back, so
    /// when the dialog closes macOS fronts the previous app and Settings ends up behind it.
    private func grantThenReturn() {
        Task {
            await grant()
            openWindow(id: "settings")
            NSApp.activate(ignoringOtherApps: true)
        }
    }
}

/// A checkbox row bound to membership of `id` in a set of exposed IDs.
struct ExposureToggle<Icon: View>: View {
    let id: String
    let title: String
    @Binding var selection: Set<String>
    @ViewBuilder let icon: () -> Icon

    var body: some View {
        Toggle(isOn: Binding(
            get: { selection.contains(id) },
            set: { isOn in
                if isOn { selection.insert(id) } else { selection.remove(id) }
            }
        )) {
            Label { Text(title) } icon: { icon() }
        }
    }
}

/// Groups items by account, keeping the order the service returned them in.
func groupedByAccount<Item>(_ items: [Item], account: (Item) -> String) -> [(account: String, items: [Item])] {
    var order: [String] = []
    var groups: [String: [Item]] = [:]
    for item in items {
        let key = account(item)
        if groups[key] == nil { order.append(key) }
        groups[key, default: []].append(item)
    }
    return order.map { ($0, groups[$0] ?? []) }
}

extension Color {
    /// `#RRGGBB` from the API's DTOs; nil when absent or malformed, so callers pick a fallback.
    init?(hex: String?) {
        guard let hex, hex.hasPrefix("#"), hex.count == 7,
              let value = UInt32(hex.dropFirst(), radix: 16)
        else { return nil }
        self.init(
            .sRGB,
            red: Double((value >> 16) & 0xFF) / 255,
            green: Double((value >> 8) & 0xFF) / 255,
            blue: Double(value & 0xFF) / 255)
    }
}
