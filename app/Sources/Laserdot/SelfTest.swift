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

    private func run() {
        guard let overlay = app.overlayForTests, let model = app.model else {
            finish(error: "no overlay")
            return
        }
        overlay.show()
        after(0.8) {
            self.type("jpg", into: overlay.panel) {
                self.after(2) {
                    self.capture("jpg", model: model, window: overlay.panel)
                    model.query = ""
                    self.after(0.5) {
                        self.type("proj", into: overlay.panel) {
                            self.after(2) {
                                self.capture("proj", model: model, window: overlay.panel)
                                model.query = ""
                                self.after(0.5) {
                                    self.type("tool", into: overlay.panel) {
                                        self.after(2) {
                                            self.capture("tool", model: model, window: overlay.panel)
                                            self.mainWindowAndSettings(model: model, overlay: overlay)
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    private func mainWindowAndSettings(model: SearchModel, overlay: OverlayController) {
        overlay.hide()
        model.query = "proj"
        after(2) {
            if let main = NSApp.windows.first(where: { !($0 is NSPanel) && $0.isVisible && $0.title == "Laserdot" }) {
                self.capture("main", model: model, window: main)
            }
            NSApp.sendAction(Selector(("showSettingsWindow:")), to: nil, from: nil)
            self.after(1.5) {
                // The Settings window: visible, not the main window, not the
                // menu-bar icon's tiny status window.
                if let settings = NSApp.windows.first(where: {
                    $0.isVisible && !($0 is NSPanel) && $0.title != "Laserdot" && $0.frame.width > 300
                }) {
                    self.screenshot(settings, name: "settings")
                }
                self.finish(error: nil)
            }
        }
    }

    // MARK: helpers

    private func after(_ seconds: Double, _ f: @escaping () -> Void) {
        DispatchQueue.main.asyncAfter(deadline: .now() + seconds, execute: f)
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
        steps.append([
            "name": name,
            "query": model.query,
            "hint": model.hint ?? "",
            "sections": model.sections.map { ["title": $0.title, "count": $0.range.count] },
            "results": model.results.map(\.name),
            "selected": model.selected?.name ?? "",
            "chips": model.completionChips.map(\.label) + model.kindChips.map(\.title),
            "accessibility": labels,
        ])
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
        let report: [String: Any] = [
            "lang": appLang,
            "layout_rtl": NSApp.userInterfaceLayoutDirection == .rightToLeft,
            "error": error ?? "",
            "steps": steps,
            "screenshots": shots,
        ]
        if let data = try? JSONSerialization.data(withJSONObject: report, options: [.prettyPrinted, .sortedKeys]) {
            try? data.write(to: out.appendingPathComponent("report.json"))
        }
        NSApp.terminate(nil)
    }
}
