import AVKit
import AppKit
import LaserdotKit
import PDFKit
import SwiftUI
import UniformTypeIdentifiers

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
                if model.undoBannerVisible, let step = model.undoStep {
                    UndoBanner(step: step, model: model)
                }
                // The main window shows status and keys in its own status bar.
                if fixedSize {
                    Divider()
                    footer
                }
            }
        }
        // The overlay fills its panel (resizable, remembered); the slim bar
        // is just the field.
        .frame(maxWidth: .infinity, maxHeight: fixedSize && model.compact ? Self.compactHeight : .infinity, alignment: .top)
        .background {
            // The floating card is the overlay's look; the main window keeps
            // the standard window background.
            if fixedSize {
                RoundedRectangle(cornerRadius: 14, style: .continuous).fill(.regularMaterial)
            }
        }
        .overlay(hiddenShortcuts)
        // Drop a folder (or a file: its folder) to search inside it.
        .onDrop(of: [.fileURL], isTargeted: nil) { providers in model.handleDrop(providers) }
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
                onSpace: { model.spaceKey() },
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
            // Result order.
            Menu {
                Picker(L("sort.title"), selection: $model.sort) {
                    ForEach(SortKey.allCases, id: \.self) { k in Text(L("sort.\(k.rawValue)")).tag(k) }
                }
                .pickerStyle(.inline)
                Toggle(L("sort.reverse"), isOn: $model.reverse)
            } label: {
                Image(systemName: model.sort == .relevance ? "arrow.up.arrow.down" : "arrow.up.arrow.down.circle.fill")
                    .font(.title3)
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()
            .help(L("sort.title"))
            .accessibilityLabel(L("sort.title"))
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
        } else if model.similarOnly {
            Label(L("results.similar"), systemImage: "wand.and.stars")
                .font(.callout)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
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
                    // ⌥1–⌥9 press a chip without the mouse (shown in its tooltip).
                    if !completions.isEmpty {
                        ForEach(Array(completions.enumerated()), id: \.element) { i, c in
                            Button(c.label) { model.apply(c) }
                                .help(c.detail + Self.chipKey(i))
                                .modifier(ChipKey(index: i))
                        }
                    } else if !kinds.isEmpty {
                        ForEach(Array(kinds.enumerated()), id: \.element.id) { i, k in
                            Button("\(k.title) (\(k.total))") { model.narrow(to: k) }
                                .help((k.filter ?? "") + Self.chipKey(i))
                                .modifier(ChipKey(index: i))
                        }
                    } else {
                        Image(systemName: "pin").foregroundStyle(.secondary).accessibilityHidden(true)
                        ForEach(Array(pins.enumerated()), id: \.element) { i, q in
                            Button(q) { model.query = q }
                                .help(Self.chipKey(i).trimmingCharacters(in: .whitespaces))
                                .modifier(ChipKey(index: i))
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

    static func chipKey(_ i: Int) -> String { i < 9 ? "  (⌥\(i + 1))" : "" }

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
                        // The end of the list: fetch the next page.
                        if model.canLoadMore {
                            ProgressView().controlSize(.small).frame(maxWidth: .infinity).padding(8)
                                .onAppear { model.loadMore() }
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
            Button("") { model.undo() }.keyboardShortcut("z", modifiers: [.command, .option])
                .disabled(model.undoStep == nil)
            // While browsing results only: typing keeps ⌘→ (end of line).
            Button("") { model.expandSelectedSection() }.keyboardShortcut(.rightArrow, modifiers: .command)
                .disabled(!model.browsing)
            ForEach(1...9, id: \.self) { n in
                Button("") { model.openNumbered(n) }.keyboardShortcut(KeyEquivalent(Character("\(n)")), modifiers: .command)
            }
        }
        .opacity(0)
        .accessibilityHidden(true) // the context menu and ⌘K list offer these actions
        .allowsHitTesting(false)
    }
}
