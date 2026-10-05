import AppKit
import ApplicationServices
import CoreGraphics
import CoreFoundation
import Foundation
import CryptoKit

private let accessibilityRequestLimit = 32 * 1024
private let accessibilityResponseLimit = 64 * 1024
private let fixtureBundleIdentifier = "com.sirerun.comuse.fixture"
private let maximumAXDepth = 16
private let maximumAXNodes = 256
private let maximumAXTextBytes = 16 * 1024
private let maximumAXDuration: TimeInterval = 0.25
private let maximumRetainedReferences = 1024
private let referenceLifetime: Duration = .seconds(60)

private struct ProbeRequest {
    let requestID: String
    let operation: String
    let scope: ProbeScope?
    let includeValues: Bool
    let windowReference: String?
}

private struct ProbeScope {
    let pid: pid_t
    let bundleID: String
    let nonce: String
}

private struct ProcessIdentity {
    let application: NSRunningApplication
    let pid: pid_t
    let bundleID: String
    let launchDate: Date
}

struct AccessibilityTargetMetadata {
    let nonce: String
    let processStartReference: String
    let windowReference: String
    let reference: String
    let stateID: String?
    let stateComplete: Bool
    let role: String
    let identifier: String?
    let isProtected: Bool
}

enum AccessibilityResolution {
    case resolved(AXUIElement)
    case expired
    case stale
    case scopeMismatch
    case unavailable

    var responseCode: String? {
        switch self {
        case .resolved: return nil
        case .expired: return "reference_expired"
        case .stale: return "reference_stale"
        case .scopeMismatch: return "scope_mismatch"
        case .unavailable: return "reference_unavailable"
        }
    }
}

@MainActor
private final class AXReferenceStore {
    struct WindowEntry {
        let reference: String
        let identity: ProcessIdentity
        let nonce: String
        let window: AXUIElement
        var expiresAt: ContinuousClock.Instant
    }

    struct ElementEntry {
        let reference: String
        let identity: ProcessIdentity
        let nonce: String
        let windowReference: String
        let window: AXUIElement
        let element: AXUIElement
        let parent: AXUIElement
        let parentReference: String?
        let role: String
        let identifier: String?
        var stateID: String?
        var stateComplete: Bool
        var childReferences: [String]
        var expiresAt: ContinuousClock.Instant
    }

    struct ProcessEntry {
        let identity: ProcessIdentity
        let reference: String
    }

    var windows: [String: WindowEntry] = [:]
    var elements: [String: ElementEntry] = [:]
    var processes: [pid_t: ProcessEntry] = [:]
    var expiredReferences: [String: ContinuousClock.Instant] = [:]

    func purgeExpired() {
        let now = ContinuousClock().now
        for (ref, entry) in windows where entry.expiresAt <= now { expiredReferences[ref] = now.advanced(by: referenceLifetime) }
        for (ref, entry) in elements where entry.expiresAt <= now { expiredReferences[ref] = now.advanced(by: referenceLifetime) }
        windows = windows.filter { $0.value.expiresAt > now }
        elements = elements.filter { $0.value.expiresAt > now }
        expiredReferences = expiredReferences.filter { $0.value > now }
        if expiredReferences.count > maximumRetainedReferences {
            for ref in expiredReferences.sorted(by: { $0.value < $1.value }).prefix(expiredReferences.count - maximumRetainedReferences) {
                expiredReferences.removeValue(forKey: ref.key)
            }
        }
    }

    func prepareScope(_ identity: ProcessIdentity, nonce: String) {
        purgeExpired()
        processes = processes.filter { $0.key == identity.pid }
        windows = windows.filter { $0.value.identity.pid != identity.pid || ($0.value.nonce == nonce && sameProcessIdentity($0.value.identity, identity)) }
        elements = elements.filter { $0.value.identity.pid != identity.pid || ($0.value.nonce == nonce && sameProcessIdentity($0.value.identity, identity)) }
        if let current = processes[identity.pid], !sameProcessIdentity(current.identity, identity) {
            processes.removeValue(forKey: identity.pid)
        }
        if processes[identity.pid] == nil {
            processes[identity.pid] = ProcessEntry(identity: identity, reference: UUID().uuidString)
        }
    }

