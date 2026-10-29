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

/// One list, calendar or folder, with what the API may do with it: Off, Read, or Read & Write.
/// Writable IDs stay a subset of exposed ones, so Off always means 404, reads and writes alike.
struct AccessLevelRow<Icon: View>: View {
    let id: String
    let title: String
    /// Whether the source itself allows changes; when it doesn't, Read & Write can't be picked.
    let canWrite: Bool
    /// Why `canWrite` is false, for the tooltip.
    let readOnlyReason: String
    @Binding var exposed: Set<String>
    @Binding var writable: Set<String>
    @ViewBuilder let icon: () -> Icon

    var body: some View {
        LabeledContent {
            Picker(title, selection: level) {
                ForEach(AccessLevel.allCases) { level in
                    Text(level.label)
                        .tag(level)
                        .disabled(level == .readWrite && !canWrite)
                }
            }
            .labelsHidden()
            .pickerStyle(.menu)
            .fixedSize()
            .help(canWrite
                  ? "Off hides it. Read shares it. Read & Write also lets the API add and edit items."
                  : readOnlyReason)
        } label: {
            Label { Text(title) } icon: { icon() }
        }
    }

    /// A writable ID the source no longer allows shows as Read, which is all the API does with it.
    private var level: Binding<AccessLevel> {
        Binding(
            get: {
                guard exposed.contains(id) else { return .off }
                return canWrite && writable.contains(id) ? .readWrite : .read
            },
            set: { level in
                if level == .off { exposed.remove(id) } else { exposed.insert(id) }
                if level == .readWrite && canWrite { writable.insert(id) } else { writable.remove(id) }
            }
        )
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
