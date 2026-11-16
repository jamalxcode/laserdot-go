import Foundation

/// When an optional reminder is due. Laserdot's recurring jobs are finding
/// files (on demand, no reminder needed) and keeping the disk tidy: the
/// reminders are only about tidying, off unless the user turns them on, and
/// never more often than they chose.
public enum TidyInterval: String, CaseIterable, Sendable {
    case never, monthly, quarterly

    public var days: Int? {
        switch self {
        case .never: return nil
        case .monthly: return 30
        case .quarterly: return 91
        }
    }
}

public enum ReminderSchedule {
    /// The duplicates reminder is due when the chosen time has passed since
    /// the later of: the last scan, the last reminder, turning it on.
    public static func tidyDue(interval: TidyInterval, lastScan: Date?, lastReminder: Date?, enabledAt: Date?,
                               now: Date = Date()) -> Bool {
        guard let days = interval.days else { return false }
        let since = [lastScan, lastReminder, enabledAt].compactMap { $0 }.max() ?? .distantPast
        return now.timeIntervalSince(since) >= Double(days) * 86_400
    }

    /// Nearly full: under 10% free or under 10 GB, whichever is less; told at
    /// most once a week.
    public static func lowDiskDue(free: Int64, total: Int64, lastWarned: Date?, now: Date = Date()) -> Bool {
        guard total > 0, free >= 0 else { return false }
        let limit = min(total / 10, 10 << 30)
        guard free < limit else { return false }
        return lastWarned.map { now.timeIntervalSince($0) >= 7 * 86_400 } ?? true
    }
}

/// A suggestion for the empty search box, drawn from the user's own files:
/// what they could find that they may not know how to ask for.
public struct Tip: Equatable, Sendable {
    /// Names the sentence (`tip.<id>` in the app's strings).
    public let id: String
    /// What a click types into the box (a valid query).
    public let query: String
    /// How many of the user's files the sentence speaks of.
    public let count: UInt64
}

public enum Tips {
    /// (id, filter key, value counted, query offered)
    static let catalog: [(String, String, String, String)] = [
        ("4k", "res", "4k", "res:4k"),
        ("hdr", "hdr", "true", "hdr:true"),
        ("portrait", "orientation", "portrait", "kind:video orientation:portrait"),
        ("lossless", "lossless", "true", "lossless:true"),
        ("atmos", "atmos", "true", "atmos:true"),
        ("surround", "channels", "5.1", "channels:5.1"),
        ("bigvideos", "kind", "video", "kind:video fsize:>2gb"),
        ("recentphotos", "kind", "image", "kind:image since:30d"),
        ("music", "kind", "audio", "kind:audio since:90d"),
        ("documents", "kind", "document", "kind:document since:7d"),
    ]

    /// Every tip that speaks of files this Mac has.
    public static func available(counts: [ValueCountInfo]) -> [Tip] {
        catalog.compactMap { id, key, value, query in
            let n = counts.first { $0.key == key && $0.value == value }?.count ?? 0
            return n > 0 ? Tip(id: id, query: query, count: n) : nil
        }
    }

    /// One tip, a different one from the last time when there is a choice.
    public static func pick(counts: [ValueCountInfo], after last: String?) -> Tip? {
        let all = available(counts: counts)
        guard !all.isEmpty else { return nil }
        let i = (all.firstIndex { $0.id == last }.map { $0 + 1 } ?? 0) % all.count
        return all[i]
    }
}
