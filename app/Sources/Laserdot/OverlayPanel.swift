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
            styleMask: [.borderless, .nonactivatingPanel, .fullSizeContentView, .resizable],
            backing: .buffered,
            defer: false
        )
        super.init()
        // The window stays open until it is closed (the owner, 0.9.46: "keep
        // the window open until I close it, even when I open one of the
        // listed files" — this replaces 0.9.33's hide-on-open): opening a
        // result or switching to another app leaves it where it is, behind
        // the app that came forward (see windowDidResignKey).
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.hidesOnDeactivate = false
        // It stays on the desktop (Space) it was opened on, and comes to the
        // current one only when it is called again — ⌥Space or the menu bar
        // (the owner: "don't let it follow when I move, unless I access the
        // menu bar again"). It used to join every Space.
        panel.collectionBehavior = [.moveToActiveSpace, .fullScreenAuxiliary]
        panel.isOpaque = false
        panel.backgroundColor = .clear
        panel.hasShadow = true
        panel.isMovableByWindowBackground = true
        panel.delegate = self
        let host = NSHostingView(rootView: SearchView(model: model, close: { [weak self] in self?.hide() })
            .environment(\.layoutDirection, appLayoutDirection))
        host.sizingOptions = [] // the panel's size is the user's, not SwiftUI's
        panel.contentView = host
        // Resizable from its edges; the size is remembered.
        panel.minSize = NSSize(width: 560, height: SearchView.compactHeight)
        panel.setContentSize(NSSize(width: savedSize.width, height: SearchView.compactHeight))
        // A slim bar until there is something to show, then the full panel;
        // the top edge stays put, as Spotlight's does.
        sizing = model.$compact.removeDuplicates().sink { [weak self] compact in
            DispatchQueue.main.async { self?.fit(compact: compact) }
        }
    }

    private var sizing: AnyCancellable?
    private var isCompact = true

    /// The full size the user left the overlay at (920×520 at first).
    private var savedSize: NSSize {
        let d = UserDefaults.standard
        let w = d.double(forKey: "laserdot.overlay.width"), h = d.double(forKey: "laserdot.overlay.height")
        return NSSize(width: w >= 560 ? w : 920, height: h >= 300 ? h : SearchView.fullHeight)
    }

    private func fit(compact: Bool) {
        isCompact = compact
        let size = savedSize
        let height = compact ? SearchView.compactHeight : size.height
        var f = panel.frame
        guard f.height != height || f.width != size.width else { return }
        f.origin.y += f.height - height
        f.origin.x += (f.width - size.width) / 2
        f.size = NSSize(width: size.width, height: height)
        positioning = true
        panel.setFrame(f, display: true, animate: panel.isVisible)
        positioning = false
    }

    /// Set while Laserdot moves the panel itself (not the user dragging it).
    private var positioning = false

    /// The top-left corner the user dragged the overlay to (nil: centred).
    private var savedTopLeft: NSPoint? {
        let d = UserDefaults.standard
        guard d.object(forKey: "laserdot.overlay.x") != nil else { return nil }
        return NSPoint(x: d.double(forKey: "laserdot.overlay.x"), y: d.double(forKey: "laserdot.overlay.y"))
    }

    /// The user dragged the overlay: open it there next time.
    func windowDidMove(_ notification: Notification) {
        guard !positioning, panel.isVisible, panel.inLiveResize == false, NSEvent.pressedMouseButtons != 0 else { return }
        let f = panel.frame
        UserDefaults.standard.set(Double(f.minX), forKey: "laserdot.overlay.x")
        UserDefaults.standard.set(Double(f.maxY), forKey: "laserdot.overlay.y")
    }

    func windowDidEndLiveResize(_ notification: Notification) {
        let s = panel.frame.size
        UserDefaults.standard.set(Double(s.width), forKey: "laserdot.overlay.width")
        if !isCompact { UserDefaults.standard.set(Double(s.height), forKey: "laserdot.overlay.height") }
    }

    /// ⌥Space or the menu: hide it when it is on this desktop; otherwise
    /// bring it here (from another desktop, or hidden).
    func toggle() {
        panel.isVisible && panel.isOnActiveSpace && panel.isKeyWindow ? hide() : show()
    }

    func show() {
        let screen = NSScreen.screens.first { NSMouseInRect(NSEvent.mouseLocation, $0.frame, false) } ?? NSScreen.main
        if let f = screen?.visibleFrame {
            let size = panel.frame.size
            positioning = true
            if let top = savedTopLeft, f.contains(top), f.contains(NSPoint(x: top.x + size.width, y: top.y - SearchView.compactHeight)) {
                // Where the user last dragged it, as Spotlight remembers.
                panel.setFrameOrigin(NSPoint(x: top.x, y: top.y - size.height))
            } else {
                // The top edge sits at the same place whatever the height.
                let topY = f.minY + f.height * 0.8
                panel.setFrameOrigin(NSPoint(x: f.midX - size.width / 2, y: max(f.minY, topY - size.height)))
            }
            positioning = false
        }
        panel.makeKeyAndOrderFront(nil)
        model.focusSearch() // the query field takes the keyboard each time
        // As in Spotlight: the last search is still there, all selected, so
        // typing replaces it and ↓ or Return reuse it.
        if !model.query.isEmpty {
            model.selectAllRequest &+= 1
            // Fresh results, not the ones from when it was closed: files
            // unzipped, moved or deleted meanwhile are gone (the owner saw
            // zips that had been unzipped and removed).
            model.queryChanged()
        }
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
        // Another app (or a file just opened) came forward: the window stays
        // open, but no longer floats over it; it floats again when it takes
        // the keyboard back (a click on it, ⌥Space, the menu).
        DispatchQueue.main.async { [weak self] in
            guard let self, !QuickLook.shared.isVisible, !self.panel.isKeyWindow else { return }
            self.panel.level = .normal
        }
    }

    func windowDidBecomeKey(_ notification: Notification) {
        panel.level = .floating
    }
}
