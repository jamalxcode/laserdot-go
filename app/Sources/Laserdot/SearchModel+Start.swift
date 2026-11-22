import AppKit
import LaserdotKit

/// The choice bar under the search box, in every window and for every search
/// (the owner: "lets have the full option, full choices screen all the time
/// for all the searches"): kinds of files and common searches, then the
/// menu's tools (duplicates, storage, drives while one is attached, index a
/// folder, settings). A kind or common search joins the current search as
/// pills, keeping the typed words; clicking it again takes it out.
extension SearchModel {
    struct StartButton: Identifiable {
        let id: String
        let symbol: String
        let title: String
        /// The menu's tools come after the kinds of files, a little apart.
        var isTool = false
        /// The filters it adds (empty for tools).
        var pills: [String] = []
        let run: () -> Void
    }

    /// The round buttons as shown: all of them on the empty box; while
    /// searching only the kinds of files (the tools wait in the "More"
    /// button's menu: `barTools`), so the screen stays calm.
    var barButtons: [StartButton] {
        let all = startButtons
        return query.trimmingCharacters(in: .whitespaces).isEmpty ? all : all.filter { !$0.isTool }
    }

    /// The tools folded into the "More" button while searching.
    var barTools: [StartButton] {
        query.trimmingCharacters(in: .whitespaces).isEmpty ? [] : startButtons.filter(\.isTool)
    }

    var startButtons: [StartButton] {
        func search(_ id: String, _ symbol: String, _ title: String, _ tokens: [String]) -> StartButton {
            StartButton(id: id, symbol: symbol, title: title, pills: tokens) { [weak self] in
                self?.toggleBar(tokens)
            }
        }
        var out: [StartButton] = [
            search("videos", "film", L("refine.kind.video"), ["kind:video"]),
            search("images", "photo", L("refine.kind.image"), ["kind:image"]),
            search("music", "music.note", L("refine.kind.audio"), ["kind:audio"]),
            search("documents", "doc.text", L("refine.kind.document"), ["kind:document"]),
            search("apps", "square.grid.2x2", L("refine.kind.app"), ["kind:app"]),
            search("downloads", "arrow.down.circle", L("start.downloads"), ["in:~/Downloads", "since:30d"]),
            search("big", "externaldrive", L("start.big"), ["fsize:>1gb"]),
        ]
        let commands = commandProvider?() ?? []
        func tool(_ id: String, _ symbol: String, _ title: String) -> StartButton? {
            commands.first { $0.id == id }.map { c in StartButton(id: id, symbol: symbol, title: title, isTool: true, run: c.run) }
        }
        out += [tool("dupes", "square.on.square", L("menu.duplicates")), tool("storage", "chart.pie", L("menu.storage"))]
            .compactMap { $0 }
        // Drives only when there is one to show.
        if commands.contains(where: { $0.id.hasPrefix("eject:") }), let settings = commands.first(where: { $0.id == "settings" }) {
            out.append(StartButton(id: "drives", symbol: "externaldrive.connected.to.line.below", title: L("menu.drives"), isTool: true) {
                UserDefaults.standard.set("drives", forKey: "laserdot.settingsTab")
                settings.run()
            })
        }
        out += [tool("index", "folder.badge.plus", L("menu.index")), tool("settings", "gearshape", L("menu.settings"))]
            .compactMap { $0 }
        return out
    }

    /// The button's filters are all in the search (it shows as chosen).
    func barButtonIsOn(_ b: StartButton) -> Bool {
        !b.pills.isEmpty && b.pills.allSatisfy(pills.contains)
    }

    /// A bar button: its filters join the search as pills (another kind
    /// takes the place of the one chosen), or leave it if all were there.
    func toggleBar(_ tokens: [String]) {
        let parts = fieldParts
        let on = tokens.allSatisfy(parts.pills.contains)
        var kept = parts.pills.filter { !tokens.contains($0) }
        if !on {
            if tokens.contains(where: { $0.hasPrefix("kind:") }) {
                kept.removeAll { $0.hasPrefix("kind:") }
            }
            kept = tokens + kept
        }
        let q = Pills.join(kept, parts.text)
        query = parts.text.isEmpty && !kept.isEmpty ? q + " " : q
        focusSearch()
    }
}
