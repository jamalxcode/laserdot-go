import LaserdotKit
import SwiftUI

/// A thin bar of months under the choices, taller where there are more
/// results (the owner chose "a timeline strip"): click a month, or drag across
/// several, to see the files from then. January carries its year.
struct TimelineStrip: View {
    @ObservedObject var model: SearchModel
    @State private var dragFrom: Int?
    @State private var dragTo: Int?

    var body: some View {
        let months = model.timeline
        let top = max(1, months.map(\.count).max() ?? 1)
        VStack(alignment: .leading, spacing: 2) {
            GeometryReader { g in
                let w = g.size.width / CGFloat(max(1, months.count))
                HStack(alignment: .bottom, spacing: 0) {
                    ForEach(Array(months.enumerated()), id: \.element.month) { i, m in
                        let picked = inDrag(i)
                        // A bar per month with a gap on each side, so months
                        // read apart (the first screenshot ran them together).
                        RoundedRectangle(cornerRadius: 1.5)
                            .fill(picked ? Color.accentColor : SearchView.bandColor("file").opacity(m.count == 0 ? 0.15 : 0.8))
                            .frame(width: max(1, w * 0.7),
                                   height: max(2, 22 * CGFloat(Double(m.count) / Double(top))))
                            .frame(width: w, alignment: .center)
                            .frame(maxHeight: .infinity, alignment: .bottom)
                            .help("\(Self.label(m.month)): \(SearchView.count(m.count))")
                    }
                }
                .contentShape(Rectangle())
                .gesture(DragGesture(minimumDistance: 0)
                    .onChanged { v in
                        let i = index(at: v.location.x, width: w, count: months.count)
                        if dragFrom == nil { dragFrom = i }
                        dragTo = i
                    }
                    .onEnded { _ in
                        if let a = dragFrom, let b = dragTo, months.indices.contains(a), months.indices.contains(b) {
                            model.chooseMonths(from: months[a].month, to: months[b].month)
                        }
                        dragFrom = nil
                        dragTo = nil
                    })
            }
            .frame(height: 24)
            HStack(spacing: 0) {
                Text(Self.label(months.first?.month ?? ""))
                Spacer()
                Text(L("timeline.hint")).foregroundStyle(.tertiary)
                Spacer()
                Text(Self.label(months.last?.month ?? ""))
            }
            .font(.caption2)
            .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 16)
        .padding(.bottom, 6)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(L("timeline.hint"))
        .accessibilityIdentifier("timeline")
    }

    private func inDrag(_ i: Int) -> Bool {
        guard let a = dragFrom, let b = dragTo else { return false }
        return (min(a, b)...max(a, b)).contains(i)
    }

    private func index(at x: CGFloat, width: CGFloat, count: Int) -> Int {
        max(0, min(count - 1, Int(x / max(1, width))))
    }

    /// "Mar 2024" in the app's language.
    static func label(_ month: String) -> String {
        let p = month.split(separator: "-")
        guard p.count == 2, let y = Int(p[0]), let m = Int(p[1]),
              let d = Calendar(identifier: .gregorian).date(from: DateComponents(year: y, month: m, day: 1)) else { return month }
        let f = DateFormatter()
        f.locale = Locale(identifier: appLang)
        f.setLocalizedDateFormatFromTemplate("MMM yyyy")
        return f.string(from: d)
    }
}
