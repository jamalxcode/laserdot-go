import AVKit
import AppKit
import LaserdotKit
import PDFKit
import SwiftUI

// MARK: preview

/// The selected result, larger: a thumbnail, a playable video or song, or
/// the PDF's pages; then name, kind, size, dates, media details, actions.
struct PreviewPane: View {
    let hit: Hit
    @ObservedObject var model: SearchModel

    private var ext: String { (hit.name as NSString).pathExtension.lowercased() }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                visual
                Text(highlighted(hit.name, model.highlightWords))
                    .font(.headline)
                    .lineLimit(3)
                    .textSelection(.enabled)
                if let found = model.snippet {
                    VStack(alignment: .leading, spacing: 3) {
                        Text(L("preview.foundInside")).font(.caption).foregroundStyle(.secondary)
                        Text(highlighted(found, model.highlightWords))
                            .font(.callout)
                            .lineLimit(6)
                            .textSelection(.enabled)
                            .padding(8)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 6))
                    }
                    .accessibilityElement(children: .combine)
                }
                Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 8, verticalSpacing: 4) {
                    detail(L("preview.kind"), Self.kindText(hit))
                    if !hit.isDir {
                        detail(L("preview.size"), ByteCountFormatter.string(fromByteCount: Int64(hit.size), countStyle: .file))
                    }
                    detail(L("preview.modified"), friendlyDate(hit.modified, long: true))
                    if !hit.badges.isEmpty {
                        detail(L("preview.media"), hit.badges.joined(separator: " · "))
                    }
                    if let v = hit.volume {
                        detail(L("preview.drive"), v)
                    }
                    detail(L("preview.where"), shortPath(hit.path))
                }
                .font(.callout)
                HStack {
                    Button(L("action.open")) { model.open(hit) }
                    Button(L("action.reveal")) { model.reveal(hit) }
                    Button { model.toggleActions() } label: { Image(systemName: "ellipsis.circle") }
                        .help(L("keys.actions") + " (⌘K)")
                        .accessibilityLabel(L("keys.actions"))
                }
                .controlSize(.small)
            }
            .padding(14)
        }
        .task(id: hit.path) { model.loadSnippet(for: hit) }
    }

    @ViewBuilder private var visual: some View {
        if hit.volume == nil && (hit.kind == "video" || hit.kind == "audio") {
            MediaPreview(path: hit.path, video: hit.kind == "video")
                .clipShape(RoundedRectangle(cornerRadius: 6))
        } else if hit.volume == nil && ext == "pdf" {
            PDFOrIcon(path: hit.path)
        } else {
            FileIcon(path: hit.path, size: 220, thumbnail: true, large: true)
                .frame(maxWidth: .infinity)
                .shadow(color: .black.opacity(0.15), radius: 4, y: 2)
                .onTapGesture(count: 2) { model.open(hit) }
                .accessibilityHidden(true)
        }
    }

    /// What Finder calls it ("JPEG image", "Folder"), else the section name.
    private static func kindText(_ hit: Hit) -> String {
        let url = URL(fileURLWithPath: hit.path)
        if let d = try? url.resourceValues(forKeys: [.localizedTypeDescriptionKey]).localizedTypeDescription, !d.isEmpty {
            return d
        }
        return L("section.\(SearchModel.category(hit))")
    }

    private func detail(_ label: String, _ value: String) -> some View {
        GridRow {
            Text(label).foregroundStyle(.secondary).gridColumnAlignment(.trailing)
            Text(value).lineLimit(2)
        }
    }
}

/// A video or song in the pane: its thumbnail with a Play button; the
/// player (AVKit) is created only when Play is clicked. Arrowing through
/// results never loads a player, so the query keeps the keyboard and nothing
/// starts by itself; Space plays it in Quick Look.
struct MediaPreview: View {
    let path: String
    let video: Bool
    @State private var player: AVPlayer?
    @State private var playerFor: String?

    var body: some View {
        Group {
            if let player, playerFor == path {
                PlayerView(player: player)
            } else {
                ZStack {
                    FileIcon(path: path, size: video ? 170 : 64, thumbnail: true, large: true)
                        .frame(maxWidth: .infinity)
                    Button {
                        let p = AVPlayer(url: URL(fileURLWithPath: path))
                        player = p
                        playerFor = path
                        p.play()
                    } label: {
                        Image(systemName: "play.circle.fill")
                            .font(.system(size: video ? 44 : 30))
                            .symbolRenderingMode(.palette)
                            .foregroundStyle(.white, Color.black.opacity(0.55))
                    }
                    .buttonStyle(.plain)
                    .help(L("preview.play"))
                    .accessibilityLabel(L("preview.play"))
                }
            }
        }
        .frame(height: video ? 170 : 64)
        .onChange(of: path) { _, _ in
            player?.pause()
            player = nil
        }
        .onDisappear { player?.pause() }
    }
}

/// AppKit's player view. SwiftUI's VideoPlayer crashed in apps built with
/// SwiftPM (its _AVKit_SwiftUI overlay could not find AVKit's classes:
/// "getSuperclassMetadata" at the first video preview); AVPlayerView links
/// AVKit directly.
struct PlayerView: NSViewRepresentable {
    let player: AVPlayer

    /// The player is clicked, never typed into: the search field keeps the
    /// keyboard (otherwise the query stops taking letters after a video is
    /// selected).
    final class Unfocused: AVPlayerView {
        override var acceptsFirstResponder: Bool { false }
    }

    func makeNSView(context: Context) -> AVPlayerView {
        let v = Unfocused()
        v.controlsStyle = .inline
        v.showsFullScreenToggleButton = true
        v.player = player
        return v
    }

    func updateNSView(_ v: AVPlayerView, context: Context) {
        if v.player !== player { v.player = player }
    }

    static func dismantleNSView(_ v: AVPlayerView, coordinator: ()) {
        v.player?.pause()
        v.player = nil
    }
}

/// The PDF's pages, or its icon when the file cannot be read as a PDF.
struct PDFOrIcon: View {
    let path: String
    @State private var document: PDFDocument?
    @State private var loadedFor: String?

    var body: some View {
        Group {
            if loadedFor == path, let document {
                PDFPreview(document: document)
                    .frame(height: 280)
                    .clipShape(RoundedRectangle(cornerRadius: 6))
            } else {
                FileIcon(path: path, size: 220, thumbnail: true, large: true).frame(maxWidth: .infinity)
            }
        }
        .task(id: path) {
            let p = path
            let doc = PDFDocument(url: URL(fileURLWithPath: p))
            document = (doc?.pageCount ?? 0) > 0 ? doc : nil
            loadedFor = p
        }
    }
}

/// A PDF's pages, scrollable in the pane (PDFKit).
struct PDFPreview: NSViewRepresentable {
    let document: PDFDocument

    func makeNSView(context: Context) -> PDFView {
        let v = PDFView()
        v.autoScales = true
        v.displayMode = .singlePageContinuous
        v.backgroundColor = .clear
        return v
    }

    func updateNSView(_ v: PDFView, context: Context) {
        if v.document !== document { v.document = document }
    }
}
