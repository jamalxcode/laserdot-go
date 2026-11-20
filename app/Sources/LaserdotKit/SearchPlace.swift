import Foundation

/// Where searches look unless they say otherwise (the owner chose a
/// "default search place"): everywhere, this Mac only, or one folder. It is
/// added to each search as an ordinary word (`volume:local`, `in:"…"`), so
/// the choice row counts within it and shows it chosen; a search that names
/// its own place (`in:`, `volume:`) is left as it is.
public enum SearchPlace {
    /// UserDefaults key: "" everywhere, "local" this Mac, else a folder path.
    public static let key = "laserdot.searchPlace"

    /// The word a place adds (nil: everywhere).
    public static func token(_ place: String) -> String? {
        switch place {
        case "": return nil
        case "local": return "volume:local"
        default:
            let escaped = place.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"")
            return "in:\"\(escaped)\""
        }
    }

    /// The search sent for `query`: with the place's word, unless it is off
    /// for this search, the query is empty or a command, or names a place.
    public static func effective(_ query: String, place: String, off: Bool) -> String {
        let q = query.trimmingCharacters(in: .whitespaces)
        guard !off, let t = token(place), !q.isEmpty, !q.hasPrefix(">"), !namesPlace(q) else { return query }
        return q + " " + t
    }

    /// The query names a place itself (in:, volume:, or leaves one out).
    public static func namesPlace(_ q: String) -> Bool {
        q.split(separator: " ").contains { w in
            let s = w.lowercased().drop(while: { $0 == "-" })
            return s.hasPrefix("in:") || s.hasPrefix("volume:")
        }
    }

    /// A short name for the place (the folder's name; "" everywhere).
    public static func title(_ place: String, thisMac: String) -> String {
        switch place {
        case "": return ""
        case "local": return thisMac
        default: return (place as NSString).lastPathComponent
        }
    }
}
