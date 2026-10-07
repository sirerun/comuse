import AppKit
import ApplicationServices
import CoreGraphics
import Foundation

let maximumDesktopGeneration: UInt64 = 9_007_199_254_740_991

struct DesktopDisplayFact: Equatable {
    let id: UInt32
    let bounds: CGRect
    let scale: Double
}

struct DesktopFacts: Equatable {
    let primaryID: UInt32
    let displays: [DesktopDisplayFact]

    var isValid: Bool {
        guard !displays.isEmpty,
              Set(displays.map(\.id)).count == displays.count,
              displays.contains(where: { $0.id == primaryID }) else { return false }
        return displays.allSatisfy { display in
            display.bounds.isFinitePositive && display.scale.isFinite && display.scale > 0
        }
    }

    var fingerprint: String? {
        guard isValid else { return nil }
        let rows = displays.sorted { $0.id < $1.id }.map { display in
            [String(display.id), String(Double(display.bounds.origin.x).bitPattern),
             String(Double(display.bounds.origin.y).bitPattern), String(Double(display.bounds.width).bitPattern),
             String(Double(display.bounds.height).bitPattern), String(display.scale.bitPattern)].joined(separator: ":")
        }
        return "p=\(primaryID);\(rows.joined(separator: ";"))"
    }
}

enum DesktopFocusResult {
    case unknown
    case noAuthorizedWindow
    case focused([String: Any])
}

/// Pure per-runtime generation tracker. Missing proof invalidates authority and
/// requires a new generation even when the same topology later reappears.
struct DesktopContextTracker {
    let displayID: String
    private(set) var generation: UInt64 = 0
    private(set) var fingerprint: String?
    private(set) var proofAvailable = false

    init(displayID: String) { self.displayID = displayID }

    mutating func update(_ facts: DesktopFacts?) -> UInt64? {
        guard let facts, let next = facts.fingerprint else {
            proofAvailable = false
            return nil
        }
        if !proofAvailable || fingerprint != next {
            guard generation < maximumDesktopGeneration else {
                proofAvailable = false
                return nil
            }
            generation += 1
            fingerprint = next
        }
        proofAvailable = true
        return generation
    }

    mutating func invalidate() { proofAvailable = false }
}

func desktopFocusEvidence(_ result: DesktopFocusResult) -> (known: Bool, value: [String: Any]?) {
    switch result {
    case .unknown: return (false, nil)
    case .noAuthorizedWindow: return (true, nil)
    case .focused(let window): return (true, window)
    }
}

func desktopReferenceIsCurrent(issued: UInt64, current: UInt64) -> Bool {
    issued > 0 && issued <= maximumDesktopGeneration && issued == current
}

func desktopFocusAuthorization(pid: Int32, scopedPIDs: Set<Int32>) -> DesktopFocusResult {
    scopedPIDs.contains(pid) ? .unknown : .noAuthorizedWindow
}

@MainActor
func inspectDesktopFacts() -> DesktopFacts? {
    var count: UInt32 = 0
    guard CGGetActiveDisplayList(0, nil, &count) == .success, count > 0, count <= 32 else { return nil }
    var ids = [CGDirectDisplayID](repeating: 0, count: Int(count))
    guard CGGetActiveDisplayList(count, &ids, &count) == .success, count > 0 else { return nil }
    ids = Array(ids.prefix(Int(count)))
    let primary = CGMainDisplayID()
    guard ids.contains(primary) else { return nil }
    let screens = NSScreen.screens
    var displays: [DesktopDisplayFact] = []
    for id in ids {
        let bounds = CGDisplayBounds(id)
        guard bounds.isFinitePositive,
              let screen = screens.first(where: {
                  ($0.deviceDescription[NSDeviceDescriptionKey("NSScreenNumber")] as? NSNumber)?.uint32Value == id
              }) else { return nil }
        let scale = Double(screen.backingScaleFactor)
        guard scale.isFinite, scale > 0 else { return nil }
        displays.append(DesktopDisplayFact(id: id, bounds: bounds, scale: scale))
    }
    let facts = DesktopFacts(primaryID: primary, displays: displays)
    return facts.isValid ? facts : nil
}

@MainActor
extension NativeRuntime {
    func refreshDesktopGeneration() throws -> UInt64 {
        let prior = desktopTracker.generation
        guard let next = desktopTracker.update(inspectDesktopFacts()) else {
            desktopTracker.invalidate()
            invalidateAccessibilityState()
            throw ProbeFailure(code: "backend_unavailable")
        }
        if prior != 0 && prior != next { invalidateAccessibilityState() }
        return next
    }

    func desktopContextJSON(requestID: UInt64, deadline: TimeInterval) throws -> [String: Any]? {
        try checkDeadline(requestID, deadline: deadline)
        guard AXIsProcessTrusted(), let generation = try? refreshDesktopGeneration() else {
            desktopTracker.invalidate()
            invalidateAccessibilityState()
            return nil
        }
        let before = generation
        let focus = inspectFocusedWindow()
        try checkDeadline(requestID, deadline: deadline)
        let evidence = desktopFocusEvidence(focus)
        guard evidence.known,
              let after = try? refreshDesktopGeneration(), after == before else {
            desktopTracker.invalidate()
            invalidateAccessibilityState()
            return nil
        }
        return [
            "display_id": desktopTracker.displayID,
            "display_generation": after,
            "focused_window": evidence.value.map { $0 as Any } ?? NSNull()
        ]
    }

    private func inspectFocusedWindow() -> DesktopFocusResult {
        guard let frontmost = NSWorkspace.shared.frontmostApplication else { return .unknown }
        let pid = frontmost.processIdentifier
        guard case .unknown = desktopFocusAuthorization(pid: pid, scopedPIDs: Set(processes.map(\.pid))) else {
            return .noAuthorizedWindow
        }
        guard let expected = processes.first(where: { $0.pid == pid }) else { return .unknown }
        guard let identity = try? matchingProcess(expected),
              identity.bundleID == frontmost.bundleIdentifier else { return .unknown }
        let application = AXUIElementCreateApplication(pid)
        guard AXUIElementSetMessagingTimeout(application, 0.5) == .success else { return .unknown }
        var raw: CFTypeRef?
        let result = AXUIElementCopyAttributeValue(application, kAXFocusedWindowAttribute as CFString, &raw)
        if result == .noValue { return .noAuthorizedWindow }
        guard result == .success else { return .unknown }
        guard let raw else { return .noAuthorizedWindow }
        guard CFGetTypeID(raw) == AXUIElementGetTypeID() else { return .unknown }
        let window = unsafeBitCast(raw, to: AXUIElement.self)
        guard let ref = try? retain(window, process: identity, windowRef: nil, kind: .window),
              let title = nativeWindowTitleEvidence(copyAttribute(window, kAXTitleAttribute as String)) else { return .unknown }
        let bounded = boundedUTF8Prefix(title, byteLimit: 4096)
        guard !bounded.truncated else { return .unknown }
        let process: [String: Any] = ["pid": identity.pid, "bundle_id": identity.bundleID, "launch_id": identity.launchID]
        return .focused(["ref": ref, "title": bounded.text, "process": process])
    }
}
