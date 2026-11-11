import AppKit
import LaserdotKit
import SwiftUI

/// The overlay: query field, hint line, results (or the cheat sheet), and a
/// footer that says whether the network is blocked.
struct SearchView: View {
    @ObservedObject var model: SearchModel
    var close: () -> Void
    /// The hot-key overlay has a fixed size; the main window resizes.
    var fixedSize = true
    @Environment(\.controlActiveState) private var activeState
    /// The selected row uses the accent colour while this window has the
    /// keyboard (the overlay always does), and grey otherwise — as lists do.
    private var emphasized: Bool { fixedSize || activeState == .key }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                Image(systemName: "magnifyingglass").font(.title2).foregroundStyle(.secondary)
                SearchField(
                    text: $model.query,
                    placeholder: L("search.placeholder"),
                    focusToken: model.focusRequest,
                    onMove: { d in model.showCheatSheet ? model.moveCheat(d) : model.move(d) },
                    onTab: { model.showCheatSheet ? model.nextCheatSection() : model.acceptCompletion() },
                    onSubmit: { model.showCheatSheet ? model.insertSelectedCheat() : model.open() },
                    onEscape: {
                        // Esc closes the cheat sheet first, then the overlay (§8.9.2).
                        if model.showCheatSheet { model.showCheatSheet = false } else { close() }
                    }
                )
                .frame(height: 30)
                // Pin the query (spec §8.6).
                Button { model.togglePin() } label: {
                    Image(systemName: model.isPinned ? "pin.fill" : "pin").font(.title3)
                }
                .buttonStyle(.borderless)
                .keyboardShortcut("d", modifiers: .command)
                .help(L("pin.help"))
                .accessibilityLabel(L(model.isPinned ? "pin.remove" : "pin.add"))
                .disabled(model.query.trimmingCharacters(in: .whitespaces).isEmpty)
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

