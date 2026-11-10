import LaserdotKit
import SwiftUI

/// Every filter from the registry, grouped by section (spec §8.9), in the
/// app's language. Click a key to insert it; click an example to run it.
/// The query field keeps focus: ↑/↓ move the selection, Return inserts,
/// Tab jumps to the next section (§8.9.8). The section of the token under
/// the caret is highlighted and scrolled into view (§8.9.6), and enumerated
/// values show live counts (§8.9.5).
struct CheatSheetView: View {
    @ObservedObject var model: SearchModel

    private var sections: [(id: String, title: String, items: [FilterInfo])] {
        var order: [(String, String)] = []
        for f in model.cheatEntries where !order.contains(where: { $0.0 == f.sectionId }) {
            order.append((f.sectionId, f.section))
        }
        return order.map { s in (s.0, s.1, model.cheatEntries.filter { $0.sectionId == s.0 }) }
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text(L("cheatsheet.title")).font(.headline)
                Spacer()
                TextField(L("cheatsheet.filter"), text: $model.cheatFilter)
                    .textFieldStyle(.roundedBorder)
                    .frame(width: 200)
                    .accessibilityLabel(L("cheatsheet.filter"))
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 8)
            ScrollViewReader { proxy in
                ScrollView {
                    VStack(alignment: .leading, spacing: 14) {
                        basics
                        ForEach(sections, id: \.id) { section in
                            VStack(alignment: .leading, spacing: 4) {
                                Text(section.title.uppercased())
                                    .font(.caption)
                                    .foregroundStyle(section.id == model.currentSection ? Color.accentColor : .secondary)
                                    .accessibilityAddTraits(.isHeader)
                                ForEach(section.items, id: \.key) { f in row(f) }
                            }
                            .padding(6)
                            .background(
                                RoundedRectangle(cornerRadius: 8)
                                    .fill(section.id == model.currentSection ? Color.accentColor.opacity(0.08) : .clear)
                            )
                            .id("section-" + section.id)
                        }
                    }
                    .padding(.horizontal, 16)
                    .padding(.bottom, 12)
                }
                .onChange(of: model.currentSection) { _, s in
                    if let s { withAnimation { proxy.scrollTo("section-" + s, anchor: .top) } }
                }
                .onChange(of: model.cheatIndex) { _, i in
                    if model.cheatEntries.indices.contains(i) { proxy.scrollTo(model.cheatEntries[i].key) }
                }
            }
        }
    }

    private var basics: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(L("cheatsheet.basics")).font(.caption).foregroundStyle(.secondary).accessibilityAddTraits(.isHeader)
            ForEach(
                [
                    ("invoice march", L("cheatsheet.words")),
                    ("\"tax return\"", L("cheatsheet.phrase")),
                    ("-draft", L("cheatsheet.exclude")),
                    ("mkv OR mp4", L("cheatsheet.either")),
                    ("report*", L("cheatsheet.wildcards")),
                    ("content:\"merger plans\"", L("cheatsheet.content")),
                ],
                id: \.0
            ) { example, meaning in
                HStack {
                    Button(example) { model.query = example }
                        .buttonStyle(.link)
                        .font(.system(.body, design: .monospaced))
                        .frame(width: 180, alignment: .leading)
                        .environment(\.layoutDirection, .leftToRight)
                    Text(meaning).foregroundStyle(.secondary)
                }
            }
        }
    }

    private func row(_ f: FilterInfo) -> some View {
        let selected = model.cheatEntries.indices.contains(model.cheatIndex)
            && model.cheatEntries[model.cheatIndex].key == f.key
        let key = f.key.split(separator: ".").last.map(String.init) ?? f.key
        return HStack(alignment: .firstTextBaseline) {
            Button(f.key + ":") { model.insertFilter(f.key) }
                .buttonStyle(.link)
                .font(.system(.body, design: .monospaced))
                .frame(width: 120, alignment: .leading)
                .environment(\.layoutDirection, .leftToRight)
            VStack(alignment: .leading, spacing: 1) {
                Text(f.description)
                if !f.values.isEmpty {
                    valuesLine(f.values, counts: model.counts[key])
                }
            }
            Spacer()
            Button(f.example) { model.query = f.example; model.showCheatSheet = false }
                .buttonStyle(.link)
                .font(.system(.caption, design: .monospaced))
                .environment(\.layoutDirection, .leftToRight)
        }
        .padding(.vertical, 1)
        .background(RoundedRectangle(cornerRadius: 4).fill(selected ? Color.accentColor.opacity(0.18) : .clear))
        .id(f.key)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("\(f.key), \(f.description)")
        .accessibilityHint(L("cheatsheet.insertHint"))
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    /// `4k · 1,204  8k · 0` — values with no matches are dimmed, not hidden.
    private func valuesLine(_ values: [String], counts: [String: UInt64]?) -> some View {
        HStack(spacing: 8) {
            ForEach(values, id: \.self) { v in
                let n = counts?[v]
                Text(n.map { "\(v) · \($0.formatted())" } ?? v)
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(n == 0 ? Color.secondary.opacity(0.5) : .secondary)
            }
        }
        .environment(\.layoutDirection, .leftToRight)
    }
}
