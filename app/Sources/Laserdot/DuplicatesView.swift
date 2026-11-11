import AppKit
import LaserdotKit
import SwiftUI

/// Duplicate finder (spec §4): scan, review each group, then move the
/// chosen copies to the Trash or a folder after a dry-run preview (§4.4).
final class DuplicatesModel: ObservableObject {
    @Published var scope = ""
    @Published var exactOnly = false
    @Published private(set) var report: DuplicateReport?
    @Published private(set) var scanning = false
    @Published private(set) var working = false
    @Published var message: String?
    /// Paths marked for removal.
    @Published var remove = Set<String>()
    /// `nil`: the Trash.
    @Published var folder: URL?
    @Published var preview: CleanupPreview?

    private let engine: Engine
    private let queue = DispatchQueue(label: "laserdot.dupes", qos: .userInitiated)

    init(engine: Engine) {
        self.engine = engine
    }

    func scan() {
        guard !scanning else { return }
        scanning = true
        message = nil
        let (engine, scope, exact) = (self.engine, self.scope, self.exactOnly)
        queue.async { [weak self] in
            let outcome = Result { try engine.findDuplicates(scope: scope, exactOnly: exact) }
            DispatchQueue.main.async {
                guard let self else { return }
                self.scanning = false
                switch outcome {
                case .success(let r):
                    self.report = r
                    // The recommendation: everything but the best copy.
                    self.remove = Set(r.groups.flatMap { $0.files.filter { !$0.keep }.map(\.path) })
                    if r.videoDecoder == nil && !exact {
                        self.message = L("dupes.noVideoDecoder")
                    }
                case .failure(let e):
                    self.message = Self.describe(e)
                }
            }
        }
    }

    /// Mark or unmark a file; the last kept copy of a group cannot be marked.
    func toggle(_ file: DuplicateFile, in group: DuplicateGroup) {
        if remove.contains(file.path) {
            remove.remove(file.path)
        } else if group.files.filter({ !remove.contains($0.path) }).count > 1 {
            remove.insert(file.path)
        }
    }

    var selectedCount: Int { remove.count }

    var selectedBytes: UInt64 {
        report?.groups.flatMap(\.files).filter { remove.contains($0.path) }.reduce(0) { $0 + $1.size } ?? 0
    }

    private var sets: [CleanupSet] {
        (report?.groups ?? []).compactMap { g in
            let gone = g.files.filter { remove.contains($0.path) }
            let kept = g.files.filter { !remove.contains($0.path) }
            return gone.isEmpty ? nil : CleanupSet(keep: kept, remove: gone)
        }
    }

    /// The dry run; the view shows it and asks before moving anything.
    func review() {
        let (engine, sets, folder) = (self.engine, self.sets, self.folder?.path)
        working = true
        queue.async { [weak self] in
            let outcome = Result { try engine.cleanupPreview(sets: sets, folder: folder) }
            DispatchQueue.main.async {
                self?.working = false
                switch outcome {
                case .success(let p): self?.preview = p
                case .failure(let e): self?.message = Self.describe(e)
                }
            }
        }
    }

    func confirm() {
        let (engine, sets, folder) = (self.engine, self.sets, self.folder?.path)
        preview = nil
        working = true
        queue.async { [weak self] in
            let outcome = Result { try engine.cleanup(sets: sets, folder: folder) }
            DispatchQueue.main.async {
                guard let self else { return }
                self.working = false
                switch outcome {
                case .success(let o):
                    var text = String(
                        format: L("dupes.moved"), Int64(o.moved),
                        ByteCountFormatter.string(fromByteCount: Int64(o.bytes), countStyle: .file))
                    if !o.failed.isEmpty { text += "\n" + o.failed.joined(separator: "\n") }
                    self.message = text
                    self.scan()
                case .failure(let e):
                    self.message = Self.describe(e)
                }
            }
        }
    }

    func chooseFolder() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        if panel.runFront() == .OK { folder = panel.url }
    }

    private static func describe(_ e: Error) -> String {
        if e.isUnsupported { return L("feature.unsupported") }
        return (e as? LaserdotError)?.message ?? e.localizedDescription
    }
}

