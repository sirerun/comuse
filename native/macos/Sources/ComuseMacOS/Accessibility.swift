import ApplicationServices
import CoreFoundation
import CryptoKit
import Foundation

enum NativeReferenceKind: Equatable { case window, element }

struct NativeReference {
    let element: AXUIElement
    let process: NativeProcess
    let windowRef: String?
    let kind: NativeReferenceKind
    var lastSeen: TimeInterval
}

struct NativeSnapshot {
    let stateID: String
    let windowRef: String
    let process: NativeProcess
    let digest: String
    let complete: Bool
    let coverageReason: String
    let refs: Set<String>
    let createdAt: TimeInterval
    let byteCount: Int
}

private struct TraversalNode {
    var ref: String
    var parentRef: String?
    var order: Int
    var role: String
    var label: String?
    var value: String?
    var enabled: Bool?
    var actions: [String]
    var classification: String

    var json: [String: Any] {
        var result: [String: Any] = [
            "ref": ref, "order": order, "role": role,
            "actions": actions, "classification": classification
        ]
        if let parentRef { result["parent_ref"] = parentRef }
        if let label { result["label"] = label }
        if let value { result["value"] = value }
        if let enabled { result["enabled"] = enabled }
        return result
    }
}

extension NativeRuntime {
    var references: [String: NativeReference] {
        get { ReferenceStore.shared.get(runtimeID: id) }
        set { ReferenceStore.shared.set(runtimeID: id, value: newValue) }
    }

    var snapshots: [String: NativeSnapshot] {
        get { SnapshotStore.shared.get(runtimeID: id) }
        set { SnapshotStore.shared.set(runtimeID: id, value: newValue) }
    }

    func retain(_ element: AXUIElement, process: NativeProcess, windowRef: String?, kind: NativeReferenceKind) throws -> String {
        var current = references
        let now = ProcessInfo.processInfo.systemUptime
        current = current.filter { now - $0.value.lastSeen <= 120 }
        for (ref, entry) in current where entry.process == process && entry.windowRef == windowRef && entry.kind == kind && CFEqual(entry.element, element) {
            current[ref]?.lastSeen = now
            references = current
            return ref
        }
        guard current.count < 8192 else { references = current; throw ProbeFailure(code: "budget_exceeded") }
        let ref = UUID().uuidString.lowercased()
        current[ref] = NativeReference(element: element, process: process, windowRef: windowRef, kind: kind, lastSeen: now)
        references = current
        return ref
    }

    func resolve(_ ref: String, kind: NativeReferenceKind, windowRef: String? = nil) throws -> NativeReference {
        guard validOpaque(ref), let entry = references[ref], entry.kind == kind,
              ProcessInfo.processInfo.systemUptime - entry.lastSeen <= 120,
              windowRef == nil || entry.windowRef == windowRef else { throw ProbeFailure(code: "state_expired") }
        let current = try matchingProcess(entry.process)
        guard current == entry.process else { throw ProbeFailure(code: "element_stale") }
        var pid: pid_t = 0
        guard AXUIElementGetPid(entry.element, &pid) == .success, pid == entry.process.pid else { throw ProbeFailure(code: "element_stale") }
        var refreshed = entry; refreshed.lastSeen = ProcessInfo.processInfo.systemUptime
        var all = references; all[ref] = refreshed; references = all
        return refreshed
    }

