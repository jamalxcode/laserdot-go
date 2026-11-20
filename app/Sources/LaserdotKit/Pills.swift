import Foundation

/// The search field shows filters as pills (the owner's review: "more easy
/// and natural"): `ext:pdf` is a "PDF" pill with ✕, `-ext:pdf` is "not PDF",
/// `in:~/Work` is "in Work". The query stays the one source of truth: pills
/// are its filter words, the field shows the rest, and typing a filter
/// followed by a space turns it into a pill. Filters can still be typed.
public enum Pills {
    /// Every filter key (and its display form, a.count) from the registry.
    static let keys: Set<String> = {
        var s = Set<String>()
        for f in Registry.shared.filters {
            s.insert(f.key.lowercased())
            s.insert(f.display_key.lowercased())
        }
        return s
    }()

    /// The query's words, split at spaces outside quotes and parentheses.
    public static func tokens(_ q: String) -> [String] {
        var out: [String] = []
        var cur = ""
        var inQuote = false
        var depth = 0
        for ch in q {
            switch ch {
            case "\"": inQuote.toggle()
            case "(" where !inQuote: depth += 1
            case ")" where !inQuote && depth > 0: depth -= 1
            case " ", "\t":
                if !inQuote && depth == 0 {
                    if !cur.isEmpty { out.append(cur); cur = "" }
                    continue
                }
            default: break
            }
            cur.append(ch)
        }
        if !cur.isEmpty { out.append(cur) }
        return out
    }

    /// A filter word with a value (`ext:pdf`, `-in:~/Work`, `a.count:0`).
    public static func isPill(_ token: String) -> Bool {
        var t = Substring(token)
        while t.hasPrefix("-") { t = t.dropFirst() }
        guard let colon = t.firstIndex(of: ":"), colon > t.startIndex else { return false }
        let key = t[..<colon].lowercased()
        guard t.index(after: colon) < t.endIndex else { return false } // "ext:" is still being typed
        if keys.contains(key) { return true }
        // A track's filter: v.codec, a.lang, s.forced.
        if key.count > 2, ["v.", "a.", "s."].contains(String(key.prefix(2))), keys.contains(String(key.dropFirst(2))) {
            return true
        }
        return false
    }

    /// The query as pills and the text the field shows. `typing`: the user
    /// is typing the last word (it stays text until a space follows).
    public static func split(_ q: String, typing: Bool) -> (pills: [String], text: String) {
        let toks = tokens(q)
        let endsInSpace = q.last == " " || q.last == "\t"
        var pills: [String] = []
        var words: [String] = []
        for (i, t) in toks.enumerated() {
            let last = i == toks.count - 1
            if isPill(t) && !(last && typing && !endsInSpace) {
                pills.append(t)
            } else {
                words.append(t)
            }
        }
        // No filter: the text exactly as typed (spaces and all).
        if pills.isEmpty { return ([], q) }
        var text = words.joined(separator: " ")
        if endsInSpace && !text.isEmpty { text += " " }
        return (pills, text)
    }

    /// The query from pills and the field's text (pills first: typing goes
    /// on at the end).
    public static func join(_ pills: [String], _ text: String) -> String {
        if pills.isEmpty { return text }
        let t = text.drop(while: { $0 == " " })
        return pills.joined(separator: " ") + (t.isEmpty ? "" : " " + t)
    }

    /// The query without one pill.
    public static func removing(_ pill: String, from q: String) -> String {
        var toks = tokens(q)
        if let i = toks.firstIndex(of: pill) { toks.remove(at: i) }
        return toks.joined(separator: " ")
    }

    /// Words for a pill, readable without knowing the query language
    /// ("PDF", "not PDF", "in Work", "4K", "since 7 days"). `words` gives
    /// the app's own phrases: "not", "in", "on", "thisMac", and "kind.video"…
    public static func label(_ token: String, words: (String) -> String) -> String {
        var t = token
        var negated = false
        while t.hasPrefix("-") { t.removeFirst(); negated = true }
        guard let colon = t.firstIndex(of: ":") else { return token }
        let key = t[..<colon].lowercased()
        var value = String(t[t.index(after: colon)...])
        if value.hasPrefix("\""), value.hasSuffix("\""), value.count >= 2 { value = String(value.dropFirst().dropLast()) }
        let text: String
        switch key {
        case "ext": text = value.uppercased().replacingOccurrences(of: ",", with: ", ")
        case "kind": text = value.split(separator: ",").map { words("kind." + $0) }.joined(separator: ", ")
        case "in":
            let name = (value as NSString).lastPathComponent
            text = String(format: words("in"), name == "~" || name.isEmpty ? value : name)
        case "volume": text = value == "local" ? words("thisMac") : String(format: words("on"), value)
        case "res": text = value.lowercased().hasSuffix("k") || value.lowercased() == "sd" ? value.uppercased() : value
        default:
            let named = words("filter." + key)
            text = named.isEmpty ? "\(key) \(value)" : "\(named) \(value)"
        }
        return negated ? String(format: words("not"), text) : text
    }
}
