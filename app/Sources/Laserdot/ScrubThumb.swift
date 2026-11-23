import AppKit
import SwiftUI

/// A video's tile in the grid: moving the pointer across it flips through
/// its frames (the owner chose "video thumbnails you can scrub"). Ten frames
/// per video, read on demand and kept while the tile shows; the usual
/// thumbnail otherwise. Reading happens off the main thread and never blocks.
struct ScrubThumb: View {
    let path: String
    let size: CGFloat
    @State private var frame: NSImage?
    /// The resting picture: a frame a tenth of the way in, for every format.
    @State private var poster: NSImage?
    @State private var bucket = -1
    @State private var cache: [Int: NSImage] = [:]
    /// All ten frames, fetched one after another once the pointer comes in
    /// (not for every tile on screen: only the one being looked at).
    @State private var prefetch: Task<Void, Never>?
    private static let buckets = 10

    var body: some View {
        ZStack(alignment: .bottom) {
            if let shown = frame ?? poster {
                Image(nsImage: shown)
                    .resizable()
                    .aspectRatio(contentMode: .fit)
                    .frame(width: size, height: size * 0.92)
                    .clipShape(RoundedRectangle(cornerRadius: 4))
            } else {
                FileIcon(path: path, size: size, thumbnail: true)
            }
            if bucket >= 0 {
                // Where in the video the pointer is.
                GeometryReader { g in
                    Capsule().fill(Color.accentColor)
                        .frame(width: max(4, g.size.width * CGFloat(bucket + 1) / CGFloat(Self.buckets)), height: 3)
                }
                .frame(height: 3)
                .padding(.horizontal, 6)
            }
        }
        .frame(width: size + 8, height: size * 0.92)
        .contentShape(Rectangle())
        .onContinuousHover(coordinateSpace: .local) { phase in
            switch phase {
            case let .active(p):
                startPrefetch()
                let b = max(0, min(Self.buckets - 1, Int(p.x / max(1, size + 8) * CGFloat(Self.buckets))))
                guard b != bucket else { return }
                bucket = b
                show(b)
            case .ended:
                bucket = -1
                frame = nil
            }
        }
        .onDisappear { prefetch?.cancel() }
        .accessibilityHidden(true)
        .task(id: "\(path)|\(Int(size / 64))") {
            poster = await VideoFrames.poster(path, edge: size)
        }
    }

    private func startPrefetch() {
        guard prefetch == nil else { return }
        let (p, edge) = (path, size)
        prefetch = Task {
            for b in 0..<Self.buckets {
                if Task.isCancelled { return }
                if cache[b] != nil { continue }
                if let img = await VideoFrames.frame(p, at: (Double(b) + 0.5) / Double(Self.buckets), edge: edge) {
                    cache[b] = img
                    if bucket == b { frame = img }
                }
            }
        }
    }

    /// The frame under the pointer; until it arrives the last one stays.
    private func show(_ b: Int) {
        if let img = cache[b] {
            frame = img
            return
        }
        let edge = size
        Task {
            let img = await VideoFrames.frame(path, at: (Double(b) + 0.5) / Double(Self.buckets), edge: edge)
            guard let img else { return }
            cache[b] = img
            if bucket == b { frame = img }
        }
    }
}
