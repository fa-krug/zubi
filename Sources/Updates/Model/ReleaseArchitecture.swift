import Foundation

/// Stable ships one universal build; Apple silicon would take a thin one if a release carried it.
enum ReleaseArchitecture: Sendable {
    case appleSilicon
    case intel

    /// Resolved per slice at compile time, so a universal binary reports the one macOS launched.
    static var current: ReleaseArchitecture {
        #if arch(x86_64)
            .intel
        #else
            .appleSilicon
        #endif
    }
}
