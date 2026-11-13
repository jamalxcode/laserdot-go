import AppKit
import LaserdotKit
import SwiftUI
import UniformTypeIdentifiers

// The columns view, as Finder's list: widths, headers that sort, rows.

/// The columns after Name, their widths (dragged in the header, remembered).
final class ColumnLayout: ObservableObject {
    static let shared = ColumnLayout()

    enum Column: String, CaseIterable {
        case modified, size, kind, opened, created

        var sort: SortKey {
            switch self {
            case .modified: return .modified
            case .size: return .size
            case .kind: return .kind
            case .opened: return .opened
            case .created: return .created
            }
        }

        var defaultWidth: CGFloat {
            switch self {
            case .size: return 72
            case .kind: return 120
            default: return 128
            }
        }

        var trailing: Bool { self == .size }
    }

    @Published private(set) var widths: [Column: CGFloat] = [:]

    init() {
        for c in Column.allCases {
            let saved = UserDefaults.standard.double(forKey: "laserdot.column.\(c.rawValue)")
            widths[c] = saved >= 40 ? saved : c.defaultWidth
        }
    }

    func width(_ c: Column) -> CGFloat { widths[c] ?? c.defaultWidth }

    func setWidth(_ c: Column, _ w: CGFloat) {
        let w = max(44, min(360, w.rounded()))
        widths[c] = w
        UserDefaults.standard.set(Double(w), forKey: "laserdot.column.\(c.rawValue)")
    }
}

/// The columns' headers, as in Finder: click one to sort by it, again for
/// the other way (the arrow shows which); drag a column's left edge to make
/// it wider or narrower.
struct ColumnHeader: View {
    @ObservedObject var model: SearchModel
    @ObservedObject var layout = ColumnLayout.shared
    @State private var dragStart: [ColumnLayout.Column: CGFloat] = [:]

    /// "JPEG image", "Folder", "Application" — from the type, no disk access.
    static func kindName(_ hit: Hit) -> String {
        if hit.kind == "app" { return UTType.application.localizedDescription ?? "Application" }
        if hit.isDir { return UTType.folder.localizedDescription ?? "Folder" }
        let ext = (hit.name as NSString).pathExtension
        return UTType(filenameExtension: ext)?.localizedDescription ?? ext.uppercased()
    }

    var body: some View {
        HStack(spacing: 0) {
            Color.clear.frame(width: 24, height: 1)
            header(L("column.name"), .name).frame(maxWidth: .infinity, alignment: .leading)
            ForEach(ColumnLayout.Column.allCases, id: \.self) { c in
                handle(c)
                header(L("column.\(c.rawValue)"), c.sort)
                    .frame(width: layout.width(c), alignment: c.trailing ? .trailing : .leading)
            }
        }
        .font(.caption.weight(.medium))
        .foregroundStyle(.secondary)
        .padding(.horizontal, 10)
        .padding(.vertical, 5)
        .background(.bar)
        .overlay(alignment: .bottom) { Divider() }
    }

    /// A column's left edge: drag to resize.
    private func handle(_ c: ColumnLayout.Column) -> some View {
        Rectangle()
            .fill(Color.secondary.opacity(0.25))
            .frame(width: 1, height: 14)
            .padding(.horizontal, 4)
            .contentShape(Rectangle())
            .onHover { inside in inside ? NSCursor.resizeLeftRight.push() : NSCursor.pop() }
            .gesture(DragGesture(minimumDistance: 1)
                .onChanged { g in
                    let start = dragStart[c] ?? layout.width(c)
                    dragStart[c] = start
                    layout.setWidth(c, start - g.translation.width)
                }
                .onEnded { _ in dragStart[c] = nil })
            .help(L("column.resize"))
            .accessibilityHidden(true)
    }

    private func header(_ title: String, _ key: SortKey) -> some View {
        let active = model.sort == key
        // Name and kind start A→Z, sizes and dates largest/newest first.
        let ascending = (key == .name || key == .kind) != model.reverse
        return Button { model.sortBy(key) } label: {
            HStack(spacing: 3) {
                Text(title).lineLimit(1).foregroundStyle(active ? Color.primary : Color.secondary)
                if active {
                    Image(systemName: ascending ? "chevron.up" : "chevron.down").font(.caption2)
                }
            }
        }
        .buttonStyle(.plain)
        .help(String(format: L("column.sortHelp"), title))
        .accessibilityLabel(title)
        .accessibilityAddTraits(active ? .isSelected : [])
    }
}

/// One row of the columns view (split out so each piece type-checks quickly).
struct ColumnRow: View {
    let hit: Hit
    let onAccent: Bool
    let highlight: [String]
    @ObservedObject var layout = ColumnLayout.shared

    private var secondary: Color { onAccent ? Color.white.opacity(0.85) : Color.secondary }

    private func text(_ c: ColumnLayout.Column) -> String {
        switch c {
        case .modified: return friendlyDate(hit.modified, long: false)
        case .size: return hit.isDir ? "--" : ByteCountFormatter.string(fromByteCount: Int64(hit.size), countStyle: .file)
        case .kind: return ColumnHeader.kindName(hit)
        case .opened: return hit.opened > 0 ? friendlyDate(hit.opened, long: false) : "--"
        case .created: return friendlyDate(hit.created, long: false)
        }
    }

    var body: some View {
        HStack(spacing: 0) {
            FileIcon(path: hit.path, size: 16).frame(width: 24, alignment: .leading)
            name
            ForEach(ColumnLayout.Column.allCases, id: \.self) { c in
                cell(text(c), c)
            }
        }
        .font(.callout)
        .foregroundStyle(onAccent ? Color.white : Color.primary)
        .padding(.horizontal, 10)
        .padding(.vertical, 3)
    }

    private var name: some View {
        Text(highlighted(hit.name, highlight))
            .lineLimit(1)
            .truncationMode(.middle)
            .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func cell(_ value: String, _ c: ColumnLayout.Column) -> some View {
        Text(value)
            .lineLimit(1)
            .foregroundStyle(secondary)
            .padding(.leading, 9) // the header's resize handle
            .frame(width: layout.width(c) + 9, alignment: c.trailing ? .trailing : .leading)
    }
}
