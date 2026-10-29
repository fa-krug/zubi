import Foundation
import IOKit.pwr_mgt

/// Holds a "prevent idle system sleep" assertion while asked to. The display still sleeps, and a
/// closed lid or the Sleep command still sleeps the Mac; `pmset -g assertions` shows the reason.
final class KeepAwake {
    private var assertion: IOPMAssertionID?

    var isHeld: Bool { assertion != nil }

    func hold(_ hold: Bool, reason: String) {
        if hold, assertion == nil {
            var id = IOPMAssertionID(0)
            let result = IOPMAssertionCreateWithName(
                kIOPMAssertPreventUserIdleSystemSleep as CFString, IOPMAssertionLevel(kIOPMAssertionLevelOn),
                reason as CFString, &id)
            if result == kIOReturnSuccess { assertion = id }
        } else if !hold, let id = assertion {
            IOPMAssertionRelease(id)
            assertion = nil
        }
    }
}
