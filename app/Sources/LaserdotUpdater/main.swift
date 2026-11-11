import AppKit
import Foundation
import LaserdotKit
import Security

// Laserdot Updater: the one part of Laserdot allowed on the network — an
// exception the owner granted for updates (spec §7.1, §7.4). It talks only
// to GitHub (api.github.com, and the storage host GitHub redirects release
// downloads to), sends nothing but the token and the request, and runs only
// when the app asks: on "Check for Updates…" or the daily check. Laserdot and
// laserdotd stay network-blocked by the OS; this helper is a separate app,
// launched by macOS rather than by Laserdot, so it does not inherit the block.
//
// Usage: Laserdot Updater --app <Laserdot.app> [--interactive]
// It writes UpdateStatus (update.json) and exits. With --interactive it may
// ask for a GitHub token (kept in the Keychain) and report problems.

let repo = "jamalxcode/laserdot-go"
let apiHost = "api.github.com"
let keychainService = "app.laserdot.updater"
let keychainAccount = "github-token"

// MARK: Keychain

func readToken() -> String? {
    let q: [String: Any] = [
        kSecClass as String: kSecClassGenericPassword,
        kSecAttrService as String: keychainService,
        kSecAttrAccount as String: keychainAccount,
        kSecReturnData as String: true,
        kSecMatchLimit as String: kSecMatchLimitOne,
    ]
    var out: CFTypeRef?
    guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let data = out as? Data else { return nil }
    let t = String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
    return t.isEmpty ? nil : t
}

func saveToken(_ token: String) {
    deleteToken()
    let q: [String: Any] = [
        kSecClass as String: kSecClassGenericPassword,
        kSecAttrService as String: keychainService,
        kSecAttrAccount as String: keychainAccount,
        kSecAttrLabel as String: "Laserdot Updater (GitHub token)",
        kSecValueData as String: Data(token.utf8),
    ]
    SecItemAdd(q as CFDictionary, nil)
}

func deleteToken() {
    let q: [String: Any] = [
        kSecClass as String: kSecClassGenericPassword,
        kSecAttrService as String: keychainService,
        kSecAttrAccount as String: keychainAccount,
    ]
    SecItemDelete(q as CFDictionary)
}

// MARK: UI (interactive runs only)

func askForToken(reason: String?) -> String? {
    NSApp.activate(ignoringOtherApps: true)
    let alert = NSAlert()
    alert.messageText = "Laserdot updates"
    alert.informativeText = (reason.map { $0 + "\n\n" } ?? "") + """
        Laserdot's releases are private on GitHub. Paste a GitHub token that can read \
        \(repo) (a fine-grained token with Contents: Read-only for that repository). \
        It is kept in your Keychain and sent only to GitHub.
        """
    let field = NSSecureTextField(frame: NSRect(x: 0, y: 0, width: 320, height: 24))
    field.placeholderString = "github_pat_…"
    alert.accessoryView = field
    alert.addButton(withTitle: "Save")
    alert.addButton(withTitle: "Cancel")
    alert.addButton(withTitle: "Create a Token…")
    alert.window.initialFirstResponder = field
    switch alert.runModal() {
    case .alertFirstButtonReturn:
        let t = field.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        return t.isEmpty ? nil : t
    case .alertThirdButtonReturn:
        NSWorkspace.shared.open(URL(string: "https://github.com/settings/personal-access-tokens/new")!)
        return askForToken(reason: nil)
    default:
        return nil
    }
}

// MARK: HTTP

/// Drops the token when GitHub redirects a download to its storage host:
/// the token is only ever sent to api.github.com.
final class RedirectGuard: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        var r = request
        if r.url?.host != apiHost {
            r.setValue(nil, forHTTPHeaderField: "Authorization")
        }
        completionHandler(r)
    }
}

let session = URLSession(configuration: .ephemeral, delegate: RedirectGuard(), delegateQueue: nil)

struct HTTPError: Error {
    let status: Int
    let message: String
}

func request(_ path: String, token: String, accept: String) -> URLRequest {
    var r = URLRequest(url: URL(string: "https://\(apiHost)\(path)")!)
    r.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
    r.setValue(accept, forHTTPHeaderField: "Accept")
    r.setValue("2022-11-28", forHTTPHeaderField: "X-GitHub-Api-Version")
    r.setValue("Laserdot-Updater", forHTTPHeaderField: "User-Agent")
    r.timeoutInterval = 60
    return r
}

