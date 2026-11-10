import Darwin
import Foundation

/// The app's own network denial (spec §7.1, pillar 1): the same Seatbelt
/// profile laserdotd applies to itself — everything allowed except
/// networking; Unix sockets inside the daemon's socket folder stay usable.
/// It is irreversible and inherited by the daemon the app starts.
public enum Sandbox {
    /// The profile (Go `sandbox.MacOSProfile`).
    public static func profile(socketDir: String?) -> String {
        var p = "(version 1)\n(allow default)\n(deny network*)\n"
        if var dir = socketDir, !dir.isEmpty {
            if let real = realpath(dir, nil) {
                dir = String(cString: real)
                free(real)
            }
            let q = dir.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"")
            p += "(allow network* (local unix-socket (subpath \"\(q)\")))\n"
            p += "(allow network* (remote unix-socket (subpath \"\(q)\")))\n"
        }
        return p
    }

    private typealias SandboxInit = @convention(c) (
        UnsafePointer<CChar>, UInt64, UnsafeMutablePointer<UnsafeMutablePointer<CChar>?>
    ) -> Int32
    private typealias SandboxFreeError = @convention(c) (UnsafeMutablePointer<CChar>?) -> Void
    private typealias SandboxCheck = @convention(c) (pid_t, UnsafePointer<CChar>?, Int32) -> Int32

    /// libsystem_sandbox symbols, looked up at run time (they are not in the
    /// Darwin module map).
    private static func symbol<T>(_ name: String, as _: T.Type) -> T? {
        let defaultHandle = UnsafeMutableRawPointer(bitPattern: -2) // RTLD_DEFAULT
        guard let p = dlsym(defaultHandle, name) else { return nil }
        return unsafeBitCast(p, to: T.self)
    }

    /// Whether this process runs sandboxed (`sandbox_check(pid, NULL,
    /// SANDBOX_FILTER_NONE)`); the profile above is the only sandbox the app
    /// gets, so this means network access is denied.
    public static var isEnforced: Bool {
        guard let check = symbol("sandbox_check", as: SandboxCheck.self) else { return false }
        return check(getpid(), nil, 0) == 1
    }

    /// Deny this process network access. Returns quietly if it is already
    /// sandboxed.
    public static func denyNetwork(socketDir: String?) throws {
        if isEnforced { return }
        guard let initSandbox = symbol("sandbox_init", as: SandboxInit.self) else {
            throw LaserdotError.failed(message: "sandbox_init is not available", code: nil)
        }
        var err: UnsafeMutablePointer<CChar>?
        let rc = profile(socketDir: socketDir).withCString { initSandbox($0, 0, &err) }
        guard rc == 0 else {
            var message = "sandbox_init returned \(rc)"
            if let err {
                message = String(cString: err)
                symbol("sandbox_free_error", as: SandboxFreeError.self)?(err)
            }
            throw LaserdotError.failed(message: message, code: nil)
        }
    }
}
