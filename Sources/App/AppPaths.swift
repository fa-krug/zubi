import Foundation

enum AppPaths {
    /// `~/Library/Caches/<bundle-id>/`, created on first use; Dev and release never share one.
    static func caches() -> URL {
        let root = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
        let url = root.appendingPathComponent(Bundle.main.bundleIdentifier ?? "Zubi", isDirectory: true)
        try? FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
        return url
    }
}

extension Bundle {
    /// "Zubi" or "Zubi Dev": what the user sees in Finder, not the executable name.
    var appDisplayName: String {
        object(forInfoDictionaryKey: "CFBundleDisplayName") as? String
            ?? object(forInfoDictionaryKey: "CFBundleName") as? String
            ?? "Zubi"
    }
}
