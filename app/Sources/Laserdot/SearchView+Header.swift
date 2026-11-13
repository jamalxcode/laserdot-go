import AppKit
import LaserdotKit
import SwiftUI
import UniformTypeIdentifiers

/// The search field, its buttons, the hint line and the suggestion chips.
extension SearchView {
    // MARK: header

    var header: some View {
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
            // Look at the folders again (deleted files leave the results).
            Button { model.refreshIndex() } label: {
                if model.refreshing {
                    ProgressView().controlSize(.small).frame(width: 20, height: 20)
                } else {
                    Image(systemName: "arrow.clockwise").font(.title3)
                }
            }
            .buttonStyle(.borderless)
            .keyboardShortcut("r", modifiers: .command)
            .disabled(model.refreshing)
            .help(L("refresh.help") + " (⌘R)")
            .accessibilityLabel(L("refresh.help"))
            // Thumbnail size, as Finder's icon view (⌘+ ⌘−).
            if model.viewMode == .grid {
                Slider(value: $model.tileSize, in: SearchModel.tileSizes)
                    .frame(width: 90)
                    .controlSize(.small)
                    .help(L("view.tileSize") + " (⌘+ ⌘−)")
                    .accessibilityLabel(L("view.tileSize"))
            }
            // Rows, Finder's columns, or thumbnails (⌘J: the next one).
            Picker(L("view.title"), selection: $model.viewMode) {
                ForEach(ResultsView.allCases, id: \.self) { v in
                    Image(systemName: v.symbol).help(L("view.\(v.rawValue)")).tag(v)
                }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()
            .help(L("view.title") + " (⌘J)")
            .accessibilityLabel(L("view.title"))
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

    @ViewBuilder var hintLine: some View {
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
    @ViewBuilder var chips: some View {
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
}
