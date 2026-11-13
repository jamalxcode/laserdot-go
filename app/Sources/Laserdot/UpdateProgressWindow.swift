import AppKit
import LaserdotKit
import SwiftUI

/// Where an update has got, shown in a small window so a check or an install
/// is never silent.
enum UpdateStep: Equatable {
    case checking
    case downloading(received: Int64, total: Int64)
    case verifying
    case signing
    case installing
    case restarting

    var title: String {
        switch self {
        case .checking: return L("update.step.checking")
        case .downloading: return L("update.step.downloading")
        case .verifying: return L("update.step.verifying")
        case .signing: return L("update.step.signing")
        case .installing: return L("update.step.installing")
        case .restarting: return L("update.step.restarting")
        }
    }

    /// "12.4 MB of 41.0 MB", while downloading.
    var detail: String? {
        guard case let .downloading(received, total) = self else { return nil }
        let f = ByteCountFormatter()
        f.countStyle = .file
        if total > 0 {
            return String(format: L("update.step.bytesOf"), f.string(fromByteCount: received), f.string(fromByteCount: total))
        }
        return received > 0 ? f.string(fromByteCount: received) : nil
    }

    /// 0…1 when known; nil shows a moving bar.
    var fraction: Double? {
        guard case let .downloading(received, total) = self, total > 0 else { return nil }
        return min(1, Double(received) / Double(total))
    }

    /// Checking and downloading run in the helper; the window can be hidden
    /// meanwhile. Installing cannot be left half done, so it stays.
    var canHide: Bool {
        switch self {
        case .checking, .downloading: return true
        default: return false
        }
    }

    init(_ p: UpdateProgress) {
        switch p.phase {
        case .checking: self = .checking
        case .downloading: self = .downloading(received: p.received, total: p.total)
        }
    }
}

struct UpdateProgressView: View {
    @ObservedObject var updates: UpdateController

    var body: some View {
        let step = updates.step ?? .checking
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 10) {
                Image(nsImage: NSApp.applicationIconImage)
                    .resizable()
                    .frame(width: 40, height: 40)
                VStack(alignment: .leading, spacing: 2) {
                    Text(updates.stepVersion.map { String(format: L("update.window.titleVersion"), $0) } ?? L("update.window.title"))
                        .font(.headline)
                    Text(step.title)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .accessibilityIdentifier("update-step")
                }
            }
            if let f = step.fraction {
                ProgressView(value: f)
                    .progressViewStyle(.linear)
                    .accessibilityIdentifier("update-bar")
            } else {
                ProgressView()
                    .progressViewStyle(.linear)
                    .accessibilityIdentifier("update-bar")
            }
            HStack {
                Text(step.detail ?? " ")
                    .font(.caption)
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
                Spacer()
                if step.canHide {
                    Button(L("update.window.hide")) { updates.hideProgress() }
                        .controlSize(.small)
                }
            }
        }
        .padding(16)
        .frame(width: 360)
    }
}

/// A small floating panel that does not take the focus from what the user is
/// doing, and stays visible when the helper app comes to the front.
enum UpdateProgressPanel {
    static func make(updates: UpdateController) -> NSPanel {
        let p = NSPanel(contentRect: NSRect(x: 0, y: 0, width: 360, height: 110),
                        styleMask: [.titled, .fullSizeContentView, .nonactivatingPanel],
                        backing: .buffered, defer: false)
        p.title = L("update.window.title")
        p.titleVisibility = .hidden
        p.titlebarAppearsTransparent = true
        p.isMovableByWindowBackground = true
        p.hidesOnDeactivate = false
        p.isReleasedWhenClosed = false
        p.level = .floating
        p.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
        let host = NSHostingView(rootView: UpdateProgressView(updates: updates))
        p.contentView = host
        p.setContentSize(host.fittingSize)
        p.center()
        return p
    }
}
