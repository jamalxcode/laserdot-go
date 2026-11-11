import AppKit
import Foundation
import LaserdotKit

// Laserdot Updater: the one part of Laserdot allowed on the network — an
// exception the owner granted for updates (spec §7.1, §7.4). It talks only
// to GitHub (api.github.com, and the storage host GitHub redirects release
// downloads to), sends no credentials and nothing about the Mac, and runs only
// when the app asks: on "Check for Updates…" or the daily check. Laserdot and
// laserdotd stay network-blocked; this helper is a separate app, launched by
// macOS rather than by Laserdot, so it does not inherit the block.
//
// The releases are public, so no token is needed. Only the owner can publish
// one; the app still checks the download (bundle id, signature) before it
// installs it (Updater.prepare).
//
// Usage: Laserdot Updater --app <Laserdot.app> [--interactive]
// It writes UpdateStatus (update.json) and exits. With --interactive it
// reports problems in an alert.

let repo = "jamalxcode/laserdot-go"
let apiHost = "api.github.com"
let allowedHosts: Set<String> = [apiHost, "github.com", "objects.githubusercontent.com",
                                 "release-assets.githubusercontent.com"]

/// Follows redirects only to GitHub's own hosts, over HTTPS.
final class RedirectGuard: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        guard let u = request.url, u.scheme == "https", let h = u.host, allowedHosts.contains(h) else {
            completionHandler(nil)
            return
        }
        completionHandler(request)
    }
}

let session = URLSession(configuration: .ephemeral, delegate: RedirectGuard(), delegateQueue: nil)

struct HTTPError: Error, LocalizedError {
    let status: Int
    var errorDescription: String? { "GitHub answered HTTP \(status)." }
}

func request(_ url: URL, accept: String) -> URLRequest {
    var r = URLRequest(url: url)
    r.setValue(accept, forHTTPHeaderField: "Accept")
    r.setValue("2022-11-28", forHTTPHeaderField: "X-GitHub-Api-Version")
    r.setValue("Laserdot-Updater", forHTTPHeaderField: "User-Agent")
    r.timeoutInterval = 60
    return r
}

func fetch(_ r: URLRequest) throws -> Data {
    let done = DispatchSemaphore(value: 0)
    var result: Result<Data, Error> = .failure(HTTPError(status: 0))
    session.dataTask(with: r) { data, response, error in
        defer { done.signal() }
        if let error { result = .failure(error); return }
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status), let data else { result = .failure(HTTPError(status: status)); return }
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
        guard (200..<300).contains(status), let tmp else { failure = HTTPError(status: status); return }
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
        let name: String
        let size: Int
        let browser_download_url: URL
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

do {
    let api = URL(string: "https://\(apiHost)/repos/\(repo)/releases/latest")!
    let rel = try JSONDecoder().decode(Release.self, from: fetch(request(api, accept: "application/vnd.github+json")))
    let latest = rel.tag_name.hasPrefix("v") ? String(rel.tag_name.dropFirst()) : rel.tag_name
    guard AppVersion(short: current.short, build: "0") < AppVersion(short: latest, build: "0") else {
        finish(UpdateStatus(state: .upToDate, latest: latest))
    }
    guard let asset = rel.assets.first(where: { $0.name.lowercased().hasPrefix("laserdot") && $0.name.lowercased().hasSuffix(".dmg") }),
          asset.browser_download_url.scheme == "https", let h = asset.browser_download_url.host, allowedHosts.contains(h),
          !asset.name.contains("/") else {
        finish(UpdateStatus(state: .failed, latest: latest, message: "release \(rel.tag_name) has no Laserdot .dmg"))
    }
    let folder = UpdateStatus.downloadsFolder
    try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
    let dest = folder.appendingPathComponent(asset.name)
    let have = (try? FileManager.default.attributesOfItem(atPath: dest.path)[.size] as? Int) ?? nil
    if have != asset.size {
        try download(request(asset.browser_download_url, accept: "application/octet-stream"), to: dest)
    }
    finish(UpdateStatus(state: .downloaded, latest: latest, file: dest.path))
} catch {
    finish(UpdateStatus(state: .failed, message: (error as? LocalizedError)?.errorDescription ?? error.localizedDescription))
}
