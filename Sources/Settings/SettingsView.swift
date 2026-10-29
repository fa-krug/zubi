import SwiftUI

/// The window "Settings…" opens: a sidebar of tabs, as in System Settings and Blitz.
struct SettingsView: View {
    @EnvironmentObject private var controller: BridgeController
    #if DEBUG
    @State private var tab: SettingsTab = SettingsTab.debugLaunchTab ?? .general
    #else
    @State private var tab: SettingsTab = .general
    #endif

    static let sidebarWidth: CGFloat = 200

    var body: some View {
        NavigationSplitView {
            List(selection: selection) {
                ForEach(SettingsSection.allCases) { section in
                    Section(section.title) {
                        ForEach(section.tabs) { tab in
                            Label {
                                Text(tab.title)
                            } icon: {
                                SettingsTabIcon(systemImage: tab.systemImage, tint: tab.tint)
                            }
                            .tag(tab)
                        }
                    }
                }
            }
            .listStyle(.sidebar)
            // Pin the style before mounting; the implicit sidebar style paints icons a frame late.
            .labelStyle(.titleAndIcon)
            .toolbar(removing: .sidebarToggle)
            .navigationSplitViewColumnWidth(
                min: Self.sidebarWidth, ideal: Self.sidebarWidth, max: Self.sidebarWidth)
        } detail: {
            pane
                .frame(minWidth: 460, maxWidth: .infinity, maxHeight: .infinity)
        }
        .navigationTitle(tab.title)
        .frame(minWidth: 700, minHeight: 520)
        .onAppear {
            UserDefaults.standard.set(true, forKey: SettingsKey.settingsOpened)
            showRequestedTab()
        }
        .onChange(of: controller.requestedSettingsTab) { showRequestedTab() }
    }

    private func showRequestedTab() {
        guard let requested = controller.requestedSettingsTab else { return }
        tab = requested
        controller.requestedSettingsTab = nil
    }

    @ViewBuilder
    private var pane: some View {
        switch tab {
        case .general: GeneralPane()
        case .server: ServerPane()
        case .keys: KeysPane()
        case .activity: ActivityPane()
        case .reminders: RemindersPane()
        case .calendars: CalendarsPane()
        case .notes: NotesPane()
        case .about: AboutPane()
        }
    }

    /// `List` hands back an optional selection; a click on a section header must not clear it.
    private var selection: Binding<SettingsTab?> {
        Binding(get: { tab }, set: { if let new = $0 { tab = new } })
    }
}
