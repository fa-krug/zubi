import Foundation
import dnssd

/// Announces the server as `_zubi._tcp` so clients on the network can find it without an IP address.
/// Only for an "All interfaces" bind: a loopback or single-address bind would announce an address
/// that doesn't answer.
final class BonjourAdvertiser {
    static let serviceType = "_zubi._tcp"

    private var service: DNSServiceRef?

    var isAdvertising: Bool { service != nil }

    func start(port: Int) {
        stop()
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0"
        // A TXT record is length-prefixed strings.
        var txt = [UInt8]()
        for entry in ["api=1", "version=\(version)"] {
            txt.append(UInt8(entry.utf8.count))
            txt.append(contentsOf: entry.utf8)
        }
        var ref: DNSServiceRef?
        // A nil name means the Mac's computer name, renamed automatically on a clash and kept up to
        // date when the user changes it. No callback: there is nothing to do with the result.
        let error = DNSServiceRegister(
            &ref, 0, 0, nil, Self.serviceType, nil, nil, UInt16(port).bigEndian, UInt16(txt.count), txt, nil, nil)
        guard error == kDNSServiceErr_NoError, let ref else { return }
        service = ref
    }

    func stop() {
        if let service { DNSServiceRefDeallocate(service) }
        service = nil
    }
}