    func processReference(_ identity: ProcessIdentity) -> String {
        if let existing = processes[identity.pid], sameProcessIdentity(existing.identity, identity) {
            return existing.reference
        }
        let entry = ProcessEntry(identity: identity, reference: UUID().uuidString)
        processes[identity.pid] = entry
        return entry.reference
    }

    func windowReference(_ window: AXUIElement, identity: ProcessIdentity, nonce: String) -> String? {
        purgeExpired()
        if let key = windows.first(where: {
            $0.value.nonce == nonce && sameProcessIdentity($0.value.identity, identity) && CFEqual($0.value.window, window)
        })?.key {
            windows[key]?.expiresAt = ContinuousClock().now.advanced(by: referenceLifetime)
            expiredReferences.removeValue(forKey: key)
            return key
        }
        guard windows.count + elements.count < maximumRetainedReferences else { return nil }
        let reference = UUID().uuidString
        windows[reference] = WindowEntry(reference: reference, identity: identity, nonce: nonce, window: window, expiresAt: ContinuousClock().now.advanced(by: referenceLifetime))
        expiredReferences.removeValue(forKey: reference)
        return reference
    }

    func elementReference(_ element: AXUIElement, parent: AXUIElement, parentReference: String?, window: AXUIElement, windowReference: String, identity: ProcessIdentity, nonce: String, role: String, identifier: String?) -> String? {
        purgeExpired()
        if let key = elements.first(where: {
            $0.value.nonce == nonce && sameProcessIdentity($0.value.identity, identity) &&
            $0.value.windowReference == windowReference && CFEqual($0.value.window, window) &&
            CFEqual($0.value.element, element) && CFEqual($0.value.parent, parent) && $0.value.parentReference == parentReference
        })?.key {
            elements[key]?.expiresAt = ContinuousClock().now.advanced(by: referenceLifetime)
            expiredReferences.removeValue(forKey: key)
            return key
        }
        guard windows.count + elements.count < maximumRetainedReferences else { return nil }
        let reference = UUID().uuidString
        elements[reference] = ElementEntry(reference: reference, identity: identity, nonce: nonce, windowReference: windowReference, window: window, element: element, parent: parent, parentReference: parentReference, role: role, identifier: identifier, stateID: nil, stateComplete: false, childReferences: [], expiresAt: ContinuousClock().now.advanced(by: referenceLifetime))
        expiredReferences.removeValue(forKey: reference)
        return reference
    }

    func removeUnobservedElements(identity: ProcessIdentity, nonce: String, windowReference: String, observed: Set<String>) {
        elements = elements.filter { key, value in
            !(value.windowReference == windowReference && value.nonce == nonce && sameProcessIdentity(value.identity, identity) && !observed.contains(key))
        }
    }

    func removeUnobservedWindows(identity: ProcessIdentity, nonce: String, observed: Set<String>) {
        let removedWindows = Set(windows.keys.filter { key in
            guard let entry = windows[key] else { return false }
            return entry.nonce == nonce && sameProcessIdentity(entry.identity, identity) && !observed.contains(key)
        })
        windows = windows.filter { !removedWindows.contains($0.key) }
        elements = elements.filter { !removedWindows.contains($0.value.windowReference) }
    }
}

@MainActor
private let axReferences = AXReferenceStore()

@MainActor
func handleAccessibilityProbe(_ requestData: Data) -> Data {
    guard requestData.count <= accessibilityRequestLimit else {
        return probeResponse(requestID: "", status: "error", error: "request_limit_exceeded")
    }
    guard let request = decodeProbeRequest(requestData) else {
        return probeResponse(requestID: "", status: "error", error: "invalid_request")
    }

    let response: Data
    switch request.operation {
    case "doctor":
        response = probeResponse(requestID: request.requestID, status: "completed", result: [
            "accessibility": ["available": AXIsProcessTrusted(), "prompted": false],
            "event_posting": ["available": CGPreflightPostEventAccess(), "prompted": false],
        ])
    case "windows":
        guard let scope = request.scope else {
            return probeResponse(requestID: request.requestID, status: "error", error: "scope_required")
        }
        response = listFixtureWindows(scope: scope, requestID: request.requestID)
    case "a11y":
        guard let scope = request.scope else {
            return probeResponse(requestID: request.requestID, status: "error", error: "scope_required")
        }
        guard let windowReference = request.windowReference else {
            return probeResponse(requestID: request.requestID, status: "error", error: "window_ref_required")
        }
        response = observeFixtureAccessibility(scope: scope, requestID: request.requestID, includeValues: request.includeValues, requestedWindowReference: windowReference)
    default:
        response = probeResponse(requestID: request.requestID, status: "error", error: "unsupported_operation")
    }
    return response.count <= accessibilityResponseLimit
        ? response
        : probeResponse(requestID: request.requestID, status: "error", error: "response_limit_exceeded")
}

