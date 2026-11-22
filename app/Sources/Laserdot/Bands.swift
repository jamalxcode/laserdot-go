import SwiftUI

/// The bands of the search screen (a stripe of the family's colour, its icon,
/// a faint tint), shared by the other windows so Laserdot looks like one app
/// (the owner: "make other windows match new look"). Families and colours are
/// `SearchView.bandColor` / `bandIcon` (Okabe–Ito, colour-blind safe).
struct BandBackground: ViewModifier {
    let family: String
    func body(content: Content) -> some View {
        // The stripe is drawn behind the content, as tall as it (in an HStack
        // a Capsule takes all the height it is offered: Storage's band filled
        // the window).
        content
            .padding(.leading, 16)
            .padding(.trailing, 8)
            .padding(.vertical, 6)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(alignment: .leading) {
                Capsule()
                    .fill(SearchView.bandColor(family))
                    .frame(width: 4)
                    .padding(.vertical, 4)
                    .padding(.leading, 4)
            }
            .background(SearchView.bandColor(family).opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
            .fixedSize(horizontal: false, vertical: true)
    }
}

extension View {
    /// Draw this view as a band of `family` (where, what, file, picture, time, photo, music).
    func band(_ family: String) -> some View { modifier(BandBackground(family: family)) }

    /// The search screen's rounded bar, for a window's controls.
    func capsuleBar() -> some View {
        padding(.horizontal, 10)
            .padding(.vertical, 6)
            .background(Capsule(style: .continuous).fill(.regularMaterial))
            .overlay(Capsule(style: .continuous).strokeBorder(Color.primary.opacity(0.1)))
    }
}

/// A band's title: its icon in the family's colour and the words.
struct BandTitle: View {
    let family: String
    let title: String
    var detail: String?
    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: SearchView.bandIcon(family))
                .foregroundStyle(SearchView.bandColor(family))
                .accessibilityHidden(true)
            Text(title).font(.headline)
            if let detail { Text(detail).foregroundStyle(.secondary) }
            Spacer()
        }
    }
}
