import AppKit
import LaserdotKit
import Security

/// The self-test after the overlay steps: Storage, Duplicates, Settings, the
/// main window, Keychain, pausing, resizing, and last Erase All Data.
extension SelfTestRunner {
    func mainWindowAndSettings(model: SearchModel, overlay: OverlayController) {
        // Remembered passphrases: saved, read back, forgotten (login Keychain).
        let status = Keychain.save("selftest-secret", for: "laserdot-selftest")
        if status != errSecSuccess {
            extra["keychain"] = "unavailable \(status)"
        } else {
            let read = Keychain.load(for: "laserdot-selftest")
            let counted = Keychain.count()
            Keychain.forget("laserdot-selftest")
            let gone = Keychain.load(for: "laserdot-selftest") == nil
            extra["keychain"] = read == "selftest-secret" && counted >= 1 && gone ? "ok" : "failed read=\(read != nil) count=\(counted) gone=\(gone)"
        }
        extra["ready_ms"] = app.readyMS
        // What the one-time permission step would ask (not asked here: no one
        // is at the CI machine to answer).
        extra["permission_places"] = Permissions.places().map(\.path)
        if let engine = app.engineForTests {
            extra["unreadable"] = (try? engine.unreadable())?.items.map { relative($0.path) } ?? ["?"]
        }
        // Battery: background work pauses and resumes.
        if let engine = app.engineForTests {
            extra["paused_on"] = engine.setBackground(paused: true) ?? false
            extra["paused_off"] = engine.setBackground(paused: false) ?? true
        }
        // Resizing the overlay is remembered.
        model.query = "jpg"
        var f = overlay.panel.frame
        f.size = NSSize(width: 1000, height: 600)
        overlay.panel.setFrame(f, display: true)
        overlay.windowDidEndLiveResize(Notification(name: NSWindow.didEndLiveResizeNotification, object: overlay.panel))
        extra["saved_width"] = UserDefaults.standard.double(forKey: "laserdot.overlay.width")
        extra["saved_height"] = UserDefaults.standard.double(forKey: "laserdot.overlay.height")
        extra["privacy_items"] = PrivacySettings.storedItems().map { ["id": $0.id, "bytes": $0.bytes] as [String: Any] }
        overlay.hide()
        windowsTour(model: model) { self.mainWindowAndSettingsRest(model: model) }
    }

    /// Storage and Duplicates windows, and the Settings tabs: screenshots and
    /// what they show.
    func windowsTour(model: SearchModel, done: @escaping () -> Void) {
        app.showStorage()
        waitFor(5, { self.app.storageModel?.report != nil }) {
            if let w = NSApp.windows.first(where: { $0.isVisible && $0.title == L("menu.storageTitle") }) {
                self.screenshot(w, name: "storage")
            }
            self.extra["storage_items"] = self.app.storageModel?.report?.items.count ?? -1
            self.app.showDuplicates()
            self.app.duplicatesModel?.scan()
            self.waitFor(12, { self.app.duplicatesModel?.report != nil }) {
                if let w = NSApp.windows.first(where: { $0.isVisible && $0.title == L("dupes.title") }) {
                    self.screenshot(w, name: "duplicates")
                }
                self.extra["duplicate_groups"] = self.app.duplicatesModel?.report?.groups.count ?? -1
                // Cleaning up has its own button and a confirmation: the
                // chosen copies, Select None / All, then the dry run.
                if let dm = self.app.duplicatesModel {
                    self.extra["dupes_button"] = dm.moveButtonTitle
                    dm.selectNone()
                    self.extra["dupes_none"] = dm.selectedCount
                    dm.selectAllExtras()
                    self.extra["dupes_all"] = dm.selectedCount
                    dm.review()
                }
                self.waitFor(10, { self.app.duplicatesModel?.preview != nil || self.app.duplicatesModel?.message != nil }) {
                if let dm = self.app.duplicatesModel {
                    self.extra["dupes_preview_files"] = dm.preview?.files ?? -1
                    self.extra["dupes_preview_error"] = dm.message ?? ""
                    if let w = NSApp.windows.first(where: { $0.isVisible && $0.title == L("dupes.title") }) {
                        self.screenshot(w, name: "duplicates-confirm")
                    }
                    dm.preview = nil // Cancel: nothing moves
                }
                UserDefaults.standard.set("folders", forKey: "laserdot.settingsTab")
                self.app.showSettings()
                self.after(1.5) {
                    if let w = self.settingsWindow() { self.screenshot(w, name: "settings-folders") }
                    UserDefaults.standard.set("drives", forKey: "laserdot.settingsTab")
                    self.after(1) {
                        if let w = self.settingsWindow() { self.screenshot(w, name: "settings-drives") }
                        UserDefaults.standard.set("privacy", forKey: "laserdot.settingsTab")
                    }
                    self.after(2.5) {
                        if let w = self.settingsWindow() { self.screenshot(w, name: "settings-privacy") }
                        UserDefaults.standard.set("general", forKey: "laserdot.settingsTab")
                        self.settingsWindow()?.close()
                        for title in [L("menu.storageTitle"), L("dupes.title")] {
                            NSApp.windows.first { $0.title == title }?.close()
                        }
                        self.updateWindow(done: done)
                    }
                }
                }
            }
        }
    }

