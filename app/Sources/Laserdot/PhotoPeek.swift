import SwiftUI

/// Resting the pointer on a picture in the grid shows it larger beside the
/// tile (the owner: "do 8" — glance through a folder of photos without opening
/// Quick Look each time). It waits a moment, so passing over tiles shows
/// nothing; moving away hides it. The model holds which picture is shown (one
/// at a time; the self-test sets it).
struct PhotoPeek: ViewModifier {
    @ObservedObject var model: SearchModel
    let path: String
    @State private var waiting: Task<Void, Never>?

    func body(content: Content) -> some View {
        content
            .onHover { inside in
                waiting?.cancel()
                if inside {
                    waiting = Task { @MainActor in
                        try? await Task.sleep(nanoseconds: 600_000_000)
                        if !Task.isCancelled { model.peekPath = path }
                    }
                } else if model.peekPath == path {
                    model.peekPath = nil
                }
            }
            .popover(isPresented: Binding(get: { model.peekPath == path },
                                          set: { if !$0, model.peekPath == path { model.peekPath = nil } }),
                     arrowEdge: .trailing) {
                FileIcon(path: path, size: 420, thumbnail: true, large: true)
                    .padding(10)
                    .accessibilityLabel((path as NSString).lastPathComponent)
            }
    }
}
