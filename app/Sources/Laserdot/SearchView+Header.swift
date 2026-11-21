import AppKit
import LaserdotKit
import SwiftUI
import UniformTypeIdentifiers

/// The search field, its buttons, the hint line and the suggestion chips.
extension SearchView {
    // MARK: header

    var header: some View {
        HStack(spacing: 10) {
            if model.isCommandMode {
                Image(systemName: "chevron.right.2").font(.title2).foregroundStyle(.secondary)
            } else {
                placeMenu
            }
            pillRow
            SearchField(
                text: $model.fieldText,
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
                onCopy: { model.copyFiles() },
                onDeleteEmpty: { model.removeLastPill() }
            )
            .frame(height: 30)
            // Reset: the words, the pills, the place and the order, at once.
            if model.canReset {
                Button { model.resetSearch() } label: {
                    Image(systemName: "xmark.circle.fill").foregroundStyle(.tertiary)
                }
                .buttonStyle(.borderless)
                .help(L("search.reset"))
                .accessibilityLabel(L("search.reset"))
                .accessibilityIdentifier("resetSearch")
            }
            if model.refreshing {
                ProgressView().controlSize(.small).frame(width: 20, height: 20)
                    .help(L("refresh.help"))
            }
            // The view switch and the … menu, always (one full screen).
            Group {
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
                Divider()
                Button(L("search.reset")) { model.resetSearch() }
                    .disabled(!model.canReset)
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
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
    }

    /// Where searches look, as Finder's "Search: This Mac": the magnifying
    /// glass is a menu (Everywhere, This Mac, recent folders, Choose Folder…),
    /// and the place's name shows beside it while it narrows the search. The
    /// same setting as Settings ▸ General ▸ Search in.
    var placeMenu: some View {
        let place = model.searchPlace
        let shown = model.placeInEffect ? SearchPlace.title(place, thisMac: L("refine.drive.local")) : ""
        return Menu {
            Button { model.setPlace("") } label: { checked(L("settings.searchIn.everywhere"), place == "") }
            Button { model.setPlace("local") } label: { checked(L("refine.drive.local"), place == "local") }
            let recent = model.recentPlaces
            if !recent.isEmpty {
                Divider()
                ForEach(recent, id: \.self) { p in
                    Button { model.setPlace(p) } label: { checked((p as NSString).lastPathComponent, place == p) }
                        .help(p)
                }
            }
            Divider()
            Button(L("place.choose")) { model.choosePlaceFolder() }
        } label: {
            HStack(spacing: 4) {
                Image(systemName: "magnifyingglass").font(.title2)
                if !shown.isEmpty {
                    Text(shown).font(.callout).lineLimit(1)
                }
            }
            .foregroundStyle(shown.isEmpty ? Color.secondary : Color.accentColor)
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help(shown.isEmpty ? L("place.help.everywhere") : String(format: L("place.searchingIn"), shown))
        .accessibilityLabel(shown.isEmpty ? L("place.help.everywhere") : String(format: L("place.searchingIn"), shown))
        .accessibilityIdentifier("placeMenu")
        .id(model.placeVersion)
    }

    @ViewBuilder func checked(_ title: String, _ on: Bool) -> some View {
        if on { Label(title, systemImage: "checkmark") } else { Text(title) }
    }

    /// The filters of the search as pills before the typed words ("PDF ✕",
    /// "not PDF ✕", "in Work ✕"); ✕ (or Delete with nothing typed) removes one.
    @ViewBuilder var pillRow: some View {
        let pills = model.pills
        if !pills.isEmpty && !model.isCommandMode {
            HStack(spacing: 4) {
                ForEach(Array(pills.prefix(4)), id: \.self) { p in
                    let label = model.pillLabel(p)
                    HStack(spacing: 3) {
                        Text(label).font(.callout).lineLimit(1)
                        Button { model.removePill(p) } label: {
                            Image(systemName: "xmark").font(.caption2.weight(.bold))
                        }
                        .buttonStyle(.borderless)
                        .help(String(format: L("pill.remove"), label))
                        .accessibilityLabel(String(format: L("pill.remove"), label))
                    }
                    .padding(.horizontal, 8)
                    .padding(.vertical, 3)
                    .background(Capsule().fill(Color.accentColor.opacity(p.hasPrefix("-") ? 0.08 : 0.16)))
                    .overlay(Capsule().strokeBorder(Color.accentColor.opacity(0.35), lineWidth: p.hasPrefix("-") ? 1 : 0))
                    .help(p)
                }
                if pills.count > 4 {
                    Text("+\(pills.count - 4)")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .help(pills.dropFirst(4).map { model.pillLabel($0) }.joined(separator: ", "))
                }
            }
            .fixedSize()
            .accessibilityIdentifier("pills")
        }
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
        let pins = model.isHome ? model.pinned : []
        if !completions.isEmpty || !pins.isEmpty {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 6) {
                    // ⌥1–⌥9 press a chip without the mouse (shown in its tooltip).
                    if !completions.isEmpty {
                        ForEach(Array(completions.enumerated()), id: \.element) { i, c in
                            Button(c.label) { model.apply(c) }
                                .help(c.detail + Self.chipKey(i))
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
            // Every chip wraps on its own, so a long group (cameras, folders)
            // continues on the next line instead of running off the edge (the
            // owner had to drag the window wider); about six lines show, more
            // scroll.
            // Top down, large to granular (where, what, size and date, picture,
            // length and sound, photo, music): each family is a band of its
            // own, shaded alike, so related choices read as one.
            ScrollView(.vertical, showsIndicators: true) {
                VStack(alignment: .leading, spacing: 3) {
                    ForEach(RefineLogic.bands(groups), id: \.family) { band in
                        FlowLayout(spacing: 4, lineSpacing: 6) {
                            ForEach(Array(band.groups.enumerated()), id: \.element.id) { gi, g in
                                Text(Self.refineTitle(g.id))
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                    .padding(.leading, gi == 0 ? 0 : 10)
                                    .fixedSize()
                                ForEach(g.options, id: \.token) { o in
                                    refineChip(o, in: g)
                                        .fixedSize()
                                }
                            }
                        }
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(Self.bandTint(band.family), in: RoundedRectangle(cornerRadius: 7))
                        .accessibilityElement(children: .contain)
                        .accessibilityIdentifier("band-" + band.family)
                    }
                }
                .buttonStyle(.bordered)
                .controlSize(.mini)
                .padding(.horizontal, 12)
            }
            .frame(maxHeight: 190)
            .fixedSize(horizontal: false, vertical: true)
            .padding(.bottom, 8)
            .accessibilityIdentifier("refinements")
        }
    }

    /// One choice: chosen ones are filled with a check (click: take it out);
    /// one that no longer matches anything is greyed out.
    /// One choice of the row (what is chosen is a pill in the field, so the
    /// row holds open choices only). Click: search only this; the "−" shown
    /// on hover (or ⌥-click, or right-click ▸ Leave Out): leave it out.
    func refineChip(_ o: RefineOption, in g: RefineGroup) -> some View {
        ChoiceChip(
            label: Self.refineLabel(o),
            count: Self.count(o.count),
            enabled: o.count > 0,
            spoken: "\(Self.refineTitle(g.id)) \(Self.refineLabel(o)), \(Self.count(o.count))",
            help: String(format: L("refine.choose.help"), Self.refineLabel(o)),
            leaveOutHelp: String(format: L("refine.leaveOut.help"), Self.refineLabel(o)),
            onChoose: { refineClick(o, in: g) },
            onLeaveOut: { model.excludeRefinement(o) }
        )
        .contextMenu {
            Button(L("refine.leaveOut")) { model.excludeRefinement(o) }
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

    static func refineTitle(_ id: String) -> String { L("refine.group." + id) }

    /// One soft shade per family of choices (light enough for the chips'
    /// text in light and dark mode).
    static func bandTint(_ family: String) -> Color {
        let c: Color
        switch family {
        case "where": c = .blue
        case "what": c = .purple
        case "file": c = .orange
        case "picture": c = .green
        case "time": c = .teal
        case "photo": c = .pink
        case "music": c = .indigo
        default: c = .gray
        }
        return c.opacity(0.10)
    }

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

/// A choice chip: the value and its count, and a "−" beside it to leave the
/// value out of the results (faint until the pointer is over the chip).
struct ChoiceChip: View {
    let label: String
    let count: String
    let enabled: Bool
    let spoken: String
    let help: String
    let leaveOutHelp: String
    let onChoose: () -> Void
    let onLeaveOut: () -> Void
    @State private var hovering = false

    var body: some View {
        HStack(spacing: 2) {
            Button(action: onChoose) {
                Text("\(label) ").font(.caption) + Text(count).font(.caption).foregroundColor(.secondary)
            }
            .disabled(!enabled)
            .help(help)
            .accessibilityLabel(spoken)
            // Always there (faint until pointed at): appearing on hover it
            // moved the chips, and slipped away from the pointer (the owner).
            if enabled {
                Button(action: onLeaveOut) {
                    Image(systemName: "minus.circle").font(.caption)
                }
                .buttonStyle(.borderless)
                .opacity(hovering ? 1 : 0.35)
                .help(leaveOutHelp)
                .accessibilityLabel(leaveOutHelp)
            }
        }
        .onHover { hovering = $0 }
    }
}
