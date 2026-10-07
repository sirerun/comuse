import Foundation
import CoreGraphics
import AppKit

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
    let windowBounds: CGRect? = nil
    let displayBounds: CGRect? = nil
}

struct NativeActionDispatch {
    let method: String
    let completedSteps: [String]
    let execution: String
    let cleanup: String

    init(method: String, completedSteps: [String], execution: String, cleanup: String = "complete") {
        self.method = method
        self.completedSteps = completedSteps
        self.execution = execution
        self.cleanup = cleanup
    }
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
                  poster: NativeInputPoster) throws -> NativeActionDispatch
}

@MainActor protocol NativeInputPoster {
    func post(_ action: NativeAction, target: NativeActionTarget) throws -> NativeActionDispatch
}

@MainActor struct QuartzInputPoster: NativeInputPoster {
    func post(_ action: NativeAction, target: NativeActionTarget) throws -> NativeActionDispatch {
        guard let source = CGEventSource(stateID: .hidSystemState),
              CGPreflightPostEventAccess() else {
            throw ProbeFailure(code: "permission_denied")
        }
        switch action.kind {
        case "insert", "type_text":
            try postUnicode(action.text, delayMS: action.kind == "type_text" ? action.delayMS : 0, source: source)
        case "click":
            guard let point = checkedPoint(action.x, action.y, target: target) else { throw ProbeFailure(code: "policy_refused") }
            try postButton(action.button, point: point, count: action.count, holdMS: action.holdMS, source: source)
        case "press_key":
            try postKeys(action.keys, holdMS: action.holdMS, source: source)
        case "coordinate_scroll":
            guard let point = checkedPoint(action.x, action.y, target: target) else { throw ProbeFailure(code: "policy_refused") }
            guard let event = CGEvent(scrollWheelEvent2Source: source, units: .line,
                                      wheelCount: 2, wheel1: Int32(action.dy), wheel2: Int32(action.dx), wheel3: 0) else {
                throw ProbeFailure(code: "unsupported")
            }
            event.location = point
            event.post(tap: .cghidEventTap)
        case "drag":
            guard let point = checkedPoint(action.x, action.y, target: target) else { throw ProbeFailure(code: "policy_refused") }
            guard let end = checkedPoint(action.endX, action.endY, target: target) else { throw ProbeFailure(code: "policy_refused") }
            try postDrag(from: point, to: end, steps: action.steps, durationMS: action.durationMS, source: source)
        default: throw ProbeFailure(code: "unsupported")
        }
        let route = nativeActionRoute(action)
        guard let route else { throw ProbeFailure(code: "unsupported") }
        return NativeActionDispatch(method: route.method, completedSteps: [route.step], execution: "applied")
    }

    private func postUnicode(_ text: String, delayMS: Int, source: CGEventSource) throws {
        let units = Array(text.utf16)
        let deadline = ProcessInfo.processInfo.systemUptime + 10
        for (index, unit) in units.enumerated() {
            guard ProcessInfo.processInfo.systemUptime < deadline else { throw ProbeFailure(code: "unknown_outcome") }
            guard let down = CGEvent(keyboardEventSource: source, virtualKey: 0, keyDown: true),
                  let up = CGEvent(keyboardEventSource: source, virtualKey: 0, keyDown: false) else {
                throw ProbeFailure(code: "unknown_outcome")
            }
            var character = unit
            down.keyboardSetUnicodeString(stringLength: 1, unicodeString: &character)
            up.keyboardSetUnicodeString(stringLength: 1, unicodeString: &character)
            down.post(tap: .cghidEventTap)
            up.post(tap: .cghidEventTap)
            if delayMS > 0 && index + 1 < units.count {
                Thread.sleep(forTimeInterval: Double(delayMS) / 1000)
            }
        }
    }

    private func postButton(_ button: String, point: CGPoint, count: Int, holdMS: Int, source: CGEventSource) throws {
        let type: (CGEventType, CGEventType, CGMouseButton)
        switch button {
        case "left": type = (.leftMouseDown, .leftMouseUp, .left)
        case "right": type = (.rightMouseDown, .rightMouseUp, .right)
        case "middle": type = (.otherMouseDown, .otherMouseUp, .center)
        default: throw ProbeFailure(code: "invalid_request")
        }
        for click in 1...count {
            guard let down = CGEvent(mouseEventSource: source, mouseType: type.0, mouseCursorPosition: point, mouseButton: type.2),
                  let up = CGEvent(mouseEventSource: source, mouseType: type.1, mouseCursorPosition: point, mouseButton: type.2) else {
                throw ProbeFailure(code: "unknown_outcome")
            }
            down.setIntegerValueField(.mouseEventClickState, value: Int64(click))
            up.setIntegerValueField(.mouseEventClickState, value: Int64(click))
            down.post(tap: .cghidEventTap)
            if holdMS > 0 { Thread.sleep(forTimeInterval: Double(holdMS) / 1000) }
            up.post(tap: .cghidEventTap)
        }
    }

