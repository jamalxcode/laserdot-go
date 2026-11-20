import AppKit
import LaserdotKit

/// The start (the owner, with a screenshot of macOS's Spotlight: "a similar
/// spotlight start when laserdot gets pressed, options appear when they are
/// needed"): ⌥Space shows a rounded search bar and a row of round buttons —
/// kinds of files first (recently opened, videos, images, music, documents,
/// apps), then the menu's tools (duplicates, storage, drives while one is
/// attached, index a folder, settings). Typing, ↓ or a button grows it into
/// the full window.
extension SearchModel {
    struct StartButton: Identifiable {
        let id: String
        let symbol: String
        let title: String
        /// The menu's tools come after the kinds of files, a little apart.
        var isTool = false
        let run: () -> Void
    }

    var startButtons: [StartButton] {
        func kind(_ id: String, _ symbol: String, _ title: String, _ q: String) -> StartButton {
            StartButton(id: id, symbol: symbol, title: title) { [weak self] in
                self?.query = q + " " // a pill, ready for words
                self?.focusSearch()
            }
        }
        var out: [StartButton] = [
            StartButton(id: "recent", symbol: "clock", title: L("start.recent")) { [weak self] in
                self?.showHomeList = true
            },
            kind("videos", "film", L("refine.kind.video"), "kind:video"),
            kind("images", "photo", L("refine.kind.image"), "kind:image"),
            kind("music", "music.note", L("refine.kind.audio"), "kind:audio"),
            kind("documents", "doc.text", L("refine.kind.document"), "kind:document"),
            kind("apps", "square.grid.2x2", L("refine.kind.app"), "kind:app"),
        ]
        let commands = commandProvider?() ?? []
        func tool(_ id: String, _ symbol: String, _ title: String) -> StartButton? {
            commands.first { $0.id == id }.map { c in StartButton(id: id, symbol: symbol, title: title, isTool: true, run: c.run) }
        }
        out += [tool("dupes", "square.on.square", L("menu.duplicates")), tool("storage", "chart.pie", L("menu.storage"))]
            .compactMap { $0 }
        // Drives only when there is one to show.
        if commands.contains(where: { $0.id.hasPrefix("eject:") }), let settings = commands.first(where: { $0.id == "settings" }) {
            out.append(StartButton(id: "drives", symbol: "externaldrive", title: L("menu.drives"), isTool: true) {
                UserDefaults.standard.set("drives", forKey: "laserdot.settingsTab")
                settings.run()
            })
        }
        out += [tool("index", "folder.badge.plus", L("menu.index")), tool("settings", "gearshape", L("menu.settings"))]
            .compactMap { $0 }
        return out
    }
}
