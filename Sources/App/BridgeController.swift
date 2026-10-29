import AppKit
import EventKit
import Foundation
import ServiceManagement

@MainActor
final class BridgeController: ObservableObject {
    enum NotesState: Equatable {
        case notConnected, connecting, connected
        case failed(String)
    }

    // MARK: Access

    @Published private(set) var reminderStatus = RemindersService.authorizationStatus
    @Published private(set) var calendarStatus = CalendarService.authorizationStatus
    @Published private(set) var notesState: NotesState = .notConnected

    // MARK: Sources and exposure

    @Published private(set) var reminderLists: [ReminderListDTO] = []
    @Published private(set) var calendars: [CalendarDTO] = []
    @Published private(set) var noteFolders: [NoteFolderDTO] = []

    @Published var exposedReminderLists = Exposure.reminderLists.ids() {
        didSet { Exposure.reminderLists.set(exposedReminderLists) }
    }
    @Published var exposedCalendars = Exposure.calendars.ids() {
        didSet { Exposure.calendars.set(exposedCalendars) }
    }
    @Published var exposedNoteFolders = Exposure.noteFolders.ids() {
        didSet { Exposure.noteFolders.set(exposedNoteFolders) }
    }

    // MARK: Server

    @Published private(set) var isRunning = false
    @Published private(set) var log: [String] = []
    @Published private(set) var lastError: String?
    @Published private(set) var apiKey: String
    @Published private(set) var launchAtLogin: Bool
    @Published var bindMode: BindMode
    @Published var customHost: String
    @Published var port: Int

    let reminders = RemindersService()
    let calendarService = CalendarService()
    let notes = NotesService()
    private var serverTask: Task<Void, Never>?

    var remindersGranted: Bool { reminderStatus == .fullAccess }
    var calendarGranted: Bool { calendarStatus == .fullAccess }
    var notesConnected: Bool { notesState == .connected }
    var hasAnySource: Bool { remindersGranted || calendarGranted || notesConnected }

    static func isDenied(_ status: EKAuthorizationStatus) -> Bool {
        status == .denied || status == .restricted
    }

    var host: String {
        switch bindMode {
        case .localhost: "127.0.0.1"
        case .allInterfaces: "0.0.0.0"
        case .custom: customHost.trimmingCharacters(in: .whitespaces)
        }
    }

    var curlExample: String {
        let target = bindMode == .allInterfaces ? "<mac-ip>" : host
        return "curl -H \"Authorization: Bearer \(apiKey)\" \"http://\(target):\(port)/api/events\""
    }

    init() {
        let defaults = UserDefaults.standard
        bindMode = BindMode(rawValue: defaults.string(forKey: SettingsKey.bindMode) ?? "") ?? .localhost
        customHost = defaults.string(forKey: SettingsKey.customHost) ?? ""
        port = defaults.object(forKey: SettingsKey.port) as? Int ?? 8787
        apiKey = APIKeyStore.loadOrCreate()
        launchAtLogin = SMAppService.mainApp.status == .enabled

        Task {
            // Only talk to Notes at launch if it was set up before, to avoid surprise prompts.
            await refreshAll(includeNotes: !exposedNoteFolders.isEmpty)
            start()
        }
    }

    // MARK: Access requests

    func requestRemindersAccess() async {
        do { _ = try await reminders.requestAccess() } catch { lastError = error.localizedDescription }
        reminderStatus = RemindersService.authorizationStatus
        if remindersGranted { reminderLists = await reminders.lists() }
    }

    func requestCalendarAccess() async {
        do { _ = try await calendarService.requestAccess() } catch { lastError = error.localizedDescription }
        calendarStatus = CalendarService.authorizationStatus
        if calendarGranted { calendars = await calendarService.calendars() }
    }

    /// The first call triggers the Automation prompt ("... wants to control Notes").
    func connectNotes() async {
        notesState = .connecting
        do {
            noteFolders = try await notes.folders()
            notesState = .connected
        } catch {
            notesState = .failed(String(describing: error))
        }
    }

    func refreshAll(includeNotes: Bool) async {
        if remindersGranted { reminderLists = await reminders.lists() }
        if calendarGranted { calendars = await calendarService.calendars() }
        if includeNotes { await connectNotes() }
    }

    /// pane: "Privacy_Reminders", "Privacy_Calendars" or "Privacy_Automation"
    func openPrivacySettings(_ pane: String) {
        if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?\(pane)") {
            NSWorkspace.shared.open(url)
        }
    }

    // MARK: Server lifecycle

    func start() {
        guard serverTask == nil else { return }
        guard !host.isEmpty else {
            lastError = "Enter an IP address to bind to."
            return
        }

        let config = ServerConfig(host: host, port: port, apiKey: apiKey)
        let server = BridgeServer(
            config: config, reminders: reminders, calendar: calendarService, notes: notes
        ) { [weak self] line in
            Task { @MainActor in self?.append(line) }
        }

        lastError = nil
        isRunning = true
        append("Listening on \(config.host):\(config.port)")

        serverTask = Task {
            do {
                try await server.run()
            } catch is CancellationError {
                // normal stop
            } catch {
                self.lastError = error.localizedDescription
            }
            self.isRunning = false
            self.serverTask = nil
            self.append("Server stopped")
        }
    }

    func stop() {
        serverTask?.cancel()
    }

    func restart() {
        Task {
            if let task = serverTask {
                task.cancel()
                await task.value
            }
            start()
        }
    }

    // MARK: Settings

    func applyNetworkSettings() {
        let defaults = UserDefaults.standard
        defaults.set(bindMode.rawValue, forKey: SettingsKey.bindMode)
        defaults.set(customHost, forKey: SettingsKey.customHost)
        defaults.set(port, forKey: SettingsKey.port)
        restart()
    }

    func regenerateAPIKey() {
        apiKey = APIKeyStore.regenerate()
        restart()
    }

    func setLaunchAtLogin(_ enabled: Bool) {
        do {
            if enabled {
                try SMAppService.mainApp.register()
            } else {
                try SMAppService.mainApp.unregister()
            }
        } catch {
            lastError = error.localizedDescription
        }
        launchAtLogin = SMAppService.mainApp.status == .enabled
    }

    func copyToPasteboard(_ string: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(string, forType: .string)
    }

    private func append(_ line: String) {
        let time = Date().formatted(date: .omitted, time: .standard)
        log.append("\(time)  \(line)")
        if log.count > 200 { log.removeFirst(log.count - 200) }
    }
}
