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
        XCTAssertEqual(RefineLogic.applying(opt("res:1440p"), in: res, to: "mov"), "mov res:1440p")
        // With 1440p chosen, 4K takes its place (a video has one resolution).
        let chosen = RefineGroup(id: "resolution", options: [opt("res:4k"), opt("res:1440p", selected: true)])
        XCTAssertEqual(RefineLogic.applying(opt("res:4k"), in: chosen, to: "mov res:1440p a.count:>=1"),
                       "mov a.count:>=1 res:4k")
        // Clicking the chosen one takes it out, tidying spaces.
        XCTAssertEqual(RefineLogic.applying(opt("res:1440p", selected: true), in: chosen, to: "mov  res:1440p a.count:>=1"),
                       "mov a.count:>=1")
        // A choice from another group is added alongside.
        XCTAssertEqual(RefineLogic.applying(opt("orientation:portrait"), in: nil, to: "mov res:1440p"),
                       "mov res:1440p orientation:portrait")
        // On an empty query the token alone.
        XCTAssertEqual(RefineLogic.applying(opt("ext:pdf"), in: nil, to: "  "), "ext:pdf")
    }

    func testRemovingHandlesQuotedTokensAndCase() {
        XCTAssertEqual(RefineLogic.removing(#"camera:"canon eos r5""#, from: #"trip camera:"canon eos r5" gps:true"#),
                       "trip gps:true")
        XCTAssertEqual(RefineLogic.removing("RES:4K", from: "mov RES:4K"), "mov")
        XCTAssertEqual(RefineLogic.removing("res:8k", from: "mov res:4k"), "mov res:4k", "absent: unchanged")
    }

    func testAChosenGroupShrinksToItsChoiceAndComesFirst() {
        let groups = [
            RefineGroup(id: "resolution", options: [opt("res:4k"), opt("res:1440p", selected: true), opt("res:sd")]),
            RefineGroup(id: "sound", options: [opt("a.count:>=1"), opt("a.count:0")]),
            RefineGroup(id: "type", options: [opt("ext:pdf"), opt("ext:txt", selected: true)]),
        ]
        let shown = RefineLogic.visible(groups)
        XCTAssertEqual(shown.map(\.id), ["resolution", "type", "sound"])
        XCTAssertEqual(shown[0].options.map(\.token), ["res:1440p"])
        XCTAssertEqual(shown[1].options.map(\.token), ["ext:txt"])
        XCTAssertEqual(shown[2].options.count, 2, "other groups keep every value")
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
        XCTAssertEqual(RefineLogic.excluding(pdf, to: "report"), "report -ext:pdf")
        let chosen = RefineOption(token: "ext:pdf", label: "PDF", key: nil, count: 2, selected: true)
        XCTAssertEqual(RefineLogic.excluding(chosen, to: "report ext:pdf"), "report -ext:pdf")
        let out = RefineOption(token: "-ext:pdf", label: "PDF", key: nil, count: 0, selected: true, excluded: true)
        XCTAssertEqual(RefineLogic.applying(out, in: nil, to: "report -ext:pdf"), "report")
        let groups = [RefineGroup(id: "type", options: [RefineOption(token: "ext:txt", label: "TXT", key: nil, count: 1), out])]
        XCTAssertEqual(RefineLogic.visible(groups)[0].options.count, 2, "TXT stays to choose")
        let json = #"[{"id":"type","options":[{"token":"-ext:pdf","label":"PDF","count":0,"selected":true,"excluded":true}]}]"#
        let decoded = try JSONDecoder().decode([RefineGroup].self, from: Data(json.utf8))
        XCTAssertTrue(decoded[0].options[0].isExcluded)
    }
}
