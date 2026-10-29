import SwiftUI

struct CalendarsPane: View {
    @EnvironmentObject private var controller: BridgeController

    var body: some View {
        Form {
            Section {
                AccessRow(
                    title: "Calendar",
                    granted: controller.calendarGranted,
                    denied: BridgeController.isDenied(controller.calendarStatus),
                    grant: { await controller.requestCalendarAccess() },
                    openSettings: { controller.openPrivacySettings("Privacy_Calendars") }
                )
            }

            if controller.calendarGranted {
                if controller.calendars.isEmpty {
                    Section { Text("No calendars found.").foregroundStyle(.secondary) }
                }
                ForEach(groupedByAccount(controller.calendars, account: \.account), id: \.account) { group in
                    Section(group.account.isEmpty ? "Calendars" : group.account) {
                        ForEach(group.items) { calendar in
                            AccessLevelRow(
                                id: calendar.id, title: calendar.title, canWrite: calendar.writable,
                                readOnlyReason: "Calendar doesn't allow changes here, as with a subscribed "
                                    + "or holiday calendar, so it can only be read.",
                                exposed: $controller.exposedCalendars, writable: $controller.writableCalendars
                            ) {
                                Image(systemName: "circle.fill")
                                    .foregroundStyle(Color(hex: calendar.color) ?? .red)
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
