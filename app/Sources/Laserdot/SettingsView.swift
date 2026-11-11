import AppKit
import LaserdotKit
import ServiceManagement
import SwiftUI

/// Laserdot ▸ Settings… (⌘,), as the Human Interface Guidelines ask: the
/// app's options in one standard window instead of scattered menu items.
struct SettingsView: View {
    @ObservedObject var delegate: AppDelegate
    @ObservedObject var updates: UpdateController

    var body: some View {
        TabView {
            GeneralSettings(delegate: delegate, updates: updates)
                .tabItem { Label(L("settings.general"), systemImage: "gearshape") }
            Group {
                if let engine = delegate.sharedEngine {
                    FoldersSettings(model: delegate.folders ?? FoldersModel(engine: engine), delegate: delegate)
                } else {
                    Text(L("daemon.starting")).foregroundStyle(.secondary).frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            .tabItem { Label(L("settings.folders"), systemImage: "folder") }
            Group {
                if let content = delegate.content {
                    ContentSettings(model: content)
                } else {
                    Text(L("daemon.starting")).foregroundStyle(.secondary).frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            .tabItem { Label(L("menu.content"), systemImage: "doc.text.magnifyingglass") }
            Group {
                if let drives = delegate.drives {
                    DrivesSettings(model: drives)
                } else {
                    Text(L("daemon.starting")).foregroundStyle(.secondary).frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            .tabItem { Label(L("menu.drives"), systemImage: "externaldrive") }
        }
        .frame(width: 560, height: 460)
    }
}

struct GeneralSettings: View {
    @ObservedObject var delegate: AppDelegate
    @ObservedObject var updates: UpdateController
    @State private var openAtLogin = SMAppService.mainApp.status == .enabled
    @State private var loginNote: String?
    @State private var fullDiskAccess = DiskAccess.granted
    @State private var historyCleared = false

    var body: some View {
        Form {
            Section {
                Picker(L("settings.hotkey"), selection: Binding(
                    get: { delegate.hotKeyChoice.label },
                    set: { label in
                        if let c = HotKeyChoice.all.first(where: { $0.label == label }) { delegate.useHotKey(c) }
                    }
                )) {
                    ForEach(HotKeyChoice.all, id: \.label) { Text($0.label).tag($0.label) }
                }
                Toggle(L("settings.openAtLogin"), isOn: Binding(get: { openAtLogin }, set: setOpenAtLogin))
                if let loginNote {
                    Text(loginNote).font(.caption).foregroundStyle(.secondary)
                }
            }
            Section(L("settings.updates")) {
                Toggle(L("settings.autoUpdate"), isOn: $updates.autoCheck)
                LabeledContent(L("settings.version"), value: updates.currentVersion)
                HStack {
                    Spacer()
                    if updates.checking { ProgressView().controlSize(.small) }
                    Button(L("settings.checkNow")) { updates.check(interactive: true) }
                        .disabled(updates.checking)
                }
            }
            Section(L("settings.index")) {
                LabeledContent(L("settings.indexFile")) {
                    HStack {
                        Text("~/Library/Application Support/laserdot/index.db")
                            .font(.caption.monospaced())
                            .foregroundStyle(.secondary)
                            .textSelection(.enabled)
                        Button(L("action.reveal")) {
                            NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: Paths.defaultDbPath)])
                        }
                    }
                }
                Text(L("settings.indexHelp")).font(.caption).foregroundStyle(.secondary)
            }
            Section(L("settings.history")) {
                HStack {
                    Text(L("settings.historyHelp")).font(.caption).foregroundStyle(.secondary)
                    Spacer()
                    Button(L("settings.clearHistory")) {
                        delegate.model?.clearHistory()
                        historyCleared = true
                    }
                    .disabled(historyCleared)
                }
                Button(L("menu.tour")) { delegate.showTour = true }
            }
            Section(L("settings.privacy")) {
                LabeledContent(L("settings.network"), value: delegate.networkLine)
                LabeledContent(L("settings.fullDiskAccess")) {
                    HStack {
                        Text(L(fullDiskAccess ? "settings.on" : "settings.off")).foregroundStyle(.secondary)
                        if !fullDiskAccess {
                            Button(L("fda.openSettings")) { DiskAccessPrompt.shared.askFromMenu() }
                        }
                    }
                }
                Text(L("settings.fullDiskAccessHelp")).font(.caption).foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            fullDiskAccess = DiskAccess.granted
            openAtLogin = SMAppService.mainApp.status == .enabled
        }
    }

    private func setOpenAtLogin(_ on: Bool) {
        do {
            if on {
                try SMAppService.mainApp.register()
            } else {
                try SMAppService.mainApp.unregister()
            }
            loginNote = nil
        } catch {
            loginNote = error.localizedDescription
        }
        let status = SMAppService.mainApp.status
        openAtLogin = status == .enabled
        if status == .requiresApproval {
            loginNote = L("settings.loginApproval")
            SMAppService.openSystemSettingsLoginItems()
        }
    }
}

/// The folders whose files' text is searchable (spec §2.4.3), with the
/// standard + and − buttons under the list.
struct ContentSettings: View {
    @ObservedObject var model: ContentModel
    @State private var selection: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(L("settings.contentIntro")).foregroundStyle(.secondary)
            List(selection: $selection) {
                ForEach(model.info?.folders ?? [], id: \.self) { f in
                    Label {
                        VStack(alignment: .leading) {
                            Text(URL(fileURLWithPath: f).lastPathComponent)
                            Text(f).font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                        }
                    } icon: {
                        Image(nsImage: NSWorkspace.shared.icon(forFile: f)).resizable().frame(width: 20, height: 20)
                    }
                    .tag(f)
                }
            }
            .listStyle(.bordered(alternatesRowBackgrounds: true))
            HStack(spacing: 0) {
                Button { model.addFolder() } label: { Image(systemName: "plus").frame(width: 22, height: 18) }
                    .accessibilityLabel(L("content.add"))
                Button {
                    if let f = selection { model.set(f, add: false) }
                    selection = nil
                } label: { Image(systemName: "minus").frame(width: 22, height: 18) }
                    .disabled(selection == nil)
                    .accessibilityLabel(L("content.remove"))
                Spacer()
                if let info = model.info {
                    Text(String(format: L("content.status"), Int64(info.documents), Int64(info.pending)))
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            .buttonStyle(.borderless)
        }
        .padding(20)
        .onAppear { model.refresh() }
    }
}

/// The indexed folders and the folders never indexed (spec §13.6).
final class FoldersModel: ObservableObject {
    @Published private(set) var indexed: [String] = []
    @Published private(set) var excluded: [String] = []
    @Published private(set) var busy = false
    private let engine: Engine
    private let queue = DispatchQueue(label: "laserdot.folders", qos: .userInitiated)

    init(engine: Engine) {
        self.engine = engine
    }

    func refresh() {
        let engine = self.engine
        queue.async { [weak self] in
            let folders = engine.status().folders.sorted()
            let excluded = engine.exclusions()
            DispatchQueue.main.async {
                self?.indexed = folders
                self?.excluded = excluded
            }
        }
    }

    private func run(_ f: @escaping (Engine) throws -> Void) {
        busy = true
        let engine = self.engine
        queue.async { [weak self] in
            var failure: Error?
            do { try f(engine) } catch { failure = error }
            DispatchQueue.main.async {
                self?.busy = false
                if let failure { NSAlert(error: failure).runFront() }
                self?.refresh()
            }
        }
    }

    func remove(_ path: String) { run { _ = try $0.forget(path: path) } }
    func exclude(_ path: String) { run { try $0.exclude(path: path) } }
    func include(_ path: String) { run { try $0.include(path: path) } }

    func chooseToExclude() {
        NSApp.activate(ignoringOtherApps: true)
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.message = L("settings.excludeChoose")
        guard panel.runFront() == .OK, let url = panel.url else { return }
        exclude(url.path)
    }
}

struct FoldersSettings: View {
    @ObservedObject var model: FoldersModel
    let delegate: AppDelegate
    @State private var selectedIndexed: String?
    @State private var selectedExcluded: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(L("settings.foldersIntro")).foregroundStyle(.secondary)
            folderList(model.indexed, selection: $selectedIndexed)
            HStack(spacing: 0) {
                Button { delegate.chooseFolder(); model.refresh() } label: { Image(systemName: "plus").frame(width: 22, height: 18) }
                    .accessibilityLabel(L("menu.index"))
                Button {
                    if let f = selectedIndexed { model.remove(f) }
                    selectedIndexed = nil
                } label: { Image(systemName: "minus").frame(width: 22, height: 18) }
                    .disabled(selectedIndexed == nil)
                    .accessibilityLabel(L("settings.removeFolder"))
                Spacer()
                if model.busy { ProgressView().controlSize(.small) }
            }
            .buttonStyle(.borderless)
            Text(L("settings.excluded")).font(.headline).padding(.top, 6)
            Text(L("settings.excludedHelp")).font(.caption).foregroundStyle(.secondary)
            folderList(model.excluded, selection: $selectedExcluded)
            HStack(spacing: 0) {
                Button { model.chooseToExclude() } label: { Image(systemName: "plus").frame(width: 22, height: 18) }
                    .accessibilityLabel(L("settings.excludeChoose"))
                Button {
                    if let f = selectedExcluded { model.include(f) }
                    selectedExcluded = nil
                } label: { Image(systemName: "minus").frame(width: 22, height: 18) }
                    .disabled(selectedExcluded == nil)
                    .accessibilityLabel(L("settings.includeAgain"))
                Spacer()
            }
            .buttonStyle(.borderless)
        }
        .padding(20)
        .onAppear { model.refresh() }
    }

    private func folderList(_ folders: [String], selection: Binding<String?>) -> some View {
        List(selection: selection) {
            ForEach(folders, id: \.self) { f in
                Label {
                    VStack(alignment: .leading) {
                        Text(f == "/" ? "/" : URL(fileURLWithPath: f).lastPathComponent)
                        Text(f).font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                    }
                } icon: {
                    Image(nsImage: NSWorkspace.shared.icon(forFile: f)).resizable().frame(width: 18, height: 18)
                }
                .tag(f)
            }
        }
        .listStyle(.bordered(alternatesRowBackgrounds: true))
        .frame(minHeight: 90)
    }
}

/// Every mounted drive: where its index is and what can be done with it
/// (the same actions as the Drives menu).
struct DrivesSettings: View {
    @ObservedObject var model: DrivesModel

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(L("settings.drivesIntro")).foregroundStyle(.secondary)
            if model.drives.isEmpty {
                Text(L("drive.none")).foregroundStyle(.secondary).frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List {
                    ForEach(model.drives, id: \.mount) { d in
                        HStack(alignment: .top, spacing: 10) {
                            Image(systemName: d.encrypted ? "lock.fill" : "externaldrive.fill")
                                .font(.title2).foregroundStyle(.secondary).frame(width: 28)
                            VStack(alignment: .leading, spacing: 2) {
                                Text(d.name).font(.headline)
                                Text(model.label(d)).font(.caption).foregroundStyle(.secondary)
                                Text(locationText(d)).font(.caption).foregroundStyle(.secondary)
                            }
                            Spacer()
                            actions(d)
                        }
                        .padding(.vertical, 4)
                    }
                }
                .listStyle(.bordered(alternatesRowBackgrounds: true))
            }
            if let busy = model.busy {
                HStack { ProgressView().controlSize(.small); Text(busy).font(.caption) }
            }
        }
        .padding(20)
    }

    private func locationText(_ d: Drive) -> String {
        if d.state == "unindexed" { return L("settings.driveNotIndexed") }
        let size = ByteCountFormatter.string(fromByteCount: Int64(d.indexBytes), countStyle: .file)
        return String(format: L(d.onDrive ? "settings.driveOnDrive" : "settings.driveOnMac"), size)
    }

    @ViewBuilder private func actions(_ d: Drive) -> some View {
        Menu(L("keys.actions")) {
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
        .fixedSize()
        .disabled(model.busy != nil)
    }
}
