import Foundation

enum SettingsKey {
    static let bindMode = "bindMode"
    static let customHost = "customHost"
    static let port = "port"
    static let keepAwake = "keepAwake"
    /// [key id: Date]. Not secret, and written far too often for the Keychain.
    static let keyLastUsed = "keyLastUsed"
    /// Set once Settings has been shown, so a first launch with nothing shared opens it only once.
    static let settingsOpened = "settingsOpened"
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

/// Which items the API may expose, and which of those it may also write to. Read per request, so
/// changes need no restart. Writable IDs are kept a subset of exposed ones; a list exposed before
/// writes existed has no writable entry, so it stays Read.
enum Exposure: String {
    case reminderLists = "exposedListIDs"
    case calendars = "exposedCalendarIDs"
    case noteFolders = "exposedNoteFolderIDs"

    private var writableKey: String {
        switch self {
        case .reminderLists: "writableListIDs"
        case .calendars: "writableCalendarIDs"
        case .noteFolders: "writableNoteFolderIDs"
        }
    }

    func ids() -> Set<String> {
        Set(UserDefaults.standard.stringArray(forKey: rawValue) ?? [])
    }

    func set(_ ids: Set<String>) {
        UserDefaults.standard.set(Array(ids), forKey: rawValue)
    }

    /// Intersected on read too, so a hand-edited preference can never make a hidden item writable.
    func writableIDs() -> Set<String> {
        Set(UserDefaults.standard.stringArray(forKey: writableKey) ?? []).intersection(ids())
    }

    func setWritable(_ ids: Set<String>) {
        UserDefaults.standard.set(Array(ids), forKey: writableKey)
    }
}

/// What Settings lets the API do with one list, calendar or folder.
enum AccessLevel: String, CaseIterable, Identifiable {
    case off, read, readWrite

    var id: String { rawValue }

    var label: String {
        switch self {
        case .off: "Off"
        case .read: "Read"
        case .readWrite: "Read & Write"
        }
    }
}