            hintLine
            Divider()
            Group {
                if model.showCheatSheet {
                    CheatSheetView(model: model)
                } else {
                    results
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            // The main window shows results and status in its own status bar.
            if fixedSize {
                Divider()
                footer
            }
        }
        .frame(width: fixedSize ? 720 : nil, height: fixedSize ? 480 : nil)
        .background {
            // The floating card is the overlay's look; the main window keeps
            // the standard window background.
            if fixedSize {
                RoundedRectangle(cornerRadius: 14, style: .continuous).fill(.regularMaterial)
            }
        }
        .overlay(hiddenShortcuts)
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
        } else if let h = model.hint {
            Text(h)
                .font(.callout)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 16)
                .padding(.bottom, 8)
        }
    }

    @ViewBuilder private var results: some View {
        if let message = model.unavailable {
            placeholder(message)
        } else if model.query.trimmingCharacters(in: .whitespaces).isEmpty {
            VStack(alignment: .leading, spacing: 10) {
                if !model.pinned.isEmpty {
                    Text(L("results.pinned")).foregroundStyle(.secondary)
                    FlowChips(items: model.pinned) { model.query = $0 }
                        .contextMenu {
                            ForEach(model.pinned, id: \.self) { q in
                                Button(String(format: L("pin.unpin"), q)) { model.togglePin(q) }
                            }
                        }
                }
                Text(L("results.examples")).foregroundStyle(.secondary)
                FlowChips(items: model.examples) { model.query = $0 }
                Text(L("results.historyHint")).font(.caption).foregroundStyle(.tertiary)
            }
            .padding(16)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        } else if model.results.isEmpty {
            VStack(spacing: 8) {
                if model.problem == nil {
                    Text(L("results.none")).foregroundStyle(.secondary)
                }
                ForEach(model.notes, id: \.self) { Text($0).font(.caption).foregroundStyle(.secondary) }
                coachingList
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(spacing: 2) {
                        ForEach(Array(model.results.enumerated()), id: \.element.path) { i, hit in
                            ResultRow(hit: hit, selected: i == model.selection, emphasized: emphasized)
                                .contentShape(Rectangle())
                                .onTapGesture(count: 2) { model.open(hit) }
                                .onTapGesture { model.selection = i }
                                .contextMenu {
                                    Button(L("action.open")) { model.open(hit) }
                                    Button(L("action.reveal")) { model.reveal(hit) }
                                    Button(L("action.copy")) { model.copyPath(hit) }
                                }
                                .onDrag { NSItemProvider(object: URL(fileURLWithPath: hit.path) as NSURL) }
                        }
                    }
                    .padding(6)
                }
                // One identity per row (its path): a second one (the index)
                // let the lazy stack reuse rows of the previous answer.
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

    private var footer: some View {
        HStack {
            ResultsSummary(model: model)
            Spacer()
            Label(
                model.networkBlocked ? L("network.blocked") : L("network.open"),
                systemImage: model.networkBlocked ? "lock.shield" : "exclamationmark.shield"
            )
            .foregroundStyle(model.networkBlocked ? Color.secondary : Color.orange)
        }
        .font(.caption)
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
    }

    /// ⌘↩ reveal, ⌘⇧C copy path (spec §5.1).
    private var hiddenShortcuts: some View {
        ZStack {
            Button("") { model.reveal() }.keyboardShortcut(.return, modifiers: .command)
            Button("") { model.copyPath() }.keyboardShortcut("c", modifiers: [.command, .shift])
        }
        .opacity(0)
        .accessibilityHidden(true) // keyboard shortcuts only; the context menu has these actions
        .allowsHitTesting(false)
    }
}

/// "28 results · 0.3 ms", or "Searching…" while the query is answered.
struct ResultsSummary: View {
    @ObservedObject var model: SearchModel

    var body: some View {
        if model.searching {
            Text(L("results.searching"))
        } else if model.total > 0 {
            Text(String(format: L("results.count"), Int64(model.total), model.elapsedMs))
        }
    }
}

struct ResultRow: View {
    let hit: Hit
    let selected: Bool
    /// Accent highlight with white text (the window has the keyboard).
    var emphasized = true

    private var light: Bool { selected && emphasized }

    var body: some View {
        HStack(spacing: 10) {
            Image(nsImage: NSWorkspace.shared.icon(forFile: hit.path))
                .resizable()
                .frame(width: 28, height: 28)
            VStack(alignment: .leading, spacing: 2) {
                Text(hit.name).fontWeight(.medium).lineLimit(1)
                    .foregroundStyle(light ? Color.white : Color.primary)
                Text(hit.path).font(.caption).lineLimit(1).truncationMode(.middle)
                    .foregroundStyle(light ? Color.white.opacity(0.85) : Color.secondary)
            }
            Spacer(minLength: 8)
            if hit.content {
                Label(L("results.inContents"), systemImage: "doc.text.magnifyingglass")
                    .font(.caption2)
                    .foregroundStyle(light ? Color.white.opacity(0.85) : Color.secondary)
            }
            if let volume = hit.volume {
                Label(volume, systemImage: "externaldrive")
                    .font(.caption2)
                    .foregroundStyle(light ? Color.white.opacity(0.85) : Color.secondary)
            }
            ForEach(hit.badges, id: \.self) { b in
                Text(b)
                    .font(.caption2.monospacedDigit())
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .foregroundStyle(light ? Color.white : Color.primary)
                    .background(Capsule().fill(light ? AnyShapeStyle(Color.white.opacity(0.25)) : AnyShapeStyle(.quaternary)))
            }
        }
        .padding(.horizontal, 10)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(
            ([hit.name, hit.kind, ByteCountFormatter.string(fromByteCount: Int64(hit.size), countStyle: .file)]
                + hit.badges + (hit.content ? [L("results.inContents")] : []) + [hit.path]).joined(separator: ", ")
        )
        .accessibilityAddTraits(selected ? .isSelected : [])
        .padding(.vertical, 6)
        .background(RoundedRectangle(cornerRadius: 8).fill(selected ? (emphasized ? Color.accentColor : Color.secondary.opacity(0.2)) : .clear))
    }
}

/// Wrapping row of clickable chips.
struct FlowChips: View {
    let items: [String]
    let action: (String) -> Void

    var body: some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 200), alignment: .leading)], alignment: .leading, spacing: 8) {
            ForEach(items, id: \.self) { item in
                Button(item) { action(item) }
                    .buttonStyle(.bordered)
                    .font(.system(.body, design: .monospaced))
            }
        }
    }
}
