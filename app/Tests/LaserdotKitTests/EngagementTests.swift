import Foundation
@testable import LaserdotKit
import XCTest

final class EngagementTests: XCTestCase {
    func testTheTidyReminderComesOnlyWhenChosenAndDue() {
        let now = Date()
        let day: TimeInterval = 86_400
        XCTAssertFalse(ReminderSchedule.tidyDue(interval: .never, lastScan: nil, lastReminder: nil, enabledAt: nil, now: now))
        // Turned on today: not yet.
        XCTAssertFalse(ReminderSchedule.tidyDue(interval: .monthly, lastScan: nil, lastReminder: nil, enabledAt: now, now: now))
        // A month after turning it on, no scan since: due.
        XCTAssertTrue(ReminderSchedule.tidyDue(interval: .monthly, lastScan: nil, lastReminder: nil,
                                               enabledAt: now - 31 * day, now: now))
        // A scan last week resets the clock (no nagging after real use).
        XCTAssertFalse(ReminderSchedule.tidyDue(interval: .monthly, lastScan: now - 7 * day, lastReminder: nil,
                                                enabledAt: now - 90 * day, now: now))
        XCTAssertFalse(ReminderSchedule.tidyDue(interval: .quarterly, lastScan: now - 40 * day, lastReminder: nil,
                                                enabledAt: now - 200 * day, now: now))
    }

    func testTheLowDiskWarningIsRareAndOnlyWhenNearlyFull() {
        let gb: Int64 = 1 << 30
        let now = Date()
        XCTAssertFalse(ReminderSchedule.lowDiskDue(free: 300 * gb, total: 1000 * gb, lastWarned: nil, now: now))
        XCTAssertTrue(ReminderSchedule.lowDiskDue(free: 8 * gb, total: 1000 * gb, lastWarned: nil, now: now))
        XCTAssertFalse(ReminderSchedule.lowDiskDue(free: 8 * gb, total: 1000 * gb, lastWarned: now - 86_400, now: now))
        XCTAssertTrue(ReminderSchedule.lowDiskDue(free: 8 * gb, total: 1000 * gb, lastWarned: now - 8 * 86_400, now: now))
        // A small disk: 10% is the limit.
        XCTAssertTrue(ReminderSchedule.lowDiskDue(free: 9 * gb, total: 128 * gb, lastWarned: nil, now: now))
        XCTAssertFalse(ReminderSchedule.lowDiskDue(free: 20 * gb, total: 128 * gb, lastWarned: nil, now: now))
    }

    func testTipsSpeakOnlyOfFilesThisMacHasAndVary() {
        let counts = [ValueCountInfo(key: "res", value: "4k", count: 812),
                      ValueCountInfo(key: "kind", value: "video", count: 4210),
                      ValueCountInfo(key: "atmos", value: "true", count: 0)]
        let all = Tips.available(counts: counts)
        XCTAssertEqual(all.map(\.id), ["4k", "bigvideos"])
        XCTAssertEqual(all.first?.count, 812)
        let first = Tips.pick(counts: counts, after: nil)
        let second = Tips.pick(counts: counts, after: first?.id)
        XCTAssertNotEqual(first, second, "the next tip is a different one")
        XCTAssertNil(Tips.pick(counts: [], after: nil), "no files, no tip")
        // Every offered query uses the query language's filters.
        for (_, _, _, q) in Tips.catalog { XCTAssertTrue(q.contains(":"), q) }
    }
}
