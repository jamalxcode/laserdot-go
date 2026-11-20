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
                onEscape: { model.escape(close: close) },
                inlineSuffix: model.inlineSuffix,
                selectAllToken: model.selectAllRequest,
                onJump: { d in model.jumpSection(d) },
                onCopy: { model.copyFiles() }
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
            if model.refreshing {
                ProgressView().controlSize(.small).frame(width: 20, height: 20)
                    .help(L("refresh.help"))
            }
            // Thumbnail size, as Finder's icon view (⌘+ ⌘−): only while
            // thumbnails are shown.
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
            // Everything else in one menu, so the bar stays calm like
            // Spotlight's (the owner's review): refresh, order, pin, the
            // filter list. Their shortcuts work without opening it.
            Menu {
                Button(L("refresh.help") + "  ⌘R") { model.refreshIndex() }
                    .disabled(model.refreshing)
                Picker(L("sort.title"), selection: $model.sort) {
                    ForEach(SortKey.allCases, id: \.self) { k in Text(L("sort.\(k.rawValue)")).tag(k) }
                }
                Toggle(L("sort.reverse"), isOn: $model.reverse)
                Divider()
                Button(L(model.isPinned ? "pin.remove" : "pin.add") + "  ⌘D") { model.togglePin() }
                    .disabled(model.isHome || model.isCommandMode)
                Button(L("cheatsheet.help")) { model.showCheatSheet.toggle() }
            } label: {
                Image(systemName: model.sort == .relevance && !model.isPinned ? "ellipsis.circle" : "ellipsis.circle.fill")
                    .font(.title3)
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()
            .help(L("search.more"))
            .accessibilityLabel(L("search.more"))
            .accessibilityIdentifier("search-more")
            .background(shortcutKeys)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
    }

    /// The menu's keyboard shortcuts, active without opening it (⌘R, ⌘D,
    /// ⌘/): invisible buttons.
    var shortcutKeys: some View {
        ZStack {
            Button("") { model.refreshIndex() }.keyboardShortcut("r", modifiers: .command).disabled(model.refreshing)
            Button("") { model.togglePin() }.keyboardShortcut("d", modifiers: .command)
                .disabled(model.isHome || model.isCommandMode)
            Button("") { model.showCheatSheet.toggle() }.keyboardShortcut("/", modifiers: .command)
        }
        .opacity(0)
        .frame(width: 0, height: 0)
        .accessibilityHidden(true)
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

    /// What the results have to choose from, by attribute ("Resolution:
    /// 4K 3 · 1080p 12   Sound: With sound 14 · No sound 1"). A choice joins
    /// the query, so the hint line explains it and it can be removed by
    /// editing the search like any word. Never hidden by completions (a
    /// choice just clicked offers none).
    @ViewBuilder var refinementChips: some View {
        let groups = model.visibleRefinements
        if model.showsRefinements {
            // Wraps to more lines instead of running off the edge (each
            // group stays together), at most three lines' worth.
            ScrollView(.vertical, showsIndicators: false) {
                FlowLayout(spacing: 12, lineSpacing: 6) {
                    ForEach(groups, id: \.id) { g in
                        HStack(spacing: 4) {
                            Text(Self.refineTitle(g.id))
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            ForEach(g.options, id: \.token) { o in
                                refineChip(o, in: g)
                            }
                        }
                        .fixedSize()
                    }
                }
                .buttonStyle(.bordered)
                .controlSize(.mini)
                .padding(.horizontal, 16)
            }
            .frame(maxHeight: 74)
            .fixedSize(horizontal: false, vertical: true)
            .padding(.bottom, 8)
            .accessibilityIdentifier("refinements")
        }
    }

    /// One choice: chosen ones are filled with a check (click: take it out);
    /// one that no longer matches anything is greyed out.
    @ViewBuilder func refineChip(_ o: RefineOption, in g: RefineGroup) -> some View {
        if o.isExcluded {
            // Left out: struck through; a click brings it back.
            Button { model.applyRefinement(o, in: g) } label: {
                Text("− " + Self.refineLabel(o)).font(.caption).strikethrough()
            }
            .help(String(format: L("refine.leftOut.help"), Self.refineLabel(o)))
            .accessibilityLabel(String(format: L("refine.leftOut.spoken"), Self.refineTitle(g.id), Self.refineLabel(o)))
        } else {
            refineChoiceChip(o, in: g)
                .contextMenu {
                    Button(L("refine.leaveOut")) { model.excludeRefinement(o) }
                }
        }
    }

    /// ⌥-click leaves a choice out instead of choosing it.
    func refineClick(_ o: RefineOption, in g: RefineGroup) {
        if NSEvent.modifierFlags.contains(.option) {
            model.excludeRefinement(o)
        } else {
            model.applyRefinement(o, in: g)
        }
    }

    @ViewBuilder func refineChoiceChip(_ o: RefineOption, in g: RefineGroup) -> some View {
        let label = (o.isSelected ? "✓ " : "") + Self.refineLabel(o)
        let content = Text("\(label) ").font(.caption)
            + Text(Self.count(o.count)).font(.caption).foregroundColor(o.isSelected ? .white.opacity(0.85) : .secondary)
        let spoken = "\(Self.refineTitle(g.id)) \(Self.refineLabel(o)), \(Self.count(o.count))"
        if o.isSelected {
            Button { model.applyRefinement(o, in: g) } label: { content }
                .buttonStyle(.borderedProminent)
                .help(String(format: L("refine.remove"), o.token))
                .accessibilityLabel(spoken)
                .accessibilityAddTraits(.isSelected)
        } else {
            Button { refineClick(o, in: g) } label: { content }
                .disabled(o.count == 0)
                .help(String(format: L("refine.choose.help"), o.token))
                .accessibilityLabel(spoken)
        }
    }

    static func refineTitle(_ id: String) -> String { L("refine.group." + id) }

    static func refineLabel(_ o: RefineOption) -> String {
        if let key = o.key { return L("refine." + key) }
        return o.label
    }

    static func count(_ n: UInt64) -> String {
        let f = NumberFormatter()
        f.numberStyle = .decimal
        return f.string(from: NSNumber(value: n)) ?? "\(n)"
    }

    static func chipKey(_ i: Int) -> String { i < 9 ? "  (⌥\(i + 1))" : "" }
}
