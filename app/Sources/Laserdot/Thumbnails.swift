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

/// Space (or ⌘Y): the system Quick Look panel, as in Finder — pictures
/// large, videos and songs playing, documents readable. It gets every
/// result, so its arrows step through them; Space or Esc closes it.
final class QuickLook: NSObject, QLPreviewPanelDataSource, QLPreviewPanelDelegate {
    static let shared = QuickLook()
    private var items: [URL] = []
    /// Called when the panel closes (the overlay takes the keyboard back).
    var onClose: (() -> Void)?

    var isVisible: Bool { QLPreviewPanel.sharedPreviewPanelExists() && QLPreviewPanel.shared().isVisible }

    func show(_ urls: [URL], at index: Int) {
        guard !urls.isEmpty, let panel = QLPreviewPanel.shared() else { return }
        items = urls
        panel.dataSource = self
        panel.delegate = self
        NSApp.activate(ignoringOtherApps: true)
        panel.reloadData()
        panel.currentPreviewItemIndex = max(0, min(index, urls.count - 1))
        if !panel.isVisible { panel.makeKeyAndOrderFront(nil) }
    }

    func show(_ url: URL) { show([url], at: 0) }

    func close() {
        if isVisible { QLPreviewPanel.shared().orderOut(nil) }
        onClose?()
    }

    /// Space opens it, Space again closes it.
    func toggle(_ urls: [URL], at index: Int) {
        if isVisible { close() } else { show(urls, at: index) }
    }

    func numberOfPreviewItems(in panel: QLPreviewPanel!) -> Int { items.count }

    func previewPanel(_ panel: QLPreviewPanel!, previewItemAt index: Int) -> QLPreviewItem! {
        items[index] as NSURL
    }

    /// Space inside the panel closes it, as in Finder.
    func previewPanel(_ panel: QLPreviewPanel!, handle event: NSEvent!) -> Bool {
        if event.type == .keyDown, event.keyCode == 49 {
            close()
            return true
        }
        return false
    }

    func windowWillClose(_ notification: Notification) {
        DispatchQueue.main.async { self.onClose?() }
    }
}
