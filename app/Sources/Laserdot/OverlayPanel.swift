import AppKit
import Combine
import SwiftUI

/// A borderless floating panel that takes keyboard focus without
/// activating the app — the Spotlight-style overlay.
final class OverlayPanel: NSPanel {
    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { true }
}

final class OverlayController: NSObject, NSWindowDelegate {
    let panel: OverlayPanel
    private let model: SearchModel

    init(model: SearchModel) {
        self.model = model
        panel = OverlayPanel(
            contentRect: NSRect(x: 0, y: 0, width: 920, height: 520),
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
        panel.contentView = NSHostingView(rootView: SearchView(model: model, close: { [weak self] in self?.hide() })
            .environment(\.layoutDirection, appLayoutDirection))
        // A slim bar until there is something to show, then the full panel;
        // the top edge stays put, as Spotlight's does.
        sizing = model.$compact.removeDuplicates().sink { [weak self] compact in
            DispatchQueue.main.async { self?.fit(compact: compact) }
        }
    }

    private var sizing: AnyCancellable?

    private func fit(compact: Bool) {
        let height = compact ? SearchView.compactHeight : SearchView.fullHeight
        var f = panel.frame
        guard f.height != height else { return }
        f.origin.y += f.height - height
        f.size.height = height
        panel.setFrame(f, display: true, animate: panel.isVisible)
    }

    func toggle() {
        panel.isVisible ? hide() : show()
    }

    func show() {
        let screen = NSScreen.screens.first { NSMouseInRect(NSEvent.mouseLocation, $0.frame, false) } ?? NSScreen.main
        if let f = screen?.visibleFrame {
            // The top edge sits at the same place whatever the height.
            let size = panel.frame.size
            let top = f.minY + f.height * 0.8
            panel.setFrameOrigin(NSPoint(x: f.midX - size.width / 2, y: max(f.minY, top - size.height)))
        }
        panel.makeKeyAndOrderFront(nil)
        model.focusSearch() // the query field takes the keyboard each time
        // When Quick Look closes, the overlay takes the keyboard back.
        QuickLook.shared.onClose = { [weak self] in
            guard let self, self.panel.isVisible else { return }
            self.panel.makeKeyAndOrderFront(nil)
        }
        model.connect()
    }

    func hide() {
        panel.orderOut(nil)
    }

    func windowDidResignKey(_ notification: Notification) {
        // Quick Look takes the keyboard while it shows a result: stay.
        DispatchQueue.main.async { [weak self] in
            guard let self, !QuickLook.shared.isVisible, !self.panel.isKeyWindow else { return }
            self.hide()
        }
    }
}
