import AppKit
import ApplicationServices
import CoreFoundation
import CoreGraphics
import CryptoKit
import Darwin
import Foundation

enum NativeReferenceKind: Equatable { case window, element }
// AX attributes are untrusted CF values; inspect their runtime type before
// invoking AXValue APIs on a retained pointer.
func nativeAXValue(_ raw: CFTypeRef) -> AXValue? {
    guard CFGetTypeID(raw) == AXValueGetTypeID() else { return nil }
    return unsafeBitCast(raw, to: AXValue.self)
}

func nativeAXMessagingTimeout(remaining: TimeInterval) -> Float? {
    guard remaining.isFinite, remaining > 0 else { return nil }
    var timeout = Float(remaining)
    guard timeout.isFinite, timeout > 0 else { return nil }
    if Double(timeout) > remaining { timeout = timeout.nextDown }
    return timeout.isFinite && timeout > 0 && Double(timeout) <= remaining ? timeout : nil
}

/// Configures the exact AX object immediately before its synchronous IPC.
/// The injected clock/configurer are also used by synthetic tests; production
/// callers supply the system clock and AXUIElementSetMessagingTimeout.
func nativeAXDeadlineIPC<T>(_ element: AXUIElement, deadline: TimeInterval,
                            now: () -> TimeInterval = { ProcessInfo.processInfo.systemUptime },
                            configure: (AXUIElement, Float) -> AXError = { AXUIElementSetMessagingTimeout($0, $1) },
                            operation: () throws -> T) throws -> T {
    let remaining = deadline - now()
    guard let timeout = nativeAXMessagingTimeout(remaining: remaining) else {
        throw ProbeFailure(code: "budget_exceeded")
    }
    guard configure(element, timeout) == .success else { throw ProbeFailure(code: "backend_unavailable") }
    return try operation()
}


func nativeElementIdentityIsCurrent(actualPID: Int32, expected: NativeProcess, current: NativeProcess) -> Bool {
    actualPID > 0 && actualPID == expected.pid && current == expected
}

struct NativeReference {
    let element: AXUIElement
    let process: NativeProcess
    let windowRef: String?
    let kind: NativeReferenceKind
    var lastSeen: TimeInterval
    let desktopGeneration: UInt64

    init(element: AXUIElement, process: NativeProcess, windowRef: String?, kind: NativeReferenceKind,
         lastSeen: TimeInterval, desktopGeneration: UInt64 = 0) {
        self.element = element; self.process = process; self.windowRef = windowRef
        self.kind = kind; self.lastSeen = lastSeen; self.desktopGeneration = desktopGeneration
    }
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
    let desktopGeneration: UInt64

    init(stateID: String, windowRef: String, process: NativeProcess, digest: String, complete: Bool,
         coverageReason: String, refs: Set<String>, createdAt: TimeInterval, byteCount: Int,
         desktopGeneration: UInt64 = 0) {
        self.stateID = stateID; self.windowRef = windowRef; self.process = process; self.digest = digest
        self.complete = complete; self.coverageReason = coverageReason; self.refs = refs
        self.createdAt = createdAt; self.byteCount = byteCount; self.desktopGeneration = desktopGeneration
    }
}

private struct TraversalNode {
    var ref: String
    var parentRef: String?
    var order: Int
    var role: String
    var label: String?
    var value: String?
    var enabled: Bool?
    var checked: Bool?
    var selected: Bool?
    var focused: Bool?
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
        if let checked { result["checked"] = checked }
        if let selected { result["selected"] = selected }
        if let focused { result["focused"] = focused }
        return result
    }
}

struct BoundedText {
    let text: String
    let truncated: Bool
}

