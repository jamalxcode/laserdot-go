import AppKit
import LaserdotKit
import SwiftUI
import UniformTypeIdentifiers

/// The results: rows in sections, Finder's columns or thumbnails; commands; suggestions.
extension SearchView {
    // MARK: the list

    @ViewBuilder var mainList: some View {
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
                    LazyVStack(alignment: .leading, spacing: model.viewMode == .columns ? 0 : 2,
                               pinnedViews: model.viewMode == .columns ? [.sectionHeaders] : []) {
                        if model.viewMode == .columns {
                            Section {
                                ForEach(model.results.indices.map { IndexedHit(index: $0, hit: model.results[$0]) }) { item in
                                    columnRow(item.index, item.hit)
                                }
                            } header: {
                                ColumnHeader(model: model)
                            }
                        } else {
                        ForEach(model.sections) { section in
                            if !model.gridView && (model.sections.count > 1 || section.id != "top") {
                                Text(section.title)
                                    .font(.caption.weight(.semibold))
                                    .foregroundStyle(.secondary)
                                    .padding(.horizontal, 10)
                                    .padding(.top, section.range.lowerBound == 0 ? 2 : 10)
                                    .accessibilityAddTraits(.isHeader)
                            }
                            if model.gridView {
                                LazyVGrid(columns: [GridItem(.adaptive(minimum: tileWidth), spacing: Self.tileSpacing)],
                                          alignment: .leading, spacing: Self.tileSpacing) {
                                    ForEach(section.range.map { IndexedHit(index: $0, hit: model.results[$0]) }) { item in
                                        tile(item.index, item.hit)
                                    }
                                }
                            } else {
                                ForEach(section.range.map { IndexedHit(index: $0, hit: model.results[$0]) }) { item in
                                    row(item.index, item.hit)
                                }
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
                // How many tiles fit in a row (↑↓ move by a row in the grid).
                .background(GeometryReader { g in
                    Color.clear
                        .onAppear { model.gridColumns = columns(for: g.size.width) }
                        .onChange(of: g.size.width) { _, w in model.gridColumns = columns(for: w) }
                        .onChange(of: model.tileSize) { _, _ in model.gridColumns = columns(for: g.size.width) }
                })
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

    /// Finder's list: name, dates, size, kind in columns.
    func columnRow(_ i: Int, _ hit: Hit) -> some View {
        let selected = model.isSelected(i)
        let stripe: Color = i % 2 == 1 ? Color.secondary.opacity(0.06) : Color.clear
        let selectedColor: Color = emphasized ? Color.accentColor : Color.secondary.opacity(0.2)
        let background: Color = selected ? selectedColor : stripe
        return ColumnRow(hit: hit, onAccent: selected && emphasized, highlight: model.highlightWords)
            .background(background)
        .contentShape(Rectangle())
        .id(hit.path)
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
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(selected ? [.isSelected, .isButton] : .isButton)
    }

    /// A tile is the thumbnail (the size slider, ⌘+ ⌘−) plus a margin.
    var tileWidth: CGFloat { model.tileSize + 28 }
    static let tileSpacing: CGFloat = 8

    func columns(for width: CGFloat) -> Int {
        max(1, Int((width - 12 + Self.tileSpacing) / (tileWidth + Self.tileSpacing)))
    }

    /// The grid: a large thumbnail and the name, as Finder's icon view.
    func tile(_ i: Int, _ hit: Hit) -> some View {
        let selected = model.isSelected(i)
        return VStack(spacing: 4) {
            FileIcon(path: hit.path, size: model.tileSize, thumbnail: true)
                .frame(width: model.tileSize + 8, height: model.tileSize * 0.92)
                .shadow(color: .black.opacity(0.12), radius: 2, y: 1)
            Text(highlighted(hit.name, model.highlightWords))
                .font(.caption)
                .lineLimit(2)
                .truncationMode(.middle)
                .multilineTextAlignment(.center)
                .foregroundStyle(selected && emphasized ? Color.white : Color.primary)
            if let badge = hit.badges.first {
                Text(badge).font(.caption2).foregroundStyle(selected && emphasized ? Color.white.opacity(0.85) : Color.secondary)
            }
        }
        .padding(6)
        .frame(width: tileWidth, height: model.tileSize + 56, alignment: .top)
        .background(RoundedRectangle(cornerRadius: 8)
            .fill(selected ? (emphasized ? Color.accentColor : Color.secondary.opacity(0.2)) : Color.clear))
        .contentShape(Rectangle())
        .id(hit.path)
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
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(selected ? [.isSelected, .isButton] : .isButton)
    }

    func row(_ i: Int, _ hit: Hit) -> some View {
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

    var commandList: some View {
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
    @ViewBuilder var coachingList: some View {
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

    func coachingText(_ c: CoachingInfo) -> String {
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

    func placeholder(_ text: String) -> some View {
        Text(text).foregroundStyle(.secondary).frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}