    private func postKeys(_ keys: String, holdMS: Int, source: CGEventSource) throws {
        let codes: [String: CGKeyCode] = ["a": 0, "b": 11, "c": 8, "d": 2, "e": 14, "f": 3,
            "g": 5, "h": 4, "i": 34, "j": 38, "k": 40, "l": 37, "m": 46, "n": 45,
            "o": 31, "p": 35, "q": 12, "r": 15, "s": 1, "t": 17, "u": 32, "v": 9,
            "w": 13, "x": 7, "y": 16, "z": 6, "0": 29, "1": 18, "2": 19, "3": 20,
            "4": 21, "5": 23, "6": 22, "7": 26, "8": 28, "9": 25, "enter": 36,
            "escape": 53, "tab": 48, "space": 49, "backspace": 51, "delete": 117,
            "up": 126, "down": 125, "left": 123, "right": 124, "home": 115, "end": 119,
            "page_up": 116, "page_down": 121]
        let modifiers: [String: CGEventFlags] = ["ctrl": .maskControl, "alt": .maskAlternate,
            "shift": .maskShift, "meta": .maskCommand]
        let tokens = keys.split(separator: " ").map(String.init)
        guard let key = tokens.last, let code = codes[key] else { throw ProbeFailure(code: "unsupported") }
        var flags: CGEventFlags = []
        for modifier in tokens.dropLast() { guard let value = modifiers[modifier] else { throw ProbeFailure(code: "unsupported") }; flags.insert(value) }
        guard let down = CGEvent(keyboardEventSource: source, virtualKey: code, keyDown: true),
              let up = CGEvent(keyboardEventSource: source, virtualKey: code, keyDown: false) else { throw ProbeFailure(code: "unknown_outcome") }
        down.flags = flags; up.flags = flags
        down.post(tap: .cghidEventTap)
        if holdMS > 0 { Thread.sleep(forTimeInterval: Double(holdMS) / 1000) }
        up.post(tap: .cghidEventTap)
    }

    private func postDrag(from start: CGPoint, to end: CGPoint, steps: Int, durationMS: Int, source: CGEventSource) throws {
        guard let down = CGEvent(mouseEventSource: source, mouseType: .leftMouseDown, mouseCursorPosition: start, mouseButton: .left),
              let up = CGEvent(mouseEventSource: source, mouseType: .leftMouseUp, mouseCursorPosition: end, mouseButton: .left) else { throw ProbeFailure(code: "unknown_outcome") }
        down.post(tap: .cghidEventTap)
        var releasePosted = false
        defer {
            if !releasePosted {
                up.post(tap: .cghidEventTap)
            }
        }
        for step in 1...steps {
            let fraction = CGFloat(step) / CGFloat(steps)
            let point = CGPoint(x: start.x + (end.x - start.x) * fraction, y: start.y + (end.y - start.y) * fraction)
            guard let move = CGEvent(mouseEventSource: source, mouseType: .leftMouseDragged, mouseCursorPosition: point, mouseButton: .left) else { throw ProbeFailure(code: "unknown_outcome") }
            move.post(tap: .cghidEventTap)
            Thread.sleep(forTimeInterval: Double(durationMS) / 1000 / Double(steps))
        }
        up.post(tap: .cghidEventTap)
        releasePosted = true
    }

    private func checkedPoint(_ x: Double, _ y: Double, target: NativeActionTarget) -> CGPoint? {
        guard x.isFinite, y.isFinite, let window = target.windowBounds, let display = target.displayBounds,
              window.origin.x.isFinite, window.origin.y.isFinite, window.width.isFinite, window.height.isFinite,
              display.origin.x.isFinite, display.origin.y.isFinite, display.width.isFinite, display.height.isFinite,
              window.width > 0, window.height > 0, display.width > 0, display.height > 0 else { return nil }
        let point = CGPoint(x: x, y: y)
        return window.contains(point) && display.contains(point) ? point : nil
    }
}

@MainActor struct NativeActionExecutor {
    let access: NativeActionAccess
    let poster: NativeInputPoster

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
        if action.kind != "focus_window", !target.windowFocused { throw ProbeFailure(code: "state_expired") }
        let route = nativeActionRoute(action)
        guard let method = route?.method, let completedStep = route?.step else { throw ProbeFailure(code: "unsupported") }
        if route?.requiresEnabled == true, target.enabled != true { throw ProbeFailure(code: "state_expired") }
        if route?.requiresFocused == true, !target.focused { throw ProbeFailure(code: "state_expired") }
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
            "cleanup": dispatched.cleanup,
            "method": dispatched.method,
            "completed_steps": dispatched.completedSteps
        ]
    }
}

