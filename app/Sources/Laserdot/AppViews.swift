import AppKit
import LaserdotKit
import SwiftUI

/// The menu-bar icon's menu.
struct StatusMenu: View {
    @ObservedObject var delegate: AppDelegate
    @ObservedObject var updates: UpdateController
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Button(L("menu.search")) { delegate.toggleOverlay() }
            .keyboardShortcut(.space, modifiers: .option)
        Button(L("menu.showWindow")) {
            NSApp.activate(ignoringOtherApps: true)
            openWindow(id: "main")
        }
        Button(L("menu.index")) { delegate.chooseFolder() }
        Button(L("menu.duplicates")) { delegate.showDuplicates() }
            .disabled(!delegate.dupesAvailable)
        Button(L("menu.storage")) { delegate.showStorage() }
        if let drives = delegate.drives {
            DrivesMenu(model: drives)
        }
        if let content = delegate.content {
            ContentMenu(model: content)
        }
        Divider()
        if let r = updates.ready {
            Button(String(format: L("update.button"), r.latest ?? "")) { updates.offerInstall() }
        }
        if updates.checking {
            Button(updates.statusText) { updates.showProgress() }
        } else {
            Button(L("menu.checkUpdates")) { updates.check(interactive: true) }
        }
        Button(L("menu.settings")) { delegate.showSettings() }
            .keyboardShortcut(",")
        Divider()
        Text(delegate.statusLine)
        Text(delegate.networkLine)
        if !delegate.watcherLine.isEmpty {
            Text(delegate.watcherLine)
        }
        Divider()
        Button(L("menu.quit")) { NSApp.terminate(nil) }
            .keyboardShortcut("q")
    }
}

/// The main window: search, and one status bar (results, index size,
/// network, update) — standard window chrome, not the overlay's card.
struct MainWindow: View {
    @ObservedObject var delegate: AppDelegate
    @ObservedObject var updates: UpdateController
    @Environment(\.openSettings) private var openSettings

    var body: some View {
        VStack(spacing: 0) {
            if let model = delegate.model {
                SearchView(model: model, close: { model.query = "" }, fixedSize: false)
            } else {
                ProgressView(L("daemon.starting")).frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            Divider()
            HStack(spacing: 12) {
                if let model = delegate.model {
                    StatusSummary(model: model)
                    Spacer()
                    ShortcutBar(model: model).font(.caption)
                }
                Spacer()
                if updates.checking {
                    ProgressView().controlSize(.small)
                    Button(updates.statusText) { updates.showProgress() }
                        .buttonStyle(.link)
                }
                if let r = updates.ready {
                    Button(String(format: L("update.button"), r.latest ?? "")) { updates.offerInstall() }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.small)
                }
                if delegate.whatsNewPending {
                    Button(L("menu.whatsNew")) { delegate.showWhatsNew() }
                        .controlSize(.small)
                }
                Text(delegate.statusLine)
                if let model = delegate.model, !delegate.networkLine.isEmpty {
                    NetworkLock(blocked: model.networkBlocked)
                }
            }
            .font(.callout)
            .foregroundStyle(.secondary)
            .padding(.horizontal, 12)
            .padding(.vertical, 6)
        }
        .frame(minWidth: 640, minHeight: 420)
        // SwiftUI's way to open Settings, kept for the menu-bar menu, `>`
        // commands and the app menu.
        .onAppear { delegate.openSettingsAction = { openSettings() } }
        .sheet(isPresented: $delegate.showTour) { WelcomeTour { delegate.tourClosed() } }
    }
}

/// The menu bar: Check for Updates in the app menu (Settings… comes from the
/// Settings scene), File (index, duplicates), Edit ▸ Find (⌘F), Library
/// (drives, content search) and Help (the search syntax).
struct LaserdotCommands: Commands {
    @ObservedObject var delegate: AppDelegate
    @ObservedObject var updates: UpdateController
    @Environment(\.openWindow) private var openWindow

    var body: some Commands {
        CommandGroup(after: .appInfo) {
            Button(L("menu.checkUpdates")) { updates.check(interactive: true) }
                .disabled(updates.checking)
            if let r = updates.ready {
                Button(String(format: L("update.button"), r.latest ?? "")) { updates.offerInstall() }
            }
        }
        CommandGroup(after: .undoRedo) {
            // ⌥⌘Z works in the search window; this is the same from the menu bar.
            Button(L("menu.undoFile")) { delegate.model?.undo() }
        }
        CommandGroup(after: .pasteboard) {
            Divider()
            Button(L("menu.find")) {
                openWindow(id: "main")
                delegate.model?.focusSearch()
            }
            .keyboardShortcut("f")
        }
        CommandGroup(replacing: .help) {
            Button(L("menu.syntax")) {
                openWindow(id: "main")
                delegate.model?.showCheatSheet = true
            }
            Button(L("menu.tour")) {
                openWindow(id: "main")
                delegate.showTour = true
            }
            Button(L("menu.whatsNew")) { delegate.showWhatsNew() }
            Divider()
            Button(L("menu.diagnostics")) { delegate.saveDiagnostics() }
        }
        CommandGroup(replacing: .newItem) {
            Button(L("menu.search")) { delegate.toggleOverlay() }
            Button(L("menu.index")) { delegate.chooseFolder() }
                .keyboardShortcut("o")
            Button(L("menu.duplicates")) { delegate.showDuplicates() }
                .disabled(!delegate.dupesAvailable)
                .keyboardShortcut("u", modifiers: [.command, .shift])
        }
        CommandMenu(L("menu.library")) {
            Button(L("menu.storage")) { delegate.showStorage() }
                .keyboardShortcut("s", modifiers: [.command, .shift])
            Divider()
            if let drives = delegate.drives {
                DrivesMenu(model: drives)
            }
            if let content = delegate.content {
                ContentMenu(model: content)
            }
        }
    }
}
