import AppKit
import LaserdotKit
import SwiftUI

/// Storage (spec §17.3): what fills the disk, from the index — folders by
/// total size with a bar each; click a folder to look inside, a file to show
/// it in Finder. A second tab lists the largest files.
final class StorageModel: ObservableObject {
    @Published private(set) var report: Wire.StorageReport?
    @Published private(set) var trail: [String] = [] // folders opened, deepest last
    @Published private(set) var largest: [Hit] = []
    @Published private(set) var loading = false
    @Published var message: String?
    let engine: Engine
    private let queue = DispatchQueue(label: "laserdot.storage", qos: .userInitiated)
    /// Moves paths to the Trash with Undo (the search model's); set by the
    /// app.
    var trash: ([String], @escaping (Error?) -> Void) -> Void = { _, done in done(nil) }

    /// Move to Trash from Storage: confirmed in plain words (what, how much
    /// space, nothing deleted, how to get it back), then moved with Undo,
    /// then the sizes are read again.
    func moveToTrash(path: String, name: String, bytes: UInt64, confirm: Bool = true, done: @escaping (Bool) -> Void = { _ in }) {
        if confirm {
            let a = NSAlert()
            a.messageText = String(format: L("storage.trash.title"), name)
            a.informativeText = String(format: L("storage.trash.text"),
                                       ByteCountFormatter.string(fromByteCount: Int64(bytes), countStyle: .file))
            a.addButton(withTitle: L("storage.trash.confirm")).hasDestructiveAction = true
            a.addButton(withTitle: L("action.cancel")).keyEquivalent = "\u{1b}"
            guard a.runFront() == .alertFirstButtonReturn else { return done(false) }
        }
        trash([path]) { [weak self] error in
            if let error {
                NSAlert(error: error).runFront()
                done(false)
                return
            }
            self?.load()
            done(true)
        }
    }

    init(engine: Engine) {
        self.engine = engine
    }

    var current: String { trail.last ?? "" }

    /// Packages still being measured: asked again shortly (each request
    /// measures more, and remembers what it measured), a few times.
    private var remeasures = 0

    func load(_ path: String? = nil, again: Bool = false) {
        if !again { remeasures = 0 }
        if let path {
            if let i = trail.firstIndex(of: path) {
                trail = Array(trail[...i])
            } else {
                trail.append(path)
            }
        }
        let (engine, p) = (self.engine, current)
        loading = true
        queue.async { [weak self] in
            let r = Result { try engine.storage(path: p) }
            let files = (try? engine.search(query: "fsize:>1mb -kind:folder", limit: 60, sort: .size).hits) ?? []
            DispatchQueue.main.async {
                self?.loading = false
                switch r {
                case .success(let rep):
                    self?.report = rep
                    self?.message = nil
                    if rep.partial, let self, self.remeasures < 10 {
                        self.remeasures += 1
                        let at = self.current
                        DispatchQueue.main.asyncAfter(deadline: .now() + 1) { [weak self] in
                            if self?.current == at { self?.load(again: true) }
                        }
                    }
                case .failure(let e): self?.message = (e as? LaserdotError)?.message ?? "\(e)"
                }
                self?.largest = files
            }
        }
    }

    func up(to index: Int) {
        trail = index < 0 ? [] : Array(trail[...index])
        load()
    }
}

struct StorageView: View {
    @ObservedObject var model: StorageModel
    @State private var tab = 0

