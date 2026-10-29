import Foundation

/// Side-by-side apps with their own bundle ids, so a build updates within its own.
enum ReleaseChannel: Sendable {
    case stable
    /// A local build. It has no release stream, and never updates itself.
    case development

    init(bundleID: String?) {
        switch bundleID {
        case "de.fa-krug.zubi": self = .stable
        default: self = .development
        }
    }

    var updatesItself: Bool { self != .development }

    /// Stable never takes a GitHub prerelease, so one can be published to test without shipping it.
    func accepts(prerelease: Bool) -> Bool {
        switch self {
        case .stable: return !prerelease
        case .development: return false
        }
    }
}