    func observeWindow(_ request: NativeRequest, requestID: UInt64, retainSnapshot: Bool = true) throws -> [String: Any] {
        try checkPermission()
        guard let windowRef = request.windowRef else { throw ProbeFailure(code: "invalid_request") }
        let root = try resolve(windowRef, kind: .window)
        let freshProcess = try matchingProcess(root.process)
        let budget = boundedBudget(request.budget)
        let deadline = ProcessInfo.processInfo.systemUptime + budget.timeout
        if let app = NSRunningApplication(processIdentifier: freshProcess.pid) {
            guard AXUIElementSetMessagingTimeout(AXUIElementCreateApplication(freshProcess.pid), Float(max(0.05, min(budget.timeout, 1.0)))) == .success else {
                throw ProbeFailure(code: "backend_unavailable")
            }
            _ = app
        }
        var output: [TraversalNode] = []
        var refs = Set<String>()
        var stack: [(AXUIElement, String?, Int, Int)] = [(root.element, nil, 0, 0)]
        var totalOutputBytes = 0
        var complete = true
        var reason = ""
        while let (element, parentRef, order, depth) = stack.popLast() {
            do { try checkDeadline(requestID, deadline: deadline) }
            catch let failure as ProbeFailure {
                if failure.code == "cancelled" { throw failure }
                complete = false
                reason = failure.code
                break
            }
            if output.count >= budget.maxNodes { complete = false; reason = "node_limit"; break }
            if depth >= budget.maxDepth {
                let childResult = children(of: element)
                if !childResult.values.isEmpty || childResult.failed {
                    complete = false
                    if reason.isEmpty { reason = "depth_limit" }
                }
                continue
            }
            var pid: pid_t = 0
            guard AXUIElementGetPid(element, &pid) == .success, pid == freshProcess.pid else {
                complete = false; reason = "scope_changed"; break
            }
            let role = stringAttribute(element, kAXRoleAttribute) ?? ""
            let subrole = stringAttribute(element, kAXSubroleAttribute)
            let classification = classify(role: role, subrole: subrole)
            if classification == "normal" {
                do {
                    let ref = try retain(element, process: freshProcess, windowRef: windowRef, kind: .element)
                    refs.insert(ref)
                    let label = (stringAttribute(element, kAXTitleAttribute) ?? stringAttribute(element, kAXDescriptionAttribute))
                        .map { String($0.prefix(4096)) }
                    let value = config.allowValues ? allowedValue(element, classification: classification, role: role) : nil
                    let enabled = boolAttribute(element, kAXEnabledAttribute)
                    let actions = advertisedActions(element, role: role)
                    let node = TraversalNode(ref: ref, parentRef: parentRef, order: order, role: role,
                                             label: label, value: value, enabled: enabled,
                                             actions: actions, classification: classification)
                    let nodeBytes = try JSONSerialization.data(withJSONObject: node.json, options: [.fragmentsAllowed, .sortedKeys]).count
                    if totalOutputBytes + nodeBytes > budget.maxBytes {
                        refs.remove(ref)
                        complete = false; reason = "byte_limit"; break
                    }
                    output.append(node)
                    totalOutputBytes += nodeBytes
                    if depth < budget.maxDepth {
                        let childResult = children(of: element)
                        if childResult.failed {
                            complete = false
                            if reason.isEmpty { reason = "child_read_unavailable" }
                        }
                        for (index, child) in childResult.values.enumerated().reversed() {
                            stack.append((child, ref, index, depth + 1))
                        }
                    }
                } catch let failure as ProbeFailure { complete = false; reason = failure.code; break }
                catch { complete = false; reason = "backend_unavailable"; break }
            } else {
                // Do not visit children under secure or uncertain containers.
                complete = false
                if reason.isEmpty { reason = classification == "secure" ? "protected_content" : "unknown_content" }
                continue
            }
        }
        let endIdentity = try matchingProcess(freshProcess)
        guard endIdentity == freshProcess else { throw ProbeFailure(code: "element_stale") }
        if complete {
            // A complete traversal is the only evidence that an old element left the tree.
            let all = references.filter { ref, entry in entry.windowRef != windowRef || refs.contains(ref) }
            references = all
        }
        let rows = output.sorted { $0.ref < $1.ref }.map(\.json)
        let digest = try projectionDigest(windowRef: windowRef, rows: rows, complete: complete, reason: reason)
        lastProjectionDigest = digest
        let stateID = UUID().uuidString.lowercased()
        let storedBytes = try JSONSerialization.data(withJSONObject: rows, options: [.fragmentsAllowed, .sortedKeys]).count
        if retainSnapshot {
            var generations = snapshots.filter { ProcessInfo.processInfo.systemUptime - $0.value.createdAt <= 120 }
            while generations.values.filter({ $0.windowRef == windowRef }).count >= 8 ||
                    generations.values.reduce(0, { $0 + $1.byteCount }) + storedBytes > 4 * 1024 * 1024 {
                guard let oldest = generations.values.min(by: { $0.createdAt < $1.createdAt }) else {
                    throw ProbeFailure(code: "budget_exceeded")
                }
                generations.removeValue(forKey: oldest.stateID)
            }
            let snapshot = NativeSnapshot(stateID: stateID, windowRef: windowRef, process: freshProcess,
                                          digest: digest, complete: complete, coverageReason: reason, refs: refs,
                                          createdAt: ProcessInfo.processInfo.systemUptime, byteCount: storedBytes)
            generations[stateID] = snapshot
            snapshots = generations
        }
        return [
            "window_ref": windowRef, "state_id": retainSnapshot ? stateID : "",
            "observed_at": ISO8601DateFormatter().string(from: Date()),
            "elements": rows,
            "coverage": ["complete": complete, "reason": reason]
        ]
    }

