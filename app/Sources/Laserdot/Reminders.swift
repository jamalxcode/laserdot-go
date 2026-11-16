import AppKit
import LaserdotKit
import UserNotifications

/// Optional reminders, as macOS notifications made on this Mac (nothing is
/// sent anywhere). Both are off until the user turns them on, in Settings or
/// when Laserdot offers it right after a cleanup:
/// - check for duplicates every month or every 3 months (counted from the
///   last scan, so using the feature never earns a reminder);
/// - tell me when the disk is nearly full (at most once a week).
/// A click opens the window that helps.
final class Reminders: NSObject, UNUserNotificationCenterDelegate {
    static let shared = Reminders()

    static let tidyKey = "laserdot.reminder.tidy"
    static let tidyEnabledKey = "laserdot.reminder.tidyEnabledAt"
    static let lastTidyKey = "laserdot.reminder.lastTidy"
    static let lastScanKey = "laserdot.dupes.lastScan"
    static let lowDiskKey = "laserdot.reminder.lowDisk"
    static let lastLowDiskKey = "laserdot.reminder.lastLowDisk"
    static let offeredKey = "laserdot.reminder.offered"

    /// Where a click leads.
    var openDuplicates: () -> Void = {}
    var openStorage: () -> Void = {}

    private var timer: Timer?
    private let d = UserDefaults.standard

    var tidy: TidyInterval {
        get { TidyInterval(rawValue: d.string(forKey: Self.tidyKey) ?? "") ?? .never }
        set {
            d.set(newValue.rawValue, forKey: Self.tidyKey)
            d.set(Date(), forKey: Self.tidyEnabledKey)
            if newValue != .never { askPermission() }
        }
    }

    var lowDisk: Bool {
        get { d.bool(forKey: Self.lowDiskKey) }
        set {
            d.set(newValue, forKey: Self.lowDiskKey)
            if newValue { askPermission() }
        }
    }

    /// After a cleanup, Laserdot offers the monthly reminder once.
    var shouldOffer: Bool { tidy == .never && !d.bool(forKey: Self.offeredKey) }
    func offered() { d.set(true, forKey: Self.offeredKey) }

    /// A duplicate scan ran: the reminder counts from now.
    static func noteScan() { UserDefaults.standard.set(Date(), forKey: lastScanKey) }

    /// Notifications need a real app bundle (not the test runner).
    private var center: UNUserNotificationCenter? {
        Bundle.main.bundleIdentifier == nil ? nil : UNUserNotificationCenter.current()
    }

    func start() {
        center?.delegate = self
        timer?.invalidate()
        timer = Timer.scheduledTimer(withTimeInterval: 3600, repeats: true) { [weak self] _ in self?.check() }
        DispatchQueue.main.asyncAfter(deadline: .now() + 120) { [weak self] in self?.check() }
    }

    /// Asked only when the user turns a reminder on, never at launch.
    private func askPermission() {
        center?.requestAuthorization(options: [.alert, .sound]) { _, _ in }
    }

    func check(now: Date = Date()) {
        if ReminderSchedule.tidyDue(interval: tidy, lastScan: d.object(forKey: Self.lastScanKey) as? Date,
                                    lastReminder: d.object(forKey: Self.lastTidyKey) as? Date,
                                    enabledAt: d.object(forKey: Self.tidyEnabledKey) as? Date, now: now) {
            d.set(now, forKey: Self.lastTidyKey)
            post(id: "tidy", title: L("reminder.tidy.title"), body: L("reminder.tidy.body"))
        }
        if lowDisk, let (free, total) = Self.diskSpace(),
           ReminderSchedule.lowDiskDue(free: free, total: total, lastWarned: d.object(forKey: Self.lastLowDiskKey) as? Date, now: now) {
            d.set(now, forKey: Self.lastLowDiskKey)
            post(id: "lowdisk", title: L("reminder.lowDisk.title"),
                 body: String(format: L("reminder.lowDisk.body"), ByteCountFormatter.string(fromByteCount: free, countStyle: .file)))
        }
    }

    /// Free (for important use) and total bytes of the startup disk.
    static func diskSpace() -> (Int64, Int64)? {
        let keys: Set<URLResourceKey> = [.volumeAvailableCapacityForImportantUsageKey, .volumeTotalCapacityKey]
        guard let v = try? URL(fileURLWithPath: "/").resourceValues(forKeys: keys),
              let free = v.volumeAvailableCapacityForImportantUsage, let total = v.volumeTotalCapacity else { return nil }
        return (free, Int64(total))
    }

    private func post(id: String, title: String, body: String) {
        guard let center else { return }
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.userInfo = ["open": id]
        center.add(UNNotificationRequest(identifier: "laserdot.\(id)", content: content, trigger: nil))
    }

    // A click on the notification opens the window that helps.
    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                withCompletionHandler done: @escaping () -> Void) {
        let open = response.notification.request.content.userInfo["open"] as? String
        DispatchQueue.main.async {
            NSApp.activate(ignoringOtherApps: true)
            if open == "lowdisk" { self.openStorage() } else { self.openDuplicates() }
        }
        done()
    }

    // Shown even while Laserdot is the active app.
    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                withCompletionHandler done: @escaping (UNNotificationPresentationOptions) -> Void) {
        done([.banner])
    }
}
