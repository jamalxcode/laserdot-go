import AppKit
import SwiftUI

/// The query field: an AppKit NSTextField. SwiftUI's TextField is unreliable
/// inside the overlay's non-activating panel (it passed on the first
/// keystroke only, so the hint and results stayed those of the first letter);
/// an NSTextField reports every change through its delegate, as
/// Spotlight-style panels do. Arrow keys, Tab, Return and Escape arrive as
/// the field editor's standard commands.
struct SearchField: NSViewRepresentable {
    @Binding var text: String
    var placeholder: String
    /// Changing it gives the field the keyboard (window shown, ⌘F).
    var focusToken: Int
    var onMove: (Int) -> Void
    /// ⇧↑/⇧↓: grow the selection.
    var onExtend: (Int) -> Void = { _ in }
    /// Space: true if it was used (Quick Look) instead of typed.
    var onSpace: () -> Bool = { false }
    var onTab: () -> Void
    var onSubmit: () -> Void
    var onEscape: () -> Void
    /// As in Spotlight: the rest of the top hit's name, shown selected after
    /// the caret (typing replaces it, Delete removes it, → or Tab takes it).
    var inlineSuffix: String? = nil
    /// Changing it selects the whole text (the overlay reopened: typing
    /// replaces the last search, as in Spotlight).
    var selectAllToken: Int = 0
    /// ⌘↑/⌘↓: the previous or next section of results.
    var onJump: (Int) -> Void = { _ in }
    /// ⌘C with no text selected: copy the selected file(s); true if done.
    var onCopy: () -> Bool = { false }

