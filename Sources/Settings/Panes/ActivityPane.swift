import SwiftUI
import ZubiAPI

/// What devices have created and edited through the API, and with which key.
struct ActivityPane: View {
    @EnvironmentObject private var controller: BridgeController

    var body: some View {
        Form {
            Section {
                if controller.activity.isEmpty {
                    Text("Nothing has been created or edited through the API yet.")
                        .foregroundStyle(.secondary)
                }
                ForEach(controller.activity) { entry in
                    LabeledContent {
                        Text(entry.date.formatted(date: .abbreviated, time: .shortened))
                            .foregroundStyle(.secondary)
                    } label: {
                        Label {
                            Text(entry.title)
                            Text(detail(entry))
                        } icon: {
                            Image(systemName: Self.icon(entry.source))
                                .foregroundStyle(Self.tint(entry.source))
                        }
                    }
                }
            } header: {
                Text("Changes made through the API")
            } footer: {
                HStack(alignment: .top) {
                    Text("The last \(ActivityLog.limit) creates and edits, newest first, kept on this Mac only.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer()
                    Button("Clear") { controller.clearActivity() }
                        .disabled(controller.activity.isEmpty)
                }
            }
        }
        .formStyle(.grouped)
    }

    private func detail(_ entry: ActivityEntry) -> String {
        let action = entry.action == .created ? "Created" : "Edited"
        let key = entry.keyName.isEmpty ? "" : " · \(entry.keyName)"
        return "\(action) in \(entry.container)\(key)"
    }

    /// As the source's own Settings tab draws it.
    private static func icon(_ source: ChangeSource) -> String {
        tab(source).systemImage
    }

    private static func tint(_ source: ChangeSource) -> Color {
        tab(source).tint
    }

    private static func tab(_ source: ChangeSource) -> SettingsTab {
        switch source {
        case .reminders: .reminders
        case .calendar: .calendars
        case .notes: .notes
        }
    }
}
