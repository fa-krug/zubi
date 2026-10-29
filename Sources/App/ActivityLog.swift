import Foundation
import ZubiAPI

/// One create or edit made through the API, as Settings › Activity lists it.
struct ActivityEntry: Codable, Identifiable, Equatable {
    var id = UUID()
    let date: Date
    let keyName: String
    let action: WriteRecord.Action
    let source: ChangeSource
    let title: String
    let container: String

    init(_ record: WriteRecord) {
        date = record.date
        keyName = record.keyName
        action = record.action
        source = record.source
        title = record.title
        container = record.container
    }
}

/// `activity.json` in Application Support, newest first, capped so it never grows without bound.
enum ActivityLog {
    static let limit = 500

    private static var url: URL { AppPaths.applicationSupport().appendingPathComponent("activity.json") }
    /// Serial, so an older list can never be written over a newer one.
    private static let queue = DispatchQueue(label: "activity-log", qos: .utility)

    static func load() -> [ActivityEntry] {
        guard let data = try? Data(contentsOf: url) else { return [] }
        return (try? JSONDecoder().decode([ActivityEntry].self, from: data)) ?? []
    }

    static func save(_ entries: [ActivityEntry]) {
        let url = url
        queue.async {
            guard let data = try? JSONEncoder().encode(entries) else { return }
            try? data.write(to: url, options: .atomic)
        }
    }
}
