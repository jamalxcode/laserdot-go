import AppKit
import LaserdotKit
import SwiftUI

/// The overlay: query field, hint line, results (or the cheat sheet), and a
/// footer that says whether the network is blocked.
struct SearchView: View {
    @ObservedObject var model: SearchModel
    var close: () -> Void
    @FocusState private var focused: Bool

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                Image(systemName: "magnifyingglass").font(.title2).foregroundStyle(.secondary)
                TextField(L("search.placeholder"), text: $model.query)
                    .textFieldStyle(.plain)
                    .font(.system(size: 22))
                    .focused($focused)
                    .onSubmit { model.showCheatSheet ? model.insertSelectedCheat() : model.open() }
                    .onKeyPress(.downArrow) {
                        model.showCheatSheet ? model.moveCheat(1) : model.move(1)
                        return .handled
                    }
                    .onKeyPress(.upArrow) {
                        model.showCheatSheet ? model.moveCheat(-1) : model.move(-1)
                        return .handled
                    }
                    .onKeyPress(.tab) {
                        model.showCheatSheet ? model.nextCheatSection() : model.acceptCompletion()
                        return .handled
                    }
                    .onExitCommand {
                        // Esc closes the cheat sheet first, then the overlay (§8.9.2).
                        if model.showCheatSheet { model.showCheatSheet = false } else { close() }
                    }
                    .accessibilityLabel(L("search.placeholder"))
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
            Divider()
            footer
        }
        .frame(width: 720, height: 480)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay(hiddenShortcuts)
        .onAppear { focused = true }
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
                            ResultRow(hit: hit, selected: i == model.selection)
                                .id(i)
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
                .onChange(of: model.selection) { _, i in proxy.scrollTo(i) }
            }
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
            if model.total > 0 {
                Text(String(format: L("results.count"), Int64(model.total), model.elapsedMs))
            }
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
        .allowsHitTesting(false)
    }
}

struct ResultRow: View {
    let hit: Hit
    let selected: Bool

    var body: some View {
        HStack(spacing: 10) {
            Image(nsImage: NSWorkspace.shared.icon(forFile: hit.path))
                .resizable()
                .frame(width: 28, height: 28)
            VStack(alignment: .leading, spacing: 2) {
                Text(hit.name).fontWeight(.medium).lineLimit(1)
                Text(hit.path).font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
            }
            Spacer(minLength: 8)
            if hit.content {
                Label(L("results.inContents"), systemImage: "doc.text.magnifyingglass")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            if let volume = hit.volume {
                Label(volume, systemImage: "externaldrive")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            ForEach(hit.badges, id: \.self) { b in
                Text(b)
                    .font(.caption2.monospacedDigit())
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(Capsule().fill(.quaternary))
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
        .background(RoundedRectangle(cornerRadius: 8).fill(selected ? Color.accentColor.opacity(0.25) : .clear))
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
