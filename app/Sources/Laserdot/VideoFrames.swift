import AVFoundation
import AppKit
import UniformTypeIdentifiers

/// A frame from any video, for thumbnails and scrubbing (the owner: "fix it
/// for all video formats", "scrubbing on mkv not working"). macOS reads
/// MP4/MOV/M4V itself (AVFoundation); for what it cannot — MKV, WebM, AVI,
/// FLV, WMV, MPEG-TS… — the ffmpeg bundled with Laserdot (LGPL, built without
/// network) extracts the frame. Frames are kept in memory; at most two ffmpeg
/// runs at a time, each ended after twenty seconds (a slow external or network
/// drive needs that long to seek; "on a different volume it stopped working").
enum VideoFrames {
    private static let cache: NSCache<NSString, NSImage> = {
        let c = NSCache<NSString, NSImage>()
        c.countLimit = 600
        return c
    }()
    private static let lock = NSLock()
    private static var durations: [String: Double] = [:]
    private static let ffmpegSlots = DispatchSemaphore(value: 2)

    /// Extensions of videos (by name, so formats macOS has no type for count too).
    /// The same list as the engine's video kind (internal/media/kind.go).
    static let videoExtensions: Set<String> = [
        "mp4", "m4v", "mov", "qt", "mkv", "webm", "avi", "wmv", "flv", "f4v", "swf", "mpg", "mpeg", "mpe",
        "m1v", "m2v", "mp2v", "mpv", "m2p", "m2ts", "mts", "ts", "trp", "3gp", "3g2", "ogv", "ogm", "vob", "evo",
        "divx", "xvid", "rm", "rmvb", "asf", "mxf", "dv", "mjpeg", "mjpg", "nut", "y4m", "wtv", "dvr-ms", "tod",
        "amv", "h264", "h265", "hevc", "264", "265", "insv", "lrv", "braw", "r3d",
    ]

    /// What macOS's own player plays; the rest open in their app.
    static let playable: Set<String> = ["mp4", "m4v", "mov", "qt", "3gp", "3g2"]

    static func isVideo(_ path: String) -> Bool {
        let ext = (path as NSString).pathExtension.lowercased()
        if videoExtensions.contains(ext) { return true }
        return UTType(filenameExtension: ext)?.conforms(to: .movie) ?? false
    }

    /// The frame at `fraction` (0…1) of the video, at most `edge` points wide.
    static func frame(_ path: String, at fraction: Double, edge: CGFloat) async -> NSImage? {
        let key = "\(path)|\(Int(fraction * 1000))|\(Int(edge))" as NSString
        if let img = cache.object(forKey: key) { return img }
        let img = await avFrame(path, fraction, edge) ?? ffmpegFrame(path, fraction, edge)
        if let img { cache.setObject(img, forKey: key) }
        return img
    }

    /// The picture a video's thumbnail shows: a frame a tenth of the way in
    /// (the very first is often black).
    static func poster(_ path: String, edge: CGFloat) async -> NSImage? {
        await frame(path, at: 0.1, edge: edge)
    }

    // MARK: macOS (AVFoundation)

    private static func avFrame(_ path: String, _ fraction: Double, _ edge: CGFloat) async -> NSImage? {
        let asset = AVURLAsset(url: URL(fileURLWithPath: path))
        guard (try? await asset.load(.isReadable)) == true,
              let d = try? await asset.load(.duration), d.seconds.isFinite, d.seconds > 0 else { return nil }
        let gen = AVAssetImageGenerator(asset: asset)
        gen.appliesPreferredTrackTransform = true
        gen.maximumSize = CGSize(width: edge * 2, height: edge * 2)
        gen.requestedTimeToleranceBefore = .positiveInfinity
        gen.requestedTimeToleranceAfter = .positiveInfinity
        let t = CMTime(seconds: d.seconds * min(0.99, max(0, fraction)), preferredTimescale: 600)
        guard let cg = try? await gen.image(at: t).image else { return nil }
        return NSImage(cgImage: cg, size: NSSize(width: cg.width, height: cg.height))
    }

    // MARK: the bundled ffmpeg

    private static func tool(_ name: String) -> String? {
        Bundle.main.url(forAuxiliaryExecutable: name)?.path
    }

    private static func ffmpegFrame(_ path: String, _ fraction: Double, _ edge: CGFloat) async -> NSImage? {
        guard let ffmpeg = tool("ffmpeg") else { return nil }
        return await Task.detached(priority: .userInitiated) { () -> NSImage? in
            ffmpegSlots.wait()
            defer { ffmpegSlots.signal() }
            let seconds = (duration(path) ?? 0) * min(0.99, max(0, fraction))
            let width = Int(edge * 2)
            guard let data = run(ffmpeg, ["-nostdin", "-v", "error", "-ss", String(format: "%.2f", seconds), "-i", path,
                                          "-frames:v", "1", "-vf", "scale=\(width):-2", "-f", "image2pipe",
                                          "-vcodec", "png", "-"]),
                  !data.isEmpty else { return nil }
            return NSImage(data: data)
        }.value
    }

    /// The video's length in seconds (ffprobe, remembered).
    private static func duration(_ path: String) -> Double? {
        lock.lock()
        if let d = durations[path] { lock.unlock(); return d }
        lock.unlock()
        guard let ffprobe = tool("ffprobe"),
              let out = run(ffprobe, ["-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path]),
              let d = Double(String(decoding: out, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)),
              d.isFinite, d > 0 else { return nil }
        lock.lock()
        durations[path] = d
        lock.unlock()
        return d
    }

    /// Run a bundled tool, its output read in full; ended after twenty seconds.
    private static func run(_ exe: String, _ args: [String]) -> Data? {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: exe)
        p.arguments = args
        let out = Pipe()
        p.standardOutput = out
        p.standardError = FileHandle.nullDevice
        p.standardInput = FileHandle.nullDevice
        guard (try? p.run()) != nil else { return nil }
        let timer = DispatchWorkItem { if p.isRunning { p.terminate() } }
        DispatchQueue.global().asyncAfter(deadline: .now() + 20, execute: timer)
        let data = out.fileHandleForReading.readDataToEndOfFile()
        p.waitUntilExit()
        timer.cancel()
        return p.terminationStatus == 0 ? data : nil
    }
}
