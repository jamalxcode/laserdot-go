import AppKit
import LaserdotKit
import SwiftUI

/// Drives submenu (spec §9.6): one entry per mounted drive, with the
/// actions its state allows. Hidden while the daemon has no drive support.
struct DrivesMenu: View {
    @ObservedObject var model: DrivesModel

    var body: some View {
        if model.available {
            menu
        }
    }

    private var menu: some View {
        Menu(L("menu.drives")) {
            if let busy = model.busy {
                Text(busy)
                Divider()
            }
            if model.drives.isEmpty {
                Text(L("drive.none"))
            }
            ForEach(model.drives, id: \.mount) { d in
                Menu(model.label(d)) {
                    switch d.state {
                    case "attached":
                        Button(L("drive.reveal")) { model.reveal(d) }
                        Button(L("drive.rebuild")) { model.promptIndex(d, rebuild: true) }
                        Button(L("drive.detach")) { model.detach(d) }
                        Button(L("drive.delete")) { model.delete(d) }
                    case "detached":
                        Button(L("drive.attach")) { model.attach(d) }
                        Button(L("drive.delete")) { model.delete(d) }
                    case "locked":
                        Button(L("drive.unlock")) { model.promptUnlock(d) }
                        Button(L("drive.delete")) { model.delete(d) }
                    case "unreadable":
                        Button(L("drive.rebuild")) { model.promptIndex(d, rebuild: true) }
                    default:
                        Button(L("drive.index")) { model.promptIndex(d) }
                    }
                    Divider()
                    Button(L("drive.eject")) { model.eject(d) }
                }
                .disabled(model.busy != nil)
            }
        }
    }
}

/// Content search folders (spec §2.4.3): which folders' files have their
/// text indexed.
final class ContentModel: ObservableObject {
    @Published private(set) var info: ContentInfo?
    /// The daemon implements content search (older ones answer `unsupported`).
    @Published private(set) var available = true
    private let engine: Engine
    private let queue = DispatchQueue(label: "laserdot.content", qos: .utility)

    init(engine: Engine) {
        self.engine = engine
    }

    func refresh() {
        let engine = self.engine
        queue.async { [weak self] in
            let outcome = Result { try engine.contentInfo() }
            DispatchQueue.main.async {
                guard let self else { return }
                self.info = try? outcome.get()
                if case let .failure(e) = outcome { self.available = !e.isUnsupported } else { self.available = true }
            }
        }
    }

    func set(_ folder: String, add: Bool) {
        let engine = self.engine
        queue.async { [weak self] in
            let info = try? engine.setContentFolder(folder: folder, add: add)
            DispatchQueue.main.async { self?.info = info ?? self?.info }
        }
    }

    func setSkipCode(_ skip: Bool) {
        let engine = self.engine
        queue.async { [weak self] in
            let info = try? engine.setContentSkipCode(skip)
            DispatchQueue.main.async { self?.info = info ?? self?.info }
        }
    }

    func addFolder() {
        NSApp.activate(ignoringOtherApps: true)
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        guard panel.runFront() == .OK, let url = panel.url else { return }
        set(url.path, add: true)
    }
}

struct ContentMenu: View {
    @ObservedObject var model: ContentModel

    var body: some View {
        if model.available {
            menu
        }
    }

    private var menu: some View {
        Menu(L("menu.content")) {
            if let info = model.info {
                Text(String(format: L("content.status"), Int64(info.documents), Int64(info.pending)))
                Divider()
                ForEach(info.folders, id: \.self) { f in
                    Menu(URL(fileURLWithPath: f).lastPathComponent) {
                        Text(f)
                        Button(L("content.remove")) { model.set(f, add: false) }
                    }
                }
                Divider()
            }
            Button(L("content.add")) { model.addFolder() }
            Button(L("content.refresh")) { model.refresh() }
        }
    }
}