@MainActor
func resolveAccessibilityElement(_ ref: String, pid: Int32) -> AXUIElement? {
    guard case let .resolved(element) = resolveAccessibilityElementResult(ref, pid: pid) else { return nil }
    return element
}

@MainActor
func resolveAccessibilityElementResult(_ ref: String, pid: Int32) -> AccessibilityResolution {
    axReferences.purgeExpired()
    guard let entry = axReferences.elements[ref] else {
        return axReferences.expiredReferences[ref] == nil ? .unavailable : .expired
    }
    guard entry.identity.pid == pid else { return .scopeMismatch }
    guard let currentIdentity = processIdentity(pid: pid), sameProcessIdentity(currentIdentity, entry.identity) else {
        return .stale
    }
    guard AXIsProcessTrusted() else { return .unavailable }
    guard fixtureWindowStillMatches(entry.window, identity: entry.identity, nonce: entry.nonce),
          sameAXElement(entry.element, parent: entry.parent),
          currentAXClassificationMatches(entry) else { return .stale }
    return .resolved(entry.element)
}

@MainActor
func accessibilityTargetMetadata(_ ref: String, pid: Int32) -> AccessibilityTargetMetadata? {
    axReferences.purgeExpired()
    guard let entry = axReferences.elements[ref], entry.identity.pid == pid,
          let currentIdentity = processIdentity(pid: pid), sameProcessIdentity(currentIdentity, entry.identity),
          AXIsProcessTrusted(),
          fixtureWindowStillMatches(entry.window, identity: entry.identity, nonce: entry.nonce),
          sameAXElement(entry.element, parent: entry.parent),
          let role = copyAXAttribute(entry.element, kAXRoleAttribute) as? String,
          (copyAXAttribute(entry.element, kAXIdentifierAttribute) as? String) == entry.identifier,
          let processStartReference = axReferences.processes[pid]?.reference else { return nil }
    let subrole = copyAXAttribute(entry.element, kAXSubroleAttribute) as? String
    let isProtected = role == (kAXTextFieldRole as String) && subrole == (kAXSecureTextFieldSubrole as String)
    return AccessibilityTargetMetadata(nonce: entry.nonce, processStartReference: processStartReference, windowReference: entry.windowReference, reference: entry.reference, stateID: entry.stateID, stateComplete: entry.stateComplete, role: role, identifier: entry.identifier, isProtected: isProtected)
}

@MainActor
private func decodeProbeRequest(_ data: Data) -> ProbeRequest? {
    guard let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
          object["schema_version"] as? Int == 1,
          let requestID = object["request_id"] as? String, !requestID.isEmpty, requestID.utf8.count <= 128,
          let operation = object["op"] as? String else { return nil }

    var scope: ProbeScope?
    if let rawScope = object["scope"] as? [String: Any] {
        guard let pidNumber = rawScope["pid"] as? NSNumber,
              let pid = strictFixturePID(pidNumber),
              let bundleID = rawScope["bundle_id"] as? String,
              let nonce = rawScope["fixture_nonce"] as? String,
              validFixtureNonce(nonce) else { return nil }
        scope = ProbeScope(pid: pid_t(pid), bundleID: bundleID, nonce: nonce)
    } else if operation != "doctor" {
        return nil
    }
    let windowReference = object["window_ref"] as? String
    if operation == "a11y" && (windowReference == nil || windowReference!.isEmpty || windowReference!.utf8.count > 128) { return nil }
    return ProbeRequest(requestID: requestID, operation: operation, scope: scope, includeValues: object["include_values"] as? Bool == true, windowReference: windowReference)
}

func strictFixturePID(_ number: NSNumber) -> Int32? {
    guard CFGetTypeID(number) != CFBooleanGetTypeID() else { return nil }
    let value = number.doubleValue
    guard value.isFinite, value.rounded(.towardZero) == value, value > 0, value <= Double(Int32.max) else { return nil }
    return Int32(value)
}