func validNativeAction(_ action: NativeAction) -> Bool {
    guard validOpaque(action.id), validOpaque(action.windowRef), action.text.utf8.count <= 8192,
          [action.x, action.y, action.endX, action.endY].allSatisfy({ $0.isFinite }) else { return false }
    let semantic = !action.elementRef.isEmpty || !action.stateID.isEmpty
    if semantic && (!validOpaque(action.elementRef) || !validOpaque(action.stateID)) { return false }
    switch action.kind {
    case "press": return semantic && action.text.isEmpty
    case "replace": return semantic
    case "insert": return semantic && !action.text.isEmpty
    case "click":
        return !semantic && inCoordinateRange(action.x, action.y) && ["left", "right", "middle"].contains(action.button) &&
            (1...2).contains(action.count) && (0...1000).contains(action.holdMS)
    case "pick", "focus": return semantic && action.text.isEmpty
    case "scroll": return semantic && ["up", "down", "left", "right"].contains(action.direction) && ["line", "page"].contains(action.amount)
    case "type_text": return !semantic && !action.text.isEmpty && action.delayMS >= 0 && action.delayMS <= 100
    case "press_key": return !semantic && nativeKeysValid(action.keys) && (0...1000).contains(action.holdMS)
    case "coordinate_scroll": return !semantic && inCoordinateRange(action.x, action.y) &&
        (-10...10).contains(action.dx) && (-10...10).contains(action.dy) && (action.dx != 0 || action.dy != 0)
    case "drag": return !semantic && inCoordinateRange(action.x, action.y) && inCoordinateRange(action.endX, action.endY) &&
        (2...64).contains(action.steps) && (1...2000).contains(action.durationMS)
    case "focus_window": return !semantic
    default: return false
    }
}

struct NativeActionRoute {
    let method: String
    let step: String
    let requiresEnabled: Bool
    let requiresFocused: Bool
}

func nativeActionRoute(_ action: NativeAction) -> NativeActionRoute? {
    let semantic = !action.elementRef.isEmpty
    switch action.kind {
    case "press": return NativeActionRoute(method: "ax_press", step: "press", requiresEnabled: true, requiresFocused: false)
    case "replace": return NativeActionRoute(method: "ax_set_value", step: "set_value", requiresEnabled: true, requiresFocused: false)
    case "insert": return NativeActionRoute(method: "cg_unicode", step: "unicode", requiresEnabled: true, requiresFocused: true)
    case "click": return NativeActionRoute(method: "cg_click", step: "click", requiresEnabled: false, requiresFocused: false)
    case "pick": return NativeActionRoute(method: "ax_pick", step: "pick", requiresEnabled: true, requiresFocused: false)
    case "focus": return NativeActionRoute(method: "ax_focus", step: "focus", requiresEnabled: true, requiresFocused: false)
    case "scroll": return NativeActionRoute(method: "ax_scroll", step: "scroll", requiresEnabled: true, requiresFocused: false)
    case "type_text": return NativeActionRoute(method: "cg_unicode", step: "unicode", requiresEnabled: false, requiresFocused: false)
    case "press_key": return NativeActionRoute(method: "cg_key", step: "key", requiresEnabled: false, requiresFocused: false)
    case "coordinate_scroll": return NativeActionRoute(method: "cg_scroll", step: "scroll", requiresEnabled: false, requiresFocused: false)
    case "drag": return NativeActionRoute(method: "cg_drag", step: "drag", requiresEnabled: false, requiresFocused: false)
    case "focus_window": return NativeActionRoute(method: "ax_focus_window", step: "focus_window", requiresEnabled: false, requiresFocused: false)
    default: return nil
    }
}

private func inCoordinateRange(_ x: Double, _ y: Double) -> Bool {
    x.isFinite && y.isFinite && x >= 0 && y >= 0 && x <= 1_000_000 && y <= 1_000_000
}

private func nativeKeysValid(_ keys: String) -> Bool {
    let parts = keys.split(separator: " ", omittingEmptySubsequences: false).map(String.init)
    guard !parts.isEmpty, parts.count <= 5, Set(parts).count == parts.count else { return false }
    let modifiers: Set<String> = ["ctrl", "alt", "shift", "meta"]
    let ordinary: Set<String> = ["enter", "escape", "tab", "space", "backspace", "delete", "up", "down", "left", "right", "home", "end", "page_up", "page_down"]
    let nonModifiers = parts.filter { !modifiers.contains($0) }
    guard nonModifiers.count == 1 else { return false }
    let key = nonModifiers[0]
    return (key.count == 1 && key.unicodeScalars.allSatisfy { (97...122).contains(Int($0.value)) || (48...57).contains(Int($0.value)) }) || ordinary.contains(key)
}
