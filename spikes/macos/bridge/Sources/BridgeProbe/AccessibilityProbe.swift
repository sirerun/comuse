import AppKit
import ApplicationServices
import CoreGraphics
import Foundation

private let accessibilityRequestLimit = 32 * 1024
private let accessibilityResponseLimit = 64 * 1024
private let fixtureBundleIdentifier = "com.sirerun.comuse.fixture"
private let maximumAXDepth = 16
private let maximumAXNodes = 256
private let maximumAXTextBytes = 16 * 1024
private let maximumAXDuration: TimeInterval = 0.25

private struct ProbeRequest {
    let requestID: String
    let operation: String
    let scope: ProbeScope?
    let includeValues: Bool
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

@MainActor
private final class AXReferenceStore {
    struct WindowEntry {
        let reference: String
        let identity: ProcessIdentity
        let nonce: String
        let window: AXUIElement
    }

    struct ElementEntry {
        let reference: String
        let identity: ProcessIdentity
        let nonce: String
        let window: AXUIElement
        let element: AXUIElement
        let parent: AXUIElement
    }

    var windows: [String: WindowEntry] = [:]
    var elements: [String: ElementEntry] = [:]
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
        response = observeFixtureAccessibility(scope: scope, requestID: request.requestID, includeValues: request.includeValues)
    default:
        response = probeResponse(requestID: request.requestID, status: "error", error: "unsupported_operation")
    }
    return response.count <= accessibilityResponseLimit
        ? response
        : probeResponse(requestID: request.requestID, status: "error", error: "response_limit_exceeded")
}

@MainActor
func resolveAccessibilityElement(_ ref: String, pid: Int32) -> AXUIElement? {
    guard let entry = axReferences.elements[ref], entry.identity.pid == pid,
          let currentIdentity = processIdentity(pid: pid), sameProcessIdentity(currentIdentity, entry.identity),
          AXIsProcessTrusted(), fixtureWindowStillMatches(entry.window, identity: entry.identity, nonce: entry.nonce),
          sameAXElement(entry.element, parent: entry.parent) else {
        axReferences.elements.removeValue(forKey: ref)
        return nil
    }
    return entry.element
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
              pidNumber.int64Value > 0, pidNumber.int64Value <= Int32.max,
              let bundleID = rawScope["bundle_id"] as? String,
              let nonce = rawScope["fixture_nonce"] as? String,
              !nonce.isEmpty, nonce.utf8.count <= 128 else { return nil }
        scope = ProbeScope(pid: pid_t(pidNumber.int32Value), bundleID: bundleID, nonce: nonce)
    } else if operation != "doctor" {
        return nil
    }
    return ProbeRequest(requestID: requestID, operation: operation, scope: scope, includeValues: object["include_values"] as? Bool == true)
}

@MainActor
private func listFixtureWindows(scope: ProbeScope, requestID: String) -> Data {
    guard scope.bundleID == fixtureBundleIdentifier,
          let identity = validateProcess(scope), AXIsProcessTrusted() else {
        return probeResponse(requestID: requestID, status: "error", error: "scope_or_permission_denied")
    }
    let appElement = AXUIElementCreateApplication(scope.pid)
    AXUIElementSetMessagingTimeout(appElement, 0.05)
    let deadline = Date().addingTimeInterval(maximumAXDuration)
    guard let rawWindows = copyAXAttribute(appElement, kAXWindowsAttribute, deadline: deadline) as? [AXUIElement] else {
        return probeResponse(requestID: requestID, status: "error", error: "windows_unavailable")
    }

    resetAllReferences()
    var rows: [[String: Any]] = []
    var timedOut = false
    for window in rawWindows.prefix(maximumAXNodes) {
        AXUIElementSetMessagingTimeout(window, 0.05)
        guard let title = copyAXAttribute(window, kAXTitleAttribute, deadline: deadline) as? String else {
            timedOut = Date() >= deadline
            if timedOut { break }
            continue
        }
        guard title == "Comuse Fixture \(scope.nonce)" else { continue }
        let reference = UUID().uuidString
        axReferences.windows[reference] = .init(reference: reference, identity: identity, nonce: scope.nonce, window: window)
        rows.append(["ref": reference, "role": "window"])
    }
    let capped = rawWindows.count > maximumAXNodes
    let partial = timedOut || capped
    guard !rows.isEmpty else {
        return probeResponse(requestID: requestID, status: "error", error: "fixture_window_unavailable")
    }
    return probeResponse(requestID: requestID, status: partial ? "partial" : "completed", result: [
        "windows": rows,
        "coverage": ["status": partial ? "partial" : "complete", "window_limit": maximumAXNodes, "window_count": rows.count, "deadline_ms": Int(maximumAXDuration * 1000), "timed_out": timedOut, "truncated": capped],
    ])
}