func boundedUTF8Prefix(_ input: String, byteLimit: Int) -> BoundedText {
    var scalars = String.UnicodeScalarView()
    var byteCount = 0
    for scalar in input.unicodeScalars {
        let count = scalar.utf8.count
        if byteCount + count > byteLimit {
            return BoundedText(text: String(scalars), truncated: true)
        }
        scalars.append(scalar)
        byteCount += count
    }
    return BoundedText(text: String(scalars), truncated: false)
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

    func retain(_ element: AXUIElement, process: NativeProcess, windowRef: String?, kind: NativeReferenceKind,
                deadline: TimeInterval) throws -> String {
        let currentProcess = try matchingProcess(process)
        var actualPID: pid_t = 0
        guard currentProcess == process,
              (try nativeAXDeadlineIPC(element, deadline: deadline) {
                  AXUIElementGetPid(element, &actualPID)
              }) == .success,
              actualPID == process.pid,
              nativeElementIdentityIsCurrent(actualPID: Int32(actualPID), expected: process, current: currentProcess) else {
            throw ProbeFailure(code: "element_stale")
        }
        let desktopGeneration = try refreshDesktopGeneration()
        guard ProcessInfo.processInfo.systemUptime < deadline else { throw ProbeFailure(code: "budget_exceeded") }
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
        current[ref] = NativeReference(element: element, process: process, windowRef: windowRef, kind: kind,
                                       lastSeen: now, desktopGeneration: desktopGeneration)
        references = current
        return ref
    }

    func resolve(_ ref: String, kind: NativeReferenceKind, windowRef: String? = nil,
                 deadline: TimeInterval) throws -> NativeReference {
        let desktopGeneration = try refreshDesktopGeneration()
        guard validOpaque(ref), let entry = references[ref], entry.kind == kind,
              desktopReferenceIsCurrent(issued: entry.desktopGeneration, current: desktopGeneration),
              ProcessInfo.processInfo.systemUptime - entry.lastSeen <= 120,
              windowRef == nil || entry.windowRef == windowRef else { throw ProbeFailure(code: "state_expired") }
        let current = try matchingProcess(entry.process)
        guard current == entry.process else { throw ProbeFailure(code: "element_stale") }
        var pid: pid_t = 0
        guard (try nativeAXDeadlineIPC(entry.element, deadline: deadline) {
            AXUIElementGetPid(entry.element, &pid)
        }) == .success, pid == entry.process.pid else { throw ProbeFailure(code: "element_stale") }
        var refreshed = entry; refreshed.lastSeen = ProcessInfo.processInfo.systemUptime
        var all = references; all[ref] = refreshed; references = all
        return refreshed
    }

    func observeWindow(_ request: NativeRequest, requestID: UInt64, retainSnapshot: Bool = true,
                       deadline: TimeInterval) throws -> [String: Any] {
        try checkPermission()
        let desktopGeneration = try refreshDesktopGeneration()
        guard let windowRef = request.windowRef else { throw ProbeFailure(code: "invalid_request") }
        let budget = try boundedBudget(request.budget)
        let root = try resolve(windowRef, kind: .window, deadline: deadline)
        let freshProcess = try matchingProcess(root.process)
        var output: [TraversalNode] = []
        var refs = Set<String>()
        var stack: [(AXUIElement, String?, Int, Int)] = [(root.element, nil, 0, 0)]
        var visitedCount = 0
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
            if visitedCount >= budget.maxNodes { complete = false; reason = "node_limit"; break }
            visitedCount += 1
            var pid: pid_t = 0
            guard (try nativeAXDeadlineIPC(element, deadline: deadline) {
                AXUIElementGetPid(element, &pid)
            }) == .success, pid == freshProcess.pid else {
                complete = false; reason = "scope_changed"; break
            }
            if !depthIsIncluded(depth, maximum: budget.maxDepth) {
                // This frontier item is omitted by the depth bound even when
                // it is a leaf. Do not inspect descendants past this point.
                complete = false
                if reason.isEmpty { reason = "depth_limit" }
                continue
            }
            let role = stringAttribute(element, kAXRoleAttribute, deadline: deadline) ?? ""
            let subrole = stringAttribute(element, kAXSubroleAttribute, deadline: deadline)
            let classification = classify(role: role, subrole: subrole)
            if classification == "normal" {
                do {
                    let ref = try retain(element, process: freshProcess, windowRef: windowRef, kind: .element,
                                         deadline: deadline)
                    refs.insert(ref)
                    var label: String?
                    if let rawLabel = stringAttribute(element, kAXTitleAttribute, deadline: deadline) ??
                        stringAttribute(element, kAXDescriptionAttribute, deadline: deadline) {
                        let bounded = boundedUTF8Prefix(rawLabel, byteLimit: 4096)
                        label = bounded.text
                        if bounded.truncated {
                            complete = false
                            if reason.isEmpty { reason = "text_limit" }
                        }
                    }
                    var value: String?
                    if config.allowValues, let bounded = allowedValue(element, classification: classification,
                                                                      role: role, deadline: deadline) {
                        value = bounded.text
                        if bounded.truncated {
                            complete = false
                            if reason.isEmpty { reason = "text_limit" }
                        }
                    }
                    let enabled = boolAttribute(element, kAXEnabledAttribute, deadline: deadline)
                    let checked = role == "AXCheckBox" || role == "AXRadioButton"
                        ? boolAttribute(element, kAXValueAttribute, deadline: deadline) : nil
                    let selected = boolAttribute(element, "AXSelected", deadline: deadline)
                    let focused = boolAttribute(element, kAXFocusedAttribute, deadline: deadline)
                    let actions = advertisedActions(element, role: role)
                    let node = TraversalNode(ref: ref, parentRef: parentRef, order: order, role: role,
                                             label: label, value: value, enabled: enabled,
                                             checked: checked, selected: selected, focused: focused,
                                             actions: actions, classification: classification)
                    let nodeBytes = try JSONSerialization.data(withJSONObject: node.json, options: [.fragmentsAllowed, .sortedKeys]).count
                    if totalOutputBytes + nodeBytes > budget.maxBytes {
                        refs.remove(ref)
                        complete = false; reason = "byte_limit"; break
                    }
                    output.append(node)
                    totalOutputBytes += nodeBytes
                    if depth < budget.maxDepth {
                        let remaining = max(0, budget.maxNodes - visitedCount - stack.count)
                        let childResult = children(of: element, limit: remaining, deadline: deadline)
                        if childResult.failed {
                            complete = false
                            if reason.isEmpty { reason = "child_read_unavailable" }
                        }
                        if childResult.truncated {
                            complete = false
                            if reason.isEmpty { reason = "node_limit" }
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
        guard try refreshDesktopGeneration() == desktopGeneration else {
            invalidateAccessibilityState()
            throw ProbeFailure(code: "state_expired")
        }
        if complete {
            // A complete traversal is the only evidence that an old element left the tree.
            let all = references.filter { ref, entry in entry.windowRef != windowRef || refs.contains(ref) }
            references = all
        }
        let rows = output.sorted { $0.ref < $1.ref }.map(\.json)
        let digest = try projectionDigest(windowRef: windowRef, rows: rows, complete: complete, reason: reason)
        lastProjectionDigest = digest
        let contextDeadline = min(deadline, ProcessInfo.processInfo.systemUptime + 1.0)
        let desktopContext = try desktopContextJSON(requestID: requestID, deadline: contextDeadline)
        let contextMatchesTraversal = desktopContext != nil && desktopTracker.proofAvailable &&
            desktopTracker.generation == desktopGeneration
        if !contextMatchesTraversal { invalidateAccessibilityState() }
        let stateID = UUID().uuidString.lowercased()
        let storedBytes = try JSONSerialization.data(withJSONObject: rows, options: [.fragmentsAllowed, .sortedKeys]).count
        let retainUsableSnapshot = retainSnapshot && contextMatchesTraversal
        if retainUsableSnapshot {
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
                                          createdAt: ProcessInfo.processInfo.systemUptime, byteCount: storedBytes,
                                          desktopGeneration: desktopGeneration)
            generations[stateID] = snapshot
            snapshots = generations
        }
        var result: [String: Any] = [
            "window_ref": windowRef, "state_id": retainUsableSnapshot ? stateID : "",
            "observed_at": ISO8601DateFormatter().string(from: Date()),
            "elements": rows,
            "coverage": ["complete": complete, "reason": reason]
        ]
        if contextMatchesTraversal, let desktopContext { result["desktop_context"] = desktopContext }
        return result
    }

    func readScopedElement(_ request: NativeRequest, requestID: UInt64) throws -> [String: Any] {
        try checkPermission()
        guard let windowRef = request.windowRef, let elementRef = request.elementRef,
              let stateID = request.stateID else { throw ProbeFailure(code: "policy_refused") }
        let budget = try boundedBudget(request.budget)
        let deadline = ProcessInfo.processInfo.systemUptime + budget.timeout
        let snapshot = try validateSnapshot(stateID, windowRef: windowRef, requestID: requestID,
                                            budget: request.budget, deadline: deadline)
        guard snapshot.refs.contains(elementRef) else { throw ProbeFailure(code: "element_stale") }
        let entry = try resolve(elementRef, kind: .element, windowRef: windowRef, deadline: deadline)
        let role = stringAttribute(entry.element, kAXRoleAttribute, deadline: deadline) ?? ""
        let subrole = stringAttribute(entry.element, kAXSubroleAttribute, deadline: deadline)
        guard classify(role: role, subrole: subrole) == "normal", role == "AXTextField" else {
            throw ProbeFailure(code: "policy_refused")
        }
        guard let bounded = allowedValue(entry.element, classification: "normal", role: role, deadline: deadline) else {
            throw ProbeFailure(code: "backend_unavailable")
        }
        guard !bounded.truncated else { throw ProbeFailure(code: "budget_exceeded") }
        return ["window_ref": windowRef, "element_ref": elementRef, "state_id": stateID, "text": bounded.text]
    }

    func validateSnapshot(_ stateID: String, windowRef: String, requestID: UInt64, budget: NativeBudget?,
                          deadline: TimeInterval) throws -> NativeSnapshot {
        let desktopGeneration = try refreshDesktopGeneration()
        guard validOpaque(stateID), let prior = snapshots[stateID], prior.windowRef == windowRef,
              prior.desktopGeneration == desktopGeneration,
              ProcessInfo.processInfo.systemUptime - prior.createdAt <= 120 else { throw ProbeFailure(code: "state_expired") }
        let freshRequest = NativeRequest(schemaVersion: 1, requestID: "revalidate", operation: "observe",
                                         windowRef: windowRef, elementRef: nil, stateID: nil,
                                         budget: budget, action: nil)
        try checkDeadline(requestID, deadline: deadline)
        let freshBudget = try boundedBudget(freshRequest.budget)
        let freshDeadline = min(deadline, ProcessInfo.processInfo.systemUptime + freshBudget.timeout)
        _ = try observeWindow(freshRequest, requestID: requestID, retainSnapshot: false, deadline: freshDeadline)
        try checkDeadline(requestID, deadline: deadline)
        guard try refreshDesktopGeneration() == prior.desktopGeneration else {
            invalidateAccessibilityState()
            throw ProbeFailure(code: "state_expired")
        }
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
        // Production input remains closed until the independent live gate. The
        // executor itself is exercised with a private fake access layer in tests.
        guard nativeActionDispatchEnabled else { throw ProbeFailure(code: "unsupported") }
        let deadline = ProcessInfo.processInfo.systemUptime + 10
        return try NativeActionExecutor(access: AXActionAccess(runtime: self, requestID: requestID, deadline: deadline),
                                        poster: QuartzInputPoster())
            .execute(action, requestID: requestID)
    }
}

// This is deliberately a private source switch, not a public configuration or
// environment override. It remains false in every shipping build.
private let nativeActionDispatchEnabled = false

@MainActor
private struct AXActionAccess: NativeActionAccess {
    let runtime: NativeRuntime
    let requestID: UInt64
    let deadline: TimeInterval

    func check(requestID: UInt64, deadline: TimeInterval) throws {
        try runtime.checkDeadline(requestID, deadline: deadline)
    }

    func revalidate(_ action: NativeAction) throws -> NativeActionTarget {
        try check(requestID: requestID, deadline: deadline)
        let window = try runtime.resolve(action.windowRef, kind: .window, deadline: deadline)
        let process = try runtime.matchingProcess(window.process)
        guard process == window.process else { throw ProbeFailure(code: "element_stale") }
        let semantic = !action.elementRef.isEmpty
        let element: NativeReference
        if semantic {
            let snapshot = try runtime.validateSnapshot(action.stateID, windowRef: action.windowRef,
                                                        requestID: requestID, budget: nil, deadline: deadline)
            guard snapshot.complete, snapshot.process == process, snapshot.refs.contains(action.elementRef) else {
                throw ProbeFailure(code: "state_expired")
            }
            element = try runtime.resolve(action.elementRef, kind: .element, windowRef: action.windowRef, deadline: deadline)
            guard element.process == process else { throw ProbeFailure(code: "element_stale") }
        } else {
            element = window
        }
        let role = stringAttribute(element.element, kAXRoleAttribute, deadline: deadline) ?? ""
        let subrole = stringAttribute(element.element, kAXSubroleAttribute, deadline: deadline)
        let classification = classify(role: role, subrole: subrole)
        guard classification == "normal" else {
            throw ProbeFailure(code: classification == "secure" ? "policy_refused" : "unsupported")
        }
        let application = AXUIElementCreateApplication(process.pid)
        let focusedWindow = copyAttribute(application, kAXFocusedWindowAttribute as String, deadline: deadline)
        if action.kind != "focus_window" {
            guard let focusedWindow, CFEqual(focusedWindow, window.element) else { throw ProbeFailure(code: "state_expired") }
        }
        let focusedValue = copyAttribute(application, kAXFocusedUIElementAttribute as String, deadline: deadline)
        let focusedElement: AXUIElement?
        if let focusedValue, CFGetTypeID(focusedValue) == AXUIElementGetTypeID() {
            focusedElement = unsafeBitCast(focusedValue, to: AXUIElement.self)
        } else {
            focusedElement = nil
        }
        var focusedRole: String?
        var focusedClassification: String?
        var focusedIdentity: String?
        if let focusedElement {
            let focusedWindowValue = copyAttribute(focusedElement, kAXWindowAttribute as String, deadline: deadline)
            if let focusedWindowValue, CFEqual(focusedWindowValue, window.element) {
                focusedRole = stringAttribute(focusedElement, kAXRoleAttribute, deadline: deadline)
                focusedClassification = classify(role: focusedRole ?? "", subrole: stringAttribute(focusedElement, kAXSubroleAttribute, deadline: deadline))
                if focusedClassification == "normal" {
                    focusedIdentity = try runtime.retain(focusedElement, process: process,
                                                         windowRef: action.windowRef, kind: .element, deadline: deadline)
                }
            }
        }
        let windowBounds = axBounds(window.element, deadline: deadline)
        let displayID = CGMainDisplayID()
        let displayBounds = CGDisplayBounds(displayID)
        return NativeActionTarget(actionID: action.id, windowRef: action.windowRef,
                                  elementRef: action.elementRef, stateID: action.stateID,
                                  process: process, role: role, classification: classification,
                                  enabled: boolAttribute(element.element, kAXEnabledAttribute, deadline: deadline),
                                  focused: boolAttribute(element.element, kAXFocusedAttribute, deadline: deadline) == true,
                                  windowFocused: focusedWindow.map { CFEqual($0, window.element) } ?? false,
                                  focusedRole: focusedRole, focusedClassification: focusedClassification,
                                  focusedIdentity: focusedIdentity,
                                  windowBounds: windowBounds, elementBounds: axBounds(element.element, deadline: deadline),
                                  displayBounds: displayBounds, displayID: displayID)
    }

    func supports(_ kind: String, target: NativeActionTarget, point: CGPoint?) throws -> Bool {
        try check(requestID: requestID, deadline: deadline)
        guard target.classification == "normal" else { return false }
        let entry = target.elementRef.isEmpty
            ? try runtime.resolve(target.windowRef, kind: .window, deadline: deadline)
            : try runtime.resolve(target.elementRef, kind: .element, windowRef: target.windowRef, deadline: deadline)
        switch kind {
        case "press":
            guard target.role == "AXButton" else { return false }
            var names: CFArray?
            let status = try nativeAXDeadlineIPC(entry.element, deadline: deadline) { AXUIElementCopyActionNames(entry.element, &names) }
            return status == .success && (names as? [String])?.contains(kAXPressAction as String) == true
        case "replace":
            guard target.role == "AXTextField" else { return false }
            var settable = DarwinBoolean(false)
            let status = try nativeAXDeadlineIPC(entry.element, deadline: deadline) { AXUIElementIsAttributeSettable(entry.element, kAXValueAttribute as CFString, &settable) }
            return status == .success && settable.boolValue
        case "insert":
            guard textRole(target.role), target.focused, target.focusedClassification == "normal" else { return false }
            guard CGPreflightPostEventAccess() else { throw ProbeFailure(code: "permission_denied") }
            return CGEventSource(stateID: .hidSystemState) != nil
        case "click" where point == nil:
            if target.role == "AXButton" {
                var names: CFArray?
                return (try nativeAXDeadlineIPC(entry.element, deadline: deadline) { AXUIElementCopyActionNames(entry.element, &names) }) == .success &&
                    (names as? [String])?.contains(kAXPressAction as String) == true
            }
            return false
        case "pick":
            var names: CFArray?
            guard (try nativeAXDeadlineIPC(entry.element, deadline: deadline) { AXUIElementCopyActionNames(entry.element, &names) }) == .success,
                  let actions = names as? [String] else { return false }
            return actions.contains("AXPick")
        case "focus":
            var settable = DarwinBoolean(false)
            return (try nativeAXDeadlineIPC(entry.element, deadline: deadline) { AXUIElementIsAttributeSettable(entry.element, kAXFocusedAttribute as CFString, &settable) }) == .success && settable.boolValue
        case "scroll":
            guard ["AXScrollArea", "AXWebArea", "AXList", "AXTable", "AXOutline", "AXTextArea", "AXTextView"].contains(target.role) else {
                return false
            }
            guard let bounds = target.elementBounds ?? target.windowBounds, bounds.isFinitePositive,
                  CGPreflightPostEventAccess(), CGEventSource(stateID: .hidSystemState) != nil else { return false }
            let center = CGPoint(x: bounds.midX, y: bounds.midY)
            guard checkedPoint(Double(center.x), Double(center.y), target: target) != nil else { return false }
            try validateCoordinateHit(center, target: target, runtime: runtime, requestID: requestID,
                                      deadline: deadline, requiresExactTarget: true)
            return true
        case "focus_window":
            var names: CFArray?
            return (try nativeAXDeadlineIPC(entry.element, deadline: deadline) { AXUIElementCopyActionNames(entry.element, &names) }) == .success && (names as? [String])?.contains("AXRaise") == true
        case "type_text", "press_key":
            if target.focusedClassification == "secure" { throw ProbeFailure(code: "policy_refused") }
            guard target.focusedRole != nil, target.focusedClassification == "normal" else { return false }
            if kind == "type_text", !textRole(target.focusedRole ?? "") { return false }
            guard CGPreflightPostEventAccess() else { throw ProbeFailure(code: "permission_denied") }
            return CGEventSource(stateID: .hidSystemState) != nil
        case "coordinate_scroll", "drag":
            guard let point, checkedPoint(Double(point.x), Double(point.y), target: target) != nil else { return false }
            try validateCoordinateHit(point, target: target, runtime: runtime, requestID: requestID, deadline: deadline)
            return CGPreflightPostEventAccess() && CGEventSource(stateID: .hidSystemState) != nil
        case "click" where point != nil:
            guard let point, checkedPoint(Double(point.x), Double(point.y), target: target) != nil else { return false }
            try validateCoordinateHit(point, target: target, runtime: runtime, requestID: requestID, deadline: deadline)
            return CGPreflightPostEventAccess() && CGEventSource(stateID: .hidSystemState) != nil
        default: return false
        }
    }

    func selection(target: NativeActionTarget) throws -> NativeTextSelection {
        try check(requestID: requestID, deadline: deadline)
        let entry = target.elementRef.isEmpty
            ? try runtime.resolve(target.windowRef, kind: .window, deadline: deadline)
            : try runtime.resolve(target.elementRef, kind: .element, windowRef: target.windowRef, deadline: deadline)
        guard let current = allowedValue(entry.element, classification: target.classification, role: target.role, deadline: deadline),
              !current.truncated,
              let rawRange = copyAttribute(entry.element, kAXSelectedTextRangeAttribute as String, deadline: deadline) else {
            throw ProbeFailure(code: "unsupported")
        }
        var range = CFRange(location: 0, length: 0)
        guard let value = nativeAXValue(rawRange) else { throw ProbeFailure(code: "state_expired") }
        guard AXValueGetType(value) == .cfRange, AXValueGetValue(value, .cfRange, &range) else {
            throw ProbeFailure(code: "state_expired")
        }
        let selection = NativeTextSelection(text: current.text, location: range.location, length: range.length)
        guard validNativeTextSelection(selection) else { throw ProbeFailure(code: "state_expired") }
        return selection
    }

    func dispatch(_ action: NativeAction, target: NativeActionTarget, method: String,
                  poster: NativeInputPoster) throws -> NativeActionDispatch {
        try check(requestID: requestID, deadline: deadline)
        guard try revalidate(action) == target else { throw ProbeFailure(code: "state_expired") }
        try check(requestID: requestID, deadline: deadline)
        let point = action.kind == "click" || action.kind == "coordinate_scroll" || action.kind == "drag"
            ? CGPoint(x: action.x, y: action.y) : nil
        guard try supports(action.kind, target: target, point: point) else { throw ProbeFailure(code: "unsupported") }
        let entry = target.elementRef.isEmpty
            ? try runtime.resolve(target.windowRef, kind: .window, deadline: deadline)
            : try runtime.resolve(target.elementRef, kind: .element, windowRef: target.windowRef, deadline: deadline)
        guard try runtime.matchingProcess(target.process) == target.process,
              target.classification == "normal" else { throw ProbeFailure(code: "element_stale") }
        let error: AXError
        try check(requestID: requestID, deadline: deadline)
        switch method {
        case "ax_press": error = try nativeAXDeadlineIPC(entry.element, deadline: deadline) { AXUIElementPerformAction(entry.element, kAXPressAction as CFString) }
        case "ax_set_value": error = try nativeAXDeadlineIPC(entry.element, deadline: deadline) { AXUIElementSetAttributeValue(entry.element, kAXValueAttribute as CFString, action.text as CFString) }
        case "ax_pick": error = try nativeAXDeadlineIPC(entry.element, deadline: deadline) { AXUIElementPerformAction(entry.element, "AXPick" as CFString) }
        case "ax_focus": error = try nativeAXDeadlineIPC(entry.element, deadline: deadline) { AXUIElementSetAttributeValue(entry.element, kAXFocusedAttribute as CFString, kCFBooleanTrue) }
        case "ax_focus_window": error = try nativeAXDeadlineIPC(entry.element, deadline: deadline) { AXUIElementPerformAction(entry.element, "AXRaise" as CFString) }
        case "ax_scroll": throw ProbeFailure(code: "unsupported")
        case "cg_unicode" where action.kind == "insert":
            _ = try selection(target: target) // Recheck immediately before the only event dispatch.
            return poster.post(action, target: target, deadline: deadline) { point in try validateStep(action, target: target, point: point) }
        case "cg_unicode", "cg_key", "cg_click", "cg_scroll", "cg_drag":
            return poster.post(action, target: target, deadline: deadline) { point in try validateStep(action, target: target, point: point) }
        default: throw ProbeFailure(code: "unsupported")
        }
        guard error == .success else {
            // AX errors can be ambiguous after the request crossed the process
            // boundary. Never try another method or report a clean failure.
            throw ProbeFailure(code: "unknown_outcome")
        }
        let step = nativeActionRoute(action)?.step ?? ""
        guard !step.isEmpty else { throw ProbeFailure(code: "unsupported") }
        return NativeActionDispatch(method: method, completedSteps: [step], execution: "applied")
    }

    private func validateStep(_ action: NativeAction, target: NativeActionTarget, point: CGPoint?) throws {
        try check(requestID: requestID, deadline: deadline)
        let current = try revalidate(action)
        guard current == target else { throw ProbeFailure(code: "state_expired") }
        guard try supports(action.kind, target: current, point: point) else { throw ProbeFailure(code: "policy_refused") }
    }
}

private func textRole(_ role: String) -> Bool {
    ["AXTextField", "AXTextArea", "AXTextView", "AXSearchField", "AXComboBox"].contains(role)
}

private func axBounds(_ element: AXUIElement, deadline: TimeInterval) -> CGRect? {
    guard let position = copyAttribute(element, kAXPositionAttribute as String, deadline: deadline),
          let size = copyAttribute(element, kAXSizeAttribute as String, deadline: deadline),
          let positionValue = nativeAXValue(position), let sizeValue = nativeAXValue(size) else { return nil }
    var point = CGPoint.zero
    var dimensions = CGSize.zero
    guard AXValueGetType(positionValue) == .cgPoint,
          AXValueGetValue(positionValue, .cgPoint, &point),
          AXValueGetType(sizeValue) == .cgSize,
          AXValueGetValue(sizeValue, .cgSize, &dimensions) else { return nil }
    let bounds = CGRect(origin: point, size: dimensions)
    return bounds.isFinitePositive ? bounds : nil
}

@MainActor
private func validateCoordinateHit(_ point: CGPoint, target: NativeActionTarget, runtime: NativeRuntime,
                                   requestID: UInt64, deadline: TimeInterval,
                                   requiresExactTarget: Bool = false) throws {
    try runtime.checkDeadline(requestID, deadline: deadline)
    guard let window = try? runtime.resolve(target.windowRef, kind: .window, deadline: deadline) else {
        throw ProbeFailure(code: "policy_refused")
    }
    let process = try runtime.matchingProcess(target.process)
    guard process == target.process else { throw ProbeFailure(code: "element_stale") }
    let app = AXUIElementCreateApplication(target.process.pid)
    guard let focusedWindow = copyAttribute(app, kAXFocusedWindowAttribute as String, deadline: deadline), CFEqual(focusedWindow, window.element) else {
        throw ProbeFailure(code: "state_expired")
    }
    let scopedTarget: AXUIElement
    if target.elementRef.isEmpty {
        scopedTarget = window.element
    } else {
        scopedTarget = try runtime.resolve(target.elementRef, kind: .element, windowRef: target.windowRef, deadline: deadline).element
    }
    var hit: AXUIElement?
    guard (try nativeAXDeadlineIPC(app, deadline: deadline) {
        AXUIElementCopyElementAtPosition(app, Float(point.x), Float(point.y), &hit)
    }) == .success,
          let hit else { throw ProbeFailure(code: "policy_refused") }
    let role = stringAttribute(hit, kAXRoleAttribute, deadline: deadline) ?? ""
    let classification = classify(role: role, subrole: stringAttribute(hit, kAXSubroleAttribute, deadline: deadline))
    var hitPID: pid_t = 0
    let exactWindow = copyAttribute(hit, kAXWindowAttribute as String, deadline: deadline).map { CFEqual($0, window.element) } ?? false
    let exactTarget = CFEqual(hit, scopedTarget)
    let targetRelated = exactTarget || axDescendant(hit, of: scopedTarget, maximumParents: 32, deadline: deadline)
    var targetPID: pid_t = 0
    guard classification == "normal",
          (try nativeAXDeadlineIPC(hit, deadline: deadline) { AXUIElementGetPid(hit, &hitPID) }) == .success,
          (try nativeAXDeadlineIPC(scopedTarget, deadline: deadline) { AXUIElementGetPid(scopedTarget, &targetPID) }) == .success,
          targetPID == target.process.pid,
          validNativeCoordinateHit(NativeCoordinateHitFacts(
            point: point, expectedPID: target.process.pid, actualPID: Int32(hitPID),
            targetBounds: axBounds(scopedTarget, deadline: deadline), hitBounds: axBounds(hit, deadline: deadline), exactWindow: exactWindow,
            targetRelated: targetRelated, exactTarget: exactTarget, requiresExactTarget: requiresExactTarget)) else {
        throw ProbeFailure(code: "policy_refused")
    }
    try runtime.checkDeadline(requestID, deadline: deadline)
}

private func axDescendant(_ candidate: AXUIElement, of ancestor: AXUIElement, maximumParents: Int,
                          deadline: TimeInterval) -> Bool {
    var current = candidate
    for _ in 0..<maximumParents {
        guard let value = copyAttribute(current, kAXParentAttribute as String, deadline: deadline),
              CFGetTypeID(value) == AXUIElementGetTypeID() else { return false }
        let parent = unsafeBitCast(value, to: AXUIElement.self)
        if CFEqual(parent, ancestor) { return true }
        current = parent
    }
    return false
}

private func classify(role: String, subrole: String?) -> String {
    if subrole == "AXSecureTextField" || role == "AXSecureTextField" { return "secure" }
    guard let subrole else { return "unknown" }
    let expected: [String: Set<String>] = [
        "AXStaticText": ["AXStaticText"], "AXTextField": ["AXTextField", "AXSearchField"],
        "AXTextArea": ["AXTextArea", "AXTextView"], "AXTextView": ["AXTextView"], "AXSearchField": ["AXSearchField"],
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

func copyAttribute(_ element: AXUIElement, _ attribute: String, deadline: TimeInterval) -> CFTypeRef? {
    var value: CFTypeRef?
    guard (try? nativeAXDeadlineIPC(element, deadline: deadline) {
        AXUIElementCopyAttributeValue(element, attribute as CFString, &value)
    }) == .success else { return nil }
    return value
}

func stringAttribute(_ element: AXUIElement, _ attribute: String, deadline: TimeInterval) -> String? {
    guard let value = copyAttribute(element, attribute, deadline: deadline) else { return nil }
    if let string = value as? String { return string }
    if let string = value as? NSAttributedString { return string.string }
    return nil
}

private func boolAttribute(_ element: AXUIElement, _ attribute: String, deadline: TimeInterval) -> Bool? {
    guard let value = copyAttribute(element, attribute, deadline: deadline), CFGetTypeID(value) == CFBooleanGetTypeID() else { return nil }
    return (value as! NSNumber).boolValue
}

private func allowedValue(_ element: AXUIElement, classification: String, role: String,
                          deadline: TimeInterval) -> BoundedText? {
    guard classification == "normal",
          ["AXTextField", "AXTextArea", "AXTextView", "AXSearchField"].contains(role),
          let value = copyAttribute(element, kAXValueAttribute as String, deadline: deadline) else { return nil }
    if let text = value as? String { return boundedUTF8Prefix(text, byteLimit: 8192) }
    if let text = value as? NSAttributedString { return boundedUTF8Prefix(text.string, byteLimit: 8192) }
    return nil
}

func boundedChildCount(_ total: Int, limit: Int) -> (count: Int, truncated: Bool) {
    let bounded = max(0, min(total, limit))
    return (bounded, total > bounded)
}

func depthIsIncluded(_ depth: Int, maximum: Int) -> Bool {
    depth < maximum
}

private func children(of element: AXUIElement, limit: Int, deadline: TimeInterval) -> (values: [AXUIElement], failed: Bool, truncated: Bool) {
    var total: CFIndex = 0
    let attribute = kAXChildrenAttribute as CFString
    guard let countStatus = try? nativeAXDeadlineIPC(element, deadline: deadline, operation: {
        AXUIElementGetAttributeValueCount(element, attribute, &total)
    }) else { return ([], true, false) }
    if countStatus == .noValue || countStatus == .attributeUnsupported { return ([], false, false) }
    guard countStatus == .success, total >= 0 else { return ([], true, false) }
    let bounds = boundedChildCount(Int(total), limit: limit)
    guard bounds.count > 0 else { return ([], false, bounds.truncated) }
    var raw: CFArray?
    guard let status = try? nativeAXDeadlineIPC(element, deadline: deadline, operation: {
        AXUIElementCopyAttributeValues(element, attribute, 0, CFIndex(bounds.count), &raw)
    }) else { return ([], true, bounds.truncated) }
    guard status == .success, let values = raw as? [AXUIElement] else { return ([], true, bounds.truncated) }
    return (values, false, bounds.truncated)
}

private func advertisedActions(_ element: AXUIElement, role: String) -> [String] {
    // Input is not a qualified runtime capability in any shipped branch.
    return []
}

func boundedBudget(_ input: NativeBudget?) throws -> (maxDepth: Int, maxNodes: Int, maxBytes: Int, timeout: TimeInterval) {
    guard let input else { return (16, 256, 32_768, 0.25) }
    guard (1...128).contains(input.maxDepth), (1...10_000).contains(input.maxNodes),
          (1...(4 * 1024 * 1024)).contains(input.maxBytes),
          (1...30_000_000_000).contains(input.timeoutNanoseconds) else {
        throw ProbeFailure(code: "invalid_request")
    }
    return (min(input.maxDepth, 64), min(input.maxNodes, 4096), min(input.maxBytes, 65_536),
            min(Double(input.timeoutNanoseconds) / 1_000_000_000, 3.0))
}

private func projectionDigest(windowRef: String, rows: [[String: Any]], complete: Bool, reason: String) throws -> String {
    let canonical = try JSONSerialization.data(withJSONObject: ["window_ref": windowRef, "complete": complete,
                                                                "reason": reason, "elements": rows],
                                                options: [.fragmentsAllowed, .sortedKeys])
    return SHA256.hash(data: canonical).map { String(format: "%02x", $0) }.joined()
}

func validOpaque(_ value: String) -> Bool {
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
