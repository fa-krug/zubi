import AppKit
import EventKit
import Foundation
import ServiceManagement
import ZubiAPI

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
    @Published var writableReminderLists = Exposure.reminderLists.writableIDs() {
        didSet { Exposure.reminderLists.setWritable(writableReminderLists) }
    }
    @Published var writableCalendars = Exposure.calendars.writableIDs() {
        didSet { Exposure.calendars.setWritable(writableCalendars) }
    }
    @Published var writableNoteFolders = Exposure.noteFolders.writableIDs() {
        didSet { Exposure.noteFolders.setWritable(writableNoteFolders) }
    }

    // MARK: Server

    @Published private(set) var isRunning = false {
        didSet { updateKeepAwake() }
    }
    @Published private(set) var log: [String] = []
    @Published private(set) var lastError: String?
    @Published private(set) var launchAtLogin: Bool
    /// Idle sleep is held off while this is on and the server runs.
    @Published var keepAwake: Bool {
        didSet {
            UserDefaults.standard.set(keepAwake, forKey: SettingsKey.keepAwake)
            updateKeepAwake()
        }
    }
    @Published private(set) var isKeepingAwake = false
    @Published private(set) var isAdvertising = false
    @Published var bindMode: BindMode
    @Published var customHost: String
    @Published var port: Int

    // MARK: Relay

    @Published var relayEnabled: Bool
    /// The relay's wss:// address, as typed; applied with `applyRelaySettings()`.
    @Published var relayURL: String
    /// Nil while the relay is off or the server stopped.
    @Published private(set) var relayStatus: RelayHost.Status?

    // MARK: Keys and activity

    /// In Settings' order. The secrets are in the Keychain; see `KeyStore`.
    @Published private(set) var keys: [StoredKey] = []
    @Published private(set) var keyLastUsed: [String: Date]
    /// Why the keys could not be read or saved; while reading fails, no key works.
    @Published private(set) var keyError: String?
    /// Newest first.
    @Published private(set) var activity: [ActivityEntry]
    /// Set to open Settings on a tab; Settings clears it once shown.
    @Published var requestedSettingsTab: SettingsTab?

    let reminders = RemindersService()
    let calendarService = CalendarService()
    let notes = NotesService()
    /// One for the app's lifetime, so EventKit's notifications reach whichever server is running.
    let changes = ChangeFeed()
    private let keyRing = KeyRing()
    private let sleepAssertion = KeepAwake()
    private let bonjour = BonjourAdvertiser()
    private var serverTask: Task<Void, Never>?
    /// Tells a late "bound" callback from a server that has since stopped apart from the current one.
    private var serverGeneration = 0
    private var relayHost: RelayHost?
    /// Tells a stopped relay connection's late status apart from the current one's.
    private var relayGeneration = 0
    private var observers: [NSObjectProtocol] = []

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

    /// The key the test command uses: the first that may write, as that one can try everything.
    var testKey: StoredKey? {
        keys.first { $0.access == .readWrite } ?? keys.first
    }

    func curlCommand(for key: StoredKey) -> String {
        let target = bindMode == .allInterfaces ? "<mac-ip>" : host
        let authority = target.contains(":") ? "[\(target)]" : target
        return "curl -H \"Authorization: Bearer \(key.secret)\" \"http://\(authority):\(port)/api/events\""
    }

    /// A subscription URL for a calendar app. Only a Read key works in it: a URL ends up in logs and
    /// sync settings, so the API refuses a key that can write there.
    func feedURL(for key: StoredKey, calendar: CalendarDTO) -> String {
        let target = bindMode == .allInterfaces ? "<mac-ip>" : host
        let authority = target.contains(":") ? "[\(target)]" : target
        let id = calendar.id.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(["/"]))
            ?? calendar.id
        let secret = key.secret.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? key.secret
        return "http://\(authority):\(port)/api/calendars/\(id)/events.ics?key=\(secret)"
    }

    /// Calendars a feed can be made for: shared, in Settings' order.
    var sharedCalendars: [CalendarDTO] {
        calendars.filter { exposedCalendars.contains($0.id) }
    }

    /// The Swagger UI page, opened on this Mac: a browser cannot connect to 0.0.0.0, so loopback stands in.
    var docsURL: URL? {
        let target = bindMode == .allInterfaces ? "127.0.0.1" : host
        let authority = target.contains(":") ? "[\(target)]" : target
        return URL(string: "http://\(authority):\(port)/docs")
    }

    init() {
        let defaults = UserDefaults.standard
        bindMode = BindMode(rawValue: defaults.string(forKey: SettingsKey.bindMode) ?? "") ?? .localhost
        customHost = defaults.string(forKey: SettingsKey.customHost) ?? ""
        port = defaults.object(forKey: SettingsKey.port) as? Int ?? 8787
        keepAwake = defaults.bool(forKey: SettingsKey.keepAwake)
        relayEnabled = defaults.bool(forKey: SettingsKey.relayEnabled)
        relayURL = defaults.string(forKey: SettingsKey.relayURL) ?? ""
        keyLastUsed = defaults.dictionary(forKey: SettingsKey.keyLastUsed) as? [String: Date] ?? [:]
        activity = ActivityLog.load()
        launchAtLogin = SMAppService.mainApp.status == .enabled
        reloadKeys()

        // It doesn't say what changed, or which of the two stores saw it; the feed sorts that out.
        let changes = changes
        observers.append(NotificationCenter.default.addObserver(
            forName: .EKEventStoreChanged, object: nil, queue: nil
        ) { _ in changes.eventStoreChanged() })
        // Released by the system when the process ends anyway; this says so promptly and on purpose.
        observers.append(NotificationCenter.default.addObserver(
            forName: NSApplication.willTerminateNotification, object: nil, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.bonjour.stop()
                self?.sleepAssertion.hold(false, reason: "")
            }
        })

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

        let config = ServerConfig(host: host, port: port)
        let keyRing = keyRing
        let server = BridgeServer(
            config: config, keys: { keyRing.keys },
            reminders: reminders, calendar: calendarService, notes: notes,
            access: SourceAccess(
                reminders: { RemindersService.authorizationStatus == .fullAccess },
                calendar: { CalendarService.authorizationStatus == .fullAccess }),
            exposed: ExposedIDs(
                reminderLists: { Exposure.reminderLists.ids() },
                calendars: { Exposure.calendars.ids() },
                noteFolders: { Exposure.noteFolders.ids() },
                writableReminderLists: { Exposure.reminderLists.writableIDs() },
                writableCalendars: { Exposure.calendars.writableIDs() },
                writableNoteFolders: { Exposure.noteFolders.writableIDs() }),
            changes: changes,
            onRequest: { [weak self] line in
                Task { @MainActor in self?.append(line) }
            },
            onKeyUsed: { [weak self] id in
                Task { @MainActor in self?.noteKeyUsed(id) }
            },
            onWrite: { [weak self] record in
                Task { @MainActor in self?.record(record) }
            })

        lastError = nil
        isRunning = true
        append("Listening on \(config.host):\(config.port)")

        serverGeneration += 1
        let generation = serverGeneration
        let advertise = bindMode == .allInterfaces
        let onRunning: @Sendable () -> Void = { [weak self] in
            Task { @MainActor in
                guard let self, self.serverGeneration == generation, self.serverTask != nil else { return }
                self.startRelay(serving: config)
                guard advertise else { return }
                self.bonjour.start(port: config.port)
                self.isAdvertising = self.bonjour.isAdvertising
            }
        }
        serverTask = Task {
            do {
                try await server.run(onRunning: onRunning)
            } catch is CancellationError {
                // normal stop
            } catch {
                self.lastError = Self.describe(error, port: config.port)
            }
            self.bonjour.stop()
            self.isAdvertising = false
            self.stopRelay()
            self.servingConfig = nil
            self.isRunning = false
            self.serverTask = nil
            self.append("Server stopped")
        }
    }

    /// NIO's errors localize to "The operation couldn't be completed", which says nothing.
    private static func describe(_ error: Error, port: Int) -> String {
        let detail = String(describing: error)
        if detail.contains("Address already in use") {
            return "Port \(port) is already in use. Quit the other app using it, or pick another port."
        }
        if detail.contains("Can't assign requested address") {
            return "This Mac has no such IP address. Check the address under Network."
        }
        return detail
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

    // MARK: Relay

    /// Saves the relay settings and reconnects, while the server runs.
    func applyRelaySettings() {
        relayURL = relayURL.trimmingCharacters(in: .whitespacesAndNewlines)
        let defaults = UserDefaults.standard
        defaults.set(relayEnabled, forKey: SettingsKey.relayEnabled)
        defaults.set(relayURL, forKey: SettingsKey.relayURL)
        stopRelay()
        if let config = servingConfig { startRelay(serving: config) }
    }

    /// The server's address and port while it runs, for the relay to pass sessions to.
    private var servingConfig: ServerConfig?

    private func startRelay(serving config: ServerConfig) {
        servingConfig = config
        guard relayEnabled, relayHost == nil else { return }
        let url = URL(string: relayURL)
        guard let url, RelayHost.isValidRelay(url) else {
            relayStatus = .failed("Enter the relay's address, starting with wss://.")
            return
        }
        relayGeneration += 1
        let generation = relayGeneration
        let keyRing = keyRing
        // A server on all interfaces is reached on loopback; one on a specific address only there.
        let target = config.host == "0.0.0.0" ? "127.0.0.1" : config.host
        do {
            let host = try RelayHost(
                relay: url, roomKey: KeyStore.roomKey(), keys: { keyRing.keys },
                targetHost: target, targetPort: config.port,
                onStatus: { [weak self] status in
                    Task { @MainActor in
                        guard let self, self.relayGeneration == generation else { return }
                        self.relayStatus = status
                    }
                })
            relayHost = host
            relayStatus = .connecting
            host.start()
        } catch {
            relayStatus = .failed(String(describing: error))
        }
    }

    private func stopRelay() {
        relayGeneration += 1
        relayHost?.stop()
        relayHost = nil
        relayStatus = nil
    }

    /// The code a device pastes into `zubi pair` to use `key` through the relay; nil while there
    /// is no relay connection.
    func pairingCode(for key: StoredKey) -> String? {
        relayHost?.pairingCode(for: key.apiKey)
    }

    private func updateKeepAwake() {
        sleepAssertion.hold(keepAwake && isRunning, reason: "\(Bundle.main.appDisplayName) is serving its API")
        isKeepingAwake = sleepAssertion.isHeld
    }

    // MARK: Keys

    /// Keys are read per request, so none of these needs a restart.
    func reloadKeys() {
        do {
            keys = try KeyStore.load()
            keyError = nil
        } catch {
            keys = []
            keyError = String(describing: error)
        }
        keyRing.set(keys.map(\.apiKey))
    }

    /// The new key, with its secret, or nil when the Keychain refused it.
    @discardableResult
    func addKey(name: String, access: KeyAccess) -> StoredKey? {
        let key = StoredKey(id: UUID().uuidString, name: Self.keyName(name), access: access, created: Date(),
                            secret: KeyStore.generateSecret())
        return commit(keys + [key]) ? key : nil
    }

    func setAccess(_ access: KeyAccess, of id: String) {
        update(id) { $0.access = access }
    }

    func renameKey(_ id: String, to name: String) {
        update(id) { $0.name = Self.keyName(name) }
    }

    /// The key with its new secret; the old one stops working at once.
    @discardableResult
    func regenerateKey(_ id: String) -> StoredKey? {
        update(id) { $0.secret = KeyStore.generateSecret() }
        return keys.first { $0.id == id }
    }

    func deleteKey(_ id: String) {
        guard commit(keys.filter { $0.id != id }) else { return }
        keyLastUsed[id] = nil
        UserDefaults.standard.set(keyLastUsed, forKey: SettingsKey.keyLastUsed)
    }

    private func update(_ id: String, _ change: (inout StoredKey) -> Void) {
        var new = keys
        guard let index = new.firstIndex(where: { $0.id == id }) else { return }
        change(&new[index])
        commit(new)
    }

    /// Saved first, then served: a key only works once it is safely stored.
    @discardableResult
    private func commit(_ new: [StoredKey]) -> Bool {
        do {
            try KeyStore.save(new)
        } catch {
            keyError = String(describing: error)
            return false
        }
        keys = new
        keyError = nil
        keyRing.set(new.map(\.apiKey))
        return true
    }

    private static func keyName(_ name: String) -> String {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? "Unnamed" : trimmed
    }

    /// Kept to the minute: a busy client would otherwise rewrite the preferences on every request.
    private func noteKeyUsed(_ id: String) {
        let now = Date()
        if let last = keyLastUsed[id], now.timeIntervalSince(last) < 60 { return }
        keyLastUsed[id] = now
        UserDefaults.standard.set(keyLastUsed, forKey: SettingsKey.keyLastUsed)
    }

    // MARK: Activity

    private func record(_ record: WriteRecord) {
        activity.insert(ActivityEntry(record), at: 0)
        if activity.count > ActivityLog.limit { activity.removeLast(activity.count - ActivityLog.limit) }
        ActivityLog.save(activity)
    }

    func clearActivity() {
        activity = []
        ActivityLog.save(activity)
    }

    // MARK: First launch

    /// True once: on a launch with nothing shared and Settings never opened, which is how a new
    /// user starts. Marked here so it can't happen twice, even if the window never shows.
    func takeFirstLaunch() -> Bool {
        let defaults = UserDefaults.standard
        guard !defaults.bool(forKey: SettingsKey.settingsOpened) else { return false }
        let nothingShared = Exposure.reminderLists.ids().isEmpty && Exposure.calendars.ids().isEmpty
            && Exposure.noteFolders.ids().isEmpty
        guard nothingShared else { return false }
        defaults.set(true, forKey: SettingsKey.settingsOpened)
        return true
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
