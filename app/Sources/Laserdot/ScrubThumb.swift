import AVFoundation
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
    @State private var bucket = -1
    @State private var cache: [Int: NSImage] = [:]
    @State private var duration: Double?
    private static let buckets = 10

    var body: some View {
        ZStack(alignment: .bottom) {
            if let frame {
                Image(nsImage: frame)
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
                let b = max(0, min(Self.buckets - 1, Int(p.x / max(1, size + 8) * CGFloat(Self.buckets))))
                guard b != bucket else { return }
                bucket = b
                show(b)
            case .ended:
                bucket = -1
                frame = nil
            }
        }
        .accessibilityHidden(true)
    }

    private func show(_ b: Int) {
        if let img = cache[b] {
            frame = img
            return
        }
        let url = URL(fileURLWithPath: path)
        let known = duration
        let edge = size * 2
        Task.detached(priority: .userInitiated) {
            let asset = AVURLAsset(url: url)
            var seconds = known ?? 0
            if seconds <= 0, let d = try? await asset.load(.duration) { seconds = d.seconds }
            guard seconds.isFinite, seconds > 0 else { return }
            let gen = AVAssetImageGenerator(asset: asset)
            gen.appliesPreferredTrackTransform = true
            gen.maximumSize = CGSize(width: edge, height: edge)
            gen.requestedTimeToleranceBefore = .positiveInfinity
            gen.requestedTimeToleranceAfter = .positiveInfinity
            let t = CMTime(seconds: seconds * (Double(b) + 0.5) / Double(Self.buckets), preferredTimescale: 600)
            guard let cg = try? await gen.image(at: t).image else { return }
            let img = NSImage(cgImage: cg, size: NSSize(width: cg.width, height: cg.height))
            await MainActor.run {
                duration = seconds
                cache[b] = img
                if bucket == b { frame = img }
            }
        }
    }
}