@MainActor
private func observeFixtureAccessibility(scope: ProbeScope, requestID: String, includeValues: Bool) -> Data {
    guard scope.bundleID == fixtureBundleIdentifier,
          let identity = validateProcess(scope), AXIsProcessTrusted() else {
        return probeResponse(requestID: requestID, status: "error", error: "scope_or_permission_denied")
    }
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
           title == "Comuse Fixture \(scope.nonce)" {
            matchedWindow = candidate
            break
        }
        if Date() >= windowDeadline { break }
    }
    guard let window = matchedWindow else {
        return probeResponse(requestID: requestID, status: "error", error: "fixture_window_unavailable")
    }

    resetAllReferences()
    let deadline = Date().addingTimeInterval(maximumAXDuration)
    var rows: [[String: Any]] = []
    var visited = 0
    var textBytes = 0
    var truncated = false
    var stack: [(AXUIElement, AXUIElement, Int)] = [(window, appElement, 0)]
    while let (element, parent, depth) = stack.popLast() {
        if Date() >= deadline || visited >= maximumAXNodes || textBytes >= maximumAXTextBytes {
            truncated = true
            break
        }
        visited += 1
        AXUIElementSetMessagingTimeout(element, 0.05)
        let role = (copyAXAttribute(element, kAXRoleAttribute, deadline: deadline) as? String) ?? "unknown"
        let identifier = copyAXAttribute(element, kAXIdentifierAttribute, deadline: deadline) as? String
        var row: [String: Any] = ["role": role]
        if let identifier, identifier.utf8.count <= 256 {
            let cost = identifier.utf8.count
            if textBytes + cost <= maximumAXTextBytes {
                row["identifier"] = identifier
                textBytes += cost
            } else {
                truncated = true
            }
        }
        if includeValues, identifier == "textfield", role == (kAXTextFieldRole as String) {
            let subrole = copyAXAttribute(element, kAXSubroleAttribute, deadline: deadline) as? String
            if subrole != (kAXSecureTextFieldSubrole as String),
               let value = copyAXAttribute(element, kAXValueAttribute, deadline: deadline) as? String {
                let cost = value.utf8.count
                if textBytes + cost <= maximumAXTextBytes {
                    row["value"] = value
                    row["value_status"] = "included_synthetic_normal"
                    textBytes += cost
                } else {
                    row["value_status"] = "omitted_limit"
                    truncated = true
                }
            } else {
                row["value_status"] = "omitted_protected_or_unavailable"
            }
        } else {
            row["value_status"] = "omitted"
        }
        let reference = UUID().uuidString
        axReferences.elements[reference] = .init(reference: reference, identity: identity, nonce: scope.nonce, window: window, element: element, parent: parent)
        row["ref"] = reference
        rows.append(row)

        guard depth < maximumAXDepth else {
            truncated = true
            continue
        }
        guard let children = copyAXAttribute(element, kAXChildrenAttribute, deadline: deadline) as? [AXUIElement] else {
            truncated = true
            continue
        }
        let available = max(0, maximumAXNodes - visited)
        if children.count > available { truncated = true }
        for child in children.prefix(available).reversed() {
            stack.append((child, element, depth + 1))
        }
    }
    return probeResponse(requestID: requestID, status: truncated ? "partial" : "completed", result: [
        "elements": rows,
        "coverage": [
            "status": truncated ? "truncated" : "complete",
            "depth_limit": maximumAXDepth,
            "node_limit": maximumAXNodes,
            "text_byte_limit": maximumAXTextBytes,
            "deadline_ms": Int(maximumAXDuration * 1000),
            "visited": visited,
            "text_bytes": textBytes,
            "truncated": truncated,
        ],
    ])
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

@MainActor
private func resetAllReferences() {
    axReferences.windows.removeAll(keepingCapacity: true)
    axReferences.elements.removeAll(keepingCapacity: true)
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
