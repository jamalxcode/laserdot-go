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

    /// Finder's icon for the file itself, read off the main thread once per
    /// path, one at a time; `done` runs on the main thread. Only apps and
    /// folders can have an icon of their own (custom folder icons, app
    /// icons): other files keep their type's icon and the disk is not read.
    private let iconQueue = DispatchQueue(label: "laserdot.icons", qos: .utility)
    func fileIcon(for path: String, done: @escaping (NSImage) -> Void) {
        if let img = icons.object(forKey: path as NSString) { return done(img) }
        let ext = (path as NSString).pathExtension.lowercased()
        guard ext.isEmpty || ext == "app" else { return }
        iconQueue.async { [icons] in
            let img = NSWorkspace.shared.icon(forFile: path)
            icons.setObject(img, forKey: path as NSString)
            DispatchQueue.main.async { done(img) }
        }
    }

    /// Quick Look thumbnails, three at a time, newest first (AsyncGate): a
    /// screen of huge videos on a spinning drive must not be read all at
    /// once, and a row scrolled away gives up its turn (its task is
    /// cancelled, and so is a request already running).
    private let gate = AsyncGate(3)

    func thumbnail(for path: String, size: CGFloat, iconFallback: Bool) async -> NSImage? {
        let key = "\(path)|\(Int(size))|\(iconFallback)" as NSString
        if let img = cache.object(forKey: key) { return img }
        let video = VideoFrames.isVideo(path)
        var rep: QLThumbnailRepresentation?
        // A video macOS cannot read itself (MKV, WebM, AVI…) goes straight
        // to the bundled ffmpeg: Quick Look would only read it for nothing.
        let quickLook = !video || VideoFrames.playable.contains((path as NSString).pathExtension.lowercased())
        if quickLook {
            await gate.enter()
            if Task.isCancelled {
                await gate.leave()
                return nil
            }
            let scale = await MainActor.run { NSScreen.main?.backingScaleFactor ?? 2 }
            let request = QLThumbnailGenerator.Request(
                fileAt: URL(fileURLWithPath: path), size: CGSize(width: size, height: size), scale: scale,
                representationTypes: iconFallback ? .all : .thumbnail)
            rep = await withTaskCancellationHandler {
                try? await QLThumbnailGenerator.shared.generateBestRepresentation(for: request)
            } onCancel: {
                QLThumbnailGenerator.shared.cancel(request)
            }
            await gate.leave()
        }
        var img = rep?.nsImage
        if video, rep == nil || rep?.type == .icon, !Task.isCancelled {
            img = await VideoFrames.poster(path, edge: size) ?? img
        }
        if let img { cache.setObject(img, forKey: key) }
        return img
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
            .task(id: "\(path)|\(Int(size / 64))") { await load() }
    }

    private func load() async {
        let p = path
        ThumbnailCache.shared.fileIcon(for: p) { img in
            icon = img
            iconFor = p
        }
        guard thumbnail else { return }
        // A row only passing by while scrolling asks for nothing.
        try? await Task.sleep(nanoseconds: 200_000_000)
        if Task.isCancelled { return }
        let step = (size / 64).rounded(.up) * 64 // one cached size per step
        let img = await ThumbnailCache.shared.thumbnail(for: p, size: max(size, step), iconFallback: large)
        if Task.isCancelled { return }
        image = img
        loadedFor = p
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