    func readScopedElement(_ request: NativeRequest, requestID: UInt64) throws -> [String: Any] {
        try checkPermission()
        guard let windowRef = request.windowRef, let elementRef = request.elementRef,
              let stateID = request.stateID else { throw ProbeFailure(code: "policy_refused") }
        let snapshot = try validateSnapshot(stateID, windowRef: windowRef, requestID: requestID, budget: request.budget)
        guard snapshot.refs.contains(elementRef) else { throw ProbeFailure(code: "element_stale") }
        let entry = try resolve(elementRef, kind: .element, windowRef: windowRef)
        let role = stringAttribute(entry.element, kAXRoleAttribute) ?? ""
        let subrole = stringAttribute(entry.element, kAXSubroleAttribute)
        guard classify(role: role, subrole: subrole) == "normal", role == "AXTextField" else {
            throw ProbeFailure(code: "policy_refused")
        }
        guard let text = allowedValue(entry.element, classification: "normal", role: role) else {
            throw ProbeFailure(code: "backend_unavailable")
        }
        return ["window_ref": windowRef, "element_ref": elementRef, "state_id": stateID, "text": text]
    }

    func validateSnapshot(_ stateID: String, windowRef: String, requestID: UInt64, budget: NativeBudget?) throws -> NativeSnapshot {
        guard validOpaque(stateID), let prior = snapshots[stateID], prior.windowRef == windowRef,
              ProcessInfo.processInfo.systemUptime - prior.createdAt <= 120 else { throw ProbeFailure(code: "state_expired") }
        let freshRequest = NativeRequest(schemaVersion: 1, requestID: "revalidate", operation: "observe",
                                         windowRef: windowRef, elementRef: nil, stateID: nil,
                                         budget: budget, action: nil)
        _ = try observeWindow(freshRequest, requestID: requestID, retainSnapshot: false)
        guard let freshDigest = lastProjectionDigest, freshDigest == prior.digest else { throw ProbeFailure(code: "state_expired") }
        return prior
    }

    func executeScopedAction(_ request: NativeRequest, requestID: UInt64) throws -> [String: Any] {
        guard let action = request.action, validOpaque(action.id), validOpaque(action.windowRef),
              validOpaque(action.elementRef), validOpaque(action.stateID),
              action.text.utf8.count <= 8192 else { throw ProbeFailure(code: "invalid_request") }
        switch action.kind {
        case "press" where action.text.isEmpty: break
        case "replace": break
        case "insert" where !action.text.isEmpty: break
        default: throw ProbeFailure(code: "invalid_request")
        }
        throw ProbeFailure(code: "unsupported")
    }
}

private func classify(role: String, subrole: String?) -> String {
    if subrole == "AXSecureTextField" || role == "AXSecureTextField" { return "secure" }
    guard let subrole else { return "unknown" }
    let expected: [String: Set<String>] = [
        "AXStaticText": ["AXStaticText"], "AXTextField": ["AXTextField"],
        "AXButton": ["AXButton", "AXDisclosureTriangle", "AXPopUpButton"],
        "AXCheckBox": ["AXCheckBox"], "AXRadioButton": ["AXRadioButton"],
        "AXPopUpButton": ["AXPopUpButton"], "AXComboBox": ["AXComboBox"],
        "AXGroup": ["AXGroup"], "AXWindow": ["AXStandardWindow", "AXDialog"],
        "AXList": ["AXList"], "AXTable": ["AXTable"], "AXRow": ["AXRow"],
        "AXCell": ["AXCell"], "AXScrollArea": ["AXScrollArea"],
        "AXOutline": ["AXOutline"], "AXWebArea": ["AXWebArea"]
    ]
    return expected[role]?.contains(subrole) == true ? "normal" : "unknown"
}

