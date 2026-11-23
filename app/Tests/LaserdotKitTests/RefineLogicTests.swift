import Foundation
@testable import LaserdotKit
import XCTest

/// The choice row's rules, in seconds rather than through the whole app
/// (the owner hit each of these on the real screen first).
final class RefineLogicTests: XCTestCase {
    private func opt(_ token: String, selected: Bool = false) -> RefineOption {
        RefineOption(token: token, label: token, key: nil, count: 3, selected: selected)
    }

    func testChoosingAddsSwitchesAndRemoves() {
        let res = RefineGroup(id: "resolution", options: [opt("res:4k"), opt("res:1440p"), opt("res:1080p")])
        // "mov" → 1440p: added.
        XCTAssertEqual(RefineLogic.applying(opt("res:1440p"), in: res, to: "mov"), "res:1440p mov")
        // With 1440p chosen, 4K takes its place (a video has one resolution).
        let chosen = RefineGroup(id: "resolution", options: [opt("res:4k"), opt("res:1440p", selected: true)])
        XCTAssertEqual(RefineLogic.applying(opt("res:4k"), in: chosen, to: "mov res:1440p a.count:>=1"),
                       "res:4k mov a.count:>=1")
        // Clicking the chosen one takes it out, tidying spaces.
        XCTAssertEqual(RefineLogic.applying(opt("res:1440p", selected: true), in: chosen, to: "mov  res:1440p a.count:>=1"),
                       "mov a.count:>=1")
        // A choice from another group is added alongside.
        XCTAssertEqual(RefineLogic.applying(opt("orientation:portrait"), in: nil, to: "mov res:1440p"),
                       "orientation:portrait mov res:1440p")
        // On an empty query the token alone.
        XCTAssertEqual(RefineLogic.applying(opt("ext:pdf"), in: nil, to: "  "), "ext:pdf")
    }

