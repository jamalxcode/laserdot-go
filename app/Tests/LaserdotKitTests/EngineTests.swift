import Foundation
@testable import LaserdotKit
import XCTest

/// The Swift side end to end: Swift client → Unix socket → Go laserdotd.
/// Features the daemon does not have yet (it answers `unsupported`) are
/// skipped, not failed.
final class EngineTests: XCTestCase {
    /// The daemon built by scripts/build-go.sh.
    private var daemonPath: String {
        if let p = ProcessInfo.processInfo.environment["LASERDOTD"], !p.isEmpty { return p }
        let here = URL(fileURLWithPath: #filePath)
        return here.deletingLastPathComponent().appendingPathComponent("../../build/go/laserdotd").standardized.path
    }

    /// `body`'s result, or nil when the daemon answers `unsupported`.
    private func ifSupported<T>(_ what: String, _ body: () throws -> T) throws -> T? {
        do {
            return try body()
        } catch let e where e.isUnsupported {
            print("skipping \(what): laserdotd answers unsupported")
            return nil
        }
    }

    /// A request as it goes on the wire, keys sorted.
    private func wire(_ request: DaemonRequest) throws -> String {
        let e = JSONEncoder()
        e.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        return String(decoding: try e.encode(request), as: UTF8.self)
    }

    func testGuidanceComesFromTheRegistry() {
        let f = filters(lang: "en")
        XCTAssertTrue(f.contains { $0.key == "res" && $0.values.contains("4k") })
        XCTAssertTrue(f.contains { $0.key == "s.forced" })
        XCTAssertTrue(hint(query: "res:", cursor: 4, lang: "en")?.contains("4k") ?? false)
        XCTAssertTrue(hint(query: "", cursor: 0, lang: "en")?.contains("res:4k") ?? false)
        XCTAssertTrue(hint(query: "invoice", cursor: 7, lang: "en")?.contains("add filters") ?? false)
        XCTAssertTrue(hint(query: "chan", cursor: 4, lang: "en")?.contains("channels:") ?? false)
        XCTAssertTrue(hint(query: "report*", cursor: 7, lang: "en")?.contains("whole name") ?? false)
        XCTAssertEqual(completions(query: "res:4", cursor: 5, lang: "en").first?.insert, "res:4k")
        XCTAssertEqual(completions(query: "res:1", cursor: 5, lang: "en").map(\.insert), ["res:1080p", "res:1440p"])
        XCTAssertEqual(completions(query: "kind:video,au", cursor: 13, lang: "en").first?.insert, "kind:video,audio")
        XCTAssertEqual(completions(query: "-a.lan", cursor: 6, lang: "en").first?.insert, "-a.lang:")
        let re = completions(query: "kind:video re", cursor: 13, lang: "en")
        XCTAssertTrue(re.contains { $0.insert == "res:" && $0.replaceStart == 11 && $0.replaceEnd == 13 })
        XCTAssertTrue(completions(query: "hello world", cursor: 3, lang: "en").isEmpty)
        XCTAssertTrue(cheatSheet(lang: "en").contains("TRACK GROUPS"))
        // Arabic guidance (spec §8.9.8, §14.4); keys stay ASCII.
        XCTAssertTrue(hint(query: "res:", cursor: 4, lang: "ar")?.contains("الدقة") ?? false)
        XCTAssertTrue(hint(query: "chan", cursor: 4, lang: "ar")?.hasPrefix("فلاتر: channels:") ?? false)
        XCTAssertTrue(completions(query: "lossl", cursor: 5, lang: "ar").first?.detail.contains("بلا فقد") ?? false)
        XCTAssertTrue(filters(lang: "ar").contains { $0.key == "res" && $0.section == "الفيديو" && $0.sectionId == "Video" })
        XCTAssertEqual(sectionAt(query: "chan", cursor: 4), "Audio")
        XCTAssertEqual(sectionAt(query: "kind:video chan", cursor: 15), "Audio")
        XCTAssertEqual(sectionAt(query: "res:4k", cursor: 2), "Video")
        XCTAssertEqual(sectionAt(query: "s.", cursor: 2), "Subtitles")
        XCTAssertNil(sectionAt(query: "invoice", cursor: 3))
        XCTAssertNil(sectionAt(query: "x", cursor: 1))
    }

    /// The translations cannot fall behind the registry (§8.8).
    func testEveryFilterHasAnArabicDescription() {
        let keys = Set(Registry.shared.filters.map(\.key))
        for k in keys {
            XCTAssertNotNil(arabicDescriptions[k], "filter `\(k)` has no Arabic description in Guidance.swift")
        }
        for k in arabicDescriptions.keys {
            XCTAssertTrue(keys.contains(k), "Guidance.swift has `\(k)`, which is not a filter")
        }
        for s in Registry.shared.sections {
            XCTAssertNotNil(arabicSections[s], "section `\(s)` has no Arabic title")
        }
    }

    /// The same socket as Go `ipc.SocketFor`, so the app, the CLI and the
    /// daemon meet.
    func testSocketPathMatchesTheGoDaemon() throws {
        XCTAssertEqual(
            Paths.socket(forDb: "/Users/me/Library/Application Support/laserdot/index.db"),
            "/Users/me/Library/Application Support/laserdot/laserdotd.sock")
        let long = "/Volumes/archive/" + String(repeating: "nested-folder/", count: 8) + "index.db"
        let socket = Paths.socket(forDb: long) // creates the short folder
        let tmp = try XCTUnwrap(realpath("/tmp/laserdot-\(getuid())", nil))
        defer { free(tmp) }
        // FNV-1a-64 of the path, as computed by the Go daemon.
        XCTAssertEqual(socket, String(cString: tmp) + "/2bc2c428908e14f9.sock")
        XCTAssertEqual(Paths.fnv1a64("/tmp/فاتورة/index.db"), 0xecb7_cf91_18ae_f441)
    }

    /// Requests are encoded as the Rust `Request` enum (serde, `tag = "op"`,
    /// snake_case), which the Go daemon reads too.
    func testRequestsUseTheRustWireFormat() throws {
        XCTAssertEqual(try wire(.ping), #"{"op":"ping"}"#)
        XCTAssertEqual(
            try wire(.search(query: "a/b", sort: .size, reverse: true, offset: 5, limit: 7, typo: false)),
            #"{"limit":7,"offset":5,"op":"search","query":"a/b","reverse":true,"sort":"size","typo":false}"#)
        XCTAssertEqual(try wire(.index(path: "/x")), #"{"op":"index","path":"/x"}"#)
        XCTAssertEqual(
            try wire(.driveIndex(mount: "/Volumes/A", passphrase: nil)), #"{"mount":"/Volumes/A","op":"drive_index"}"#)
        XCTAssertEqual(
            try wire(.driveAttach(mount: "/Volumes/A", passphrase: "pw")),
            #"{"mount":"/Volumes/A","op":"drive_attach","passphrase":"pw"}"#)
        XCTAssertEqual(try wire(.valueCounts), #"{"op":"value_counts"}"#)
        XCTAssertEqual(try wire(.coach(query: "q")), #"{"op":"coach","query":"q"}"#)
        let item = Wire.CleanupItem(path: "/a", size: 3, modified: 9)
        let plan = Wire.CleanupRequest(groups: [Wire.CleanupGroup(keep: [item], remove: [])], dest: .folder("/f"))
        XCTAssertEqual(
            try wire(.cleanupPlan(request: plan)),
            #"{"op":"cleanup_plan","request":{"dest":{"folder":"/f","to":"folder"},"groups":[{"keep":[{"modified":9,"path":"/a","size":3}],"remove":[]}]}}"#)
        XCTAssertEqual(
            try wire(.cleanup(request: Wire.CleanupRequest(groups: [], dest: .trash))),
            #"{"op":"cleanup","request":{"dest":{"to":"trash"},"groups":[]}}"#)
        XCTAssertEqual(
            try wire(.dupes(options: Wire.DupeOptions(scope: "kind:video", images: false, videos: false))),
            #"{"op":"dupes","options":{"duration_tolerance_ms":2000,"exact":true,"image_distance":7,"images":false,"min_size":1,"scope":"kind:video","video_distance":12,"videos":false}}"#)
        XCTAssertEqual(
            try wire(.setContentSettings(settings: Wire.ContentSettings(roots: ["/d"], maxBytes: 5))),
            #"{"op":"set_content_settings","settings":{"max_bytes":5,"roots":["/d"]}}"#)
        XCTAssertEqual(
            try wire(.hint(query: "res:", cursor: 4, lang: "ar")), #"{"cursor":4,"lang":"ar","op":"hint","query":"res:"}"#)
        XCTAssertEqual(try wire(.sectionAt(query: "chan", cursor: 4)), #"{"cursor":4,"op":"section_at","query":"chan"}"#)
        XCTAssertEqual(
            try wire(.completions(query: "res:4", cursor: 5, lang: "en")),
            #"{"cursor":5,"lang":"en","op":"completions","query":"res:4"}"#)
        XCTAssertEqual(try wire(.filters(lang: "en")), #"{"lang":"en","op":"filters"}"#)
        XCTAssertEqual(try wire(.cheatSheet(lang: "en")), #"{"lang":"en","op":"cheat_sheet"}"#)
    }

    /// Results in the Rust shapes decode (serde's tagged enums included).
    func testResultsInTheRustShapesDecode() throws {
        let d = JSONDecoder()
        let drive = try d.decode(Wire.DriveInfo.self, from: Data("""
            {"id":null,"name":"Archive","mount":"/Volumes/Archive","fs_type":"apfs","read_only":false,
             "total_bytes":10,"free_bytes":4,"state":{"state":"unreadable","detail":"damaged"},"on_drive":true,
             "index_path":null,"index_bytes":0,"encrypted":false,"entries":0,"complete":false,"last_scan":null}
            """.utf8))
        XCTAssertEqual(drive.state.state, "unreadable")
        XCTAssertEqual(drive.state.detail, "damaged")
        let coaching = try d.decode([Wire.Coaching].self, from: Data("""
            [{"kind":"relax","removed":"ext:docx","query":"invoice","count":2},{"kind":"show_syntax"},
             {"kind":"index_folder","path":"/x"}]
            """.utf8))
        XCTAssertEqual(coaching.map(\.kind), ["relax", "show_syntax", "index_folder"])
        XCTAssertEqual(coaching[0].removed, "ext:docx")
        let plan = try d.decode(Wire.CleanupPlan.self, from: Data("""
            {"moves":[{"from":"/a","to":null,"size":3}],"bytes":3,"skipped":[]}
            """.utf8))
        XCTAssertEqual(plan.moves.first?.from, "/a")
        // An empty list may arrive as null.
        let results = try d.decode(Wire.SearchResults.self, from: Data("""
            {"total":0,"hits":null,"typo_fallback":false,"warnings":null,"elapsed_us":12}
            """.utf8))
        XCTAssertTrue(results.hits.isEmpty)
        let completion = try d.decode([Completion].self, from: Data("""
            [{"label":"4k","detail":"resolution class by long edge","insert":"res:4k","replace_start":0,"replace_end":5}]
            """.utf8))
        XCTAssertEqual(completion.first?.replaceEnd, 5)
        let filter = try d.decode([FilterInfo].self, from: Data("""
            [{"key":"res","section":"الفيديو","section_id":"Video","description":"d","example":"res:4k","values":["4k"]}]
            """.utf8))
        XCTAssertEqual(filter.first?.sectionId, "Video")
    }

    func testWithoutADaemonTheEngineReportsUnavailable() throws {
        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent("laserdot-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: tmp) }
        let engine = Engine(dbPath: tmp.appendingPathComponent("index.db").path, daemonPath: nil)
        XCTAssertFalse(engine.status().running)
        XCTAssertThrowsError(try engine.search(query: "x")) { error in
            guard case .unavailable? = error as? LaserdotError else { return XCTFail("got \(error)") }
        }
    }

    func testSearchThroughTheDaemon() throws {
        XCTAssertTrue(FileManager.default.isExecutableFile(atPath: daemonPath), "no laserdotd at \(daemonPath)")
        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent("laserdot-\(UUID().uuidString)")
        let lib = tmp.appendingPathComponent("lib")
        try FileManager.default.createDirectory(at: lib, withIntermediateDirectories: true)
        try Data("pdf".utf8).write(to: lib.appendingPathComponent("invoice-march.pdf"))
        try Data("txt".utf8).write(to: lib.appendingPathComponent("فاتورة ٢٠٢٤.txt"))
        defer { try? FileManager.default.removeItem(at: tmp) }

        let engine = Engine(dbPath: tmp.appendingPathComponent("data/index.db").path, daemonPath: daemonPath)
        let status = try engine.connect()
        defer { engine.shutdown() }
        XCTAssertTrue(status.running)
        XCTAssertEqual(status.protocolVersion, 5)
        XCTAssertTrue(status.networkDenied, "laserdotd must run with network access denied by macOS")

        try engine.index(path: lib.path)
        XCTAssertTrue(engine.status().folders.contains { $0.hasSuffix("/lib") })
        XCTAssertEqual(try engine.search(query: "invoice march", limit: 10).hits.map(\.name), ["invoice-march.pdf"])
        XCTAssertEqual(try engine.search(query: "فاتوره 2024", limit: 10).total, 1)
        // Sorting is the daemon's.
        let byName = try engine.search(query: "kind:document", limit: 10, sort: .name)
        XCTAssertEqual(byName.hits.map(\.name), ["invoice-march.pdf", "فاتورة ٢٠٢٤.txt"])
        let reversed = try engine.search(query: "kind:document", limit: 10, sort: .name, reverse: true)
        XCTAssertEqual(reversed.hits.map(\.name), ["فاتورة ٢٠٢٤.txt", "invoice-march.pdf"])

        // Query guidance (spec §8.1, §8.2, §8.9) from the daemon, or the
        // bundled registry when it does not have those ops yet.
        XCTAssertTrue(engine.hint(query: "res:", cursor: 4, lang: "ar")?.contains("الدقة") ?? false)
        print("guidance answered by \(engine.guidanceFromDaemon ? "laserdotd" : "the bundled registry")")
        XCTAssertNil(engine.sectionAt(query: "invoice", cursor: 3))
        XCTAssertEqual(engine.sectionAt(query: "chan", cursor: 4), "Audio")
        XCTAssertEqual(engine.completions(query: "res:4", cursor: 5, lang: "en").first?.insert, "res:4k")
        XCTAssertTrue(engine.completions(query: "hello world", cursor: 3, lang: "en").isEmpty)
        XCTAssertTrue(engine.filters(lang: "ar").contains { $0.key == "res" && $0.section == "الفيديو" && $0.sectionId == "Video" })
        XCTAssertTrue(engine.cheatSheet(lang: "en").contains("TRACK GROUPS"))

        // A bad query comes back positioned, with a fix (spec §8.1).
        XCTAssertThrowsError(try engine.search(query: "res:4l", limit: 10)) { error in
            guard case let .query(_, start, end, suggestion)? = error as? LaserdotError else {
                return XCTFail("expected a query error, got \(error)")
            }
            XCTAssertEqual(start, 4)
            XCTAssertEqual(end, 6)
            XCTAssertEqual(suggestion, "res:4k")
        }

        // Drives (spec §9): the list comes back, states are known, and a
        // folder that is not a mounted drive cannot be planned.
        if let drives = try ifSupported("drives", { try engine.drives() }) {
            let known: Set<String> = ["unindexed", "attached", "detached", "locked", "unreadable"]
            for d in drives {
                XCTAssertTrue(known.contains(d.state), "unknown drive state \(d.state)")
            }
            XCTAssertThrowsError(try engine.drivePlan(mount: tmp.appendingPathComponent("nope").path))
        }

        // Duplicates (spec §4): an identical copy is found and recommended
        // for removal; the dry run moves nothing.
        try Data("pdf".utf8).write(to: lib.appendingPathComponent("invoice-march copy.pdf"))
        try engine.index(path: lib.path)
        if let dupes = try ifSupported("dupes", { try engine.findDuplicates(scope: "", exactOnly: true) }) {
            XCTAssertEqual(dupes.groups.count, 1)
            XCTAssertEqual(dupes.groups.first?.files.filter(\.keep).count, 1)
            let sets = dupes.groups.map { g in CleanupSet(keep: g.files.filter(\.keep), remove: g.files.filter { !$0.keep }) }
            if let preview = try ifSupported("cleanup_plan", { try engine.cleanupPreview(sets: sets, folder: tmp.path) }) {
                XCTAssertEqual(preview.files, 1)
            }
            XCTAssertTrue(FileManager.default.fileExists(atPath: lib.appendingPathComponent("invoice-march copy.pdf").path))
        }

        // Content search (spec §2.4): the folder's text becomes searchable.
        try Data("the zanzibar proposal".utf8).write(to: lib.appendingPathComponent("notes.txt"))
        try engine.index(path: lib.path)
        if let info = try ifSupported("content", { try engine.setContentFolder(folder: lib.path, add: true) }) {
            XCTAssertTrue(info.folders.contains { $0.hasSuffix("/lib") })
            var found: [Hit] = []
            for _ in 0..<50 where found.isEmpty {
                found = try engine.search(query: "zanzibar", limit: 10).hits
                if found.isEmpty { Thread.sleep(forTimeInterval: 0.2) }
            }
            XCTAssertEqual(found.map(\.name), ["notes.txt"])
            XCTAssertEqual(found.first?.content, true)
        }

        // Zero-results coaching and live counts (spec §8.3, §8.9.5).
        if let coaching = try ifSupported("coach", { try engine.coach(query: "invoice ext:docx") }) {
            XCTAssertEqual(coaching.first?.kind, "relax")
            XCTAssertEqual(coaching.first?.query, "invoice")
        }
        if let counts = try ifSupported("value_counts", { try engine.valueCounts() }) {
            XCTAssertTrue(counts.contains { $0.key == "kind" && $0.value == "document" && $0.count >= 1 })
        }

        // An unsupported op is remembered; the connection keeps working.
        if !engine.supports("drives") {
            XCTAssertThrowsError(try engine.drives()) { XCTAssertTrue($0.isUnsupported) }
        }
        XCTAssertEqual(try engine.search(query: "invoice march", limit: 10).total, 2)
    }
}
