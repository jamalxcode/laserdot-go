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
    var onTab: () -> Void
    var onSubmit: () -> Void
    var onEscape: () -> Void

    func makeNSView(context: Context) -> NSTextField {
        let f = NSTextField(string: text)
        f.isBordered = false
        f.isBezeled = false
        f.drawsBackground = false
        f.focusRingType = .none
        f.font = .systemFont(ofSize: 22)
        f.placeholderString = placeholder
        f.usesSingleLineMode = true
        f.cell?.wraps = false
        f.cell?.isScrollable = true
        f.delegate = context.coordinator
        f.setAccessibilityLabel(placeholder)
        f.setContentHuggingPriority(.defaultLow, for: .horizontal)
        DispatchQueue.main.async { f.window?.makeFirstResponder(f) }
        return f
    }

    func updateNSView(_ f: NSTextField, context: Context) {
        context.coordinator.parent = self
        // The model changed the query (history, completion, a chip): show it.
        if f.stringValue != text {
            f.stringValue = text
            if let editor = f.currentEditor() {
                editor.selectedRange = NSRange(location: (text as NSString).length, length: 0)
            }
        }
        if context.coordinator.focusToken != focusToken {
            context.coordinator.focusToken = focusToken
            DispatchQueue.main.async { f.window?.makeFirstResponder(f) }
        }
    }

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    final class Coordinator: NSObject, NSTextFieldDelegate {
        var parent: SearchField
        var focusToken: Int

        init(_ parent: SearchField) {
            self.parent = parent
            focusToken = parent.focusToken
        }

        func controlTextDidChange(_ note: Notification) {
            guard let f = note.object as? NSTextField else { return }
            if parent.text != f.stringValue { parent.text = f.stringValue }
        }

        func control(_ control: NSControl, textView: NSTextView, doCommandBy selector: Selector) -> Bool {
            switch selector {
            case #selector(NSResponder.moveUp(_:)): parent.onMove(-1)
            case #selector(NSResponder.moveDown(_:)): parent.onMove(1)
            case #selector(NSResponder.insertTab(_:)): parent.onTab()
            case #selector(NSResponder.insertNewline(_:)): parent.onSubmit()
            case #selector(NSResponder.cancelOperation(_:)): parent.onEscape()
            default: return false
            }
            return true
        }
    }
}
