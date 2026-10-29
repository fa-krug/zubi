import SwiftUI

struct RemindersPane: View {
    @EnvironmentObject private var controller: BridgeController

    var body: some View {
        Form {
            Section {
                AccessRow(
                    title: "Reminders",
                    granted: controller.remindersGranted,
                    denied: BridgeController.isDenied(controller.reminderStatus),
                    grant: { await controller.requestRemindersAccess() },
                    openSettings: { controller.openPrivacySettings("Privacy_Reminders") }
                )
            }

            if controller.remindersGranted {
                if controller.reminderLists.isEmpty {
                    Section { Text("No lists found.").foregroundStyle(.secondary) }
                }
                ForEach(groupedByAccount(controller.reminderLists, account: \.account), id: \.account) { group in
                    Section(group.account.isEmpty ? "Lists" : group.account) {
                        ForEach(group.items) { list in
                            ExposureToggle(id: list.id, title: list.title, selection: $controller.exposedReminderLists) {
                                Image(systemName: "list.bullet.circle.fill")
                                    .foregroundStyle(.white, Color(hex: list.color) ?? .orange)
                            }
                        }
                    }
                }
                ReloadFooter()
            }
        }
        .formStyle(.grouped)
    }
}

/// Lists, calendars and folders are read when Settings asks, not watched, so new ones need this.
struct ReloadFooter: View {
    @EnvironmentObject private var controller: BridgeController

    var body: some View {
        Section {
        } footer: {
            HStack {
                Text("Only ticked items are served. Everything else answers 404.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Spacer()
                Button("Reload") {
                    Task { await controller.refreshAll(includeNotes: controller.notesConnected) }
                }
            }
        }
    }
}
