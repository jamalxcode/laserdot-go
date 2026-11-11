import AppKit
import LaserdotKit
import SwiftUI

// The pieces of the result list: rows, the status and key bar, the ⌘K pane.

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
            Text(String(format: L(model.total == 1 ? "results.countOne" : "results.count"), Int64(model.total), model.elapsedMs))
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
        // Space works once ↑/↓ or a click has moved into the results.
        let look = model.browsing ? k("space", L("action.quickLook")) : k("↓ space", L("action.quickLook"))
        return [k("↩", L("action.open")), look, k("⌘↩", L("action.reveal")), k("⌘K", L("keys.actions"))]
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

/// A result and its place in `SearchModel.results`; identified by its path,
/// one identity per row.
struct IndexedHit: Identifiable {
    let index: Int
    let hit: Hit
    var id: String { hit.path }
}

/// ⌥1–⌥9 for the first nine chips.
struct ChipKey: ViewModifier {
    let index: Int

    func body(content: Content) -> some View {
        if index < 9 {
            content.keyboardShortcut(KeyEquivalent(Character("\(index + 1)")), modifiers: .option)
        } else {
            content
        }
    }
}
