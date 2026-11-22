import AppKit
import LaserdotKit

/// Show in Finder that reaches the file, even inside a package (RevealPlan).
enum Finder {
    static func reveal(_ urls: [URL]) {
        let plans = urls.map { RevealPlan.plan(for: $0.path) }
        if plans.allSatisfy({ $0 == .select }) {
            NSWorkspace.shared.activateFileViewerSelecting(urls)
            return
        }
        for (url, plan) in zip(urls, plans) {
            switch plan {
            case .select:
                NSWorkspace.shared.activateFileViewerSelecting([url])
            case let .insidePackage(folder, package):
                // A window on the file's own folder, the file selected; if
                // Finder refuses, at least the package it is in.
                if !NSWorkspace.shared.selectFile(url.path, inFileViewerRootedAtPath: folder) {
                    NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: package)])
                }
            }
        }
    }

    static func reveal(_ url: URL) { reveal([url]) }
}
