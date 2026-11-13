import AVKit
import AppKit
import LaserdotKit
import PDFKit
import SwiftUI
import UniformTypeIdentifiers

/// The search UI, in the ⌥Space overlay and in the main window: the query
/// field, a hint only when it helps, suggestion chips, the results in
/// sections (or the empty box's recent files, or `>` commands), a preview
/// pane (or the ⌘K actions), and a footer with the keys to press.
struct SearchView: View {
    @ObservedObject var model: SearchModel
    var close: () -> Void
    /// The hot-key overlay has a fixed size; the main window resizes.
    var fixedSize = true
    @Environment(\.controlActiveState) private var activeState
    /// The selected row uses the accent colour while this window has the
    /// keyboard (the overlay always does), and grey otherwise — as lists do.
    var emphasized: Bool { fixedSize || activeState == .key }

    /// The overlay's slim bar: just the field.
    static let compactHeight: CGFloat = 58
    static let fullHeight: CGFloat = 520

    var body: some View {
        VStack(spacing: 0) {
            header
            if !(fixedSize && model.compact) {
                hintLine
                chips
                Divider()
                Group {
                    if model.showCheatSheet {
                        CheatSheetView(model: model)
                    } else {
                        HStack(spacing: 0) {
                            mainList
                            if model.showActions {
                                Divider()
                                ActionsPane(model: model).frame(width: fixedSize ? 280 : 300)
                            } else if let hit = model.selected, !model.results.isEmpty, !model.isCommandMode,
                                      model.viewMode != .columns {
                                Divider()
                                PreviewPane(hit: hit, model: model).frame(width: fixedSize ? 280 : 300)
                            }
                        }
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                if model.undoBannerVisible, let step = model.undoStep {
                    UndoBanner(step: step, model: model)
                }
                // The main window shows status and keys in its own status bar.
                if fixedSize {
                    Divider()
                    footer
                }
            }
        }
        // The overlay fills its panel (resizable, remembered); the slim bar
        // is just the field.
        .frame(maxWidth: .infinity, maxHeight: fixedSize && model.compact ? Self.compactHeight : .infinity, alignment: .top)
        .background {
            // The floating card is the overlay's look; the main window keeps
            // the standard window background.
            if fixedSize {
                RoundedRectangle(cornerRadius: 14, style: .continuous).fill(.regularMaterial)
            }
        }
        .overlay(hiddenShortcuts)
        // Drop a folder (or a file: its folder) to search inside it.
        .onDrop(of: [.fileURL], isTargeted: nil) { providers in model.handleDrop(providers) }
    }

    // MARK: footer and keys

    var footer: some View {
        HStack(spacing: 12) {
            StatusSummary(model: model)
            Spacer()
            ShortcutBar(model: model)
            Spacer()
            NetworkLock(blocked: model.networkBlocked)
        }
        .font(.caption)
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
    }

    /// Keys without a visible button: ⌘↩ reveal, ⌘⇧C copy path, ⌘Y Quick
    /// Look, ⌘K actions, ⌘1–⌘9 open (spec §5.1).
    var hiddenShortcuts: some View {
        ZStack {
            Button("") { model.reveal() }.keyboardShortcut(.return, modifiers: .command)
            Button("") { model.copyPath() }.keyboardShortcut("c", modifiers: [.command, .shift])
            Button("") { model.quickLook() }.keyboardShortcut("y", modifiers: .command)
            Button("") { model.toggleActions() }.keyboardShortcut("k", modifiers: .command)
            Button("") { model.viewMode = model.viewMode.next }.keyboardShortcut("j", modifiers: .command)
            Button("") { model.resizeTiles(by: 24) }.keyboardShortcut("+", modifiers: .command)
                .disabled(model.viewMode != .grid)
            Button("") { model.resizeTiles(by: 24) }.keyboardShortcut("=", modifiers: .command)
                .disabled(model.viewMode != .grid)
            Button("") { model.resizeTiles(by: -24) }.keyboardShortcut("-", modifiers: .command)
                .disabled(model.viewMode != .grid)
            Button("") { model.undo() }.keyboardShortcut("z", modifiers: [.command, .option])
                .disabled(model.undoStep == nil)
            // While browsing results only: typing keeps ⌘→ (end of line).
            Button("") { model.expandSelectedSection() }.keyboardShortcut(.rightArrow, modifiers: .command)
                .disabled(!model.browsing)
            ForEach(1...9, id: \.self) { n in
                Button("") { model.openNumbered(n) }.keyboardShortcut(KeyEquivalent(Character("\(n)")), modifiers: .command)
            }
        }
        .opacity(0)
        .accessibilityHidden(true) // the context menu and ⌘K list offer these actions
        .allowsHitTesting(false)
    }
}
