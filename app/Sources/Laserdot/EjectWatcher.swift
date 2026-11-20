import DiskArbitration
import Foundation

/// Removable media awareness: macOS asks the apps before it unmounts a disk
/// (DiskArbitration's unmount approval). When the disk is a drive Laserdot
/// has attached, the engine first lets go of it — a first index or a
/// catch-up stops at once, keeping what it read, and the drive's index file
/// is closed — then Laserdot approves, so ejecting from Finder or the menu
/// bar always works. (The owner had to quit Laserdot to eject a drive.)
/// Between jobs Laserdot holds nothing open on a drive anyway; this covers a
/// job in progress. If the engine does not answer within a few seconds the
/// eject is approved regardless: Laserdot never stands in its way.
final class EjectWatcher {
    private let session: DASession
    private let box: Box

    final class Box {
        let release: (String) -> Void
        init(release: @escaping (String) -> Void) { self.release = release }
    }

    /// `release(mount)` is called on the main thread just before `mount` is
    /// unmounted; it must return within seconds.
    init?(release: @escaping (String) -> Void) {
        guard let s = DASessionCreate(kCFAllocatorDefault) else { return nil }
        session = s
        box = Box(release: release)
        let context = Unmanaged.passUnretained(box).toOpaque()
        DARegisterDiskUnmountApprovalCallback(session, nil, { disk, context in
            guard let context else { return nil }
            let box = Unmanaged<EjectWatcher.Box>.fromOpaque(context).takeUnretainedValue()
            if let description = DADiskCopyDescription(disk) as NSDictionary?,
               let url = description[kDADiskDescriptionVolumePathKey as String] as? URL {
                box.release(url.path)
            }
            return nil // approved: Laserdot never keeps a disk from being ejected
        }, context)
        // Common modes: answered even while a dialog is open (the "index
        // this drive?" prompt runs the main loop in its own mode).
        DASessionScheduleWithRunLoop(session, CFRunLoopGetMain(), CFRunLoopMode.commonModes.rawValue)
    }

    deinit {
        DASessionUnscheduleFromRunLoop(session, CFRunLoopGetMain(), CFRunLoopMode.commonModes.rawValue)
    }
}
