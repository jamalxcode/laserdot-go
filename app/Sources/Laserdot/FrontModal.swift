import AppKit

// A modal dialog blocks every Laserdot menu item until it is answered. If it
// opened behind another app's window the user would only see a greyed-out
// menu, so every dialog comes to the front and stays above other windows.

extension NSAlert {
    @discardableResult
    func runFront() -> NSApplication.ModalResponse {
        NSApp.activate(ignoringOtherApps: true)
        layout()
        window.level = .modalPanel
        window.center()
        window.orderFrontRegardless()
        return runModal()
    }
}

extension NSSavePanel {
    func runFront() -> NSApplication.ModalResponse {
        NSApp.activate(ignoringOtherApps: true)
        level = .modalPanel
        orderFrontRegardless()
        return runModal()
    }
}
