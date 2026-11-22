import AppKit
import Foundation
import LaserdotKit

// MARK: text helpers

/// "~ › Documents › AI Code": the folder a result is in, short. Long paths
/// keep their last three folders; drives start with the drive's name.
func shortPath(_ path: String) -> String {
    let dir = (path as NSString).deletingLastPathComponent
    let home = FileManager.default.homeDirectoryForCurrentUser.path
    let parts = PathText.parts(dir, home: home) { root in
        switch root {
        case .home: return L("path.home")
        case .iCloud: return L("path.icloud")
        case .temporary: return L("path.temporary")
        case let .drive(name): return name
        case .computer: return L("path.computer")
        case let .iCloudApp(name, container):
            // Finder's own name for it ("Ulysses"), else the guess.
            let shown = FileManager.default.displayName(atPath: container)
            let app = shown.contains("~") || shown.isEmpty ? name : shown
            return L("path.icloud") + (appLayoutDirection == .rightToLeft ? " / " : " › ") + app
        }
    }
    // › is a mirrored character: right to left it turns into ‹, so Arabic
    // uses a plain slash.
    let separator = appLayoutDirection == .rightToLeft ? " / " : " › "
    // Paths read left to right in every language: isolate them, or the
    // Arabic layout mirrors › into ‹.
    return "\u{2066}" + parts.joined(separator: separator) + "\u{2069}"
}

private let relativeFormatter: RelativeDateTimeFormatter = {
    let f = RelativeDateTimeFormatter()
    f.locale = Locale(identifier: appLang)
    f.unitsStyle = .full
    return f
}()

private let dayFormatter: DateFormatter = {
    let f = DateFormatter()
    f.locale = Locale(identifier: appLang)
    f.doesRelativeDateFormatting = true // "Today", "Yesterday"
    f.dateStyle = .medium
    f.timeStyle = .short
    return f
}()

private let dateOnlyFormatter: DateFormatter = {
    let f = DateFormatter()
    f.locale = Locale(identifier: appLang)
    f.dateStyle = .medium
    f.timeStyle = .none
    return f
}()

/// "5 minutes ago", "Today at 9:04", "Yesterday at 18:20", "3 days ago",
/// then the date.
func friendlyDate(_ unix: Int64, long: Bool = false) -> String {
    let d = Date(timeIntervalSince1970: TimeInterval(unix))
    let age = Date().timeIntervalSince(d)
    if age >= 0 && age < 3600 { return relativeFormatter.localizedString(for: d, relativeTo: Date()) }
    if Calendar.current.isDateInToday(d) || Calendar.current.isDateInYesterday(d) { return dayFormatter.string(from: d) }
    if age >= 0 && age < 7 * 86400 { return relativeFormatter.localizedString(for: d, relativeTo: Date()) }
    return long ? dayFormatter.string(from: d) : dateOnlyFormatter.string(from: d)
}

/// The name with each query word in bold where it occurs (case and accents
/// ignored), so it is clear why a result matched.
func highlighted(_ name: String, _ words: [String]) -> AttributedString {
    var attr = AttributedString(name)
    for w in words where !w.isEmpty {
        var from = name.startIndex
        while from < name.endIndex,
              let r = name.range(of: w, options: [.caseInsensitive, .diacriticInsensitive], range: from..<name.endIndex) {
            if let ar = Range(r, in: attr) {
                attr[ar].inlinePresentationIntent = .stronglyEmphasized
            }
            from = r.upperBound
        }
    }
    return attr
}
