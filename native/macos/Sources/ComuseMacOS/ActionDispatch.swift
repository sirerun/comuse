import Foundation
import CoreGraphics

struct NativeActionTarget: Equatable {
    let actionID: String
    let windowRef: String
    let elementRef: String
    let stateID: String
    let process: NativeProcess
    let role: String
    let classification: String
    let enabled: Bool?
    let focused: Bool
    let windowFocused: Bool
}

struct NativeActionDispatch {
    let method: String
    let completedSteps: [String]
    let execution: String
}

struct NativeTextSelection {
    let text: String
    let location: Int
    let length: Int
}

func validNativeTextSelection(_ selection: NativeTextSelection) -> Bool {
    guard selection.text.utf8.count <= 8192, selection.location >= 0, selection.length >= 0 else { return false }
    let stringLength = (selection.text as NSString).length
    guard selection.location <= stringLength, selection.length <= stringLength - selection.location else { return false }
    return Range(NSRange(location: selection.location, length: selection.length), in: selection.text) != nil
}

@MainActor protocol NativeActionAccess {
    func check(requestID: UInt64, deadline: TimeInterval) throws
    func revalidate(_ action: NativeAction) throws -> NativeActionTarget
    func supports(_ kind: String, target: NativeActionTarget) throws -> Bool
    func selection(target: NativeActionTarget) throws -> NativeTextSelection
    func dispatch(_ action: NativeAction, target: NativeActionTarget, method: String,
                  poster: NativeUnicodePoster) throws -> NativeActionDispatch
}

@MainActor protocol NativeUnicodePoster {
    func post(_ text: String) throws
}

@MainActor struct QuartzUnicodePoster: NativeUnicodePoster {
    func post(_ text: String) throws {
        guard let source = CGEventSource(stateID: .hidSystemState),
              let down = CGEvent(keyboardEventSource: source, virtualKey: 0, keyDown: true),
              let up = CGEvent(keyboardEventSource: source, virtualKey: 0, keyDown: false) else {
            throw ProbeFailure(code: "unsupported")
        }
        let units = Array(text.utf16)
        units.withUnsafeBufferPointer { buffer in
            down.keyboardSetUnicodeString(stringLength: buffer.count, unicodeString: buffer.baseAddress)
            up.keyboardSetUnicodeString(stringLength: buffer.count, unicodeString: buffer.baseAddress)
        }
        down.post(tap: .cghidEventTap)
        up.post(tap: .cghidEventTap)
    }
}

@MainActor struct NativeActionExecutor {
    let access: NativeActionAccess
    let poster: NativeUnicodePoster

    func execute(_ action: NativeAction, requestID: UInt64) throws -> [String: Any] {
        guard validNativeAction(action) else { throw ProbeFailure(code: "invalid_request") }
        let deadline = ProcessInfo.processInfo.systemUptime + 10
        try access.check(requestID: requestID, deadline: deadline)
        let target = try access.revalidate(action)
        guard target.actionID == action.id, target.windowRef == action.windowRef,
              target.elementRef == action.elementRef, target.stateID == action.stateID else {
            throw ProbeFailure(code: "element_stale")
        }
        guard target.classification == "normal" else { throw ProbeFailure(code: "policy_refused") }
        guard target.enabled == true, target.windowFocused,
              action.kind != "insert" || target.focused else { throw ProbeFailure(code: "state_expired") }
        let method: String
        let completedStep: String
        switch action.kind {
        case "press": method = "ax_press"; completedStep = "press"
        case "replace": method = "ax_set_value"; completedStep = "set_value"
        case "insert": method = "cg_unicode"; completedStep = "unicode"
        default: throw ProbeFailure(code: "unsupported")
        }
        guard try access.supports(action.kind, target: target) else { throw ProbeFailure(code: "unsupported") }
        if action.kind == "insert" {
            guard validNativeTextSelection(try access.selection(target: target)) else {
                throw ProbeFailure(code: "state_expired")
            }
        }
        try access.check(requestID: requestID, deadline: deadline)
        let dispatched = try access.dispatch(action, target: target, method: method, poster: poster)
        guard dispatched.method == method, dispatched.completedSteps == [completedStep], dispatched.execution == "applied" else {
            throw ProbeFailure(code: "unknown_outcome")
        }
        return [
            "action_id": action.id,
            "execution": dispatched.execution,
            "verification": ["status": "unavailable"],
            "state_status": "unavailable",
            "cleanup": "complete",
            "method": dispatched.method,
            "completed_steps": dispatched.completedSteps
        ]
    }
}

func validNativeAction(_ action: NativeAction) -> Bool {
    guard validOpaque(action.id), validOpaque(action.windowRef), validOpaque(action.elementRef),
          validOpaque(action.stateID), action.text.utf8.count <= 8192 else { return false }
    switch action.kind {
    case "press": return action.text.isEmpty
    case "replace": return true
    case "insert": return !action.text.isEmpty
    default: return false
    }
}