    /// The update window part way through a download: a filled bar and
    /// "x MB of y MB" (no real update is fetched).
    func updateWindow(done: @escaping () -> Void) {
        extra["launch_phases"] = LaunchTimer.summary
        // The install prompt fits a laptop screen however long the notes are,
        // and its buttons show.
        let longNotes = (1...300).map { "- Change number \($0): a line of what is new in this version" }.joined(separator: "\n")
        let prompt = UpdateController.installAlert(version: "9.9.9", current: "0.9.0", notes: longNotes)
        prompt.layout()
        extra["install_prompt_height"] = Double(prompt.window.frame.height)
        extra["install_prompt_buttons"] = prompt.buttons.count
        // The Duplicates window shows the step, a bar and the count.
        if let dm = app.duplicatesModel {
            dm.showProgressForTests(#"{"phase":"full","done":1200,"total":5000,"current":"/Users/x/Pictures/2019/IMG_1234.HEIC"}"#)
            extra["dupes_progress_file"] = dm.progressFile ?? ""
            extra["dupes_progress_title"] = dm.progressTitle
            extra["dupes_progress_detail"] = dm.progressDetail
            extra["dupes_progress_fraction"] = dm.progressFraction ?? -1
            dm.showProgressForTests(nil)
        }
        // Indexing says how many files it has found so far (a big folder
        // takes a while; the count shows it is moving).
        let found = #"{"root":"/x/Movies","phase":"scanning","done":412000,"total":0}"#
        let details = #"{"root":"","phase":"details","done":1200,"total":80000}"#
        for (key, json) in [("progress_found", found), ("progress_details", details)] {
            if let p = try? JSONDecoder().decode(Wire.IndexProgress.self, from: Data(json.utf8)) {
                extra[key] = AppDelegate.progressText(p)
            }
        }
        let u = app.updates
        u.showStepForTests(.downloading(received: 18_400_000, total: 41_000_000), version: "9.9.9")
        after(1) {
            let w = u.progressPanelForTests
            if let w { self.screenshot(w, name: "update-progress") }
            self.extra["update_window_visible"] = w?.isVisible ?? false
            self.extra["update_step"] = u.step?.title ?? ""
            self.extra["update_detail"] = u.step?.detail ?? ""
            self.extra["update_menu"] = u.statusText
            // Cancel stops the download and closes the window.
            u.cancelCheck()
            self.extra["update_cancelled"] = u.step == nil && !u.checking
            self.extra["update_window_closes"] = !(w?.isVisible ?? false)
            done()
        }
    }

    func settingsWindow() -> NSWindow? {
        NSApp.windows.first(where: {
            $0.isVisible && ($0.identifier?.rawValue.contains("Settings") == true
                || (!($0 is NSPanel) && $0.title != "Laserdot" && $0.frame.width > 300 && $0.title != L("menu.storageTitle") && $0.title != L("dupes.title")))
        })
    }

    func mainWindowAndSettingsRest(model: SearchModel, overlay: OverlayController? = nil) {
        model.query = "proj"
        after(2) {
            if let main = NSApp.windows.first(where: { !($0 is NSPanel) && $0.isVisible && $0.title == "Laserdot" }) {
                self.capture("main", model: model, window: main)
            }
            self.app.showSettings()
            self.after(1.5) {
                // The Settings window: visible, not the main window, not the
                // menu-bar icon's tiny status window.
                if let settings = self.settingsWindow() {
                    self.screenshot(settings, name: "settings")
                }
                self.eraseAndCheck()
                self.finish(error: nil)
            }
        }
    }

    /// Settings ▸ Privacy ▸ Erase All Laserdot Data, last (without the
    /// relaunch): the data folder, the passphrases and the settings go, the
    /// daemon stops and stays stopped.
    func eraseAndCheck() {
        guard let engine = app.engineForTests else { return }
        Keychain.save("selftest-secret", for: "laserdot-erase-check")
        let dir = (Paths.defaultDbPath as NSString).deletingLastPathComponent
        let before = FileManager.default.fileExists(atPath: dir)
        app.eraseAll(relaunch: false)
        var r: [String: Any] = [:]
        r["data_before"] = before
        r["data_gone"] = !FileManager.default.fileExists(atPath: dir)
        r["daemon_stopped"] = DaemonClient.connectLive(socketPath: engine.socketPath) == nil
        r["passphrases"] = Keychain.count()
        r["welcomed"] = UserDefaults.standard.bool(forKey: "laserdot.welcomed")
        r["no_reindex"] = UserDefaults.standard.bool(forKey: "laserdot.defaultsIndexed")
        r["history_gone"] = UserDefaults.standard.stringArray(forKey: "laserdot.history") == nil
        extra["erase"] = r
    }
}