private func copyAttribute(_ element: AXUIElement, _ attribute: String) -> CFTypeRef? {
    var value: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, attribute as CFString, &value) == .success else { return nil }
    return value
}

func stringAttribute(_ element: AXUIElement, _ attribute: String) -> String? {
    guard let value = copyAttribute(element, attribute) else { return nil }
    if let string = value as? String { return string }
    if let string = value as? NSAttributedString { return string.string }
    return nil
}

private func boolAttribute(_ element: AXUIElement, _ attribute: String) -> Bool? {
    guard let value = copyAttribute(element, attribute), CFGetTypeID(value) == CFBooleanGetTypeID() else { return nil }
    return (value as! NSNumber).boolValue
}

private func allowedValue(_ element: AXUIElement, classification: String, role: String) -> String? {
    guard classification == "normal", role == "AXTextField",
          let value = copyAttribute(element, kAXValueAttribute as String) else { return nil }
    if let text = value as? String { return String(text.prefix(8192)) }
    if let text = value as? NSAttributedString { return String(text.string.prefix(8192)) }
    return nil
}

private func children(of element: AXUIElement) -> (values: [AXUIElement], failed: Bool) {
    var raw: CFTypeRef?
    let status = AXUIElementCopyAttributeValue(element, kAXChildrenAttribute as CFString, &raw)
    if status == .success { return ((raw as? [AXUIElement]) ?? [], false) }
    if status == .noValue || status == .attributeUnsupported { return ([], false) }
    return ([], true)
}

private func advertisedActions(_ element: AXUIElement, role: String) -> [String] {
    var names: CFArray?
    guard AXUIElementCopyActionNames(element, &names) == .success,
          let actions = names as? [String] else { return [] }
    if role == "AXButton", actions.contains(kAXPressAction as String) { return ["press"] }
    if role == "AXTextField" { return ["replace", "insert"] }
    return []
}

private func boundedBudget(_ input: NativeBudget?) -> (maxDepth: Int, maxNodes: Int, maxBytes: Int, timeout: TimeInterval) {
    let depth = min(max(input?.maxDepth ?? 16, 1), 64)
    let nodes = min(max(input?.maxNodes ?? 256, 1), 4096)
    let bytes = min(max(input?.maxBytes ?? 32768, 1024), 65536)
    let timeout = min(max(Double(input?.timeoutNanoseconds ?? 250_000_000) / 1_000_000_000, 0.01), 3.0)
    return (depth, nodes, bytes, timeout)
}

private func projectionDigest(windowRef: String, rows: [[String: Any]], complete: Bool, reason: String) throws -> String {
    let canonical = try JSONSerialization.data(withJSONObject: ["window_ref": windowRef, "complete": complete,
                                                                "reason": reason, "elements": rows],
                                                options: [.fragmentsAllowed, .sortedKeys])
    return SHA256.hash(data: canonical).map { String(format: "%02x", $0) }.joined()
}

private func validOpaque(_ value: String) -> Bool {
    guard !value.isEmpty, value.utf8.count <= 128 else { return false }
    return value.utf8.allSatisfy { byte in
        (48...57).contains(byte) || (65...90).contains(byte) || (97...122).contains(byte) || byte == 45 || byte == 46 || byte == 95
    }
}

@MainActor final class ReferenceStore {
    static let shared = ReferenceStore()
    private var values: [UInt64: [String: NativeReference]] = [:]
    func get(runtimeID: UInt64) -> [String: NativeReference] { values[runtimeID] ?? [:] }
    func set(runtimeID: UInt64, value: [String: NativeReference]) { values[runtimeID] = value }
    func remove(runtimeID: UInt64) { values.removeValue(forKey: runtimeID) }
}

@MainActor final class SnapshotStore {
    static let shared = SnapshotStore()
    private var values: [UInt64: [String: NativeSnapshot]] = [:]
    func get(runtimeID: UInt64) -> [String: NativeSnapshot] { values[runtimeID] ?? [:] }
    func set(runtimeID: UInt64, value: [String: NativeSnapshot]) { values[runtimeID] = value }
    func remove(runtimeID: UInt64) { values.removeValue(forKey: runtimeID) }
}