func validFixtureNonce(_ nonce: String) -> Bool {
    let bytes = Array(nonce.utf8)
    return (1...64).contains(bytes.count) && bytes.allSatisfy {
        (48...57).contains($0) || (65...90).contains($0) || (97...122).contains($0) || [45, 46, 95].contains($0)
    }
}

@MainActor
private func listFixtureWindows(scope: ProbeScope, requestID: String) -> Data {
    guard scope.bundleID == fixtureBundleIdentifier,
          let identity = validateProcess(scope), AXIsProcessTrusted() else {
        return probeResponse(requestID: requestID, status: "error", error: "scope_or_permission_denied")
    }
    axReferences.prepareScope(identity, nonce: scope.nonce)
    let processStartReference = axReferences.processReference(identity)
    let appElement = AXUIElementCreateApplication(scope.pid)
    AXUIElementSetMessagingTimeout(appElement, 0.05)
    let deadline = Date().addingTimeInterval(maximumAXDuration)
    guard let rawWindows = copyAXAttribute(appElement, kAXWindowsAttribute, deadline: deadline) as? [AXUIElement] else {
        return probeResponse(requestID: requestID, status: "error", error: "windows_unavailable")
    }

    var rows: [[String: Any]] = []
    var observed = Set<String>()
    var timedOut = false
    for window in rawWindows.prefix(maximumAXNodes) {
        AXUIElementSetMessagingTimeout(window, 0.05)
        guard let title = copyAXAttribute(window, kAXTitleAttribute, deadline: deadline) as? String else {
            timedOut = Date() >= deadline
            if timedOut { break }
            continue
        }
        guard title == "Comuse Fixture \(scope.nonce)" else { continue }
        guard let reference = axReferences.windowReference(window, identity: identity, nonce: scope.nonce) else {
            return probeResponse(requestID: requestID, status: "error", error: "reference_limit_exceeded")
        }
        observed.insert(reference)
        rows.append(["ref": reference, "role": "window"])
    }
    let capped = rawWindows.count > maximumAXNodes
    let complete = !timedOut && !capped
    guard !rows.isEmpty else {
        return probeResponse(requestID: requestID, status: "error", error: "fixture_window_unavailable")
    }
    let stillSame = currentProcessMatches(identity) && rows.allSatisfy { row in
        guard let reference = row["ref"] as? String, let entry = axReferences.windows[reference] else { return false }
        return fixtureWindowStillMatches(entry.window, identity: identity, nonce: scope.nonce)
    }
    if complete && stillSame {
        axReferences.removeUnobservedWindows(identity: identity, nonce: scope.nonce, observed: observed)
    }
    let partial = !complete || !stillSame
    let windowCoverageReason: Any = stillSame ? NSNull() : "concurrent_change"
    let result: [String: Any] = [
        "process_start_ref": processStartReference,
        "windows": rows,
        "coverage": ["status": partial ? "partial" : "complete", "window_limit": maximumAXNodes, "window_count": rows.count, "deadline_ms": Int(maximumAXDuration * 1000), "timed_out": timedOut, "truncated": capped, "reason": windowCoverageReason],
    ]
    return probeResponse(requestID: requestID, status: partial ? "partial" : "completed", result: result)
}

@MainActor
private struct ObservedNode {
    let element: AXUIElement
    let parent: AXUIElement
    let parentIndex: Int?
    let depth: Int
    var role: String = "unknown"
    var identifier: String?
    var value: String?
    var valueStatus = "omitted"
    var childIndices: [Int] = []
    var visited = false
}

