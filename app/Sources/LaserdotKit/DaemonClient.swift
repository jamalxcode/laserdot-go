import Darwin
import Foundation

/// Why a call to laserdotd failed.
public enum DaemonClientError: Error, CustomStringConvertible {
    /// The socket could not be opened, written or read.
    case io(String)
    /// The daemon answered something that is not a protocol response.
    case malformed(String)
    /// The daemon refused the request (`{"ok":false,...}`).
    case remote(RemoteError)

    public var description: String {
        switch self {
        case let .io(m): return "cannot reach laserdotd: \(m)"
        case let .malformed(m): return "laserdotd protocol error: \(m)"
        case let .remote(e): return e.message
        }
    }
}

/// One connection to laserdotd over its Unix socket (spec §13.2a): a JSON
/// request per line, a JSON response per line. Local IPC only — the socket
/// is a file; there is no TCP anywhere in the app. Not thread-safe: use one
/// connection per thread (or serialize calls, as `Engine` does).
public final class DaemonClient {
    private var fd: Int32
    private var buffer: [UInt8] = []
    private let encoder: JSONEncoder = {
        let e = JSONEncoder()
        e.outputFormatting = [.withoutEscapingSlashes]
        return e
    }()

    /// Connects to the daemon's socket (it must be listening).
    public init(socketPath: String, timeout: TimeInterval = 600) throws {
        var addr = sockaddr_un()
        let path = Array(socketPath.utf8)
        guard path.count < MemoryLayout.size(ofValue: addr.sun_path) else {
            throw DaemonClientError.io("socket path too long: \(socketPath)")
        }
        fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { throw DaemonClientError.io(String(cString: strerror(errno))) }
        addr.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        addr.sun_family = sa_family_t(AF_UNIX)
        withUnsafeMutableBytes(of: &addr.sun_path) { raw in
            raw.copyBytes(from: path)
            raw[path.count] = 0
        }
        let rc = withUnsafePointer(to: &addr) { p in
            p.withMemoryRebound(to: sockaddr.self, capacity: 1) { sa in
                Darwin.connect(fd, sa, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard rc == 0 else {
            let why = String(cString: strerror(errno))
            Darwin.close(fd)
            fd = -1 // deinit runs for a thrown, fully initialized object
            throw DaemonClientError.io("\(socketPath): \(why)")
        }
        // A daemon that went away must not kill the app with SIGPIPE.
        var one: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &one, socklen_t(MemoryLayout<Int32>.size))
        setTimeout(timeout)
    }

    /// Connects only if a daemon answers `ping` on the socket.
    public static func connectLive(socketPath: String) -> DaemonClient? {
        guard let c = try? DaemonClient(socketPath: socketPath, timeout: 5) else { return nil }
        guard let pong = try? c.call(.ping, as: String.self), pong == "pong" else { return nil }
        c.setTimeout(600)
        return c
    }

    deinit {
        if fd >= 0 { Darwin.close(fd) }
    }

    /// How long a call may wait for its answer (0: no limit). A first
    /// duplicate scan can take minutes, or hours on big drives.
    public func setTimeout(_ seconds: TimeInterval) {
        var tv = timeval()
        tv.tv_sec = Int(seconds)
        tv.tv_usec = 0
        setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))
        setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))
    }

    /// Sends `request` and decodes the result as `T`.
    public func call<T: Decodable>(_ request: DaemonRequest, as type: T.Type = T.self) throws -> T {
        guard let result = try callNullable(request, as: T.self) else {
            throw DaemonClientError.malformed("\(request.op): malformed response")
        }
        return result
    }

    /// `call` for ops whose result may be `null` (`hint`, `section_at`).
    public func callNullable<T: Decodable>(_ request: DaemonRequest, as type: T.Type = T.self) throws -> T? {
        let line = try callRaw(request)
        let envelope: Envelope<T>
        do {
            envelope = try JSONDecoder().decode(Envelope<T>.self, from: line)
        } catch {
            throw DaemonClientError.malformed("\(request.op): \(error)")
        }
        if envelope.ok {
            return envelope.result
        }
        if !envelope.ok, let error = envelope.error {
            throw DaemonClientError.remote(error)
        }
        throw DaemonClientError.malformed("\(request.op): malformed response")
    }

    /// Sends `request` and returns the response line (without the newline).
    public func callRaw(_ request: DaemonRequest) throws -> Data {
        var data: Data
        do {
            data = try encoder.encode(request)
        } catch {
            throw DaemonClientError.malformed("cannot encode \(request.op): \(error)")
        }
        data.append(0x0A)
        try writeAll(data)
        return try readLine()
    }

    private struct Envelope<T: Decodable>: Decodable {
        var ok: Bool
        var result: T?
        var error: RemoteError?
    }

    private func writeAll(_ data: Data) throws {
        try data.withUnsafeBytes { (raw: UnsafeRawBufferPointer) in
            guard let base = raw.baseAddress else { return }
            var sent = 0
            while sent < raw.count {
                let n = Darwin.write(fd, base + sent, raw.count - sent)
                if n < 0 {
                    if errno == EINTR { continue }
                    throw DaemonClientError.io(String(cString: strerror(errno)))
                }
                sent += n
            }
        }
    }

    private func readLine() throws -> Data {
        var chunk = [UInt8](repeating: 0, count: 64 * 1024)
        while true {
            if let nl = buffer.firstIndex(of: 0x0A) {
                let line = Data(buffer[..<nl])
                buffer.removeSubrange(...nl)
                return line
            }
            let n = chunk.withUnsafeMutableBytes { Darwin.read(fd, $0.baseAddress, $0.count) }
            if n < 0 {
                if errno == EINTR { continue }
                if errno == EAGAIN || errno == EWOULDBLOCK {
                    throw DaemonClientError.io("timed out waiting for laserdotd")
                }
                throw DaemonClientError.io(String(cString: strerror(errno)))
            }
            if n == 0 { throw DaemonClientError.malformed("connection closed") }
            buffer.append(contentsOf: chunk[..<n])
        }
    }
}

