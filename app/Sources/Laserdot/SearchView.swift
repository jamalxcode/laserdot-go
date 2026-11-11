import AVKit
import AppKit
import LaserdotKit
import PDFKit
import SwiftUI

/// The search UI, in the ⌥Space overlay and in the main window: the query
/// field, a hint only when it helps, suggestion chips, the results in
/// sections (or the empty box's recent files, or `>` commands), a preview
/// pane (or the ⌘K actions), and a footer with the keys to press.
struct SearchView: View {
    @ObservedObject var model: SearchModel
    var close: () -> Void
    /// The hot-key overlay has a fixed size; the main window resizes.
    var fixedSize = true
    @Environment(\.controlActiveState) private var activeState
    /// The selected row uses the accent colour while this window has the
    /// keyboard (the overlay always does), and grey otherwise — as lists do.
    private var emphasized: Bool { fixedSize || activeState == .key }

    /// The overlay's slim bar: just the field.
    static let compactHeight: CGFloat = 58
    static let fullHeight: CGFloat = 520

    var body: some View {
        VStack(spacing: 0) {
            header
            if !(fixedSize && model.compact) {
                hintLine
                chips
                Divider()
                Group {
                    if model.showCheatSheet {
                        CheatSheetView(model: model)
                    } else {
                        HStack(spacing: 0) {
                            mainList
                            if model.showActions {
                                Divider()
                                ActionsPane(model: model).frame(width: fixedSize ? 280 : 300)
                            } else if let hit = model.selected, !model.results.isEmpty, !model.isCommandMode {
                                Divider()
                                PreviewPane(hit: hit, model: model).frame(width: fixedSize ? 280 : 300)
                            }
                        }
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                // The main window shows status and keys in its own status bar.
                if fixedSize {
                    Divider()
                    footer
                }
            }
        }
        .frame(width: fixedSize ? 920 : nil, height: fixedSize ? (model.compact ? Self.compactHeight : Self.fullHeight) : nil)
        .background {
            // The floating card is the overlay's look; the main window keeps
            // the standard window background.
            if fixedSize {
                RoundedRectangle(cornerRadius: 14, style: .continuous).fill(.regularMaterial)
            }
        }
        .overlay(hiddenShortcuts)
    }

    // MARK: header

    private var header: some View {
        HStack(spacing: 10) {
            Image(systemName: model.isCommandMode ? "chevron.right.2" : "magnifyingglass")
                .font(.title2).foregroundStyle(.secondary)
            SearchField(
                text: $model.query,
                placeholder: L("search.placeholder"),
                focusToken: model.focusRequest,
                onMove: { d in model.showCheatSheet ? model.moveCheat(d) : model.move(d) },
                onExtend: { d in model.extend(d) },
                onTab: { model.showCheatSheet ? model.nextCheatSection() : model.acceptCompletion() },
                onSubmit: { model.showCheatSheet ? model.insertSelectedCheat() : model.submit() },
                onEscape: { model.escape(close: close) }
            )
            .frame(height: 30)
            if !model.query.isEmpty {
                Button { model.query = ""; model.focusSearch() } label: {
                    Image(systemName: "xmark.circle.fill").foregroundStyle(.tertiary)
                }
                .buttonStyle(.borderless)
                .help(L("search.clear"))
                .accessibilityLabel(L("search.clear"))
            }
            // Pin the query (spec §8.6).
            Button { model.togglePin() } label: {
                Image(systemName: model.isPinned ? "pin.fill" : "pin").font(.title3)
            }
            .buttonStyle(.borderless)
            .keyboardShortcut("d", modifiers: .command)
            .help(L("pin.help"))
            .accessibilityLabel(L(model.isPinned ? "pin.remove" : "pin.add"))
            .disabled(model.isHome || model.isCommandMode)
            // Cheat sheet button (spec §8.9); typing `?` stays a wildcard.
            Button { model.showCheatSheet.toggle() } label: {
                Image(systemName: model.showCheatSheet ? "questionmark.circle.fill" : "questionmark.circle")
                    .font(.title2)
            }
            .buttonStyle(.borderless)
            .keyboardShortcut("/", modifiers: .command)
            .help(L("cheatsheet.help"))
            .accessibilityLabel(L("cheatsheet.help"))
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
    }

    @ViewBuilder private var hintLine: some View {
        if let p = model.problem {
            HStack {
                Image(systemName: "exclamationmark.triangle").foregroundStyle(.orange)
                Text(p.message).lineLimit(2)
                Spacer()
                if let fix = p.suggestion {
                    Button(String(format: L("action.useSuggestion"), fix)) { model.applySuggestion() }
                        .buttonStyle(.link)
                }
            }
            .font(.callout)
            .padding(.horizontal, 16)
            .padding(.bottom, 8)
        } else if let h = model.visibleHint {
            Text(h)
                .font(.callout)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 16)
                .padding(.bottom, 8)
        }
    }

    /// Suggestions under the hint: completions for the word being typed, the
    /// kinds a mixed result list contains, or the pinned searches.
    @ViewBuilder private var chips: some View {
        let completions = model.completionChips
        let kinds = model.kindChips
        let pins = model.isHome ? model.pinned : []
        if !completions.isEmpty || !kinds.isEmpty || !pins.isEmpty {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 6) {
                    if !completions.isEmpty {
                        ForEach(completions, id: \.self) { c in
                            Button(c.label) { model.apply(c) }.help(c.detail)
                        }
                    } else if !kinds.isEmpty {
                        ForEach(kinds) { k in
                            Button("\(k.title) (\(k.total))") { model.narrow(to: k) }.help(k.filter ?? "")
                        }
                    } else {
                        Image(systemName: "pin").foregroundStyle(.secondary).accessibilityHidden(true)
                        ForEach(pins, id: \.self) { q in
                            Button(q) { model.query = q }
                                .contextMenu { Button(String(format: L("pin.unpin"), q)) { model.togglePin(q) } }
                        }
                    }
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .padding(.horizontal, 16)
            }
            .padding(.bottom, 8)
        }
    }

    // MARK: the list

    @ViewBuilder private var mainList: some View {
        if let message = model.unavailable {
            placeholder(message)
        } else if model.isCommandMode {
            commandList
        } else if model.results.isEmpty {
            if model.isHome {
                VStack(spacing: 8) {
                    Text(L("home.empty")).foregroundStyle(.secondary)
                    Text(L("home.tips")).font(.caption).foregroundStyle(.tertiary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if !model.searching {
                VStack(spacing: 8) {
                    if model.problem == nil {
                        Text(L("results.none")).foregroundStyle(.secondary)
                    }
                    ForEach(model.notes, id: \.self) { Text($0).font(.caption).foregroundStyle(.secondary) }
                    coachingList
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                Color.clear.frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        } else {
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 2) {
                        ForEach(model.sections) { section in
                            if model.sections.count > 1 || section.id != "top" {
                                Text(section.title)
                                    .font(.caption.weight(.semibold))
                                    .foregroundStyle(.secondary)
                                    .padding(.horizontal, 10)
                                    .padding(.top, section.range.lowerBound == 0 ? 2 : 10)
                                    .accessibilityAddTraits(.isHeader)
                            }
                            ForEach(section.range.map { IndexedHit(index: $0, hit: model.results[$0]) }) { item in
                                row(item.index, item.hit)
                            }
                            if section.hidden > 0 {
                                Button(String(format: L("results.showAll"), Int64(section.total))) { model.expand(section.id) }
                                    .buttonStyle(.link)
                                    .font(.caption)
                                    .padding(.leading, 48)
                                    .padding(.vertical, 2)
                            }
                        }
                    }
                    .padding(6)
                }
                // One identity per row (its path); rebuilt on every answer.
                .id(model.resultsID)
                .onChange(of: model.selection) { _, i in
                    if model.results.indices.contains(i) { proxy.scrollTo(model.results[i].path) }
                }
            }
            // Results of an earlier keystroke fade while the current query
            // is answered (only if that takes a moment, so typing never flickers).
            .opacity(model.searching ? 0.35 : 1)
            .animation(model.searching ? .easeIn(duration: 0.15).delay(0.15) : .none, value: model.searching)
        }
    }

    private func row(_ i: Int, _ hit: Hit) -> some View {
        ResultRow(
            hit: hit, selected: model.isSelected(i), emphasized: emphasized,
            number: i < 9 ? i + 1 : nil, highlight: model.highlightWords)
            .contentShape(Rectangle())
            .onTapGesture(count: 2) { model.open(hit) }
            .onTapGesture { model.click(i) }
            .contextMenu {
                Button(L("action.open")) { model.open(hit) }
                Button(L("action.quickLook")) { model.quickLook(hit) }
                Button(L("action.reveal")) { model.reveal(hit) }
                Button(L("action.copy")) { model.copyPath(hit) }
                Divider()
                Button(L("action.more")) {
                    if !model.isSelected(i) { model.click(i) }
                    model.toggleActions()
                }
            }
            .onDrag { NSItemProvider(object: URL(fileURLWithPath: hit.path) as NSURL) }
    }

    private var commandList: some View {
        let list = model.commandEntries
        return ScrollView {
            LazyVStack(alignment: .leading, spacing: 2) {
                Text(L("commands.title"))
                    .font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    .padding(.horizontal, 10).padding(.top, 2)
                ForEach(Array(list.enumerated()), id: \.element.id) { i, c in
                    ActionRow(item: c, selected: i == model.commandIndex, emphasized: emphasized)
                        .contentShape(Rectangle())
                        .onTapGesture { model.runCommand(i) }
                }
                if list.isEmpty {
                    Text(L("results.none")).foregroundStyle(.secondary).padding(10)
                }
            }
            .padding(6)
        }
    }

    /// Zero-results coaching (spec §8.3): each suggestion is one click.
    @ViewBuilder private var coachingList: some View {
        if !model.coaching.isEmpty {
            VStack(alignment: .leading, spacing: 6) {
                Text(L("coach.title")).font(.caption).foregroundStyle(.secondary)
                ForEach(Array(model.coaching.enumerated()), id: \.offset) { _, c in
                    Button(coachingText(c)) { model.follow(c) }
                        .buttonStyle(.link)
                        .accessibilityLabel(coachingText(c))
                }
            }
            .padding(.top, 4)
        }
    }

    private func coachingText(_ c: CoachingInfo) -> String {
        switch c.kind {
        case "index_folder":
            return String(format: L("coach.index"), URL(fileURLWithPath: c.path ?? "").lastPathComponent)
        case "fix_value":
            return String(format: L("coach.fix"), c.to ?? "", c.from ?? "", Int64(c.count))
        case "relax":
            return String(format: L("coach.relax"), c.from ?? "", Int64(c.count))
        default:
            return L("coach.syntax")
        }
    }

    private func placeholder(_ text: String) -> some View {
        Text(text).foregroundStyle(.secondary).frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    // MARK: footer and keys

    private var footer: some View {
        HStack(spacing: 12) {
            StatusSummary(model: model)
            Spacer()
            ShortcutBar(model: model)
            Spacer()
            NetworkLock(blocked: model.networkBlocked)
        }
        .font(.caption)
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
    }

    /// Keys without a visible button: ⌘↩ reveal, ⌘⇧C copy path, ⌘Y Quick
    /// Look, ⌘K actions, ⌘1–⌘9 open (spec §5.1).
    private var hiddenShortcuts: some View {
        ZStack {
            Button("") { model.reveal() }.keyboardShortcut(.return, modifiers: .command)
            Button("") { model.copyPath() }.keyboardShortcut("c", modifiers: [.command, .shift])
            Button("") { model.quickLook() }.keyboardShortcut("y", modifiers: .command)
            Button("") { model.toggleActions() }.keyboardShortcut("k", modifiers: .command)
            ForEach(1...9, id: \.self) { n in
                Button("") { model.openNumbered(n) }.keyboardShortcut(KeyEquivalent(Character("\(n)")), modifiers: .command)
            }
        }
        .opacity(0)
        .accessibilityHidden(true) // the context menu and ⌘K list offer these actions
        .allowsHitTesting(false)
    }
}

/// Indexing progress if a folder is being indexed, else "28 results · 0.3
/// ms" or "Searching…".
struct StatusSummary: View {
    @ObservedObject var model: SearchModel

    var body: some View {
        if let p = model.progressText {
            HStack(spacing: 6) {
                ProgressView().controlSize(.mini)
                Text(p)
            }
        } else {
            ResultsSummary(model: model)
        }
    }
}

/// "28 results · 0.3 ms", or "Searching…" while the query is answered.
struct ResultsSummary: View {
    @ObservedObject var model: SearchModel

    var body: some View {
        if model.searching {
            Text(L("results.searching"))
        } else if model.total > 0 && !model.isHome {
            Text(String(format: L("results.count"), Int64(model.total), model.elapsedMs))
        }
    }
}

/// The keys that work right now, so people learn them without the cheat sheet.
struct ShortcutBar: View {
    @ObservedObject var model: SearchModel

    var body: some View {
        HStack(spacing: 10) {
            ForEach(items) { item in
                HStack(spacing: 4) {
                    Text(item.key)
                        .font(.caption2.monospaced())
                        .padding(.horizontal, 4)
                        .padding(.vertical, 1)
                        .background(RoundedRectangle(cornerRadius: 3).fill(.quaternary))
                    Text(item.label)
                }
            }
        }
        .foregroundStyle(.secondary)
        .accessibilityElement(children: .combine)
    }

    struct KeyHint: Identifiable {
        let key: String
        let label: String
        var id: String { key }
    }

    private var items: [KeyHint] {
        func k(_ key: String, _ label: String) -> KeyHint { KeyHint(key: key, label: label) }
        if model.showActions {
            return [k("↑↓", L("keys.choose")), k("↩", L("keys.run")), k("esc", L("keys.back"))]
        }
        if model.isCommandMode {
            return [k("↑↓", L("keys.choose")), k("↩", L("keys.run")), k("esc", L("keys.cancel"))]
        }
        if model.results.isEmpty {
            return [k(">", L("keys.commands")), k("⌘/", L("keys.syntax"))]
        }
        return [k("↩", L("action.open")), k("⌘↩", L("action.reveal")), k("⌘Y", L("action.quickLook")), k("⌘K", L("keys.actions"))]
    }
}

/// A lock: the search engine cannot reach the network (spec §7.1). The
/// words are in the tooltip and in Settings.
struct NetworkLock: View {
    let blocked: Bool

    var body: some View {
        Image(systemName: blocked ? "lock.shield" : "exclamationmark.shield")
            .foregroundStyle(blocked ? Color.secondary : Color.orange)
            .help(L(blocked ? "network.blocked" : "network.open"))
            .accessibilityLabel(L(blocked ? "network.blocked" : "network.open"))
    }
}

// MARK: rows

struct ResultRow: View {
    let hit: Hit
    let selected: Bool
    /// Accent highlight with white text (the window has the keyboard).
    var emphasized = true
    /// ⌘1–⌘9 opens this row.
    var number: Int?
    /// Query words, shown bold where the name contains them.
    var highlight: [String] = []

    private var light: Bool { selected && emphasized }
    private var mainColor: Color { light ? Color.white : Color.primary }
    private var subColor: Color { light ? Color.white.opacity(0.85) : Color.secondary }
    private var background: Color {
        selected ? (emphasized ? Color.accentColor : Color.secondary.opacity(0.2)) : Color.clear
    }
    private var subtitle: String { shortPath(hit.path) + " · " + friendlyDate(hit.modified) }
    private var spoken: String {
        let size = ByteCountFormatter.string(fromByteCount: Int64(hit.size), countStyle: .file)
        let parts: [String] = [hit.name, hit.kind, size] + hit.badges + (hit.content ? [L("results.inContents")] : []) + [hit.path]
        return parts.joined(separator: ", ")
    }

    var body: some View {
        HStack(spacing: 10) {
            FileIcon(path: hit.path, size: 28, thumbnail: hit.kind == "image" || hit.kind == "video")
            names
            Spacer(minLength: 8)
            trailing
        }
        .padding(.horizontal, 10)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(spoken)
        .accessibilityAddTraits(selected ? .isSelected : [])
        .padding(.vertical, 6)
        .background(RoundedRectangle(cornerRadius: 8).fill(background))
    }

    private var names: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(highlighted(hit.name, highlight))
                .lineLimit(1)
                .foregroundStyle(mainColor)
            Text(subtitle)
                .font(.caption)
                .lineLimit(1)
                .truncationMode(.middle)
                .foregroundStyle(subColor)
                .help(hit.path)
        }
    }

    @ViewBuilder private var trailing: some View {
        if hit.content {
            Label(L("results.inContents"), systemImage: "doc.text.magnifyingglass")
                .font(.caption2)
                .foregroundStyle(subColor)
        }
        if let volume = hit.volume {
            Label(volume, systemImage: "externaldrive")
                .font(.caption2)
                .foregroundStyle(subColor)
        }
        ForEach(Array(hit.badges.prefix(2)), id: \.self) { b in
            Badge(text: b, light: light)
        }
        if let number {
            Text("⌘\(number)")
                .font(.caption2.monospacedDigit())
                .foregroundStyle(subColor)
                .accessibilityHidden(true)
        }
    }
}

/// A media badge on a row ("3840×2160", "hevc hdr10").
struct Badge: View {
    let text: String
    let light: Bool

    var body: some View {
        Text(text)
            .font(.caption2.monospacedDigit())
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .foregroundStyle(light ? Color.white : Color.primary)
            .background(Capsule().fill(light ? AnyShapeStyle(Color.white.opacity(0.25)) : AnyShapeStyle(.quaternary)))
    }
}

/// An action or command row: symbol, title, its key.
struct ActionRow: View {
    let item: ActionItem
    let selected: Bool
    var emphasized = true

    private var light: Bool { selected && emphasized }

    var body: some View {
        HStack(spacing: 10) {
            if let image = item.image {
                Image(nsImage: image).resizable().frame(width: 18, height: 18).frame(width: 20)
            } else {
                Image(systemName: item.symbol).frame(width: 20)
            }
            Text(item.title).lineLimit(1)
            Spacer(minLength: 8)
            if let k = item.shortcut {
                Text(k).font(.caption.monospaced()).opacity(0.8)
            }
        }
        .foregroundStyle(light ? Color.white : Color.primary)
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
        .background(RoundedRectangle(cornerRadius: 8).fill(selected ? (emphasized ? Color.accentColor : Color.secondary.opacity(0.2)) : .clear))
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(selected ? [.isSelected, .isButton] : .isButton)
    }
}

/// ⌘K: what can be done with the selection, with ↑/↓ and Return.
struct ActionsPane: View {
    @ObservedObject var model: SearchModel

    var body: some View {
        let hits = model.selectedHits
        let list = model.actions
        ScrollView {
            VStack(alignment: .leading, spacing: 2) {
                Text(hits.count > 1 ? String(format: L("actions.many"), Int64(hits.count)) : (hits.first?.name ?? ""))
                    .font(.headline).lineLimit(2)
                    .padding(.horizontal, 10).padding(.vertical, 6)
                ForEach(Array(list.enumerated()), id: \.element.id) { i, a in
                    ActionRow(item: a, selected: i == model.actionIndex)
                        .contentShape(Rectangle())
                        .onTapGesture {
                            model.actionIndex = i
                            model.submit()
                        }
                }
            }
            .padding(6)
        }
    }
}

// MARK: preview

/// The selected result, larger: a thumbnail, a playable video or song, or
/// the PDF's pages; then name, kind, size, dates, media details, actions.
struct PreviewPane: View {
    let hit: Hit
    @ObservedObject var model: SearchModel

    private var ext: String { (hit.name as NSString).pathExtension.lowercased() }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                visual
                Text(highlighted(hit.name, model.highlightWords))
                    .font(.headline)
                    .lineLimit(3)
                    .textSelection(.enabled)
                Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 8, verticalSpacing: 4) {
                    detail(L("preview.kind"), Self.kindText(hit))
                    if !hit.isDir {
                        detail(L("preview.size"), ByteCountFormatter.string(fromByteCount: Int64(hit.size), countStyle: .file))
                    }
                    detail(L("preview.modified"), friendlyDate(hit.modified, long: true))
                    if !hit.badges.isEmpty {
                        detail(L("preview.media"), hit.badges.joined(separator: " · "))
                    }
                    if let v = hit.volume {
                        detail(L("preview.drive"), v)
                    }
                    detail(L("preview.where"), shortPath(hit.path))
                }
                .font(.callout)
                HStack {
                    Button(L("action.open")) { model.open(hit) }
                    Button(L("action.reveal")) { model.reveal(hit) }
                    Button { model.toggleActions() } label: { Image(systemName: "ellipsis.circle") }
                        .help(L("keys.actions") + " (⌘K)")
                        .accessibilityLabel(L("keys.actions"))
                }
                .controlSize(.small)
            }
            .padding(14)
        }
    }

    @ViewBuilder private var visual: some View {
        if hit.volume == nil && (hit.kind == "video" || hit.kind == "audio") {
            MediaPreview(path: hit.path, video: hit.kind == "video")
                .clipShape(RoundedRectangle(cornerRadius: 6))
        } else if hit.volume == nil && ext == "pdf" {
            PDFOrIcon(path: hit.path)
        } else {
            FileIcon(path: hit.path, size: 220, thumbnail: true, large: true)
                .frame(maxWidth: .infinity)
                .shadow(color: .black.opacity(0.15), radius: 4, y: 2)
                .onTapGesture(count: 2) { model.open(hit) }
                .accessibilityHidden(true)
        }
    }

    /// What Finder calls it ("JPEG image", "Folder"), else the section name.
    private static func kindText(_ hit: Hit) -> String {
        let url = URL(fileURLWithPath: hit.path)
        if let d = try? url.resourceValues(forKeys: [.localizedTypeDescriptionKey]).localizedTypeDescription, !d.isEmpty {
            return d
        }
        return L("section.\(SearchModel.category(hit))")
    }

    private func detail(_ label: String, _ value: String) -> some View {
        GridRow {
            Text(label).foregroundStyle(.secondary).gridColumnAlignment(.trailing)
            Text(value).lineLimit(2)
        }
    }
}

