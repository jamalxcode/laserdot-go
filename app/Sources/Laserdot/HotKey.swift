import Carbon.HIToolbox

/// Global ⌥Space (spec §6.4) through Carbon's hot-key API, which needs no
/// Accessibility permission.
final class HotKey {
    private var ref: EventHotKeyRef?
    private static var action: (() -> Void)?

    /// The event handler is installed once; re-registering a different key
    /// (spec §6.4) must not add a second one.
    private static var handlerInstalled = false

    init?(keyCode: UInt32 = UInt32(kVK_Space), modifiers: UInt32 = UInt32(optionKey), action: @escaping () -> Void) {
        HotKey.action = action
        if !HotKey.handlerInstalled {
            var spec = EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
            let installed = InstallEventHandler(
                GetApplicationEventTarget(),
                { _, _, _ -> OSStatus in
                    HotKey.action?()
                    return OSStatus(noErr)
                },
                1, &spec, nil, nil
            )
            guard installed == OSStatus(noErr) else { return nil }
            HotKey.handlerInstalled = true
        }
        let id = EventHotKeyID(signature: OSType(0x4C53_4454), id: 1) // 'LSDT'
        let status = RegisterEventHotKey(keyCode, modifiers, id, GetApplicationEventTarget(), 0, &ref)
        // Another app (Raycast, Alfred, Spotlight) may own ⌥Space (§6.4).
        guard status == OSStatus(noErr) else { return nil }
    }

    deinit {
        if let ref { UnregisterEventHotKey(ref) }
    }
}
