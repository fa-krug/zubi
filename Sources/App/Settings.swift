import Foundation

enum SettingsKey {
    static let bindMode = "bindMode"
    static let customHost = "customHost"
    static let port = "port"
}

enum BindMode: String, CaseIterable, Identifiable {
    case localhost, allInterfaces, custom

    var id: String { rawValue }

    var label: String {
        switch self {
        case .localhost: "Localhost only (use SSH tunnel)"
        case .allInterfaces: "All interfaces"
        case .custom: "Specific IP (e.g. Tailscale)"
        }
    }
}

struct ServerConfig: Sendable {
    let host: String
    let port: Int
    let apiKey: String
}

/// Which items the API may expose. Read per request, so changes need no restart.
enum Exposure: String {
    case reminderLists = "exposedListIDs"
    case calendars = "exposedCalendarIDs"
    case noteFolders = "exposedNoteFolderIDs"

    func ids() -> Set<String> {
        Set(UserDefaults.standard.stringArray(forKey: rawValue) ?? [])
    }

    func set(_ ids: Set<String>) {
        UserDefaults.standard.set(Array(ids), forKey: rawValue)
    }
}
