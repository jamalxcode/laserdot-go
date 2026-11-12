import AppKit
import LaserdotKit
import Security

/// CI's check of the real, bundled app (LASERDOT_SELFTEST=<output folder>,
/// LASERDOT_SELFTEST_FOLDER=<files to index>). It does what a person does —
/// opens the ⌥Space overlay and types into it with key events — then records
/// what the screen shows (the hint, the sections and the result rows, read
/// through the accessibility tree) and saves screenshots, writes
/// report.json, and quits. app/scripts/check-selftest.py judges the report.
/// Nothing here runs in normal use.
enum SelfTest {
    static let outDir: URL? = ProcessInfo.processInfo.environment["LASERDOT_SELFTEST"].map { URL(fileURLWithPath: $0) }
    static var active: Bool { outDir != nil }
}

final class SelfTestRunner {
    private let app: AppDelegate
    private let out: URL
    private var steps: [[String: Any]] = []
    private var shots: [String] = []
    private var closeAttempts = 0
    private var extra: [String: Any] = [:]

    init(app: AppDelegate, out: URL) {
        self.app = app
        self.out = out
    }

    func start() {
        guard let engine = app.engineForTests, let folder = ProcessInfo.processInfo.environment["LASERDOT_SELFTEST_FOLDER"] else {
            finish(error: "no engine or LASERDOT_SELFTEST_FOLDER")
            return
        }
        extra["launch_ms"] = app.launchMS
        // A drive that never answers (a dead network mount) must not hold
        // Laserdot up: the permission step skips it after its time limit.
        let started = Date()
        Permissions.askAll(places: [Permissions.Place(name: "hung", path: "/hung", drive: true),
                                    Permissions.Place(name: "refused", path: "/refused")],
                           open: { path in
                               if path == "/hung" { Thread.sleep(forTimeInterval: 600) }
                               return false
                           },
                           done: { refused in
                               self.extra["hung_place_seconds"] = Int(Date().timeIntervalSince(started))
                               self.extra["refused_places"] = refused.map(\.name)
                           })
        DispatchQueue.global().async {
            // The fixtures' words are searchable too (content search).
            _ = try? engine.setContentFolder(folder: folder, add: true)
            let indexed = Result { try engine.index(path: folder) }
            try? engine.updateContent()
            DispatchQueue.main.async {
                if case let .failure(e) = indexed {
                    self.finish(error: "index failed: \(e)")
                    return
                }
                self.run()
            }
        }
    }

    // MARK: the script

    /// One step: reset the query, optionally prepare, type, optionally act,
    /// then record the screen.
    private struct Step {
        let name: String
        let typed: String
        var before: (() -> Void)?
        var after: (() -> Void)?
        /// What the screen must show before it is recorded (files changed
        /// on disk reach the index through the watcher; this waits for it).
        var until: ((SearchModel) -> Bool)?
    }