    func makeNSView(context: Context) -> NSTextField {
        let f = NSTextField(string: text)
        f.isBordered = false
        f.isBezeled = false
        f.drawsBackground = false
        f.focusRingType = .none
        f.font = .systemFont(ofSize: 22)
        f.placeholderString = placeholder
        f.usesSingleLineMode = true
        // AppKit does not know the app runs right to left (see appLayoutDirection).
        f.alignment = appLayoutDirection == .rightToLeft ? .right : .left
        f.baseWritingDirection = .natural
        f.cell?.wraps = false
        f.cell?.isScrollable = true
        f.delegate = context.coordinator
        f.setAccessibilityLabel(placeholder)
        f.setContentHuggingPriority(.defaultLow, for: .horizontal)
        DispatchQueue.main.async { f.window?.makeFirstResponder(f) }
        // Space reaches the field as text, not as a command: catch it before
        // the field does, when it is meant for Quick Look.
        let coordinator = context.coordinator
        coordinator.monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak f, weak coordinator] e in
            guard let f, let coordinator, e.window === f.window, let editor = f.currentEditor() else { return e }
            let mods = e.modifierFlags.intersection([.command, .option, .control, .shift])
            // ⌘C with no text selected copies the file, as in Spotlight.
            if mods == .command, e.charactersIgnoringModifiers == "c",
               editor.selectedRange.length == 0 || coordinator.shownSuffix != nil {
                return coordinator.parent.onCopy() ? nil : e
            }
            guard e.keyCode == 49, mods.isEmpty else { return e }
            return coordinator.parent.onSpace() ? nil : e
        }
        return f
    }

    static func dismantleNSView(_ f: NSTextField, coordinator: Coordinator) {
        if let m = coordinator.monitor { NSEvent.removeMonitor(m) }
        coordinator.monitor = nil
    }

    func updateNSView(_ f: NSTextField, context: Context) {
        let c = context.coordinator
        c.parent = self
        let typed = text as NSString
        // The model changed the query (history, completion, a chip): show it.
        if f.stringValue != text && !(c.shownSuffix != nil && f.stringValue == text + (c.shownSuffix ?? "")) {
            f.stringValue = text
            c.shownSuffix = nil
            if let editor = f.currentEditor() {
                editor.selectedRange = NSRange(location: typed.length, length: 0)
            }
        }
        c.showInline(in: f, typed: text, suffix: inlineSuffix)
        if c.focusToken != focusToken {
            c.focusToken = focusToken
            DispatchQueue.main.async { f.window?.makeFirstResponder(f) }
        }
        if c.selectAllToken != selectAllToken {
            c.selectAllToken = selectAllToken
            DispatchQueue.main.async {
                f.window?.makeFirstResponder(f)
                f.currentEditor()?.selectAll(nil)
            }
        }
    }

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    final class Coordinator: NSObject, NSTextFieldDelegate {
        var parent: SearchField
        var focusToken: Int
        var selectAllToken: Int
        var monitor: Any?
        /// The inline completion now shown after the typed text.
        var shownSuffix: String?
        /// The user deleted the completion (or a letter): none until the
        /// next letter typed, as in Spotlight.
        var suppressInline = false

        init(_ parent: SearchField) {
            self.parent = parent
            focusToken = parent.focusToken
            selectAllToken = parent.selectAllToken
        }

        /// Show (or take away) the completion after the caret, selected.
        func showInline(in f: NSTextField, typed: String, suffix: String?) {
            guard let editor = f.currentEditor() else { return }
            let len = (typed as NSString).length
            let want = suppressInline ? nil : suffix.flatMap { $0.isEmpty ? nil : $0 }
            if want == shownSuffix { return }
            if let s = want {
                // Only with the caret at the end of what was typed.
                let r = editor.selectedRange
                let atEnd = r.location == len && (r.length == 0 || shownSuffix != nil)
                guard atEnd else { return }
                f.stringValue = typed + s
                editor.selectedRange = NSRange(location: len, length: (s as NSString).length)
                shownSuffix = s
            } else if shownSuffix != nil {
                f.stringValue = typed
                editor.selectedRange = NSRange(location: len, length: 0)
                shownSuffix = nil
            }
        }

        /// → or Tab with a completion shown: it becomes the query.
        func acceptInline(_ f: NSTextField) -> Bool {
            guard let s = shownSuffix else { return false }
            shownSuffix = nil
            parent.text += s
            f.stringValue = parent.text
            f.currentEditor()?.selectedRange = NSRange(location: (parent.text as NSString).length, length: 0)
            return true
        }

        func controlTextDidChange(_ note: Notification) {
            guard let f = note.object as? NSTextField else { return }
            let now = f.stringValue
            if let s = shownSuffix, now == parent.text + s { return }
            if shownSuffix != nil && now == parent.text {
                // Delete took the completion away: keep it away.
                shownSuffix = nil
                suppressInline = true
                return
            }
            // Typing the next letter of the completion keeps the rest shown
            // (no flicker while the new results come).
            if let s = shownSuffix, let first = s.first, now.count == parent.text.count + 1,
               now.hasPrefix(parent.text), String(now.last!).lowercased() == String(first).lowercased() {
                let rest = String(s.dropFirst())
                parent.text = now
                shownSuffix = nil
                if !rest.isEmpty, let editor = f.currentEditor() {
                    f.stringValue = now + rest
                    editor.selectedRange = NSRange(location: (now as NSString).length, length: (rest as NSString).length)
                    shownSuffix = rest
                }
                suppressInline = false
                return
            }
            shownSuffix = nil
            // Letters taken off the end: a deletion, no completion until the
            // next letter (typing over a selection is not one).
            suppressInline = now.count < parent.text.count && parent.text.hasPrefix(now)
            if parent.text != now { parent.text = now }
        }

        func control(_ control: NSControl, textView: NSTextView, doCommandBy selector: Selector) -> Bool {
            switch selector {
            case #selector(NSResponder.moveUp(_:)): parent.onMove(-1)
            case #selector(NSResponder.moveDown(_:)): parent.onMove(1)
            case #selector(NSResponder.moveUpAndModifySelection(_:)): parent.onExtend(-1)
            case #selector(NSResponder.moveDownAndModifySelection(_:)): parent.onExtend(1)
            case #selector(NSResponder.moveRight(_:)):
                guard let f = control as? NSTextField, acceptInline(f) else { return false }
            case #selector(NSResponder.moveToEndOfDocument(_:)): parent.onJump(1)
            case #selector(NSResponder.moveToBeginningOfDocument(_:)): parent.onJump(-1)
            case #selector(NSResponder.insertTab(_:)):
                if let f = control as? NSTextField, acceptInline(f) { return true }
                parent.onTab()
            case #selector(NSResponder.insertNewline(_:)): parent.onSubmit()
            case #selector(NSResponder.cancelOperation(_:)):
                shownSuffix = nil
                parent.onEscape()
            default: return false
            }
            return true
        }
    }
}
