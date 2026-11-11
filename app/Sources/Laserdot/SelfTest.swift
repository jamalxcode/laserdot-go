import AppKit
import LaserdotKit

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
        DispatchQueue.global().async {
            let indexed = Result { try engine.index(path: folder) }
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
            Step(name: "trashed", typed: "trashme"),
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
                self.after(2) {
                    step.after?()
                    self.after(0.6) {
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
        overlay.hide()
        windowsTour(model: model) { self.mainWindowAndSettingsRest(model: model) }
    }

    /// Storage and Duplicates windows, and the Settings tabs: screenshots and
    /// what they show.
    private func windowsTour(model: SearchModel, done: @escaping () -> Void) {
        app.showStorage()
        after(3) {
            if let w = NSApp.windows.first(where: { $0.isVisible && $0.title == L("menu.storageTitle") }) {
                self.screenshot(w, name: "storage")
            }
            self.extra["storage_items"] = self.app.storageModel?.report?.items.count ?? -1
            self.app.showDuplicates()
            self.app.duplicatesModel?.scan()
            self.after(8) {
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
                self.finish(error: nil)
            }
        }
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
            after(0.12, next)
        }
        next()
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