@MainActor
private func observeFixtureAccessibility(scope: ProbeScope, requestID: String, includeValues: Bool, requestedWindowReference: String) -> Data {
    guard scope.bundleID == fixtureBundleIdentifier,
          let identity = validateProcess(scope), AXIsProcessTrusted() else {
        return probeResponse(requestID: requestID, status: "error", error: "scope_or_permission_denied")
    }
    axReferences.prepareScope(identity, nonce: scope.nonce)
    guard let requestedWindow = axReferences.windows[requestedWindowReference],
          requestedWindow.nonce == scope.nonce,
          sameProcessIdentity(requestedWindow.identity, identity) else {
        return probeResponse(requestID: requestID, status: "error", error: "stale_window_reference")
    }
    let processStartReference = axReferences.processReference(identity)
    let appElement = AXUIElementCreateApplication(scope.pid)
    AXUIElementSetMessagingTimeout(appElement, 0.05)
    let windowDeadline = Date().addingTimeInterval(maximumAXDuration)
    guard let rawWindows = copyAXAttribute(appElement, kAXWindowsAttribute, deadline: windowDeadline) as? [AXUIElement] else {
        return probeResponse(requestID: requestID, status: "error", error: "fixture_window_unavailable")
    }
    var matchedWindow: AXUIElement?
    for candidate in rawWindows.prefix(maximumAXNodes) {
        AXUIElementSetMessagingTimeout(candidate, 0.05)
        if let title = copyAXAttribute(candidate, kAXTitleAttribute, deadline: windowDeadline) as? String,
           title == "Comuse Fixture \(scope.nonce)",
           CFEqual(candidate, requestedWindow.window) {
            matchedWindow = candidate
            break
        }
        if Date() >= windowDeadline { break }
    }
    guard let window = matchedWindow else {
        return probeResponse(requestID: requestID, status: "error", error: "fixture_window_unavailable")
    }
    guard let windowReference = axReferences.windowReference(window, identity: identity, nonce: scope.nonce) else {
        return probeResponse(requestID: requestID, status: "error", error: "reference_limit_exceeded")
    }
    guard windowReference == requestedWindowReference else {
        return probeResponse(requestID: requestID, status: "error", error: "scope_mismatch")
    }

    let deadline = Date().addingTimeInterval(maximumAXDuration)
    var nodes = [ObservedNode(element: window, parent: appElement, parentIndex: nil, depth: 0)]
    var stack = [0]
    var visited = 0
    var textBytes = 0
    var truncated = false
    while let index = stack.popLast() {
        if Date() >= deadline || visited >= maximumAXNodes || textBytes >= maximumAXTextBytes {
            truncated = true
            break
        }
        visited += 1
        AXUIElementSetMessagingTimeout(nodes[index].element, 0.05)
        nodes[index].visited = true
        nodes[index].role = (copyAXAttribute(nodes[index].element, kAXRoleAttribute, deadline: deadline) as? String) ?? "unknown"
        nodes[index].identifier = copyAXAttribute(nodes[index].element, kAXIdentifierAttribute, deadline: deadline) as? String
        if nodes[index].role == "unknown" { truncated = true }
        if let identifier = nodes[index].identifier, identifier.utf8.count <= 256 {
            let cost = identifier.utf8.count
            if textBytes + cost <= maximumAXTextBytes { textBytes += cost } else { truncated = true }
        }
        let allowedValueTarget =
            (nodes[index].identifier == "textfield" && nodes[index].role == (kAXTextFieldRole as String)) ||
            (nodes[index].identifier == "counter-value" && nodes[index].role == (kAXStaticTextRole as String))
        if includeValues, allowedValueTarget {
            let subrole = nodes[index].role == (kAXTextFieldRole as String)
                ? copyAXAttribute(nodes[index].element, kAXSubroleAttribute, deadline: deadline) as? String
                : nil
            if subrole != (kAXSecureTextFieldSubrole as String),
               let value = copyAXAttribute(nodes[index].element, kAXValueAttribute, deadline: deadline) as? String {
                let cost = value.utf8.count
                if textBytes + cost <= maximumAXTextBytes {
                    nodes[index].value = value
                    nodes[index].valueStatus = nodes[index].identifier == "textfield" ? "included_synthetic_normal" : "included_synthetic_counter"
                    textBytes += cost
                } else {
                    nodes[index].valueStatus = "omitted_limit"
                    truncated = true
                }
            } else {
                nodes[index].valueStatus = "omitted_protected_or_unavailable"
            }
        } else {
            nodes[index].valueStatus = "omitted"
        }
        guard let children = copyAXAttribute(nodes[index].element, kAXChildrenAttribute, deadline: deadline) as? [AXUIElement] else {
            truncated = true
            continue
        }
        if nodes[index].depth >= maximumAXDepth {
            if !children.isEmpty { truncated = true }
            continue
        }
        let available = max(0, maximumAXNodes - visited - stack.count)
        if children.count > available { truncated = true }
        let selectedChildren = Array(children.prefix(available))
        var childIndices: [Int] = []
        for child in selectedChildren {
            childIndices.append(nodes.count)
            nodes.append(ObservedNode(element: child, parent: nodes[index].element, parentIndex: index, depth: nodes[index].depth + 1))
        }
        nodes[index].childIndices = childIndices
        stack.append(contentsOf: childIndices.reversed())
    }

    let observationID = UUID().uuidString
    let windowStillMatches = fixtureWindowStillMatches(window, identity: identity, nonce: scope.nonce)
    let processStillMatches = currentProcessMatches(identity)
    let concurrentChange = !windowStillMatches || !processStillMatches
    if concurrentChange { truncated = true }

    var references: [Int: String] = [:]
    var observedReferences = Set<String>()
    for index in nodes.indices where nodes[index].visited {
        let parentReference = nodes[index].parentIndex.flatMap { references[$0] }
        guard let reference = axReferences.elementReference(nodes[index].element, parent: nodes[index].parent, parentReference: parentReference, window: window, windowReference: windowReference, identity: identity, nonce: scope.nonce, role: nodes[index].role, identifier: nodes[index].identifier) else {
            return probeResponse(requestID: requestID, status: "error", error: "reference_limit_exceeded")
        }
        references[index] = reference
        observedReferences.insert(reference)
    }
    var rows: [[String: Any]] = []
    for index in nodes.indices where nodes[index].visited {
        guard let reference = references[index] else { continue }
        let childReferences = nodes[index].childIndices.compactMap { references[$0] }
        let entry = axReferences.elements[reference]
        if var updated = entry {
            updated.childReferences = childReferences
            axReferences.elements[reference] = updated
        }
        var row: [String: Any] = [
            "ref": reference,
            "role": nodes[index].role,
            "value_status": nodes[index].valueStatus,
            "child_refs": childReferences,
        ]
        if let parentReference = nodes[index].parentIndex.flatMap({ references[$0] }) { row["parent_ref"] = parentReference }
        if index == 0 { row["parent_ref"] = NSNull() }
        if let identifier = nodes[index].identifier, identifier.utf8.count <= 256 { row["identifier"] = identifier }
        if let value = nodes[index].value { row["value"] = value }
        rows.append(row)
    }
    let rootReferences = nodes[0].visited ? [references[0]].compactMap { $0 } : []
    let canonicalRows: [[String: Any]] = nodes.indices.filter { nodes[$0].visited }.map { index in
        var canonical: [String: Any] = ["role": nodes[index].role]
        if let identifier = nodes[index].identifier, identifier.utf8.count <= 256 { canonical["identifier"] = identifier }
        if let value = nodes[index].value { canonical["value"] = value }
        canonical["parent_index"] = nodes[index].parentIndex.map { $0 as Any } ?? NSNull()
        canonical["child_indices"] = nodes[index].childIndices.filter { nodes[$0].visited }
        return canonical
    }
    let canonicalData = (try? JSONSerialization.data(withJSONObject: canonicalRows, options: [.sortedKeys])) ?? Data()
    let stateID = SHA256.hash(data: canonicalData).map { String(format: "%02x", $0) }.joined()
    let complete = !truncated && !concurrentChange && nodes.allSatisfy(\.visited)
    for reference in observedReferences {
        guard var entry = axReferences.elements[reference] else { continue }
        entry.stateID = stateID
        entry.stateComplete = complete
        entry.expiresAt = ContinuousClock().now.advanced(by: referenceLifetime)
        axReferences.elements[reference] = entry
    }
    if complete {
        axReferences.removeUnobservedElements(identity: identity, nonce: scope.nonce, windowReference: windowReference, observed: observedReferences)
    }
    let coverageReason: Any = concurrentChange ? "concurrent_change" : (truncated ? "bounded_or_ax_uncertainty" : NSNull())
    let result: [String: Any] = [
        "observation_id": observationID,
        "state_id": stateID,
        "process_start_ref": processStartReference,
        "window_ref": windowReference,
        "root_refs": rootReferences,
        "elements": rows,
        "coverage": ["status": complete ? "complete" : "truncated", "reason": coverageReason, "depth_limit": maximumAXDepth, "node_limit": maximumAXNodes, "text_byte_limit": maximumAXTextBytes, "deadline_ms": Int(maximumAXDuration * 1000), "visited": visited, "text_bytes": textBytes, "truncated": truncated],
    ]
    return probeResponse(requestID: requestID, status: complete ? "completed" : "partial", result: result)
}

