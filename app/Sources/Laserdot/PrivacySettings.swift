import AppKit
import LaserdotKit
import SwiftUI

/// Settings ▸ Privacy: what Laserdot keeps about your files, where, and how
/// big; the network lock; and Erase All Laserdot Data.
struct PrivacySettings: View {
    @ObservedObject var delegate: AppDelegate
    @State private var items: [StoredItem] = []
    @State private var passphrases = 0
    @State private var fullDiskAccess = DiskAccess.granted
    @State private var historyCleared = false

    struct StoredItem: Identifiable {
        let id: String
        let title: String
        let path: String
        let bytes: Int64
    }

    var body: some View {
        Form {
            Section(L("privacy.kept")) {
                ForEach(items) { item in
                    LabeledContent {
                        HStack {
                            Text(ByteCountFormatter.string(fromByteCount: item.bytes, countStyle: .file))
                                .foregroundStyle(.secondary)
                                .monospacedDigit()
                            Button { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: item.path)]) } label: {
                                Image(systemName: "magnifyingglass.circle")
                            }
                            .buttonStyle(.borderless)
                            .help(L("action.reveal"))
                            .accessibilityLabel(L("action.reveal"))
                        }
                    } label: {
                        Text(item.title)
                    }
                }
                LabeledContent(L("privacy.passphrases"), value: "\(passphrases)")
                Text(L("privacy.keptHelp")).font(.caption).foregroundStyle(.secondary)
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
            Section {
                HStack {
                    Text(L("privacy.eraseHelp")).font(.caption).foregroundStyle(.secondary)
                    Spacer()
                    Button(L("privacy.erase"), role: .destructive) { delegate.confirmEraseAll() }
                }
            }
        }
        .formStyle(.grouped)
        .onAppear(perform: load)
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            fullDiskAccess = DiskAccess.granted
            load()
        }
    }

    private func load() {
        items = Self.storedItems()
        passphrases = Keychain.count()
    }

    /// The files in the data folder, grouped as people think of them.
    static func storedItems() -> [StoredItem] {
        let dir = (Paths.defaultDbPath as NSString).deletingLastPathComponent
        let fm = FileManager.default
        func size(_ name: String) -> Int64 {
            let path = (dir as NSString).appendingPathComponent(name)
            var isDir: ObjCBool = false
            guard fm.fileExists(atPath: path, isDirectory: &isDir) else { return 0 }
            if !isDir.boolValue { return (try? fm.attributesOfItem(atPath: path)[.size] as? Int64) ?? 0 }
            var total: Int64 = 0
            if let files = fm.enumerator(atPath: path) {
                for case let f as String in files {
                    let full = (path as NSString).appendingPathComponent(f)
                    total += (try? fm.attributesOfItem(atPath: full)[.size] as? Int64) ?? 0
                }
            }
            return total
        }
        let names = (try? fm.contentsOfDirectory(atPath: dir)) ?? []
        let index = names.filter { $0.hasPrefix("index.db") }.reduce(0) { $0 + size($1) }
        let content = names.filter { $0.hasPrefix("content.db") }.reduce(0) { $0 + size($1) }
        let known = Set(names.filter { $0.hasPrefix("index.db") || $0.hasPrefix("content.db") || $0 == "drives" })
        let other = names.filter { !known.contains($0) && !$0.hasSuffix(".sock") }.reduce(0) { $0 + size($1) }
        return [
            StoredItem(id: "index", title: L("privacy.index"), path: (dir as NSString).appendingPathComponent("index.db"), bytes: index),
            StoredItem(id: "content", title: L("privacy.content"), path: dir, bytes: content),
            StoredItem(id: "drives", title: L("privacy.drives"), path: dir, bytes: size("drives")),
            StoredItem(id: "other", title: L("privacy.other"), path: dir, bytes: other),
        ] + signingItem()
    }

    /// This Mac's key for signing updates (LocalSigner), once it exists.
    static func signingItem() -> [StoredItem] {
        let signer = LocalSigner()
        guard signer.hasKey else { return [] }
        let fm = FileManager.default
        let files = (try? fm.contentsOfDirectory(atPath: signer.dir.path)) ?? []
        let bytes = files.reduce(Int64(0)) { sum, f in
            sum + ((try? fm.attributesOfItem(atPath: signer.dir.appendingPathComponent(f).path)[.size] as? Int64) ?? 0)
        }
        return [StoredItem(id: "signing", title: L("privacy.signing"), path: signer.dir.path, bytes: bytes)]
    }
}
