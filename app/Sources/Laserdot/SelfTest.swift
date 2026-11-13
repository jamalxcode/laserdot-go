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
    let app: AppDelegate
    let out: URL
    var steps: [[String: Any]] = []
    var shots: [String] = []
    var closeAttempts = 0
    var extra: [String: Any] = [:]

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
    struct Step {
        let name: String
        let typed: String
        var before: (() -> Void)?
        var after: (() -> Void)?
        /// What the screen must show before it is recorded (files changed
        /// on disk reach the index through the watcher; this waits for it).
        var until: ((SearchModel) -> Bool)?
    }

    func run() {
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
            // Thumbnails (⌘J): the same results as a grid.
            Step(name: "grid", typed: "kind:image", before: {
                model.sort = .relevance
                model.tileSize = 104
                model.gridView = true
            }),
            // Bigger thumbnails (the size slider, ⌘+): fewer to a row.
            Step(name: "gridbig", typed: "kind:image", before: { model.tileSize = 220 }),
            // Columns, as in Finder: a click on Size sorts largest first, a
            // second click smallest first; one flat list.
            Step(name: "columns", typed: "kind:image", before: {
                model.tileSize = 104
                model.viewMode = .columns
                model.sortBy(.size)
                ColumnLayout.shared.setWidth(.kind, 180) // as a drag in the header does
            }),
            Step(name: "columnsreversed", typed: "kind:image", before: { model.sortBy(.size) }),
            Step(name: "multi", typed: "jpg", before: {
                model.viewMode = .list
                model.sort = .relevance
                model.reverse = false
            }, after: { model.extend(1) }),
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
            // Deleted behind Laserdot's back (as Finder would): gone from
            // the results at once; Refresh (⌘R) looks at every folder again.
            Step(name: "gone", typed: "gonefile", before: {
                try? FileManager.default.removeItem(atPath: folder + "/gonefile.txt")
            }, after: { model.refreshIndex() }, until: { $0.results.isEmpty }),
            Step(name: "refreshed", typed: "gonefile", until: { !$0.refreshing }),
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

    func runSteps(_ steps: ArraySlice<Step>, model: SearchModel, overlay: OverlayController) {
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

}
