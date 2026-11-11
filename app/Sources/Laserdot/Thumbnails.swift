import AppKit
import Quartz
import QuickLookThumbnailing
import SwiftUI

/// Thumbnails from Quick Look (local, made by macOS): pictures and videos in
/// the result rows, any file in the preview pane. Kept in memory only.
final class ThumbnailCache {
    static let shared = ThumbnailCache()
    private let cache = NSCache<NSString, NSImage>()

    init() {
        cache.countLimit = 400
    }

    /// `done` runs on the main thread, with nil when macOS has no thumbnail.
    func image(for path: String, size: CGFloat, iconFallback: Bool, done: @escaping (NSImage?) -> Void) {
        let key = "\(path)|\(Int(size))|\(iconFallback)" as NSString
        if let img = cache.object(forKey: key) {
            done(img)
            return
        }
        let scale = NSScreen.main?.backingScaleFactor ?? 2
        let request = QLThumbnailGenerator.Request(
            fileAt: URL(fileURLWithPath: path), size: CGSize(width: size, height: size), scale: scale,
            representationTypes: iconFallback ? .all : .thumbnail)
        QLThumbnailGenerator.shared.generateBestRepresentation(for: request) { [cache] rep, _ in
            let img = rep?.nsImage
            if let img { cache.setObject(img, forKey: key) }
            DispatchQueue.main.async { done(img) }
        }
    }
}

/// A file's icon, replaced by its thumbnail once macOS has made one.
struct FileIcon: View {
    let path: String
    var size: CGFloat = 28
    /// Ask Quick Look for a thumbnail (pictures, videos, PDFs…).
    var thumbnail = false
    /// In the preview pane: Quick Look's icon when it has no thumbnail.
    var large = false
    @State private var image: NSImage?
    @State private var loadedFor: String?

    var body: some View {
        Image(nsImage: (loadedFor == path ? image : nil) ?? NSWorkspace.shared.icon(forFile: path))
            .resizable()
            .interpolation(.high)
            .aspectRatio(contentMode: .fit)
            .frame(width: size, height: size)
            .task(id: path) { load() }
    }

    private func load() {
        guard thumbnail else { return }
        let p = path
        ThumbnailCache.shared.image(for: p, size: size, iconFallback: large) { img in
            image = img
            loadedFor = p
        }
    }
}

/// ⌘Y: the system Quick Look panel for a result, as in Finder.
final class QuickLook: NSObject, QLPreviewPanelDataSource {
    static let shared = QuickLook()
    private var items: [URL] = []

    func show(_ url: URL) {
        items = [url]
        guard let panel = QLPreviewPanel.shared() else { return }
        panel.dataSource = self
        NSApp.activate(ignoringOtherApps: true)
        if panel.isVisible {
            panel.reloadData()
        } else {
            panel.makeKeyAndOrderFront(nil)
        }
    }

    func numberOfPreviewItems(in panel: QLPreviewPanel!) -> Int { items.count }

    func previewPanel(_ panel: QLPreviewPanel!, previewItemAt index: Int) -> QLPreviewItem! {
        items[index] as NSURL
    }
}
