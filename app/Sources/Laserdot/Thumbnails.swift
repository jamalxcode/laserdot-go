import AppKit
import Quartz
import QuickLookThumbnailing
import SwiftUI
import UniformTypeIdentifiers

/// Thumbnails from Quick Look (local, made by macOS): pictures and videos in
/// the result rows, any file in the preview pane. Kept in memory only.
final class ThumbnailCache {
    static let shared = ThumbnailCache()
    private let cache = NSCache<NSString, NSImage>()

    init() {
        cache.countLimit = 400
        icons.countLimit = 1000
    }

    private let icons = NSCache<NSString, NSImage>()

    /// The file's icon if already read, else its type's icon (from the
    /// name alone): never a disk read while drawing. Finder's per-file lookup
    /// reads the disk, and a big drive waking up froze the window for seconds
    /// (the owner's beachball on an 8 TB drive). `fileIcon` reads it later.
    func icon(for path: String) -> NSImage {
        if let img = icons.object(forKey: path as NSString) { return img }
        return Self.typeIcon(for: path)
    }

    /// The icon for a name's type (a folder when it has no extension).
    static func typeIcon(for path: String) -> NSImage {
        let ext = (path as NSString).pathExtension
        let type = ext.isEmpty ? UTType.folder : (UTType(filenameExtension: ext) ?? .data)
        return NSWorkspace.shared.icon(for: type)
    }

    /// Finder's icon for the file itself (custom icons, app icons), read off
    /// the main thread once per path; `done` runs on the main thread.
    func fileIcon(for path: String, done: @escaping (NSImage) -> Void) {
        if let img = icons.object(forKey: path as NSString) { return done(img) }
        DispatchQueue.global(qos: .userInitiated).async { [icons] in
            let img = NSWorkspace.shared.icon(forFile: path)
            icons.setObject(img, forKey: path as NSString)
            DispatchQueue.main.async { done(img) }
        }
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
        let video = VideoFrames.isVideo(path)
        QLThumbnailGenerator.shared.generateBestRepresentation(for: request) { [cache] rep, _ in
            // A video macOS has no thumbnail for (MKV, WebM, AVI…): a frame
            // from the bundled ffmpeg instead of the generic icon.
            if video, rep == nil || rep?.type == .icon {
                Task {
                    var img = await VideoFrames.poster(path, edge: size)
                    if img == nil { img = rep?.nsImage }
                    if let img { cache.setObject(img, forKey: key) }
                    await MainActor.run { done(img) }
                }
                return
            }
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
    @State private var icon: NSImage?
    @State private var iconFor: String?

    var body: some View {
        Image(nsImage: (loadedFor == path ? image : nil) ?? (iconFor == path ? icon : nil) ?? ThumbnailCache.shared.icon(for: path))
            .resizable()
            .interpolation(.high)
            .aspectRatio(contentMode: .fit)
            .frame(width: size, height: size)
            // A new thumbnail when the path changes, or the size grows a step
            // (the grid's size slider); in between it is scaled.
            .task(id: "\(path)|\(Int(size / 64))") { load() }
    }

    private func load() {
        let p = path
        ThumbnailCache.shared.fileIcon(for: p) { img in
            icon = img
            iconFor = p
        }
        guard thumbnail else { return }
        let step = (size / 64).rounded(.up) * 64 // one cached size per step
        ThumbnailCache.shared.image(for: p, size: max(size, step), iconFallback: large) { img in
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

    /// Times show() was asked (for the self-test's report).
    private(set) var shows = 0

    var isVisible: Bool { QLPreviewPanel.sharedPreviewPanelExists() && QLPreviewPanel.shared().isVisible }

    func show(_ urls: [URL], at index: Int) {
        shows += 1
        guard !urls.isEmpty, let panel = QLPreviewPanel.shared() else { return }
        items = urls
        NSApp.activate(ignoringOtherApps: true)
        // Ask the panel to find its controller again (the app delegate): a
        // panel that was closed keeps a stale one and would not reopen.
        panel.updateController()
        panel.dataSource = self
        panel.delegate = self
        panel.reloadData()
        panel.currentPreviewItemIndex = max(0, min(index, urls.count - 1))
        if !panel.isVisible { panel.makeKeyAndOrderFront(nil) }
    }

    func show(_ url: URL) { show([url], at: 0) }

    func close() {
        // close(), not orderOut: the panel then ends its controller properly,
        // so the next Space can open it again.
        if isVisible, let panel = QLPreviewPanel.shared() {
            panel.close()
            if panel.isVisible { panel.orderOut(nil) } // close() can be ignored mid-animation
        }
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