    private func run() {
        guard let overlay = app.overlayForTests, let model = app.model, let engine = app.engineForTests else {
            finish(error: "no overlay")
            return
        }
        let folder = ProcessInfo.processInfo.environment["LASERDOT_SELFTEST_FOLDER"] ?? ""
        let steps = [
            Step(name: "jpg", typed: "jpg"),
            Step(name: "proj", typed: "proj"),
            Step(name: "tool", typed: "tool"),
            Step(name: "pdftoday", typed: "pdf today"),
            Step(name: "commands", typed: ">dup"),
            Step(name: "actions", typed: "jpg", after: { model.toggleActions() }),
            // ↓ into the results, then Space through the app's event queue
            // (as a real key press arrives): Quick Look opens.
            Step(name: "space", typed: "jpg", after: {
                self.key(String(UnicodeScalar(UInt16(NSDownArrowFunctionKey))!), code: 125, into: overlay.panel)
                self.post(" ", code: 49, to: overlay.panel)
            }),
            // A second space in a row previews without ↓.
            // (The key path itself is the "space" step; a second posted key
            // event can be lost in CI, so this one asks the field's handler.)
            Step(name: "spacespace", typed: "jpg ", after: { _ = model.spaceKey() }),
            // A video and a song: their previews play in the pane (AVKit).
            Step(name: "video", typed: "clip"),
            Step(name: "song", typed: "song"),
            // Sorted by size: one list, largest first.
            Step(name: "sorted", typed: "kind:image", before: { model.sort = .size }),
            // ⇧↓ selects a second result.
            Step(name: "multi", typed: "jpg", before: { model.sort = .relevance }, after: { model.extend(1) }),
            // ⌘K ▸ Move to Trash removes a file; a new search no longer finds it.
            Step(name: "trash", typed: "trashme", after: {
                model.actions.first { $0.id == "trash" }?.run()
            }),
            Step(name: "trashed", typed: "trashme", until: { $0.results.isEmpty }),
            // Undo (the banner's button, ⌥⌘Z) brings it back from the Trash.
            Step(name: "untrashed", typed: "trashme", before: { model.undo() }, until: { $0.results.map(\.name) == ["trashme.txt"] }),
            // Rename, then Move to…, then Undo puts it back where it was.
            Step(name: "rename", typed: "renameme", after: {
                if let h = model.results.first(where: { $0.name == "renameme.txt" }) {
                    model.renameFile(h, to: "renamed ok.txt")
                }
            }),
            Step(name: "renamed", typed: "renamed ok", until: { $0.results.map(\.name) == ["renamed ok.txt"] }),
            Step(name: "moveto", typed: "renamed ok", before: {
                try? FileManager.default.createDirectory(atPath: folder + "/Moved Here", withIntermediateDirectories: true)
            }, after: {
                if let h = model.results.first(where: { $0.name == "renamed ok.txt" }) {
                    model.moveFiles([h], to: URL(fileURLWithPath: folder + "/Moved Here"))
                }
            }),
            Step(name: "movedto", typed: "renamed ok", until: { $0.results.first?.path.contains("/Moved Here/") == true }),
            Step(name: "moveundone", typed: "renamed ok", before: { model.undo() },
                 until: { $0.results.count == 1 && !$0.results[0].path.contains("/Moved Here/") }),
            // Compress makes beach.jpg.zip beside it; Undo moves the zip to the Trash.
            Step(name: "compress", typed: "beach", after: {
                if let h = model.results.first(where: { $0.name == "beach.jpg" }) { model.compressFiles([h]) { _ in } }
            }),
            Step(name: "compressed", typed: "zip", until: { $0.results.contains { $0.name == "beach.jpg.zip" } }),
            Step(name: "compressundone", typed: "zip", before: { model.undo() },
                 until: { !$0.results.contains { $0.name == "beach.jpg.zip" } }),
            // The words inside a document: the preview shows where.
            Step(name: "snippet", typed: "zebra", until: { $0.snippet != nil }),
            // ⌘K ▸ Tags…: a Finder tag, searchable; Undo takes it off.
            Step(name: "tag", typed: "beach", after: {
                if let h = model.results.first(where: { $0.name == "beach.jpg" }) {
                    model.setTags([h], adding: ["Red"], removing: [])
                }
            }),
            Step(name: "tagged", typed: "tag:red", until: { $0.results.map(\.name) == ["beach.jpg"] }),
            Step(name: "untagged", typed: "tag:red", before: { model.undo() }, until: { $0.results.isEmpty }),
            // ↑ in the empty box brings back the last search.
            Step(name: "history", typed: "", before: {
                model.query = "holiday"
                model.remember()
                model.query = ""
            }, after: { model.move(-1) }),
            // A folder dropped on the window: search inside it.
            Step(name: "drop", typed: "", before: {
                _ = model.handleDrop([NSItemProvider(object: URL(fileURLWithPath: folder + "/Projects") as NSURL)])
            }, until: { $0.query.hasPrefix("in:") && !$0.results.isEmpty }),
            // ⌘K ▸ Search in This Folder.
            Step(name: "searchin", typed: "", before: { model.searchInFolder(folder + "/Projects") }),
            // A laserdot:// link opens the overlay with its search.
            Step(name: "url", typed: "", before: { self.app.handle(URL(string: "laserdot://search?q=beach")!) }),
            // A swapped-letter typo finds the file, and says so.
            Step(name: "similar", typed: "pfd"),
            Step(name: "home", typed: "", before: {
                engine.recordOpen(path: folder + "/beach.jpg")
                model.loadHome()
            }),
        ]
        overlay.show()
        after(1) {
            // Nothing typed, nothing opened yet: the slim bar.
            self.capture("start", model: model, window: overlay.panel)
            self.runSteps(steps[...], model: model, overlay: overlay)
        }
    }

    private func runSteps(_ steps: ArraySlice<Step>, model: SearchModel, overlay: OverlayController) {
        guard let step = steps.first else {
            mainWindowAndSettings(model: model, overlay: overlay)
            return
        }
        // Start each step with Quick Look really closed.
        guard !QuickLook.shared.isVisible || closeAttempts > 10 else {
            closeAttempts += 1
            QuickLook.shared.close()
            after(0.3) { self.runSteps(steps, model: model, overlay: overlay) }
            return
        }
        closeAttempts = 0
        model.query = ""
        after(0.5) {
            step.before?()
            self.type(step.typed, into: overlay.panel) {
                self.settle(model, until: step.until) {
                    step.after?()
                    self.after(step.after == nil ? 0.1 : 0.6) {
                        self.capture(step.name, model: model, window: overlay.panel)
                        let next = { self.runSteps(steps.dropFirst(), model: model, overlay: overlay) }
                        // CI reads the screen with an accessibility client
                        // (app/scripts/ax-dump.swift) while this is shown.
                        if step.name == "jpg", ProcessInfo.processInfo.environment["LASERDOT_SELFTEST_HOLD"] != nil {
                            self.hold(then: next)
                        } else {
                            next()
                        }
                    }
                }
            }
        }
    }

