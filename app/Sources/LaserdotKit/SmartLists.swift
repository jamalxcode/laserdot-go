import Foundation

/// Ready-made searches on the empty box, each with a live count (the owner
/// chose "smart lists on the empty box"); a pinned search (⌘D) is the owner's
/// own smart list and gets a count too. Lists with nothing in them are not
/// shown.
public struct SmartList: Equatable, Sendable {
    public let id: String
    public let query: String

    public static let builtIn: [SmartList] = [
        SmartList(id: "screenshots", query: "screenshot:true since:7d"),
        SmartList(id: "photos", query: "kind:image since:30d"),
        SmartList(id: "bigvideos", query: "kind:video fsize:>1gb"),
        SmartList(id: "tidy", query: "in:~/Downloads before:30d -kind:folder"),
    ]
}
