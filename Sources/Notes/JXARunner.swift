import Foundation
import ZubiAPI

/// The API answers 404 for `.notFound`, 409 for `.locked` and 503 for anything else.
enum NotesError: SourceError {
    case scriptFailed(String)
    case badOutput

    /// What the write scripts throw for a locked note, so it can be told apart from other failures.
    static let lockedMarker = "ZUBI_LOCKED"

    var kind: SourceErrorKind {
        guard case .scriptFailed(let message) = self else { return .unavailable }
        if message.contains(Self.lockedMarker) { return .locked }
        if message.contains("-1728") { return .notFound }
        return .unavailable
    }

    var description: String {
        switch self {
        case .scriptFailed(let message):
            if message.contains("-1743") {
                return "Not allowed to control Notes. Enable it under Privacy & Security > Automation."
            }
            switch kind {
            case .notFound: return "Note or folder not found."
            case .locked: return "The note is locked, and Zubi never changes a locked note."
            default: break
            }
            if message.contains("timed out") { return "Notes did not respond in time." }
            return "Notes script failed: \(message.trimmingCharacters(in: .whitespacesAndNewlines))"
        case .badOutput:
            return "Unexpected output from Notes."
        }
    }
}

/// Runs JavaScript for Automation through /usr/bin/osascript.
/// Arguments are passed to the script's run(argv) handler, never interpolated into source.
/// macOS attributes the Automation permission to this app, since it spawns osascript.
enum JXARunner {
    static func run(_ source: String, args: [String] = [], timeout: TimeInterval = 20) async throws -> Data {
        try await withCheckedThrowingContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                let process = Process()
                process.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
                process.arguments = ["-l", "JavaScript", "-e", source] + args

                let stdout = Pipe()
                let stderr = Pipe()
                process.standardOutput = stdout
                process.standardError = stderr

                do {
                    try process.run()
                } catch {
                    // As a NotesError, so the API answers 503 like for any other failure to read Notes.
                    continuation.resume(throwing: NotesError.scriptFailed(error.localizedDescription))
                    return
                }

                var timedOut = false
                let killer = DispatchWorkItem {
                    if process.isRunning {
                        timedOut = true
                        process.terminate()
                    }
                }
                DispatchQueue.global().asyncAfter(deadline: .now() + timeout, execute: killer)

                // Read before waiting, otherwise large note bodies fill the pipe buffer and deadlock.
                let output = stdout.fileHandleForReading.readDataToEndOfFile()
                let errorOutput = stderr.fileHandleForReading.readDataToEndOfFile()
                process.waitUntilExit()
                killer.cancel()

                if process.terminationStatus == 0 {
                    continuation.resume(returning: output)
                } else {
                    let message = timedOut ? "timed out" : String(decoding: errorOutput, as: UTF8.self)
                    continuation.resume(throwing: NotesError.scriptFailed(message))
                }
            }
        }
    }
}