    private func mainWindowAndSettings(model: SearchModel, overlay: OverlayController) {
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
    private func windowsTour(model: SearchModel, done: @escaping () -> Void) {
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
                        done()
                    }
                }
            }
        }
    }

    private func settingsWindow() -> NSWindow? {
        NSApp.windows.first(where: {
            $0.isVisible && ($0.identifier?.rawValue.contains("Settings") == true
                || (!($0 is NSPanel) && $0.title != "Laserdot" && $0.frame.width > 300 && $0.title != L("menu.storageTitle") && $0.title != L("dupes.title")))
        })
    }

    private func mainWindowAndSettingsRest(model: SearchModel, overlay: OverlayController? = nil) {
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
    private func eraseAndCheck() {
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

    // MARK: helpers

    /// Write `hold`, then wait (at most two minutes) for CI to write `release`.
    private func hold(then next: @escaping () -> Void) {
        try? Data().write(to: out.appendingPathComponent("hold"))
        let release = out.appendingPathComponent("release")
        func poll(_ waited: Double) {
            if FileManager.default.fileExists(atPath: release.path) || waited > 120 {
                next()
            } else {
                after(0.5) { poll(waited + 0.5) }
            }
        }
        poll(0)
    }

    /// Wait until the results stop changing (at most 2.5 s), or until
    /// `until` holds (at most 8 s, searching again now and then so a change
    /// the watcher has just indexed shows up).
    private func settle(_ model: SearchModel, until: ((SearchModel) -> Bool)?, done: @escaping () -> Void) {
        let started = Date()
        var lastID = model.resultsID, stableSince = Date(), lastRequery = Date()
        func poll() {
            let now = Date()
            if model.resultsID != lastID { lastID = model.resultsID; stableSince = now }
            let waited = now.timeIntervalSince(started)
            let stable = !model.searching && now.timeIntervalSince(stableSince) >= 0.3 && waited >= 0.4
            let ok = until?(model) ?? true
            if (stable && ok) || waited > (until == nil ? 2.5 : 8) {
                done()
                return
            }
            if stable && !ok && now.timeIntervalSince(lastRequery) > 1 {
                lastRequery = now
                model.queryChanged()
            }
            after(0.1, poll)
        }
        poll()
    }

    /// Wait until `ready` holds, at most `limit` seconds.
    private func waitFor(_ limit: Double, _ ready: @escaping () -> Bool, then: @escaping () -> Void) {
        let started = Date()
        func poll() {
            // A moment more once ready, so the window has drawn it.
            if ready() || Date().timeIntervalSince(started) > limit { after(0.4, then) } else { after(0.2, poll) }
        }
        poll()
    }

    private func after(_ seconds: Double, _ f: @escaping () -> Void) {
        DispatchQueue.main.asyncAfter(deadline: .now() + seconds, execute: f)
    }

    private func keyEvent(_ c: String, code: UInt16, kind: NSEvent.EventType, window: NSWindow) -> NSEvent? {
        NSEvent.keyEvent(
            with: kind, location: .zero, modifierFlags: [], timestamp: ProcessInfo.processInfo.systemUptime,
            windowNumber: window.windowNumber, context: nil, characters: c, charactersIgnoringModifiers: c,
            isARepeat: false, keyCode: code)
    }

    /// A key straight to the window.
    private func key(_ c: String, code: UInt16, into window: NSWindow) {
        for kind in [NSEvent.EventType.keyDown, .keyUp] {
            if let e = keyEvent(c, code: code, kind: kind, window: window) { window.sendEvent(e) }
        }
    }

    /// A key through the app's event queue, like a real key press (event
    /// monitors see it).
    private func post(_ c: String, code: UInt16, to window: NSWindow) {
        for kind in [NSEvent.EventType.keyDown, .keyUp] {
            if let e = keyEvent(c, code: code, kind: kind, window: window) { NSApp.postEvent(e, atStart: false) }
        }
    }

    /// One key event per character, delivered to the window as macOS would.
    private func type(_ text: String, into window: NSWindow, done: @escaping () -> Void) {
        var chars = Array(text)
        func next() {
            guard !chars.isEmpty else {
                done()
                return
            }
            let c = String(chars.removeFirst())
            for kind in [NSEvent.EventType.keyDown, .keyUp] {
                if let e = NSEvent.keyEvent(
                    with: kind, location: .zero, modifierFlags: [], timestamp: ProcessInfo.processInfo.systemUptime,
                    windowNumber: window.windowNumber, context: nil, characters: c, charactersIgnoringModifiers: c,
                    isARepeat: false, keyCode: 0) {
                    window.sendEvent(e)
                }
            }
            after(0.08, next)
        }
        next()
    }

    private var folder: String { ProcessInfo.processInfo.environment["LASERDOT_SELFTEST_FOLDER"] ?? "" }

    /// A path inside the fixtures, from the fixtures folder (/var and /private/var alike).
    private func relative(_ path: String) -> String {
        let real = realpath(folder, nil).map { p in defer { free(p) }; return String(cString: p) } ?? folder
        return path.replacingOccurrences(of: real, with: "").replacingOccurrences(of: folder, with: "")
    }

    private func capture(_ name: String, model: SearchModel, window: NSWindow) {
        var labels: [String] = []
        if let v = window.contentView { collect(v, depth: 0, into: &labels) }
        // Built field by field: one big literal is too slow to type-check.
        var r: [String: Any] = [:]
        r["name"] = name
        r["query"] = model.query
        r["hint"] = model.hint ?? ""
        r["sections"] = model.sections.map { s -> [String: Any] in ["title": s.title, "count": s.range.count] }
        r["results"] = model.results.map(\.name)
        r["selected"] = model.selected?.name ?? ""
        let chips: [String] = model.completionChips.map(\.label) + model.kindChips.map(\.title)
        r["chips"] = chips
        r["compact"] = model.compact
        r["commands"] = model.isCommandMode ? model.commandEntries.map(\.title) : [String]()
        r["actions"] = model.showActions ? model.actions.map(\.title) : [String]()
        r["highlight"] = model.highlightWords
        r["visible_hint"] = model.visibleHint ?? ""
        r["browsing"] = model.browsing
        r["similar"] = model.similarOnly
        r["quicklook"] = QuickLook.shared.isVisible
        r["space_presses"] = model.spacePresses
        r["quicklook_shows"] = QuickLook.shared.shows
        r["key_window_is_overlay"] = NSApp.keyWindow === window
        let keyWindow: String = NSApp.keyWindow.map { String(describing: Swift.type(of: $0)) } ?? "none"
        r["key_window"] = keyWindow
        r["accessibility"] = labels
        r["sizes"] = model.results.map { Int($0.size) }
        r["selected_count"] = model.selectedHits.count
        r["section_ids"] = model.sections.map(\.id)
        r["paths"] = model.results.map { relative($0.path) }
        r["undo"] = model.undoStep?.title ?? ""
        r["undo_banner"] = model.undoBannerVisible
        r["snippet"] = model.snippet ?? ""
        r["action_ids"] = model.selected == nil ? [String]() : model.actions.map(\.id)
        steps.append(r)
        writeReport(error: "unfinished: last step \(name)") // so a hang still shows where
        screenshot(window, name: name)
    }

    /// The labels and values of the accessibility tree: what VoiceOver (and
    /// the eye) gets, rendered rows included.
    private func collect(_ x: Any, depth: Int, into out: inout [String]) {
        guard depth < 80, let e = x as? NSAccessibilityProtocol else { return }
        if let l = e.accessibilityLabel(), !l.isEmpty { out.append(l) }
        if let v = e.accessibilityValue() as? String, !v.isEmpty { out.append(v) }
        for c in e.accessibilityChildren() ?? [] { collect(c, depth: depth + 1, into: &out) }
    }

    private func screenshot(_ window: NSWindow, name: String) {
        guard let view = window.contentView?.superview ?? window.contentView,
              let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds) else { return }
        view.cacheDisplay(in: view.bounds, to: rep)
        guard let png = rep.representation(using: .png, properties: [:]) else { return }
        let file = out.appendingPathComponent("\(name).png")
        try? png.write(to: file)
        shots.append(file.lastPathComponent)
    }

    private func finish(error: String?) {
        writeReport(error: error ?? "")
        NSApp.terminate(nil)
    }

    private func writeReport(error: String) {
        var report: [String: Any] = extra
        let base: [String: Any] = [
            "lang": appLang,
            "layout_rtl": appLayoutDirection == .rightToLeft,
            "appkit_rtl": NSApp.userInterfaceLayoutDirection == .rightToLeft,
            "error": error,
            "steps": steps,
            "screenshots": shots,
        ]
        report.merge(base) { _, b in b }
        if let data = try? JSONSerialization.data(withJSONObject: report, options: [.prettyPrinted, .sortedKeys]) {
            try? data.write(to: out.appendingPathComponent("report.json"))
        }
    }
}