struct DuplicatesView: View {
    @ObservedObject var model: DuplicatesModel

    private func bytes(_ n: UInt64) -> String {
        ByteCountFormatter.string(fromByteCount: Int64(n), countStyle: .file)
    }

    private func kindTitle(_ kind: String) -> String {
        switch kind {
        case "exact": return L("dupes.kind.exact")
        case "reencoded_video": return L("dupes.kind.video")
        default: return L("dupes.kind.image")
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                TextField(L("dupes.scope"), text: $model.scope)
                    .textFieldStyle(.roundedBorder)
                    .onSubmit { model.scan() }
                Toggle(L("dupes.exactOnly"), isOn: $model.exactOnly)
                Button(L("dupes.scan")) { model.scan() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(model.scanning)
            }
            .padding(10)
            Divider()
            if model.scanning {
                ProgressView(L("dupes.scanning")).frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if let r = model.report {
                if r.groups.isEmpty {
                    Text(L("dupes.none")).foregroundStyle(.secondary).frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    // One card per group: the copies side by side, to compare
                    // at a glance; the recommended one is marked Keep.
                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 14) {
                            ForEach(Array(r.groups.enumerated()), id: \.offset) { _, g in
                                VStack(alignment: .leading, spacing: 8) {
                                    HStack(spacing: 6) {
                                        Text(kindTitle(g.kind)).font(.headline)
                                        Text(String(format: L("dupes.reclaim"), bytes(g.reclaimable))).foregroundStyle(.secondary)
                                        Spacer()
                                    }
                                    ScrollView(.horizontal, showsIndicators: false) {
                                        HStack(alignment: .top, spacing: 12) {
                                            ForEach(g.files, id: \.path) { f in tile(f, group: g) }
                                        }
                                        .padding(.bottom, 2)
                                    }
                                }
                                .padding(12)
                                .background(RoundedRectangle(cornerRadius: 10).fill(Color.secondary.opacity(0.08)))
                            }
                        }
                        .padding(12)
                    }
                }
            } else {
                Text(L("dupes.intro")).foregroundStyle(.secondary).multilineTextAlignment(.center)
                    .padding().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            if let m = model.message {
                Text(m).font(.caption).foregroundStyle(.secondary).padding(.horizontal, 10).padding(.top, 6)
            }
            Divider()
            HStack {
                if let r = model.report {
                    Text(String(format: L("dupes.summary"), Int64(r.groups.count), bytes(r.reclaimable)))
                        .font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                Picker(L("dupes.moveTo"), selection: Binding(
                    get: { model.folder == nil ? 0 : 1 },
                    set: { if $0 == 0 { model.folder = nil } else { model.chooseFolder() } }
                )) {
                    Text(L("dupes.trash")).tag(0)
                    Text(model.folder?.lastPathComponent ?? L("dupes.folder")).tag(1)
                }
                .frame(width: 220)
                Button(String(format: L("dupes.review"), Int64(model.selectedCount), bytes(model.selectedBytes))) {
                    model.review()
                }
                .disabled(model.selectedCount == 0 || model.working || model.scanning)
            }
            .padding(10)
        }
        .frame(minWidth: 720, minHeight: 460)
        .alert(
            L("dupes.confirm.title"),
            isPresented: Binding(get: { model.preview != nil }, set: { if !$0 { model.preview = nil } }),
            presenting: model.preview
        ) { _ in
            Button(L("dupes.confirm.move"), role: .destructive) { model.confirm() }
            Button(L("drive.notNow"), role: .cancel) { model.preview = nil }
        } message: { p in
            Text(confirmText(p))
        }
    }

    /// The dry run, in words (spec §4.4).
    private func confirmText(_ p: CleanupPreview) -> String {
        let place = model.folder?.path ?? L("dupes.trash")
        var text = String(format: L("dupes.confirm.text"), Int64(p.files), bytes(p.bytes), place)
        if !p.skipped.isEmpty { text += "\n\n" + p.skipped.joined(separator: "\n") }
        return text
    }

    /// A copy: its thumbnail, name, details, and whether it will be moved.
    private func tile(_ f: DuplicateFile, group: DuplicateGroup) -> some View {
        let moving = model.remove.contains(f.path)
        return VStack(alignment: .leading, spacing: 4) {
            ZStack(alignment: .topLeading) {
                FileIcon(path: f.path, size: 150, thumbnail: true, large: true)
                    .frame(width: 170, height: 150)
                    .opacity(moving ? 0.55 : 1)
                if f.keep {
                    Text(L("dupes.recommended"))
                        .font(.caption2.weight(.semibold))
                        .padding(.horizontal, 6).padding(.vertical, 2)
                        .background(Capsule().fill(Color.accentColor))
                        .foregroundStyle(.white)
                        .padding(4)
                }
            }
            Text(URL(fileURLWithPath: f.path).lastPathComponent)
                .font(.callout.weight(f.keep ? .semibold : .regular)).lineLimit(2)
            Text(f.summary).font(.caption.monospacedDigit()).foregroundStyle(.secondary).lineLimit(1)
            Text(bytes(f.size) + " · " + friendlyDate(f.modified)).font(.caption).foregroundStyle(.secondary)
            Text(shortPath(f.path)).font(.caption2).foregroundStyle(.tertiary).lineLimit(1).truncationMode(.middle)
                .help(f.path)
            if let v = f.volume {
                Label(v, systemImage: "externaldrive").font(.caption2).foregroundStyle(.secondary)
            }
            Toggle(L("dupes.markRemove"), isOn: Binding(
                get: { model.remove.contains(f.path) },
                set: { _ in model.toggle(f, in: group) }
            ))
            .toggleStyle(.checkbox)
            .font(.caption)
        }
        .frame(width: 170, alignment: .leading)
        .padding(8)
        .background(RoundedRectangle(cornerRadius: 8).strokeBorder(moving ? Color.orange.opacity(0.7) : (f.keep ? Color.accentColor : Color.clear), lineWidth: 2))
        .contentShape(Rectangle())
        .onTapGesture(count: 2) { DiskAccessPrompt.shared.open(URL(fileURLWithPath: f.path)) }
        .contextMenu {
            Button(L("action.open")) { DiskAccessPrompt.shared.open(URL(fileURLWithPath: f.path)) }
            Button(L("action.quickLook")) { QuickLook.shared.show(group.files.map { URL(fileURLWithPath: $0.path) }, at: group.files.firstIndex { $0.path == f.path } ?? 0) }
            Button(L("action.reveal")) { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: f.path)]) }
        }
        .accessibilityElement(children: .combine)
    }

    private func row(_ f: DuplicateFile, group: DuplicateGroup) -> some View {
        HStack(spacing: 10) {
            Toggle("", isOn: Binding(
                get: { model.remove.contains(f.path) },
                set: { _ in model.toggle(f, in: group) }
            ))
            .toggleStyle(.checkbox)
            .labelsHidden()
            .help(L("dupes.markRemove"))
            Image(nsImage: NSWorkspace.shared.icon(forFile: f.path)).resizable().frame(width: 24, height: 24)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(URL(fileURLWithPath: f.path).lastPathComponent).fontWeight(f.keep ? .semibold : .regular)
                    if f.keep {
                        Text(L("dupes.recommended")).font(.caption2).padding(.horizontal, 5)
                            .background(Capsule().fill(Color.accentColor.opacity(0.2)))
                    }
                    if let v = f.volume {
                        Label(v, systemImage: "externaldrive").font(.caption2).foregroundStyle(.secondary)
                    }
                }
                Text(f.path).font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
            }
            Spacer()
            Text(f.summary).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
            Text(bytes(f.size)).font(.caption.monospacedDigit()).frame(width: 70, alignment: .trailing)
        }
        .contextMenu {
            Button(L("action.reveal")) {
                NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: f.path)])
            }
            Button(L("action.open")) { DiskAccessPrompt.shared.open(URL(fileURLWithPath: f.path)) }
        }
    }
}