/// A video or song, playable in the pane (local file, AVKit). It never
/// starts by itself and stops when another result is selected.
struct MediaPreview: View {
    let path: String
    let video: Bool
    @State private var player: AVPlayer?

    var body: some View {
        VStack {
            if let player {
                VideoPlayer(player: player)
            } else {
                Color.black.opacity(0.05)
            }
        }
        .frame(height: video ? 170 : 64)
        .task(id: path) {
            player?.pause()
            player = AVPlayer(url: URL(fileURLWithPath: path))
        }
        .onDisappear { player?.pause() }
    }
}

/// The PDF's pages, or its icon when the file cannot be read as a PDF.
struct PDFOrIcon: View {
    let path: String
    @State private var document: PDFDocument?
    @State private var loadedFor: String?

    var body: some View {
        Group {
            if loadedFor == path, let document {
                PDFPreview(document: document)
                    .frame(height: 280)
                    .clipShape(RoundedRectangle(cornerRadius: 6))
            } else {
                FileIcon(path: path, size: 220, thumbnail: true, large: true).frame(maxWidth: .infinity)
            }
        }
        .task(id: path) {
            let p = path
            let doc = PDFDocument(url: URL(fileURLWithPath: p))
            document = (doc?.pageCount ?? 0) > 0 ? doc : nil
            loadedFor = p
        }
    }
}

