import AppKit

// A modal dialog blocks every Laserdot menu item until it is answered. If it
// opened behind another app's window the user would only see a greyed-out
// menu, so every dialog comes to the front and stays above other windows.

// It also stays put: a dialog panel normally hides whenever Laserdot is
// not the front app, and opens on the desktop Space it was made on, so with a
// browser in front (or full screen) it vanished behind it and could only be
// reached by closing other apps (the owner's report). Now it shows on every
// Space, over full-screen apps, and does not hide when another app is used.
extension NSWindow {
    func stayInFront() {
        hidesOnDeactivate = false
        level = .modalPanel
        collectionBehavior.insert([.canJoinAllSpaces, .fullScreenAuxiliary])
    }
}

/// Bring Laserdot forward (a menu-bar app is never the front app on its own).
func activateLaserdot() {
    NSApp.activate()
    NSApp.activate(ignoringOtherApps: true)
}

extension NSAlert {
    /// The dialog, set up to stay in front (also used by the self-test).
    func prepareFront() {
        layout()
        window.stayInFront()
        window.center()
    }

    @discardableResult
    func runFront() -> NSApplication.ModalResponse {
        activateLaserdot()
        prepareFront()
        window.orderFrontRegardless()
        return runModal()
    }
}

extension NSSavePanel {
    func runFront() -> NSApplication.ModalResponse {
        activateLaserdot()
        stayInFront()
        orderFrontRegardless()
        return runModal()
    }
}
