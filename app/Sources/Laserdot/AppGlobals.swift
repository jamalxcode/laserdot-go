import AppKit
import Carbon.HIToolbox
import SwiftUI

/// The app's language for guidance (hints, filter descriptions): the
/// localization the app's own strings use.
let appLang: String = appResources.preferredLocalizations.first ?? "en"

/// The layout direction of the app's language: right to left in Arabic.
/// Set on every window's root view, so the layout follows the strings
/// shown rather than what AppKit derives from the bundle.
let appLayoutDirection: LayoutDirection =
    Locale.Language(identifier: appLang).characterDirection == .rightToLeft ? .rightToLeft : .leftToRight

/// Global shortcuts the overlay can use (spec §6.4).
struct HotKeyChoice {
    let label: String
    let keyCode: UInt32
    let modifiers: UInt32

    static let all = [
        HotKeyChoice(label: "⌥Space", keyCode: UInt32(kVK_Space), modifiers: UInt32(optionKey)),
        HotKeyChoice(label: "⌃Space", keyCode: UInt32(kVK_Space), modifiers: UInt32(controlKey)),
        HotKeyChoice(label: "⌥⌘Space", keyCode: UInt32(kVK_Space), modifiers: UInt32(optionKey | cmdKey)),
        HotKeyChoice(label: "⌃⌥Space", keyCode: UInt32(kVK_Space), modifiers: UInt32(controlKey | optionKey)),
    ]

    static var saved: HotKeyChoice {
        let label = UserDefaults.standard.string(forKey: "laserdot.hotkey")
        return all.first { $0.label == label } ?? all[0]
    }

    func save() {
        UserDefaults.standard.set(label, forKey: "laserdot.hotkey")
    }
}

/// The app's resources (strings in English and Arabic). In Laserdot.app they
/// live in Contents/Resources, the only place code signing allows; SwiftPM's
/// generated `Bundle.module` looks beside the .app instead and stops the app
/// when it is not there, so it is only the fallback for `swift run`.
let appResources: Bundle = {
    let name = "Laserdot_Laserdot.bundle"
    let places = [Bundle.main.resourceURL, Bundle.main.bundleURL, Bundle.main.executableURL?.deletingLastPathComponent()]
    for case let dir? in places {
        if let b = Bundle(url: dir.appendingPathComponent(name)) { return b }
    }
    return Bundle.module
}()

/// Localized string from the app's resources (English + Arabic, spec §14.4).
func L(_ key: String) -> String {
    NSLocalizedString(key, bundle: appResources, comment: "")
}


/// The menu-bar icon: a laser dot in a ring with four ticks, drawn as a
/// template so it follows the menu bar's light and dark look.
let menuBarIcon: NSImage = {
    let img = NSImage(size: NSSize(width: 18, height: 18), flipped: false) { r in
        NSColor.black.set()
        let ring = NSBezierPath(ovalIn: r.insetBy(dx: 3.5, dy: 3.5))
        ring.lineWidth = 1.5
        ring.stroke()
        NSBezierPath(ovalIn: NSRect(x: 6.75, y: 6.75, width: 4.5, height: 4.5)).fill()
        let ticks = NSBezierPath()
        ticks.lineWidth = 1.5
        for (from, to) in [((9.0, 0.5), (9.0, 3.5)), ((9.0, 14.5), (9.0, 17.5)), ((0.5, 9.0), (3.5, 9.0)), ((14.5, 9.0), (17.5, 9.0))] {
            ticks.move(to: NSPoint(x: from.0, y: from.1))
            ticks.line(to: NSPoint(x: to.0, y: to.1))
        }
        ticks.stroke()
        return true
    }
    img.isTemplate = true
    img.accessibilityDescription = "Laserdot"
    return img
}()

/// Milliseconds since this process started (launch time).
func msSinceProcessStart() -> Int {
    var info = kinfo_proc()
    var size = MemoryLayout<kinfo_proc>.stride
    var mib: [Int32] = [CTL_KERN, KERN_PROC, KERN_PROC_PID, getpid()]
    guard sysctl(&mib, 4, &info, &size, nil, 0) == 0 else { return -1 }
    let t = info.kp_proc.p_un.__p_starttime
    let started = Double(t.tv_sec) + Double(t.tv_usec) / 1e6
    return Int((Date().timeIntervalSince1970 - started) * 1000)
}

/// How long each launch step took, in ms since the process started (Save
/// Diagnostics and the self-test show them; the main thread only).
enum LaunchTimer {
    private(set) static var marks: [(name: String, ms: Int)] = []
    static func mark(_ name: String) {
        guard !marks.contains(where: { $0.name == name }) else { return }
        marks.append((name, msSinceProcessStart()))
    }
    static var summary: String { marks.map { "\($0.name) \($0.ms)" }.joined(separator: ", ") }
}
