import AVFoundation
import AppKit
import LaserdotKit
import UniformTypeIdentifiers

/// At most `n` jobs at a time; the newest waiting job goes first (what was
/// asked for last is what is on screen now: rows scrolled past wait, and
/// give up when cancelled). A job that was cancelled while it waited leaves
/// as soon as it gets in.
actor AsyncGate {
    private var free: Int
    private var waiters: [CheckedContinuation<Void, Never>] = []
    init(_ n: Int) { free = n }
    func enter() async {
        if free > 0 {
            free -= 1
            return
        }
        await withCheckedContinuation { waiters.append($0) }
    }
    func leave() {
        if waiters.isEmpty { free += 1 } else { waiters.removeLast().resume() }
    }
}

/// A running tool, ended when the job that asked for it is cancelled.
final class ProcessBox: @unchecked Sendable {
    private let lock = NSLock()
    private var process: Process?
    private var cancelled = false
    var isCancelled: Bool { lock.lock(); defer { lock.unlock() }; return cancelled }
    func set(_ p: Process) -> Bool {
        lock.lock(); defer { lock.unlock() }
        process = p
        return !cancelled
    }
    func cancel() {
        lock.lock(); defer { lock.unlock() }
        cancelled = true
        if let p = process, p.isRunning { p.terminate() }
    }
}

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
    /// Two ffmpeg runs and two macOS readers at a time: a big video on a
    /// spinning drive is slow to read, and many at once thrash the disk.
    private static let ffmpegGate = AsyncGate(2)
    private static let avGate = AsyncGate(2)

    /// The lengths the index already knows (results carry them): no ffprobe
    /// run before ffmpeg for those.
    static func noteDurations(_ hits: [Hit]) {
        lock.lock()
        for h in hits where h.kind == "video" {
            if let ms = h.durationMs, ms > 0 { durations[h.path] = Double(ms) / 1000 }
        }
        if durations.count > 20_000 { durations.removeAll() }
        lock.unlock()
    }

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
        if Task.isCancelled { return nil }
        var img = await avFrame(path, fraction, edge)
        if img == nil, !Task.isCancelled { img = await ffmpegFrame(path, fraction, edge) }
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
        // Formats macOS cannot read go straight to ffmpeg (no wasted read).
        guard playable.contains((path as NSString).pathExtension.lowercased()) else { return nil }
        await avGate.enter()
        defer { Task { await avGate.leave() } }
        if Task.isCancelled { return nil }
        let asset = AVURLAsset(url: URL(fileURLWithPath: path))
        guard (try? await asset.load(.isReadable)) == true,
              let d = try? await asset.load(.duration), d.seconds.isFinite, d.seconds > 0 else { return nil }
        let gen = AVAssetImageGenerator(asset: asset)
        gen.appliesPreferredTrackTransform = true
        gen.maximumSize = CGSize(width: edge * 2, height: edge * 2)
        gen.requestedTimeToleranceBefore = .positiveInfinity
        gen.requestedTimeToleranceAfter = .positiveInfinity
        let t = CMTime(seconds: d.seconds * min(0.99, max(0, fraction)), preferredTimescale: 600)
        let cgOrNil = await withTaskCancellationHandler {
            try? await gen.image(at: t).image
        } onCancel: {
            gen.cancelAllCGImageGeneration()
        }
        guard let cg = cgOrNil else { return nil }
        return NSImage(cgImage: cg, size: NSSize(width: cg.width, height: cg.height))
    }

    // MARK: the bundled ffmpeg

    private static func tool(_ name: String) -> String? {
        Bundle.main.url(forAuxiliaryExecutable: name)?.path
    }

    private static func ffmpegFrame(_ path: String, _ fraction: Double, _ edge: CGFloat) async -> NSImage? {
        guard let ffmpeg = tool("ffmpeg") else { return nil }
        await ffmpegGate.enter()
        defer { Task { await ffmpegGate.leave() } }
        if Task.isCancelled { return nil } // scrolled away while it waited
        let box = ProcessBox()
        return await withTaskCancellationHandler {
            await Task.detached(priority: .userInitiated) { () -> NSImage? in
                let seconds = (duration(path, box) ?? 0) * min(0.99, max(0, fraction))
                let width = Int(edge * 2)
                guard !box.isCancelled,
                      let data = run(ffmpeg, ["-nostdin", "-v", "error", "-ss", String(format: "%.2f", seconds), "-i", path,
                                              "-frames:v", "1", "-vf", "scale=\(width):-2", "-f", "image2pipe",
                                              "-vcodec", "png", "-"], box),
                      !data.isEmpty else { return nil }
                return NSImage(data: data)
            }.value
        } onCancel: {
            box.cancel()
        }
    }

    /// The video's length in seconds (ffprobe, remembered).
    private static func duration(_ path: String, _ box: ProcessBox) -> Double? {
        lock.lock()
        if let d = durations[path] { lock.unlock(); return d }
        lock.unlock()
        guard let ffprobe = tool("ffprobe"),
              let out = run(ffprobe, ["-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path], box),
              let d = Double(String(decoding: out, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)),
              d.isFinite, d > 0 else { return nil }
        lock.lock()
        durations[path] = d
        lock.unlock()
        return d
    }

    /// Run a bundled tool, its output read in full; ended after twenty seconds.
    private static func run(_ exe: String, _ args: [String], _ box: ProcessBox) -> Data? {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: exe)
        p.arguments = args
        let out = Pipe()
        p.standardOutput = out
        p.standardError = FileHandle.nullDevice
        p.standardInput = FileHandle.nullDevice
        guard box.set(p), (try? p.run()) != nil else { return nil }
        if box.isCancelled { p.terminate() }
        let timer = DispatchWorkItem { if p.isRunning { p.terminate() } }
        DispatchQueue.global().asyncAfter(deadline: .now() + 20, execute: timer)
        let data = out.fileHandleForReading.readDataToEndOfFile()
        p.waitUntilExit()
        timer.cancel()
        return p.terminationStatus == 0 ? data : nil
    }
}
