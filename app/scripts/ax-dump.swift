// Prints a running app's accessibility tree, as VoiceOver sees it:
//   swift app/scripts/ax-dump.swift <pid>
// Used by CI while the self-test holds the ⌥Space window open. Needs the
// Accessibility permission for the process running it; without it, it says
// so and exits 0 (the check is then skipped).
import ApplicationServices
import Foundation

guard CommandLine.arguments.count > 1, let pid = pid_t(CommandLine.arguments[1]) else {
    print("usage: ax-dump.swift <pid>")
    exit(2)
}
guard AXIsProcessTrusted() else {
    print("untrusted: this process may not use the accessibility API")
    exit(0)
}

func attr(_ e: AXUIElement, _ name: String) -> AnyObject? {
    var v: AnyObject?
    return AXUIElementCopyAttributeValue(e, name as CFString, &v) == .success ? v : nil
}

func walk(_ e: AXUIElement, _ depth: Int) {
    guard depth < 60 else { return }
    var parts = [(attr(e, kAXRoleAttribute as String) as? String) ?? "?"]
    for a in [kAXTitleAttribute, kAXDescriptionAttribute, kAXValueAttribute] {
        if let s = attr(e, a as String) as? String, !s.isEmpty { parts.append(s) }
    }
    print(String(repeating: "  ", count: depth) + parts.joined(separator: " | "))
    if let kids = attr(e, kAXChildrenAttribute as String) as? [AXUIElement] {
        for k in kids { walk(k, depth + 1) }
    }
}

walk(AXUIElementCreateApplication(pid), 0)
