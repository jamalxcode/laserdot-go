import AppKit
import SwiftUI

/// A borderless floating panel that takes keyboard focus without
/// activating the app — the Spotlight-style overlay.
final class OverlayPanel: NSPanel {
    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { true }
}

final class OverlayController: NSObject, NSWindowDelegate {
    private let panel: OverlayPanel
    private let model: SearchModel

    init(model: SearchModel) {
        self.model = model
        panel = OverlayPanel(
            contentRect: NSRect(x: 0, y: 0, width: 720, height: 480),
            styleMask: [.borderless, .nonactivatingPanel, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        super.init()
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.hidesOnDeactivate = false
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
        panel.isOpaque = false
        panel.backgroundColor = .clear
        panel.hasShadow = true
        panel.isMovableByWindowBackground = true
        panel.delegate = self
        panel.contentView = NSHostingView(rootView: SearchView(model: model, close: { [weak self] in self?.hide() }))
    }

    func toggle() {
        panel.isVisible ? hide() : show()
    }

    func show() {
        let screen = NSScreen.screens.first { NSMouseInRect(NSEvent.mouseLocation, $0.frame, false) } ?? NSScreen.main
        if let f = screen?.visibleFrame {
            let size = panel.frame.size
            panel.setFrameOrigin(NSPoint(x: f.midX - size.width / 2, y: f.minY + f.height * 0.62 - size.height / 2))
        }
        panel.makeKeyAndOrderFront(nil)
        model.focusSearch() // the query field takes the keyboard each time
        model.connect()
    }

    func hide() {
        panel.orderOut(nil)
    }

    func windowDidResignKey(_ notification: Notification) {
        hide()
    }
}
