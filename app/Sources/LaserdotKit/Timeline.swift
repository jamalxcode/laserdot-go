import Foundation

/// How many of a search's results last changed in one month ("2024-03").
public struct MonthCount: Codable, Equatable, Hashable, Sendable {
    public let month: String
    public let count: UInt64

    public init(month: String, count: UInt64) {
        self.month = month
        self.count = count
    }
}

extension Engine {
    /// The results of `query` by month, oldest first (empty when the daemon
    /// cannot say).
    public func timeline(query: String) -> [MonthCount] {
        guard let answer = guide(.timeline(query: query), as: [MonthCount].self) else { return [] }
        return answer ?? []
    }
}

/// The timeline strip (the owner chose "a thin bar of months … click or drag
/// across it"): every month from the first to the last result, empty ones
/// included, at most the last `limit` months; and the filter a click or a drag
/// adds, replacing any date filter already there.
public enum TimelineLogic {
    public static func months(_ counts: [MonthCount], limit: Int = 120) -> [MonthCount] {
        guard let first = counts.first, let last = counts.last,
              let start = parse(first.month), let end = parse(last.month) else { return [] }
        var (y, m) = start
        let (ly, lm) = end
        let byMonth = Dictionary(counts.map { ($0.month, $0.count) }, uniquingKeysWith: +)
        var out: [MonthCount] = []
        while y < ly || (y == ly && m <= lm) {
            let key = String(format: "%04d-%02d", y, m)
            out.append(MonthCount(month: key, count: byMonth[key] ?? 0))
            m += 1
            if m > 12 { m = 1; y += 1 }
            if out.count > 2400 { break }
        }
        return Array(out.suffix(limit))
    }

    /// "modified:2024-03" for one month, "modified:2024-03..2024-05" for a span.
    public static func token(from a: String, to b: String) -> String {
        let (lo, hi) = a <= b ? (a, b) : (b, a)
        return lo == hi ? "modified:\(lo)" : "modified:\(lo)..\(hi)"
    }

    /// The query with `token` in place of any date filter it had.
    public static func applying(_ token: String, to query: String) -> String {
        let dateKeys = ["modified:", "since:", "before:", "-modified:"]
        let kept = Pills.tokens(query).filter { t in !dateKeys.contains { t.lowercased().hasPrefix($0) } }
        return ([token] + kept).joined(separator: " ") + (query.hasSuffix(" ") || kept.isEmpty ? " " : "")
    }

    static func parse(_ s: String) -> (Int, Int)? {
        let p = s.split(separator: "-")
        guard p.count == 2, let y = Int(p[0]), let m = Int(p[1]), (1...12).contains(m) else { return nil }
        return (y, m)
    }
}