    private func bytes(_ n: UInt64) -> String { ByteCountFormatter.string(fromByteCount: Int64(n), countStyle: .file) }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Picker("", selection: $tab) {
                    Text(L("storage.folders")).tag(0)
                    Text(L("storage.largest")).tag(1)
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(width: 280)
                Spacer()
                if model.loading { ProgressView().controlSize(.small) }
                Button { model.load() } label: { Image(systemName: "arrow.clockwise") }
                    .help(L("content.refresh"))
            }
            .padding(10)
            Divider()
            if tab == 0 { folders } else { files }
        }
        .frame(minWidth: 560, minHeight: 420)
        .onAppear { model.load() }
    }

    private var folders: some View {
        VStack(alignment: .leading, spacing: 6) {
            // Breadcrumbs: Indexed folders › Documents › Projects
            HStack(spacing: 4) {
                Button(L("storage.all")) { model.up(to: -1) }.buttonStyle(.link)
                ForEach(Array(model.trail.enumerated()), id: \.offset) { i, p in
                    Image(systemName: "chevron.right").font(.caption2).foregroundStyle(.tertiary)
                    Button(URL(fileURLWithPath: p).lastPathComponent) { model.up(to: i) }.buttonStyle(.link)
                }
                Spacer()
                if let r = model.report {
                    Text(String(format: L("storage.total"), StorageRow.size(r.bytes, partial: r.partial, isDir: true),
                                StorageRow.counts(files: r.files, apps: r.apps, packages: r.packages)))
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            .padding(.horizontal, 12).padding(.top, 8)
            if let m = model.message {
                Text(m).foregroundStyle(.secondary).padding(12)
            }
            List(model.report?.items ?? [], id: \.path) { item in
                StorageRow(item: item, total: max(model.report?.bytes ?? 1, 1))
                    .contentShape(Rectangle())
                    .onTapGesture(count: 2) {
                        if item.isDir { model.load(item.path) } else { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: item.path)]) }
                    }
                    .contextMenu {
                        if item.isDir { Button(L("storage.open")) { model.load(item.path) } }
                        Button(L("action.reveal")) { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: item.path)]) }
                        Divider()
                        Button(L("storage.trash.menu")) {
                            model.moveToTrash(path: item.path, name: item.name, bytes: item.bytes)
                        }
                    }
            }
            Text(L("storage.help") + " " + L("storage.trash.hint")).font(.caption).foregroundStyle(.secondary).padding(.horizontal, 12).padding(.bottom, 8)
        }
    }

    private var files: some View {
        List(model.largest, id: \.path) { h in
            HStack(spacing: 10) {
                FileIcon(path: h.path, size: 24, thumbnail: h.kind == "image" || h.kind == "video")
                VStack(alignment: .leading, spacing: 2) {
                    Text(h.name).lineLimit(1)
                    Text(shortPath(h.path)).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer()
                Text(bytes(h.size)).font(.callout.monospacedDigit())
            }
            .contentShape(Rectangle())
            .onTapGesture(count: 2) { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: h.path)]) }
            .contextMenu {
                Button(L("action.reveal")) { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: h.path)]) }
                Button(L("action.quickLook")) { QuickLook.shared.show(URL(fileURLWithPath: h.path)) }
                Divider()
                Button(L("storage.trash.menu")) { model.moveToTrash(path: h.path, name: h.name, bytes: h.size) }
            }
        }
    }
}

/// A folder or file with its share of the whole as a bar.
struct StorageRow: View {
    let item: Wire.StorageItem
    let total: UInt64

    var body: some View {
        HStack(spacing: 10) {
            Image(nsImage: NSWorkspace.shared.icon(forFile: item.path)).resizable().frame(width: 22, height: 22)
            VStack(alignment: .leading, spacing: 3) {
                HStack {
                    Text(item.name).lineLimit(1)
                    Spacer()
                    Text(Self.size(item.bytes, partial: item.partial, isDir: item.isDir))
                        .font(.callout.monospacedDigit())
                }
                GeometryReader { g in
                    let share = CGFloat(Double(item.bytes) / Double(total))
                    ZStack(alignment: .leading) {
                        Capsule().fill(Color.secondary.opacity(0.15))
                        Capsule().fill(Color.accentColor).frame(width: max(2, g.size.width * share))
                    }
                }
                .frame(height: 5)
                if item.isDir {
                    Text(Self.counts(files: item.files, apps: item.apps, packages: item.packages))
                        .font(.caption2).foregroundStyle(.secondary)
                }
            }
        }
        .padding(.vertical, 3)
        .accessibilityElement(children: .combine)
    }

    /// "37.31 GB", "at least 2.1 GB" (packages still being measured), or
    /// "Empty" for a folder with nothing in it.
    static func size(_ n: UInt64, partial: Bool, isDir: Bool) -> String {
        if n == 0 && !partial && isDir { return L("storage.empty") }
        let s = ByteCountFormatter.string(fromByteCount: Int64(n), countStyle: .file)
        return partial ? String(format: L("storage.atLeast"), s) : s
    }

    /// "12 apps · 3 packages · 173,959 files" (numbers in the language's
    /// format; parts with nothing left out).
    static func counts(files: UInt64, apps: UInt64, packages: UInt64) -> String {
        let f = NumberFormatter()
        f.numberStyle = .decimal
        func n(_ v: UInt64) -> String { f.string(from: NSNumber(value: v)) ?? "\(v)" }
        var parts: [String] = []
        if apps > 0 { parts.append(String(format: L(apps == 1 ? "storage.app" : "storage.apps"), n(apps))) }
        if packages > 0 { parts.append(String(format: L(packages == 1 ? "storage.package" : "storage.packages"), n(packages))) }
        if files > 0 || parts.isEmpty { parts.append(String(format: L(files == 1 ? "storage.file" : "storage.files"), n(files))) }
        return parts.joined(separator: " · ")
    }
}
