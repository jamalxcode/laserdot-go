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

    /// The overlay's smallest height (it opens at its full size).
    static let compactHeight: CGFloat = 62
    static let fullHeight: CGFloat = 520

    var body: some View {
        card
            .overlay(hiddenShortcuts)
            // Drop a folder (or a file: its folder) to search inside it.
            .onDrop(of: [.fileURL], isTargeted: nil) { providers in model.handleDrop(providers) }
    }

    /// The choice bar under the search box, the same in every window and for
    /// every search (the owner: "the full option, full choices screen all the
    /// time", "lean towards the spotlight"): round buttons with a caption, as
    /// macOS's Spotlight shows them — kinds of files and common searches,
    /// then the menu's tools. A kind joins the search as a pill (click it
    /// again to take it out); the typed words stay.
    @ViewBuilder var choiceBar: some View {
        if !model.isCommandMode {
            let buttons = model.startButtons
            // Wraps onto more lines in a narrow window (the owner: "let the
            // icons wrap around if the window is small").
            Group {
                FlowLayout(spacing: 10, lineSpacing: 8) {
                    ForEach(Array(buttons.enumerated()), id: \.element.id) { i, b in
                        let on = model.barButtonIsOn(b)
                        Button(action: b.run) {
                            VStack(spacing: 4) {
                                Image(systemName: b.symbol)
                                    .font(.system(size: 17, weight: .regular))
                                    .frame(width: 40, height: 40)
                                    .foregroundStyle(on ? Color.white : Color.primary.opacity(0.8))
                                    .background(Circle().fill(on ? AnyShapeStyle(Color.accentColor) : AnyShapeStyle(.regularMaterial)))
                                    .overlay(Circle().strokeBorder(Color.primary.opacity(0.12)))
                                Text(b.title)
                                    .font(.caption2)
                                    .lineLimit(1)
                                    .foregroundStyle(.secondary)
                            }
                            .frame(minWidth: 58)
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .padding(.leading, b.isTool && i > 0 && !buttons[i - 1].isTool ? 12 : 0)
                        .help(b.title)
                        .accessibilityLabel(b.title)
                        .accessibilityAddTraits(on ? .isSelected : [])
                        .accessibilityIdentifier("start-" + b.id)
                    }
                }
                .padding(.horizontal, 16)
            }
            .padding(.bottom, 8)
        }
    }

    // MARK: the full window

    var card: some View {
        VStack(spacing: 0) {
            // The search box as a rounded bar, as Spotlight's.
            header
                .background(Capsule(style: .continuous).fill(.regularMaterial).padding(.horizontal, 8).padding(.vertical, 4))
                .overlay(Capsule(style: .continuous).strokeBorder(Color.primary.opacity(0.1)).padding(.horizontal, 8).padding(.vertical, 4))
                .padding(.top, 4)
            choiceBar
            Group {
                hintLine
                chips
                refinementChips
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
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
        .background {
            // The floating card is the overlay's look; the main window keeps
            // the standard window background.
            if fixedSize {
                RoundedRectangle(cornerRadius: 14, style: .continuous).fill(.regularMaterial)
            }
        }
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