func fetch(_ r: URLRequest) throws -> Data {
    let done = DispatchSemaphore(value: 0)
    var result: Result<Data, Error> = .failure(HTTPError(status: 0, message: "no answer"))
    session.dataTask(with: r) { data, response, error in
        defer { done.signal() }
        if let error { result = .failure(error); return }
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status), let data else {
            let body = data.map { String(decoding: $0.prefix(300), as: UTF8.self) } ?? ""
            result = .failure(HTTPError(status: status, message: body))
            return
        }
        result = .success(data)
    }.resume()
    done.wait()
    return try result.get()
}

func download(_ r: URLRequest, to dest: URL) throws {
    let done = DispatchSemaphore(value: 0)
    var failure: Error?
    session.downloadTask(with: r) { tmp, response, error in
        defer { done.signal() }
        if let error { failure = error; return }
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status), let tmp else {
            failure = HTTPError(status: status, message: "download failed")
            return
        }
        do {
            try? FileManager.default.removeItem(at: dest)
            try FileManager.default.moveItem(at: tmp, to: dest)
        } catch { failure = error }
    }.resume()
    done.wait()
    if let failure { throw failure }
}

struct Release: Decodable {
    struct Asset: Decodable {
        let id: Int
        let name: String
        let size: Int
    }
    let tag_name: String
    let assets: [Asset]
}

// MARK: main

let args = CommandLine.arguments
func arg(_ name: String) -> String? {
    guard let i = args.firstIndex(of: name), i + 1 < args.count else { return nil }
    return args[i + 1]
}
let interactive = args.contains("--interactive")
let appURL = arg("--app").map { URL(fileURLWithPath: $0) }
    ?? Bundle.main.bundleURL.deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()

let app = NSApplication.shared
app.setActivationPolicy(.accessory)

func finish(_ s: UpdateStatus) -> Never {
    try? s.save()
    if interactive, s.state == .failed, let m = s.message {
        let a = NSAlert()
        a.alertStyle = .warning
        a.messageText = "Could not check for Laserdot updates"
        a.informativeText = m
        NSApp.activate(ignoringOtherApps: true)
        a.runModal()
    }
    exit(0)
}

guard let current = AppVersion(bundle: appURL) else {
    finish(UpdateStatus(state: .failed, message: "Laserdot.app not found at \(appURL.path)"))
}

var token = readToken()
var reason: String?
for _ in 0..<3 {
    if token == nil {
        guard interactive, let t = askForToken(reason: reason) else { finish(UpdateStatus(state: .needsToken)) }
        saveToken(t)
        token = t
    }
    do {
        let data = try fetch(request("/repos/\(repo)/releases/latest", token: token!, accept: "application/vnd.github+json"))
        let rel = try JSONDecoder().decode(Release.self, from: data)
        let latest = rel.tag_name.hasPrefix("v") ? String(rel.tag_name.dropFirst()) : rel.tag_name
        guard AppVersion(short: current.short, build: "0") < AppVersion(short: latest, build: "0") else {
            finish(UpdateStatus(state: .upToDate, latest: latest))
        }
        guard let asset = rel.assets.first(where: { $0.name.lowercased().hasPrefix("laserdot") && $0.name.lowercased().hasSuffix(".dmg") }) else {
            finish(UpdateStatus(state: .failed, latest: latest, message: "release \(rel.tag_name) has no Laserdot .dmg"))
        }
        let folder = UpdateStatus.downloadsFolder
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let dest = folder.appendingPathComponent(asset.name)
        let have = (try? FileManager.default.attributesOfItem(atPath: dest.path)[.size] as? Int) ?? nil
        if have != asset.size {
            try download(request("/repos/\(repo)/releases/assets/\(asset.id)", token: token!, accept: "application/octet-stream"), to: dest)
        }
        finish(UpdateStatus(state: .downloaded, latest: latest, file: dest.path))
    } catch let e as HTTPError where e.status == 401 || e.status == 403 || e.status == 404 {
        // A token that cannot read the repository: ask again (interactive).
        deleteToken()
        token = nil
        reason = "GitHub did not accept the token (HTTP \(e.status))."
        if !interactive { finish(UpdateStatus(state: .needsToken, message: reason)) }
    } catch {
        finish(UpdateStatus(state: .failed, message: error.localizedDescription))
    }
}
finish(UpdateStatus(state: .failed, message: reason ?? "no token"))
