import AppKit
import LaserdotKit

/// The pattern, the first number and a live preview of the new names, for
/// renaming several files at once.
final class RenamePreview: NSObject, NSTextFieldDelegate {
    let files: [String]
    let pattern = NSTextField(string: "")
    let start = NSTextField(string: "1")
    let list = NSTextField(labelWithString: "")
    let view: NSView

    var startNumber: Int { max(0, Int(start.stringValue) ?? 1) }

    init(files: [String]) {
        self.files = files
        let stack = NSStackView()
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 6
        view = stack
        super.init()
        pattern.placeholderString = L("renameMany.placeholder")
        pattern.stringValue = ((files.first ?? "") as NSString).deletingPathExtension
        pattern.frame.size.width = 320
        start.frame.size.width = 60
        pattern.delegate = self
        start.delegate = self
        let row = NSStackView(views: [NSTextField(labelWithString: L("renameMany.startAt")), start])
        list.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        list.textColor = .secondaryLabelColor
        list.maximumNumberOfLines = 6
        for v in [pattern, row, list] { stack.addArrangedSubview(v) }
        pattern.widthAnchor.constraint(equalToConstant: 320).isActive = true
        start.widthAnchor.constraint(equalToConstant: 60).isActive = true
        stack.frame = NSRect(x: 0, y: 0, width: 340, height: 160)
        update()
    }

    func controlTextDidChange(_ obj: Notification) { update() }

    private func update() {
        let names = BatchRename.names(for: files, pattern: pattern.stringValue, start: startNumber)
        var lines = zip(files, names).prefix(5).map { "\($0) → \($1)" }
        if files.count > 5 { lines.append("…") }
        if let p = BatchRename.problem(names) { lines.append(L("renameMany.problem." + p)) }
        list.stringValue = lines.joined(separator: "\n")
    }
}
