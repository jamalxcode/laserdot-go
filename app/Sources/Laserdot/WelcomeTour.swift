import SwiftUI

/// Three pages on first launch (and Help ▸ Welcome Tour): how to search,
/// the keys, and what stays private.
struct WelcomeTour: View {
    var done: () -> Void
    @State private var page = 0

    private struct Page {
        let symbol: String
        let title: String
        let lines: [String]
    }

    private var pages: [Page] {
        [
            Page(symbol: "text.magnifyingglass", title: L("tour.1.title"), lines: [
                L("tour.1.a"), L("tour.1.b"), L("tour.1.c"), L("tour.1.d"),
            ]),
            Page(symbol: "keyboard", title: L("tour.2.title"), lines: [
                L("tour.2.a"), L("tour.2.b"), L("tour.2.c"), L("tour.2.d"),
            ]),
            Page(symbol: "lock.shield", title: L("tour.3.title"), lines: [
                L("tour.3.a"), L("tour.3.b"), L("tour.3.c"),
            ]),
        ]
    }

    var body: some View {
        let p = pages[page]
        VStack(spacing: 18) {
            Image(systemName: p.symbol).font(.system(size: 44)).foregroundStyle(.tint)
            Text(p.title).font(.title2.weight(.semibold))
            VStack(alignment: .leading, spacing: 10) {
                ForEach(p.lines, id: \.self) { line in
                    Label(line, systemImage: "checkmark.circle").labelStyle(.titleAndIcon)
                }
            }
            .frame(maxWidth: 420, alignment: .leading)
            Spacer(minLength: 0)
            HStack {
                // Page dots.
                HStack(spacing: 6) {
                    ForEach(0..<pages.count, id: \.self) { i in
                        Circle().fill(i == page ? Color.accentColor : Color.secondary.opacity(0.3)).frame(width: 7, height: 7)
                    }
                }
                .accessibilityLabel("\(page + 1) / \(pages.count)")
                Spacer()
                if page > 0 {
                    Button(L("tour.back")) { page -= 1 }
                }
                Button(page == pages.count - 1 ? L("tour.start") : L("tour.next")) {
                    if page == pages.count - 1 { done() } else { page += 1 }
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(28)
        .frame(width: 520, height: 400)
    }
}
