import SwiftUI

struct SetupView: View {
    @EnvironmentObject var controller: BridgeController
    @Environment(UpdateCoordinator.self) private var updates

    var body: some View {
        Form {
            Section("1. Access") {
                AccessRow(
                    title: "Reminders",
                    granted: controller.remindersGranted,
                    denied: BridgeController.isDenied(controller.reminderStatus),
                    grant: { Task { await controller.requestRemindersAccess() } },
                    openSettings: { controller.openPrivacySettings("Privacy_Reminders") }
                )
                AccessRow(
                    title: "Calendar",
                    granted: controller.calendarGranted,
                    denied: BridgeController.isDenied(controller.calendarStatus),
                    grant: { Task { await controller.requestCalendarAccess() } },
                    openSettings: { controller.openPrivacySettings("Privacy_Calendars") }
                )
                AccessRow(
                    title: "Notes",
                    granted: controller.notesConnected,
                    denied: notesError != nil,
                    detail: notesError,
                    grant: { Task { await controller.connectNotes() } },
                    openSettings: { controller.openPrivacySettings("Privacy_Automation") }
                )
                Text("Notes has no public API. The app reads it through Notes.app scripting, and locked notes stay private.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Section("2. Reminder lists") {
                ExposureList(
                    items: controller.reminderLists.map { ($0.id, $0.title) },
                    selection: $controller.exposedReminderLists,
                    emptyText: controller.remindersGranted ? "No lists found." : "Grant Reminders access first."
                )
            }

            Section("3. Calendars") {
                ExposureList(
                    items: controller.calendars.map { ($0.id, "\($0.title)  ·  \($0.account)") },
                    selection: $controller.exposedCalendars,
                    emptyText: controller.calendarGranted ? "No calendars found." : "Grant Calendar access first."
                )
            }

            Section("4. Notes folders") {
                ExposureList(
                    items: controller.noteFolders.map { ($0.id, "\($0.name)  ·  \($0.account)") },
                    selection: $controller.exposedNoteFolders,
                    emptyText: controller.notesConnected ? "No folders found." : "Connect Notes first."
                )
                if controller.notesState == .connecting {
                    ProgressView().controlSize(.small)
                }
            }

            Section {
                Button("Reload all") {
                    Task { await controller.refreshAll(includeNotes: controller.notesConnected) }
                }
            }

            Section("5. Network") {
                Picker("Bind to", selection: $controller.bindMode) {
                    ForEach(BindMode.allCases) { mode in
                        Text(mode.label).tag(mode)
                    }
                }
                if controller.bindMode == .custom {
                    TextField("IP address", text: $controller.customHost)
                }
                TextField("Port", value: $controller.port, format: .number.grouping(.never))
                Button("Apply and restart server") { controller.applyNetworkSettings() }
            }

            Section("6. API key") {
                Text(controller.apiKey)
                    .font(.caption.monospaced())
                    .textSelection(.enabled)
                HStack {
                    Button("Copy") { controller.copyToPasteboard(controller.apiKey) }
                    Button("Regenerate") { controller.regenerateAPIKey() }
                }
            }

            Section("7. Startup") {
                Toggle("Launch at login", isOn: Binding(
                    get: { controller.launchAtLogin },
                    set: { controller.setLaunchAtLogin($0) }
                ))
            }

            Section("8. Updates") {
                Toggle("Automatically check for updates", isOn: Binding(
                    get: { updates.automaticallyCheckForUpdates },
                    set: { updates.automaticallyCheckForUpdates = $0 }
                ))
                .disabled(!updates.channel.updatesItself)
                HStack {
                    Text("Version \(updates.runningVersion)")
                        .foregroundStyle(.secondary)
                    Spacer()
                    Button("Check for Updates…") { updates.checkForUpdates() }
                }
            }

            Section("Test from your Pi") {
                Text(controller.curlExample)
                    .font(.caption.monospaced())
                    .textSelection(.enabled)
                Button("Copy command") { controller.copyToPasteboard(controller.curlExample) }
            }
        }
        .formStyle(.grouped)
        .frame(width: 520, height: 820)
    }

    private var notesError: String? {
        if case .failed(let message) = controller.notesState { return message }
        return nil
    }
}

private struct AccessRow: View {
    let title: String
    let granted: Bool
    let denied: Bool
    var detail: String? = nil
    let grant: () -> Void
    let openSettings: () -> Void

    var body: some View {
        HStack {
            Label(title, systemImage: granted ? "checkmark.circle.fill" : "exclamationmark.circle")
                .foregroundStyle(granted ? Color.green : Color.orange)
            Spacer()
            if denied {
                Button("Open Settings", action: openSettings)
                Button("Retry", action: grant)
            } else if !granted {
                Button("Grant", action: grant)
            }
        }
        if let detail {
            Text(detail)
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }
}

/// Checkbox list over (id, label) pairs bound to a set of exposed IDs.
private struct ExposureList: View {
    let items: [(id: String, label: String)]
    @Binding var selection: Set<String>
    let emptyText: String

    var body: some View {
        if items.isEmpty {
            Text(emptyText).foregroundStyle(.secondary)
        }
        ForEach(items, id: \.id) { item in
            Toggle(item.label, isOn: Binding(
                get: { selection.contains(item.id) },
                set: { isOn in
                    if isOn { selection.insert(item.id) } else { selection.remove(item.id) }
                }
            ))
        }
    }
}
