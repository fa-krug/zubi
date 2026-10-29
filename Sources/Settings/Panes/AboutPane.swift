import AppKit
import SwiftUI

struct AboutPane: View {
    @Environment(UpdateCoordinator.self) private var updates

    private static var version: String {
        let short = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "—"
        let build = Bundle.main.infoDictionary?["CFBundleVersion"] as? String ?? "—"
        return "Version \(short) (\(build))"
    }

    // Cached, and read from the bundle: the app icon is generic until LaunchServices registers.
    @MainActor private static let appIcon: NSImage = {
        if let name = Bundle.main.infoDictionary?["CFBundleIconFile"] as? String,
            let url = Bundle.main.url(forResource: name, withExtension: "icns"),
            let image = NSImage(contentsOf: url)
        {
            return image
        }
        return NSApp.applicationIconImage
    }()

    private static let iconSize: CGFloat = 88

    var body: some View {
        VStack(spacing: 0) {
            Form {
                Section {
                    hero
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, Theme.Spacing.lg)
                }
                links
            }
            .formStyle(.grouped)

            // Outside the form, so the copyright stays pinned to the bottom edge.
            footer
                .padding(.bottom, Theme.Spacing.xxl)
        }
    }

    private var hero: some View {
        VStack(spacing: Theme.Spacing.xl) {
            Image(nsImage: Self.appIcon)
                .resizable()
                .interpolation(.high)
                .frame(width: Self.iconSize, height: Self.iconSize)
                .shadow(color: .black.opacity(0.35), radius: 12, y: 6)

            VStack(spacing: Theme.Spacing.sm) {
                Text(Bundle.main.appDisplayName)
                    .font(.title.weight(.bold))
                Text(Self.version)
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
                    .padding(.horizontal, Theme.Spacing.md)
                    .padding(.vertical, 2)
                    .background(Capsule().fill(Theme.Colors.cardFill))
                    .overlay(Capsule().strokeBorder(Theme.Colors.cardStroke, lineWidth: 1))
                Button("Check for Updates") { updates.checkForUpdates() }
                    .buttonStyle(.link)
                    .font(.caption)
            }

            Text("Zubi is Basque for “bridge”: your Mac's Reminders, Calendar and Notes, served to the devices you choose.")
                .font(.callout)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: 360)
        }
    }

    private var links: some View {
        Section("Links") {
            ForEach(AboutLink.all) { link in
                AboutLinkRow(link: link)
            }
        }
    }

    private var footer: some View {
        Text("© 2026 Sascha Krug · Released under MIT")
            .font(.caption2)
            .foregroundStyle(.tertiary)
    }
}

/// One external destination in the About "Links" card.
private struct AboutLink: Identifiable {
    enum Glyph {
        case symbol(String)
        /// A brand mark from the asset catalog; SF Symbols ships no such logo.
        case brand(String)
    }

    let id: String
    let glyph: Glyph
    let title: String
    let detail: String
    let url: URL

    /// Every link is the repository `ReleaseFeed` reads, so the About page and the updater agree.
    static let all: [AboutLink] = [
        AboutLink(
            id: "github", glyph: .brand("BrandGitHub"), title: "GitHub",
            detail: "github.com/\(ReleaseFeed.repository)",
            url: URL(string: "https://github.com/\(ReleaseFeed.repository)")!),
        AboutLink(
            id: "releases", glyph: .symbol("sparkles"), title: "Release Notes",
            detail: "Every version and what changed",
            url: URL(string: "https://github.com/\(ReleaseFeed.repository)/releases")!),
        AboutLink(
            id: "issues", glyph: .symbol("ladybug"), title: "Report a Problem",
            detail: "Bug reports and feature requests",
            url: URL(string: "https://github.com/\(ReleaseFeed.repository)/issues/new/choose")!),
        AboutLink(
            id: "notices", glyph: .symbol("doc.text"), title: "Acknowledgements",
            detail: "MIT, and the open source Zubi is built on",
            url: URL(string: "https://github.com/\(ReleaseFeed.repository)/blob/main/NOTICE.md")!),
    ]
}

/// A row in the About "Links" card: glyph, title, destination and the arrow.
private struct AboutLinkRow: View {
    let link: AboutLink

    @State private var hovered = false

    var body: some View {
        Button {
            NSWorkspace.shared.open(link.url)
        } label: {
            LabeledContent {
                HStack(spacing: Theme.Spacing.sm) {
                    Text(link.detail)
                        .foregroundStyle(.tertiary)
                        .lineLimit(1)
                    Image(systemName: "arrow.up.right")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(hovered ? .secondary : .tertiary)
                }
            } label: {
                Label {
                    Text(link.title)
                } icon: {
                    glyph
                }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovered = $0 }
    }

    @ViewBuilder
    private var glyph: some View {
        switch link.glyph {
        case .symbol(let name):
            Image(systemName: name)
                .font(.system(size: 13, weight: .medium))
        case .brand(let name):
            // Brand marks paint edge to edge, so they sit under the symbol box to match.
            Image(name)
                .renderingMode(.template)
                .resizable()
                .scaledToFit()
                .frame(width: 14, height: 14)
        }
    }
}
