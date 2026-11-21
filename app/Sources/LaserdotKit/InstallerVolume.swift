import DiskArbitration
import Foundation

/// A disk image opened to install an app (a downloaded .dmg with the app and
/// a shortcut to Applications) is not a drive the owner keeps files on, so
/// Laserdot does not offer to index it (the owner: "distinguish app volumes
/// versus actual volumes and not react on indexing app volumes"). It still
/// shows in the Drives list and can be indexed from there by hand.
public enum InstallerVolume {
    /// The volume at `mount` is an installer: a disk image that is read-only
    /// or holds an app, an installer package or an Applications shortcut at
    /// its top; or, when macOS cannot say whether it is a disk image, a
    /// read-only volume with such contents.
    public static func isInstaller(mount: String, readOnly: Bool) -> Bool {
        let image = isDiskImage(mount: mount)
        let looks = looksLikeInstaller(mount: mount)
        switch image {
        case true?: return readOnly || looks
        case false?: return false
        case nil: return readOnly && looks
        }
    }

    /// Whether macOS says the volume is a mounted disk image (nil: it
    /// cannot tell).
    public static func isDiskImage(mount: String) -> Bool? {
        guard let session = DASessionCreate(kCFAllocatorDefault),
              let disk = DADiskCreateFromVolumePath(kCFAllocatorDefault, session, URL(fileURLWithPath: mount) as CFURL),
              let d = DADiskCopyDescription(disk) as NSDictionary? else { return nil }
        let model = (d[kDADiskDescriptionDeviceModelKey as String] as? String ?? "")
            .trimmingCharacters(in: .whitespaces)
        if model.caseInsensitiveCompare("Disk Image") == .orderedSame { return true }
        if (d[kDADiskDescriptionDeviceProtocolKey as String] as? String) == "Virtual Interface" { return true }
        return false
    }

    /// An app, an installer package or a shortcut to /Applications at the top
    /// of the volume.
    public static func looksLikeInstaller(mount: String) -> Bool {
        let fm = FileManager.default
        guard let names = try? fm.contentsOfDirectory(atPath: mount) else { return false }
        for n in names {
            let ext = (n as NSString).pathExtension.lowercased()
            if ["app", "pkg", "mpkg"].contains(ext) { return true }
            let p = (mount as NSString).appendingPathComponent(n)
            if let dest = try? fm.destinationOfSymbolicLink(atPath: p),
               (dest as NSString).standardizingPath == "/Applications" { return true }
        }
        return false
    }
}
