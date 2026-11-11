import AppKit
import IOKit.ps

/// Background work (reading file contents, Tier 2 media details) pauses on
/// battery (Settings ▸ General, on by default), when the Mac is hot, and in
/// Low Power Mode, and resumes when that ends (spec §12.3–12.5). Searching and
/// keeping the index current never pause.
final class PowerWatcher {
    static let batteryKey = "laserdot.pauseOnBattery"
    private let onChange: (Bool) -> Void
    private var last: Bool?
    private var timer: Timer?
    private var observers: [NSObjectProtocol] = []

    init(onChange: @escaping (Bool) -> Void) {
        self.onChange = onChange
        let nc = NotificationCenter.default
        for name in [ProcessInfo.thermalStateDidChangeNotification, Notification.Name.NSProcessInfoPowerStateDidChange] {
            observers.append(nc.addObserver(forName: name, object: nil, queue: .main) { [weak self] _ in self?.check() })
        }
        // Plugging in or out has no notification here; a minute is soon enough.
        timer = Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { [weak self] _ in self?.check() }
        check()
    }

    deinit {
        timer?.invalidate()
        observers.forEach { NotificationCenter.default.removeObserver($0) }
    }

    func check() {
        let pause = Self.shouldPause
        guard pause != last else { return }
        last = pause
        onChange(pause)
    }

    static var onBattery: Bool {
        guard let info = IOPSCopyPowerSourcesInfo()?.takeRetainedValue(),
              let type = IOPSGetProvidingPowerSourceType(info)?.takeUnretainedValue() as String? else { return false }
        return type == "Battery Power"
    }

    static var shouldPause: Bool {
        let p = ProcessInfo.processInfo
        if p.isLowPowerModeEnabled || p.thermalState == .serious || p.thermalState == .critical { return true }
        let onBatteryPauses = UserDefaults.standard.object(forKey: batteryKey) as? Bool ?? true
        return onBatteryPauses && onBattery
    }
}