/// A PDF's pages, scrollable in the pane (PDFKit).
struct PDFPreview: NSViewRepresentable {
    let document: PDFDocument

    func makeNSView(context: Context) -> PDFView {
        let v = PDFView()
        v.autoScales = true
        v.displayMode = .singlePageContinuous
        v.backgroundColor = .clear
        return v
    }

    func updateNSView(_ v: PDFView, context: Context) {
        if v.document !== document { v.document = document }
    }
}

// MARK: text helpers

/// "~ › Documents › AI Code": the folder a result is in, short. Long paths
/// keep their last three folders; drives start with the drive's name.
func shortPath(_ path: String) -> String {
    var dir = (path as NSString).deletingLastPathComponent
    let home = FileManager.default.homeDirectoryForCurrentUser.path
    var parts: [String] = []
    if dir == home || dir.hasPrefix(home + "/") {
        parts = ["~"]
        dir = String(dir.dropFirst(home.count))
    } else if dir.hasPrefix("/Volumes/") {
        dir = String(dir.dropFirst("/Volumes/".count))
    }
    parts += dir.split(separator: "/").map(String.init)
    if parts.count > 4 { parts = [parts[0], "…"] + parts.suffix(3) }
    // › is a mirrored character: right to left it turns into ‹, so Arabic
    // uses a plain slash.
    let separator = appLayoutDirection == .rightToLeft ? " / " : " › "
    let text = parts.isEmpty ? "/" : parts.joined(separator: separator)
    // Paths read left to right in every language: isolate them, or the
    // Arabic layout mirrors › into ‹.
    return "\u{2066}" + text + "\u{2069}"
}

