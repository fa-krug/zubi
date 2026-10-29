import AppKit
import SwiftUI

/// The Software Update window: built on first show and torn down on close. Never quits the app.
@MainActor
final class UpdateWindowController: NSObject, NSWindowDelegate {
    private let title: String
    private let contentSize: CGSize
    private var window: NSWindow?

    init(title: String, contentSize: CGSize) {
        self.title = title
        self.contentSize = contentSize
    }

    func show<Content: View>(@ViewBuilder content: () -> Content) {
        if let window {
            raise(window)
            return
        }
        let hosting = NSHostingController(rootView: content())
        // Keep the window's size authoritative: an unconstrained fill would drive the frame.
        hosting.sizingOptions = []
        let window = NSWindow(
            contentRect: NSRect(origin: .zero, size: contentSize),
            styleMask: [.titled, .closable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        window.title = title
        // Edge-to-edge under a transparent titlebar, so it reads as one surface.
        window.titlebarAppearsTransparent = true
        window.titleVisibility = .hidden
        window.isMovableByWindowBackground = true
        // A menu bar app's raise never switches Space, so the window comes to the user instead.
        window.collectionBehavior.insert(.moveToActiveSpace)
        window.isReleasedWhenClosed = false
        window.isRestorable = false
        window.delegate = self
        window.contentViewController = hosting
        // `contentViewController` resets the frame to the controller's fitting size.
        window.setContentSize(contentSize)
        window.center()
        self.window = window
        raise(window)
    }

    @discardableResult
    func focus() -> Bool {
        guard let window else { return false }
        raise(window)
        return true
    }

    func close() {
        window?.close()
    }

    /// The title bar sits inside the frame but outside the layout area, so it is added back.
    func fitContent(width: CGFloat, height: CGFloat) {
        guard let window else { return }
        let titlebar = window.frame.height - window.contentLayoutRect.height
        let size = CGSize(width: width, height: height + titlebar)
        guard window.contentMinSize != size else { return }
        let top = window.frame.maxY
        window.contentMinSize = size
        window.setContentSize(size)
        var frame = window.frame
        frame.origin.y = top - frame.height
        window.setFrame(frame, display: true, animate: false)
    }

    func windowWillClose(_ notification: Notification) {
        window = nil
    }

    private func raise(_ window: NSWindow) {
        NSApp.activate(ignoringOtherApps: true)
        window.makeKeyAndOrderFront(nil)
    }
}
