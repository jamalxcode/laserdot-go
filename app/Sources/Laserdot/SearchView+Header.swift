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
                onMove: { d in
                    if model.showCheatSheet { model.moveCheat(d) } else if model.choiceFocus != nil { model.moveChoiceGroup(d) } else { model.move(d) }
                },
                onExtend: { d in model.extend(d) },
                onSpace: { model.spaceKey() },
                onTab: {
                    if model.showCheatSheet { model.nextCheatSection() } else if model.choiceFocus != nil { model.moveChoiceGroup(1) } else { model.acceptCompletion() }
                },
                onSubmit: {
                    if model.showCheatSheet { model.insertSelectedCheat() } else if model.choiceFocus != nil { model.chooseFocusedChoice() } else { model.submit() }
                },
                onEscape: { if model.choiceFocus != nil { model.choiceFocus = nil } else { model.escape(close: close) } },
                inlineSuffix: model.inlineSuffix,
                selectAllToken: model.selectAllRequest,
                onJump: { d in model.jumpSection(d) },
                onCopy: { model.copyFiles() },
                onDeleteEmpty: { model.removeLastPill() },
                onRight: { model.arrowRight() },
                onLeft: { model.arrowLeft() }
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
        let smart = model.isHome ? SmartList.builtIn.filter { (model.smartCounts[$0.query] ?? 0) > 0 } : []
        if !completions.isEmpty || !pins.isEmpty || !smart.isEmpty {
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
                        // Smart lists: ready-made searches with live counts,
                        // then the pinned searches (your own), counted too.
                        if !smart.isEmpty {
                            Image(systemName: "sparkles").foregroundStyle(.secondary).accessibilityHidden(true)
                            ForEach(smart, id: \.id) { s in
                                Button {
                                    model.query = s.query + " "
                                } label: {
                                    Text(L("smart." + s.id) + " ") + Text(Self.count(model.smartCounts[s.query] ?? 0)).foregroundColor(.secondary)
                                }
                                .help(s.query)
                                .accessibilityIdentifier("smart-" + s.id)
                            }
                        }
                        if !pins.isEmpty {
                            Image(systemName: "pin").foregroundStyle(.secondary).accessibilityHidden(true)
                                .padding(.leading, smart.isEmpty ? 0 : 8)
                        }
                        ForEach(Array(pins.enumerated()), id: \.element) { i, q in
                            Button {
                                model.query = q
                            } label: {
                                if let n = model.smartCounts[q] {
                                    Text(q + " ") + Text(Self.count(n)).foregroundColor(.secondary)
                                } else {
                                    Text(q)
                                }
                            }
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
            // own, marked by a thin stripe of its colour on a faint tint. The
            // calm view (the owner: "less cluttered") leaves out what gives
            // nothing, keeps a group's biggest values and the first bands;
            // More choices brings the rest back.
            ScrollView(.vertical, showsIndicators: true) {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(RefineLogic.bands(groups), id: \.family) { band in
                        HStack(alignment: .top, spacing: 6) {
                            Capsule()
                                .fill(Self.bandColor(band.family))
                                .frame(width: 4)
                            // The family's icon too: colour is never the only cue.
                            Image(systemName: Self.bandIcon(band.family))
                                .font(.caption2.weight(.semibold))
                                .foregroundStyle(Self.bandColor(band.family))
                                .frame(width: 14, height: 18)
                                .accessibilityHidden(true)
                            FlowLayout(spacing: 4, lineSpacing: 5) {
                                ForEach(Array(band.groups.enumerated()), id: \.element.id) { gi, g in
                                    Text(Self.refineTitle(g.id))
                                        .font(.caption2.weight(.medium))
                                        .foregroundStyle(.secondary)
                                        .padding(.leading, gi == 0 ? 0 : 12)
                                        .fixedSize()
                                    ForEach(g.options, id: \.token) { o in
                                        refineChip(o, in: g)
                                            .fixedSize()
                                    }
                                }
                            }
                            .padding(.vertical, 3)
                        }
                        .padding(.leading, 4)
                        .padding(.trailing, 8)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(Self.bandColor(band.family).opacity(0.08), in: RoundedRectangle(cornerRadius: 6))
                        .accessibilityElement(children: .contain)
                        .accessibilityIdentifier("band-" + band.family)
                    }
                    moreChoicesButton
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
    @ViewBuilder
    func refineChip(_ o: RefineOption, in g: RefineGroup) -> some View {
        let all = model.flatChoices
        let focused = model.choiceFocus.map { all.indices.contains($0) && all[$0].option.token == o.token && all[$0].group.id == g.id } ?? false
        if o.isWaitingDrive {
            // A mounted drive with no index (or locked, or detached): a click
            // offers to index it; nothing to count or leave out yet.
            let state = o.state ?? "unindexed"
            let label = String(format: L("refine.drive." + state), o.label)
            ChoiceChip(
                label: label, count: "", enabled: true, spoken: "\(Self.refineTitle(g.id)) \(label)",
                help: String(format: L("refine.drive.help." + state), o.label), leaveOutHelp: "",
                onChoose: { model.applyRefinement(o, in: g) }, onLeaveOut: {}, focused: focused, canLeaveOut: false
            )
            .accessibilityIdentifier("drive-waiting")
        } else {
            ChoiceChip(
                label: Self.refineLabel(o),
                count: Self.count(o.count),
                enabled: o.count > 0,
                spoken: "\(Self.refineTitle(g.id)) \(Self.refineLabel(o)), \(Self.count(o.count))",
                help: String(format: L("refine.choose.help"), Self.refineLabel(o)),
                leaveOutHelp: String(format: L("refine.leaveOut.help"), Self.refineLabel(o)),
                onChoose: { refineClick(o, in: g) },
                onLeaveOut: { model.excludeRefinement(o) },
                focused: focused
            )
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

    static func refineTitle(_ id: String) -> String { L("refine.group." + id) }

    /// "More choices (N)" / "Fewer choices": shows what the calm view leaves
    /// out, and hides it again (remembered).
    @ViewBuilder var moreChoicesButton: some View {
        let hidden = model.hiddenChoices
        if hidden > 0 || model.allChoices {
            HStack {
                Spacer()
                Button {
                    withAnimation(.easeInOut(duration: 0.15)) { model.allChoices.toggle() }
                } label: {
                    Label(model.allChoices ? L("refine.fewer") : String(format: L("refine.more"), hidden),
                          systemImage: model.allChoices ? "chevron.up" : "chevron.down")
                        .font(.caption)
                }
                .buttonStyle(.plain)
                .foregroundStyle(Color.accentColor)
                .help(model.allChoices ? L("refine.fewer.help") : L("refine.more.help"))
                .accessibilityIdentifier("more-choices")
            }
        }
    }

    /// One colour per family of choices: a stripe and a faint tint. The
    /// Okabe–Ito colours, which stay apart for colour-blind people (the
    /// owner: "more distinct especially for color blind people"); next
    /// bands never use two similar ones.
    static func bandColor(_ family: String) -> Color {
        func rgb(_ r: Double, _ g: Double, _ b: Double) -> Color { Color(red: r / 255, green: g / 255, blue: b / 255) }
        switch family {
        case "where": return rgb(0, 114, 178)      // blue
        case "what": return rgb(230, 159, 0)       // orange
        case "file": return rgb(86, 180, 233)      // sky blue
        case "picture": return rgb(0, 158, 115)    // bluish green
        case "time": return rgb(213, 94, 0)        // vermillion
        case "photo": return rgb(204, 121, 167)    // reddish purple
        case "music": return rgb(181, 160, 0)      // yellow, deepened to show on white
        case "doc": return .primary                // black (white in dark mode), the last Okabe-Ito tone
        default: return .gray
        }
    }

    /// One icon per family, beside its stripe.
    static func bandIcon(_ family: String) -> String {
        switch family {
        case "where": return "externaldrive"
        case "what": return "square.grid.2x2"
        case "file": return "doc"
        case "picture": return "photo"
        case "time": return "clock"
        case "photo": return "camera"
        case "music": return "music.note"
        case "doc": return "doc.text.magnifyingglass"
        default: return "line.3.horizontal.decrease"
        }
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
    /// The keyboard is on it (Tab into the choices).
    var focused = false
    /// The "−" beside it (not for a drive that has no index yet).
    var canLeaveOut = true
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
            if enabled && canLeaveOut {
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
        .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(Color.accentColor, lineWidth: focused ? 2 : 0).padding(-2))
    }
}