@MainActor
private func validateProcess(_ scope: ProbeScope) -> ProcessIdentity? {
    guard let application = NSRunningApplication(processIdentifier: scope.pid),
          application.processIdentifier == scope.pid,
          application.bundleIdentifier == fixtureBundleIdentifier,
          scope.bundleID == application.bundleIdentifier,
          let launchDate = application.launchDate else { return nil }
    return ProcessIdentity(application: application, pid: scope.pid, bundleID: fixtureBundleIdentifier, launchDate: launchDate)
}

@MainActor
private func processIdentity(pid: pid_t) -> ProcessIdentity? {
    guard let application = NSRunningApplication(processIdentifier: pid),
          application.bundleIdentifier == fixtureBundleIdentifier,
          let launchDate = application.launchDate else { return nil }
    return ProcessIdentity(application: application, pid: pid, bundleID: fixtureBundleIdentifier, launchDate: launchDate)
}


@MainActor
private func sameProcessIdentity(_ current: ProcessIdentity, _ expected: ProcessIdentity) -> Bool {
    current.pid == expected.pid && current.bundleID == expected.bundleID && current.launchDate == expected.launchDate && current.application.isEqual(expected.application)
}

@MainActor
private func currentProcessMatches(_ expected: ProcessIdentity) -> Bool {
    guard let current = processIdentity(pid: expected.pid) else { return false }
    return sameProcessIdentity(current, expected)
}