    func testRemovingHandlesQuotedTokensAndCase() {
        XCTAssertEqual(RefineLogic.removing(#"camera:"canon eos r5""#, from: #"trip camera:"canon eos r5" gps:true"#),
                       "trip gps:true")
        XCTAssertEqual(RefineLogic.removing("RES:4K", from: "mov RES:4K"), "mov")
        XCTAssertEqual(RefineLogic.removing("res:8k", from: "mov res:4k"), "mov res:4k", "absent: unchanged")
    }

    func testAChosenGroupLeavesTheRowForItsPill() {
        let groups = [
            RefineGroup(id: "resolution", options: [opt("res:4k"), opt("res:1440p", selected: true), opt("res:sd")]),
            RefineGroup(id: "sound", options: [opt("a.count:>=1"), opt("a.count:0")]),
            RefineGroup(id: "folder", options: [opt("in:\"/Users/j/Docs\"", selected: true), opt("in:~/Docs/Work")]),
        ]
        let shown = RefineLogic.visible(groups, hiding: ["in:\"/Users/j/Docs\""])
        XCTAssertEqual(shown.map(\.id), ["sound", "folder"], "the chosen resolution is a pill; the default place does not close Folder")
        XCTAssertEqual(shown[0].options.count, 2, "other groups keep every value")
        XCTAssertEqual(shown[1].options.map(\.token), ["in:~/Docs/Work"])
    }

    func testPlainWordIsTheLastWordWhenNotAFilter() {
        XCTAssertEqual(RefineLogic.plainWord("invoice JPG"), "jpg")
        XCTAssertNil(RefineLogic.plainWord("mov res:1440p"))
        XCTAssertNil(RefineLogic.plainWord("jpg "))
        XCTAssertNil(RefineLogic.plainWord(""))
    }

    func testChoicesDecodeFromTheDaemonWithAndWithoutSelected() throws {
        let json = #"[{"id":"resolution","options":[{"token":"res:4k","label":"4K","count":3},{"token":"res:1080p","label":"1080p","count":0,"selected":true}]}]"#
        let groups = try JSONDecoder().decode([RefineGroup].self, from: Data(json.utf8))
        XCTAssertFalse(groups[0].options[0].isSelected)
        XCTAssertTrue(groups[0].options[1].isSelected)
        XCTAssertNil(groups[0].options[0].key)
    }

    // The owner chose "exclude choices": ⌥-click leaves a value out with a
    // "-" word; clicking the struck-through chip brings it back; the group
    // stays open for more.
    func testLeavingAChoiceOutAndBringingItBack() throws {
        let pdf = RefineOption(token: "ext:pdf", label: "PDF", key: nil, count: 2)
        XCTAssertEqual(RefineLogic.excluding(pdf, to: "report"), "-ext:pdf report")
        let chosen = RefineOption(token: "ext:pdf", label: "PDF", key: nil, count: 2, selected: true)
        XCTAssertEqual(RefineLogic.excluding(chosen, to: "report ext:pdf"), "-ext:pdf report")
        let out = RefineOption(token: "-ext:pdf", label: "PDF", key: nil, count: 0, selected: true, excluded: true)
        XCTAssertEqual(RefineLogic.applying(out, in: nil, to: "report -ext:pdf"), "report")
        let groups = [RefineGroup(id: "type", options: [RefineOption(token: "ext:txt", label: "TXT", key: nil, count: 1), out])]
        XCTAssertEqual(RefineLogic.visible(groups)[0].options.map(\.token), ["ext:txt"], "TXT stays to choose; PDF is a pill")
        let json = #"[{"id":"type","options":[{"token":"-ext:pdf","label":"PDF","count":0,"selected":true,"excluded":true}]}]"#
        let decoded = try JSONDecoder().decode([RefineGroup].self, from: Data(json.utf8))
        XCTAssertTrue(decoded[0].options[0].isExcluded)
    }
}

final class SearchPlaceTests: XCTestCase {
    // The owner chose a default search place: it joins each search as a
    // word, unless turned off for this search or the search names a place.
    func testThePlaceJoinsSearchesThatNameNone() {
        XCTAssertEqual(SearchPlace.effective("invoice", place: "", off: false), "invoice")
        XCTAssertEqual(SearchPlace.effective("invoice", place: "local", off: false), "invoice volume:local")
        XCTAssertEqual(SearchPlace.effective("invoice", place: "/Users/j/Docs", off: false), "invoice in:\"/Users/j/Docs\"")
        XCTAssertEqual(SearchPlace.effective("invoice", place: "local", off: true), "invoice")
        XCTAssertEqual(SearchPlace.effective("invoice in:~/Work", place: "local", off: false), "invoice in:~/Work")
        XCTAssertEqual(SearchPlace.effective("invoice -volume:archive", place: "local", off: false), "invoice -volume:archive")
        XCTAssertEqual(SearchPlace.effective("", place: "local", off: false), "")
        XCTAssertEqual(SearchPlace.effective(">dup", place: "local", off: false), ">dup")
        XCTAssertEqual(SearchPlace.title("/Users/j/Docs", thisMac: "This Mac"), "Docs")
    }
}

final class PillsTests: XCTestCase {
    // The owner's review: filters show as pills, the field keeps the words.
    func testFiltersBecomePillsAndWordsStay() {
        let s = Pills.split("ext:pdf -in:~/Downloads proj", typing: true)
        XCTAssertEqual(s.pills, ["ext:pdf", "-in:~/Downloads"])
        XCTAssertEqual(s.text, "proj")
        // The last word being typed stays text until a space follows.
        XCTAssertEqual(Pills.split("proj ext:pd", typing: true).text, "proj ext:pd")
        XCTAssertEqual(Pills.split("proj ext:pdf ", typing: true).pills, ["ext:pdf"])
        XCTAssertEqual(Pills.split("proj ext:pdf", typing: false).pills, ["ext:pdf"])
        // Quoted values and track groups stay whole; "ext:" alone is no pill.
        XCTAssertEqual(Pills.split(#"camera:"canon eos" trip ext:"#, typing: false).pills, [#"camera:"canon eos""#])
        XCTAssertTrue(Pills.isPill("a.count:0"))
        XCTAssertTrue(Pills.isPill("v.codec:hevc"))
        XCTAssertFalse(Pills.isPill("note:this"), "not a filter: a word")
        XCTAssertEqual(Pills.join(["ext:pdf"], "proj "), "ext:pdf proj ")
        XCTAssertEqual(Pills.join([], "proj"), "proj")
        XCTAssertEqual(Pills.removing("ext:pdf", from: "ext:pdf proj"), "proj")
    }

    func testPillsReadAsWords() {
        let w: (String) -> String = { k in
            ["not": "not %@", "in": "in %@", "on": "on %@", "thisMac": "This Mac", "kind.video": "Videos"][k] ?? ""
        }
        XCTAssertEqual(Pills.label("ext:pdf", words: w), "PDF")
        XCTAssertEqual(Pills.label("-ext:pdf", words: w), "not PDF")
        XCTAssertEqual(Pills.label(#"in:"/Users/j/Trips/2023""#, words: w), "in 2023")
        XCTAssertEqual(Pills.label("volume:local", words: w), "This Mac")
        XCTAssertEqual(Pills.label("kind:video", words: w), "Videos")
        XCTAssertEqual(Pills.label("res:4k", words: w), "4K")
    }
}

final class PathTextTests: XCTestCase {
    func testPathsReadAsPlaces() {
        let home = "/Users/j"
        XCTAssertEqual(PathText.components("/Users/j/Trips/2023", home: home).root, .home)
        XCTAssertEqual(PathText.components("/Users/j/Trips/2023", home: home).folders, ["Trips", "2023"])
        XCTAssertEqual(PathText.components("/Users/j/Library/Mobile Documents/com~apple~CloudDocs/Notes", home: home).root, .iCloud)
        XCTAssertEqual(PathText.components("/Volumes/Archive/Films", home: home).root, .drive("Archive"))
        let tmp = PathText.components("/private/var/folders/q2/abc123/T/tmp.X/fixtures", home: home)
        XCTAssertEqual(tmp.root, .temporary)
        XCTAssertEqual(tmp.folders, ["tmp.X", "fixtures"])
        let name: (PathText.Root) -> String = { r in
            switch r {
            case .home: return "Home"
            case .iCloud: return "iCloud Drive"
            case .temporary: return "Temporary"
            case let .drive(n): return n
            case .computer: return "Mac"
            case let .iCloudApp(n, _): return "iCloud Drive › " + n
            }
        }
        // An app's iCloud folder reads as the app (Finder: "iCloud Drive › Ulysses").
        let ul = PathText.components("/Users/j/Library/Mobile Documents/X5AZV975AG~com~soulmen~ulysses3/Documents/Library/Groups", home: home)
        XCTAssertEqual(ul.root, .iCloudApp(name: "Ulysses", container: "/Users/j/Library/Mobile Documents/X5AZV975AG~com~soulmen~ulysses3"))
        XCTAssertEqual(ul.folders, ["Library", "Groups"])
        XCTAssertEqual(PathText.appName(fromContainer: "com~apple~Pages"), "Pages")
        XCTAssertEqual(PathText.parts("/Users/j/a/b/c/d", home: home, rootName: name), ["Home", "…", "b", "c", "d"])
    }

    /// Consecutive groups of one family share a band; families keep the
    /// daemon's top-down order.
    func testGroupsFormBandsByFamily() {
        let o = RefineOption(token: "x", label: "x", key: nil, count: 1)
        let groups = [
            RefineGroup(id: "drive", family: "where", options: [o]),
            RefineGroup(id: "folder", family: "where", options: [o]),
            RefineGroup(id: "kind", family: "what", options: [o]),
            RefineGroup(id: "size", family: "file", options: [o]),
            RefineGroup(id: "modified", family: "file", options: [o]),
            RefineGroup(id: "resolution", family: "picture", options: [o]),
        ]
        let bands = RefineLogic.bands(groups)
        XCTAssertEqual(bands.map(\.family), ["where", "what", "file", "picture"])
        XCTAssertEqual(bands[0].groups.map(\.id), ["drive", "folder"])
        XCTAssertEqual(bands[2].groups.map(\.id), ["size", "modified"])
        // Visible keeps the family.
        XCTAssertEqual(RefineLogic.visible(groups).first?.family, "where")
    }

    /// The calm view: nothing that gives no results, four values a group
    /// (the biggest, in their usual order), four bands; More shows it all.
    func testCompactHidesWhatDoesNotApplyAndCountsIt() {
        func o(_ t: String, _ n: UInt64) -> RefineOption { RefineOption(token: t, label: t, key: nil, count: n) }
        let groups = [
            RefineGroup(id: "folder", family: "where", options: [o("a", 5), o("b", 0)]),
            RefineGroup(id: "kind", family: "what", options: [o("k", 3)]),
            RefineGroup(id: "size", family: "file", options: [o("s", 2)]),
            RefineGroup(id: "resolution", family: "picture",
                        options: [o("8k", 1), o("4k", 9), o("1440p", 2), o("1080p", 20), o("720p", 7), o("sd", 3)]),
            RefineGroup(id: "sound", family: "time", options: [o("with", 4), o("none", 1)]),
        ]
        let (shown, hidden) = RefineLogic.compact(groups, expanded: false)
        XCTAssertEqual(shown.map(\.id), ["folder", "kind", "size", "resolution"], "four bands")
        XCTAssertEqual(shown[0].options.map(\.token), ["a"], "no result: hidden")
        XCTAssertEqual(shown[3].options.map(\.token), ["4k", "1080p", "720p", "sd"], "the biggest four, in order")
        XCTAssertEqual(hidden, 1 + 2 + 2, "zero, two resolutions, the sound band")
        let all = RefineLogic.compact(groups, expanded: true)
        XCTAssertEqual(all.shown, groups)
        XCTAssertEqual(all.hidden, 0)
    }
}

final class NavigationTests: XCTestCase {
    /// → into a folder keeps the pills, replaces a folder pill, drops the words.
    func testEnteringAFolderMakesItAPill() {
        XCTAssertEqual(FolderNav.entering("/Users/j/Trips", from: "kind:image beach"), "kind:image in:\"/Users/j/Trips\" ")
        XCTAssertEqual(FolderNav.entering("/Users/j/Trips/2023", from: "in:\"/Users/j/Trips\" "), "in:\"/Users/j/Trips/2023\" ")
    }

    /// Tab and ↑↓ go from group to group; the choices read in order.
    func testChoicesInOrderAndGroupStarts() {
        func o(_ t: String) -> RefineOption { RefineOption(token: t, label: t, key: nil, count: 1) }
        let groups = [RefineGroup(id: "folder", options: [o("a"), o("b")]), RefineGroup(id: "kind", options: [o("k")]),
                      RefineGroup(id: "size", options: [o("s1"), o("s2")])]
        XCTAssertEqual(ChoiceNav.flat(groups).map(\.option.token), ["a", "b", "k", "s1", "s2"])
        XCTAssertEqual(ChoiceNav.groupStart(groups, from: 1, delta: 1), 2)
        XCTAssertEqual(ChoiceNav.groupStart(groups, from: 2, delta: 1), 3)
        XCTAssertEqual(ChoiceNav.groupStart(groups, from: 4, delta: 1), 3, "stays on the last group")
        XCTAssertEqual(ChoiceNav.groupStart(groups, from: 3, delta: -1), 2)
    }
}

final class TimelineTests: XCTestCase {
    /// Empty months between the first and the last are filled in; a click
    /// is one month, a drag a span; it replaces a date filter.
    func testMonthsAndTokens() {
        let m = TimelineLogic.months([MonthCount(month: "2023-11", count: 2), MonthCount(month: "2024-02", count: 5)])
        XCTAssertEqual(m.map(\.month), ["2023-11", "2023-12", "2024-01", "2024-02"])
        XCTAssertEqual(m.map(\.count), [2, 0, 0, 5])
        XCTAssertEqual(TimelineLogic.token(from: "2024-05", to: "2024-03"), "modified:2024-03..2024-05")
        XCTAssertEqual(TimelineLogic.token(from: "2024-03", to: "2024-03"), "modified:2024-03")
        XCTAssertEqual(TimelineLogic.applying("modified:2024-03", to: "since:7d trip"), "modified:2024-03 trip")
        XCTAssertEqual(TimelineLogic.applying("modified:2024-03", to: "kind:image "), "modified:2024-03 kind:image ")
    }
}

final class BatchRenameTests: XCTestCase {
    /// #s become the number (padded), each file keeps its extension; with
    /// no #, the number goes at the end.
    func testPatternNames() {
        let files = ["IMG_1.jpg", "IMG_2.heic", "notes"]
        XCTAssertEqual(BatchRename.names(for: files, pattern: "Trip – ##"), ["Trip – 01.jpg", "Trip – 02.heic", "Trip – 03"])
        XCTAssertEqual(BatchRename.names(for: files, pattern: "Trip", start: 9), ["Trip 09.jpg", "Trip 10.heic", "Trip 11"])
        XCTAssertNil(BatchRename.problem(["a.jpg", "b.jpg"]))
        XCTAssertEqual(BatchRename.problem(["a.jpg", "A.jpg"]), "twice")
        XCTAssertEqual(BatchRename.problem(["a/b.jpg"]), "slash")
    }
}