/// Where things live, by the same rules as the Go daemon and CLI.
public enum Paths {
    /// The longest Unix socket path macOS accepts is 104 bytes (`sun_path`).
    static let maxSocketPath = 100

    /// `$LASERDOT_DB`, else `~/Library/Application Support/laserdot/index.db`
    /// (Go `core.DefaultDBPath`).
    public static var defaultDbPath: String {
        if let p = ProcessInfo.processInfo.environment["LASERDOT_DB"], !p.isEmpty { return p }
        return (NSHomeDirectory() as NSString).appendingPathComponent("Library/Application Support/laserdot/index.db")
    }

    /// The daemon socket for an index (Go `ipc.SocketFor`):
    /// `<db folder>/laserdotd.sock`, or, when that is longer than 100 bytes,
    /// `/tmp/laserdot-<uid>/<FNV-1a-64 of the db path, 16 hex>.sock` in an
    /// owner-only folder (symlinks resolved, so `/private/tmp` on macOS).
    public static func socket(forDb db: String) -> String {
        let dir = (db as NSString).deletingLastPathComponent
        let preferred = dir.isEmpty ? "laserdotd.sock" : (dir as NSString).appendingPathComponent("laserdotd.sock")
        if preferred.utf8.count <= maxSocketPath { return preferred }
        var short = "/tmp/laserdot-\(getuid())"
        mkdir(short, 0o700)
        chmod(short, 0o700)
        if let real = realpath(short, nil) {
            short = String(cString: real)
            free(real)
        }
        return (short as NSString).appendingPathComponent(String(format: "%016llx.sock", fnv1a64(db)))
    }

    /// The folder holding the daemon socket (the sandbox profile allows Unix
    /// sockets there only).
    public static func socketDir(forDb db: String) -> String {
        (socket(forDb: db) as NSString).deletingLastPathComponent
    }

    /// FNV-1a, 64-bit, over the UTF-8 bytes.
    static func fnv1a64(_ s: String) -> UInt64 {
        var h: UInt64 = 0xcbf2_9ce4_8422_2325
        for b in s.utf8 {
            h = (h ^ UInt64(b)) &* 0x0000_0100_0000_01b3
        }
        return h
    }
}