@MainActor
private func fixtureWindowStillMatches(_ window: AXUIElement, identity: ProcessIdentity, nonce: String) -> Bool {
    guard let currentIdentity = processIdentity(pid: identity.pid), sameProcessIdentity(currentIdentity, identity) else { return false }
    let deadline = Date().addingTimeInterval(maximumAXDuration)
    let appElement = AXUIElementCreateApplication(identity.pid)
    AXUIElementSetMessagingTimeout(appElement, 0.05)
    guard let windows = copyAXAttribute(appElement, kAXWindowsAttribute, deadline: deadline) as? [AXUIElement] else { return false }
    for candidate in windows.prefix(maximumAXNodes) where CFEqual(candidate, window) {
        AXUIElementSetMessagingTimeout(candidate, 0.05)
        return (copyAXAttribute(candidate, kAXTitleAttribute, deadline: deadline) as? String) == "Comuse Fixture \(nonce)"
    }
    return false
}

@MainActor
private func sameAXElement(_ element: AXUIElement, parent: AXUIElement) -> Bool {
    AXUIElementSetMessagingTimeout(element, 0.05)
    AXUIElementSetMessagingTimeout(parent, 0.05)
    guard let currentParent = copyAXAttribute(element, kAXParentAttribute) as? AXUIElement,
          CFEqual(currentParent, parent),
          let children = copyAXAttribute(parent, kAXChildrenAttribute) as? [AXUIElement] else { return false }
    return children.contains(where: { CFEqual($0, element) })
}

@MainActor
private func currentAXClassificationMatches(_ entry: AXReferenceStore.ElementEntry) -> Bool {
    guard let role = copyAXAttribute(entry.element, kAXRoleAttribute) as? String,
          role == entry.role,
          (copyAXAttribute(entry.element, kAXIdentifierAttribute) as? String) == entry.identifier else { return false }
    if role == (kAXTextFieldRole as String),
       let subrole = copyAXAttribute(entry.element, kAXSubroleAttribute) as? String,
       subrole == (kAXSecureTextFieldSubrole as String) {
        return false
    }
    return true
}

@MainActor
private func copyAXAttribute(_ element: AXUIElement, _ attribute: String, deadline: Date? = nil) -> CFTypeRef? {
    if let deadline {
        let remaining = deadline.timeIntervalSinceNow
        guard remaining > 0 else { return nil }
        AXUIElementSetMessagingTimeout(element, Float(min(0.05, remaining)))
    }
    var value: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, attribute as CFString, &value) == .success else { return nil }
    return value
}

private func probeResponse(requestID: String, status: String, error: String? = nil, result: [String: Any]? = nil) -> Data {
    var envelope: [String: Any] = ["schema_version": 1, "request_id": requestID, "status": status]
    if let error {
        envelope["error"] = ["code": error]
    } else {
        envelope["error"] = NSNull()
    }
    envelope["result"] = result ?? [:]
    guard let data = try? JSONSerialization.data(withJSONObject: envelope, options: [.sortedKeys]) else { return Data() }
    return data
}
