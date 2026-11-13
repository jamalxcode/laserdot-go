import AppKit
import LaserdotKit
import Security

/// Typing, waiting, recording the screen, the report.
extension SelfTestRunner {
    // MARK: helpers

    /// Write `hold`, then wait (at most two minutes) for CI to write `release`.
    func hold(then next: @escaping () -> Void) {
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
    func settle(_ model: SearchModel, until: ((SearchModel) -> Bool)?, done: @escaping () -> Void) {
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
    func waitFor(_ limit: Double, _ ready: @escaping () -> Bool, then: @escaping () -> Void) {
        let started = Date()
        func poll() {
            // A moment more once ready, so the window has drawn it.
            if ready() || Date().timeIntervalSince(started) > limit { after(0.4, then) } else { after(0.2, poll) }
        }
        poll()
    }

    func after(_ seconds: Double, _ f: @escaping () -> Void) {
        DispatchQueue.main.asyncAfter(deadline: .now() + seconds, execute: f)
    }

    func keyEvent(_ c: String, code: UInt16, kind: NSEvent.EventType, window: NSWindow) -> NSEvent? {
        NSEvent.keyEvent(
            with: kind, location: .zero, modifierFlags: [], timestamp: ProcessInfo.processInfo.systemUptime,
            windowNumber: window.windowNumber, context: nil, characters: c, charactersIgnoringModifiers: c,
            isARepeat: false, keyCode: code)
    }

    /// A key straight to the window.
    func key(_ c: String, code: UInt16, into window: NSWindow) {
        for kind in [NSEvent.EventType.keyDown, .keyUp] {
            if let e = keyEvent(c, code: code, kind: kind, window: window) { window.sendEvent(e) }
        }
    }

    /// A key through the app's event queue, like a real key press (event
    /// monitors see it).
    func post(_ c: String, code: UInt16, to window: NSWindow) {
        for kind in [NSEvent.EventType.keyDown, .keyUp] {
            if let e = keyEvent(c, code: code, kind: kind, window: window) { NSApp.postEvent(e, atStart: false) }
        }
    }

    /// One key event per character, delivered to the window as macOS would.
    func type(_ text: String, into window: NSWindow, done: @escaping () -> Void) {
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

    var folder: String { ProcessInfo.processInfo.environment["LASERDOT_SELFTEST_FOLDER"] ?? "" }

    /// A path inside the fixtures, from the fixtures folder (/var and /private/var alike).
    func relative(_ path: String) -> String {
        let real = realpath(folder, nil).map { p in defer { free(p) }; return String(cString: p) } ?? folder
        return path.replacingOccurrences(of: real, with: "").replacingOccurrences(of: folder, with: "")
    }

    func capture(_ name: String, model: SearchModel, window: NSWindow) {
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
        r["refreshing"] = model.refreshing
        r["refresh_note"] = model.refreshNote ?? ""
        r["grid"] = model.gridView
        r["view"] = model.viewMode.rawValue
        r["tile_size"] = Double(model.tileSize)
        r["kind_width"] = Double(ColumnLayout.shared.width(.kind))
        r["sort"] = model.sort.rawValue
        r["reverse"] = model.reverse
        r["grid_columns"] = model.gridColumns
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
    func collect(_ x: Any, depth: Int, into out: inout [String]) {
        guard depth < 80, let e = x as? NSAccessibilityProtocol else { return }
        if let l = e.accessibilityLabel(), !l.isEmpty { out.append(l) }
        if let v = e.accessibilityValue() as? String, !v.isEmpty { out.append(v) }
        for c in e.accessibilityChildren() ?? [] { collect(c, depth: depth + 1, into: &out) }
    }

    func screenshot(_ window: NSWindow, name: String) {
        guard let view = window.contentView?.superview ?? window.contentView,
              let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds) else { return }
        view.cacheDisplay(in: view.bounds, to: rep)
        guard let png = rep.representation(using: .png, properties: [:]) else { return }
        let file = out.appendingPathComponent("\(name).png")
        try? png.write(to: file)
        shots.append(file.lastPathComponent)
    }

    func finish(error: String?) {
        writeReport(error: error ?? "")
        NSApp.terminate(nil)
    }

    func writeReport(error: String) {
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
