import SwiftUI

enum SettingsTab: CaseIterable, Identifiable {
    case general, server, api, reminders, calendars, notes, about
    /// The case, never an index: a selectable `List` flattens section and row IDs together.
    var id: Self { self }

    var title: String {
        switch self {
        case .general: return "General"
        case .server: return "Server"
        case .api: return "API Key"
        case .reminders: return "Reminders"
        case .calendars: return "Calendars"
        case .notes: return "Notes"
        case .about: return "About"
        }
    }

    var systemImage: String {
        switch self {
        case .general: return "switch.2"
        case .server: return "network"
        case .api: return "key"
        case .reminders: return "checklist"
        case .calendars: return "calendar"
        case .notes: return "note.text"
        case .about: return "info.circle"
        }
    }

    /// The colour each source has in its own app, so a tab reads as the app it exposes.
    var tint: Color {
        switch self {
        case .general: return .gray
        case .server: return .blue
        case .api: return .purple
        case .reminders: return .orange
        case .calendars: return .red
        case .notes: return .yellow
        case .about: return Theme.Colors.brand
        }
    }
}

#if DEBUG
extension SettingsTab {
    /// The tab named by `--open-settings <tab>`, matched against its title.
    static let debugLaunchTab: SettingsTab? = {
        let arguments = ProcessInfo.processInfo.arguments
        guard let flag = arguments.firstIndex(of: "--open-settings") else { return nil }
        let name = arguments.indices.contains(flag + 1) ? arguments[flag + 1].lowercased() : ""
        return allCases.first { $0.title.lowercased().hasPrefix(name) } ?? .general
    }()
}
#endif

/// Declaration order is display order; not `.Section`, which would shadow SwiftUI's `Section`.
enum SettingsSection: CaseIterable, Identifiable {
    case app, sources, about
    var id: Self { self }

    var title: String {
        switch self {
        case .app: return "Zubi"
        case .sources: return "Sources"
        // Untitled: a header reading "About" over a row reading "About" says it twice.
        case .about: return ""
        }
    }

    var tabs: [SettingsTab] {
        switch self {
        case .app: return [.general, .server, .api]
        case .sources: return [.reminders, .calendars, .notes]
        case .about: return [.about]
        }
    }
}
