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
                if let content = delegate.content {
                    ContentSettings(model: content)
                } else {
                    Text(L("daemon.starting")).foregroundStyle(.secondary).frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            .tabItem { Label(L("menu.content"), systemImage: "doc.text.magnifyingglass") }
        }
        .frame(width: 520, height: 400)
    }
}

struct GeneralSettings: View {
    @ObservedObject var delegate: AppDelegate
    @ObservedObject var updates: UpdateController
    @State private var openAtLogin = SMAppService.mainApp.status == .enabled
    @State private var loginNote: String?
    @State private var fullDiskAccess = DiskAccess.granted

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