private let relativeFormatter: RelativeDateTimeFormatter = {
    let f = RelativeDateTimeFormatter()
    f.locale = Locale(identifier: appLang)
    f.unitsStyle = .full
    return f
}()

private let dayFormatter: DateFormatter = {
    let f = DateFormatter()
    f.locale = Locale(identifier: appLang)
    f.doesRelativeDateFormatting = true // "Today", "Yesterday"
    f.dateStyle = .medium
    f.timeStyle = .short
    return f
}()

private let dateOnlyFormatter: DateFormatter = {
    let f = DateFormatter()
    f.locale = Locale(identifier: appLang)
    f.dateStyle = .medium
    f.timeStyle = .none
    return f
}()

/// "5 minutes ago", "Today at 9:04", "Yesterday at 18:20", "3 days ago",
/// then the date.
func friendlyDate(_ unix: Int64, long: Bool = false) -> String {
    let d = Date(timeIntervalSince1970: TimeInterval(unix))
    let age = Date().timeIntervalSince(d)
    if age >= 0 && age < 3600 { return relativeFormatter.localizedString(for: d, relativeTo: Date()) }
    if Calendar.current.isDateInToday(d) || Calendar.current.isDateInYesterday(d) { return dayFormatter.string(from: d) }
    if age >= 0 && age < 7 * 86400 { return relativeFormatter.localizedString(for: d, relativeTo: Date()) }
    return long ? dayFormatter.string(from: d) : dateOnlyFormatter.string(from: d)
}

/// The name with each query word in bold where it occurs (case and accents
/// ignored), so it is clear why a result matched.
func highlighted(_ name: String, _ words: [String]) -> AttributedString {
    var attr = AttributedString(name)
    for w in words where !w.isEmpty {
        var from = name.startIndex
        while from < name.endIndex,
              let r = name.range(of: w, options: [.caseInsensitive, .diacriticInsensitive], range: from..<name.endIndex) {
            if let ar = Range(r, in: attr) {
                attr[ar].inlinePresentationIntent = .stronglyEmphasized
            }
            from = r.upperBound
        }
    }
    return attr
}

/// A result and its place in `SearchModel.results`; identified by its path,
/// one identity per row.
struct IndexedHit: Identifiable {
    let index: Int
    let hit: Hit
    var id: String { hit.path }
}
