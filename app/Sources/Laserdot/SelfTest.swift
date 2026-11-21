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
    /// When the run and the current step began (each step's time goes into
    /// the report, so CI's slowest steps can be found and trimmed).
    let runStarted = Date()
    var stepStarted = Date()
    /// Where the time between steps goes (milliseconds, all steps together):
    /// waiting for Quick Look to close, and recording (report + screenshot).
    var waitedForQuickLook = 0.0
    var recording = 0.0
    var firstStepAt: Double?
    /// Quick Look that will not close on this machine: not waited for again.
    var quickLookStuck = false
    /// Milestones (seconds since the start), in order.
    var marks: [String] = []

    func mark(_ what: String) {
        marks.append(String(format: "%@ %.1f", what, Date().timeIntervalSince(runStarted)))
    }

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
        /// Extra seconds before the screen is recorded, after `after`.
        var wait: Double = 0
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
            // What the results have to choose from (here: their types), with
            // counts; choosing one narrows the search to it.
            Step(name: "refine", typed: "proj", until: { !$0.refinements.isEmpty }),
            Step(name: "refined", typed: "proj", after: {
                if let pdf = model.refinements.flatMap(\.options).first(where: { $0.token == "ext:pdf" }) {
                    model.applyRefinement(pdf)
                }
            }, until: { $0.refinements.flatMap(\.options).contains { $0.token == "ext:pdf" } }, wait: 1.0),
            // After a choice its group shrinks to it (PDF only; TXT goes)
            // and comes first, while the other groups stay (Size).
            Step(name: "chosen", typed: "proj ext:pdf",
                 until: { $0.refinements.flatMap(\.options).contains { $0.isSelected && $0.token == "ext:pdf" } }),
            Step(name: "unrefined", typed: "proj ext:txt", after: {
                if let g = model.refinements.first(where: { $0.id == "type" }),
                   let txt = g.options.first(where: \.isSelected) {
                    model.applyRefinement(txt, in: g)
                }
            }, until: { $0.refinements.flatMap(\.options).contains(where: \.isSelected) }, wait: 1.0),
            // Leave a choice out (⌥-click): "-ext:pdf" joins the search, the
            // PDF goes, and PDF shows struck through to bring it back.
            Step(name: "leftout", typed: "proj", after: {
                if let pdf = model.refinements.flatMap(\.options).first(where: { $0.token == "ext:pdf" }) {
                    model.excludeRefinement(pdf)
                }
            }, until: { $0.refinements.flatMap(\.options).contains { $0.token == "ext:pdf" } }, wait: 1.0),
            Step(name: "leftoutshown", typed: "-ext:pdf proj",
                 until: { $0.refinements.flatMap(\.options).contains(where: \.isExcluded) }),
            // Where: the folders the results are in are choices too (the
            // owner: "choose the drives and possibly directories it searches").
            Step(name: "where", typed: "trip", until: { $0.refinements.contains { $0.id == "folder" } }),
            Step(name: "wherechosen", typed: "trip", after: {
                if let g = model.refinements.first(where: { $0.id == "folder" }), let o = g.options.first {
                    model.applyRefinement(o, in: g)
                }
            }, until: { $0.refinements.contains { $0.id == "folder" } }, wait: 1.0),
            // A default search place (Settings ▸ Search in): searches look
            // only there, the pill says so, and its button searches everywhere.
            Step(name: "place", typed: "trip", before: {
                UserDefaults.standard.set(folder + "/Trips/2023", forKey: SearchPlace.key)
            }, until: { $0.placeInEffect && $0.results.count == 1 }),
            Step(name: "placeoff", typed: "trip", after: {
                model.dropPlace()
                UserDefaults.standard.removeObject(forKey: SearchPlace.key)
            }, until: { $0.placeInEffect }, wait: 1.0),
            // The choice bar: Documents joins the typed words as a pill, and
            // a second click takes it out again.
            Step(name: "quickstart", typed: "proj", after: {
                model.startButtons.first { $0.id == "documents" }?.run()
            }, until: { !$0.results.isEmpty }, wait: 1.5),
            Step(name: "bartoggle", typed: "", before: { model.query = "kind:document proj" }, after: {
                model.startButtons.first { $0.id == "documents" }?.run()
            }, until: { !$0.results.isEmpty }, wait: 1.0),
            Step(name: "pillback", typed: "", after: {
                model.query = "kind:video "
                _ = model.removeLastPill()
            }, until: { $0.isHome }, wait: 0.5),
            // Inside a zip: a word finds the zip holding a file named with it.
            Step(name: "zip", typed: "quokka", until: { !$0.results.isEmpty }),
            Step(name: "pdftoday", typed: "pdf today"),
            Step(name: "commands", typed: ">dup"),
            // As in Spotlight: the top hit's name completes inline, the rest
            // selected (typing goes on over it).
            Step(name: "inline", typed: "beac"),
            // Reopened: the last search is there, all selected.
            Step(name: "reopen", typed: "jpg", after: { overlay.hide(); overlay.show() }),
            // Reopened after a result was deleted (unzipped, moved…) while
            // the window was closed: the search runs again, it is gone.
            Step(name: "reopenfresh", typed: "staleme", after: {
                overlay.hide()
                try? FileManager.default.removeItem(atPath: folder + "/staleme.txt")
                overlay.show()
            }, until: { $0.results.map(\.name) == ["staleme.txt"] }, wait: 1.5),
            // Esc clears the search; Esc again (empty) closes the overlay.
            Step(name: "escape", typed: "jpg", after: {
                self.key("\u{1b}", code: 53, into: overlay.panel)
                self.extra["escape_first_query"] = model.query
                self.extra["escape_first_visible"] = overlay.panel.isVisible
                self.key("\u{1b}", code: 53, into: overlay.panel)
                self.extra["escape_second_visible"] = overlay.panel.isVisible
                overlay.show()
            }),
            // ⌘↓: the first result of the next section.
            Step(name: "jump", typed: "proj", after: {
                self.key(String(UnicodeScalar(UInt16(NSDownArrowFunctionKey))!), code: 125, into: overlay.panel, flags: .command)
            }),
            // ⌘C: the selected file goes to the clipboard, for Finder.
            Step(name: "copy", typed: "beach", after: {
                NSPasteboard.general.clearContents()
                _ = model.copyFiles()
                let urls = NSPasteboard.general.readObjects(forClasses: [NSURL.self]) as? [URL] ?? []
                self.extra["copied_files"] = urls.map(\.lastPathComponent)
            }),
            Step(name: "actions", typed: "jpg", after: { model.toggleActions() }),
            // ↓ into the results, then Space through the app's event queue
            // (as a real key press arrives): Quick Look opens.
            // A key posted on a busy CI machine is now and then lost: if
            // Quick Look was not asked to show within a moment, the key is
            // pressed once more (a press that arrived is never repeated, as
            // a second one would close the preview).
            Step(name: "space", typed: "jpg", after: {
                self.key(String(UnicodeScalar(UInt16(NSDownArrowFunctionKey))!), code: 125, into: overlay.panel)
                let shows = QuickLook.shared.shows
                self.post(" ", code: 49, to: overlay.panel)
                self.after(0.4) {
                    if QuickLook.shared.shows == shows {
                        self.extra["space_pressed_again"] = true
                        self.post(" ", code: 49, to: overlay.panel)
                    }
                }
            }, wait: 0.8),
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
                // What was found, before it goes (results now refresh at once).
                self.extra["trash_found"] = model.results.map(\.name)
                model.allActions.first { $0.id == "trash" }?.run()
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
            // More Like This: the search becomes the file's type, size and date.
            Step(name: "morelike", typed: "", before: { model.moreLike(path: folder + "/project.pdf") },
                 until: { $0.query.hasPrefix("ext:pdf") && !$0.results.isEmpty }),
            // Deleted behind Laserdot's back (as Finder would): gone from
            // the results at once; Refresh (⌘R) looks at every folder again.
            Step(name: "gone", typed: "gonefile", before: {
                try? FileManager.default.removeItem(atPath: folder + "/gonefile.txt")
            }, after: {
                model.refreshIndex()
                // Working: the menu-bar dot flashes.
                self.app.updateFlash()
                self.extra["flash_when_working"] = self.app.isFlashing
            }, until: { $0.results.isEmpty }),
            Step(name: "refreshed", typed: "gonefile", until: { !$0.refreshing }),
            // A laserdot:// link opens the overlay with its search.
            Step(name: "url", typed: "", before: { self.app.handle(URL(string: "laserdot://search?q=beach")!) }),
            // A swapped-letter typo finds the file, and says so.
            Step(name: "similar", typed: "pfd"),
            Step(name: "home", typed: "", before: {
                engine.recordOpen(path: folder + "/beach.jpg")
                model.loadHome()
                model.showHomeList = true // ↓ or Recently Opened
            }),
            // Limited to a place where nothing matches: Search Everywhere.
            Step(name: "noneplace", typed: "quokka", before: {
                UserDefaults.standard.set(folder + "/Trips/2023", forKey: SearchPlace.key)
            }, until: { $0.placeInEffect && $0.results.isEmpty && !$0.searching }),
            Step(name: "noneplaceoff", typed: "quokka", after: {
                model.dropPlace()
                UserDefaults.standard.removeObject(forKey: SearchPlace.key)
            }, until: { $0.placeInEffect }, wait: 1.5),
            // Reset clears the words, pills, place and order at once; a place
            // whose folder is gone (an unplugged drive) is ignored.
            Step(name: "reset", typed: "trip", before: {
                UserDefaults.standard.set(folder + "/Trips/2023", forKey: SearchPlace.key)
                model.sort = .name
            }, after: { model.resetSearch() }, until: { $0.placeInEffect }, wait: 1.0),
            Step(name: "placegone", typed: "trip", before: {
                UserDefaults.standard.set("/Volumes/LaserdotGoneDrive/Photos", forKey: SearchPlace.key)
            }, after: { UserDefaults.standard.removeObject(forKey: SearchPlace.key) },
               until: { !$0.results.isEmpty }),
            // The start's round buttons: Videos runs its search as a pill.
            Step(name: "startvideos", typed: "", after: {
                model.startButtons.first { $0.id == "videos" }?.run()
            }, until: { $0.isHome }, wait: 1.5),
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
            mark("steps done")
            mainWindowAndSettings(model: model, overlay: overlay)
            return
        }
        // Start each step with Quick Look really closed. On CI's Macs the
        // preview sometimes reports itself visible after it was closed; it
        // is given about a second once, then not waited for again (waiting
        // the full time before every later step cost 105 s per language).
        if QuickLook.shared.isVisible && !quickLookStuck {
            if closeAttempts < 3 {
                closeAttempts += 1
                QuickLook.shared.close()
                waitedForQuickLook += 300
                after(0.3) { self.runSteps(steps, model: model, overlay: overlay) }
                return
            }
            quickLookStuck = true
            extra["quicklook_stuck_before"] = step.name
        }
        if firstStepAt == nil {
            firstStepAt = Date().timeIntervalSince(runStarted) * 1000
            mark("steps start")
        }
        closeAttempts = 0
        stepStarted = Date()
        model.query = ""
        after(0.5) {
            step.before?()
            self.type(step.typed, into: overlay.panel) {
                self.settle(model, until: step.until) {
                    step.after?()
                    self.after((step.after == nil ? 0.1 : 0.6) + step.wait) {
                        self.capture(step.name, model: model, window: overlay.panel)
                        let next = { self.runSteps(steps.dropFirst(), model: model, overlay: overlay) }
                        // CI reads the screen with an accessibility client
                        // (app/scripts/ax-dump.swift) while this is shown.
                        // Only when asked (CI sets it, empty, for the Arabic run, which has
                        // no reader: holding there waited out the 2-minute limit).
                        if step.name == "jpg", !(ProcessInfo.processInfo.environment["LASERDOT_SELFTEST_HOLD"] ?? "").isEmpty {
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
