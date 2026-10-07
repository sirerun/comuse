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
    let focusedRole: String?
    let focusedClassification: String?
    let windowBounds: CGRect?
    let elementBounds: CGRect?
    let displayBounds: CGRect?
    let displayID: CGDirectDisplayID?

    init(actionID: String, windowRef: String, elementRef: String, stateID: String,
         process: NativeProcess, role: String, classification: String, enabled: Bool?,
         focused: Bool, windowFocused: Bool, focusedRole: String? = nil,
         focusedClassification: String? = nil, windowBounds: CGRect? = nil,
         elementBounds: CGRect? = nil, displayBounds: CGRect? = nil,
         displayID: CGDirectDisplayID? = nil) {
        self.actionID = actionID; self.windowRef = windowRef; self.elementRef = elementRef
        self.stateID = stateID; self.process = process; self.role = role
        self.classification = classification; self.enabled = enabled
        self.focused = focused; self.windowFocused = windowFocused
        self.focusedRole = focusedRole; self.focusedClassification = focusedClassification
        self.windowBounds = windowBounds; self.elementBounds = elementBounds
        self.displayBounds = displayBounds; self.displayID = displayID
    }
}

struct NativeActionDispatch {
    let method: String
    let completedSteps: [String]
    let execution: String
    let cleanup: String
    let failureCode: String?

    init(method: String, completedSteps: [String], execution: String, cleanup: String = "complete", failureCode: String? = nil) {
        self.method = method
        self.completedSteps = completedSteps
        self.execution = execution
        self.cleanup = cleanup
        self.failureCode = failureCode
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
    func supports(_ kind: String, target: NativeActionTarget, point: CGPoint?) throws -> Bool
    func selection(target: NativeActionTarget) throws -> NativeTextSelection
    func dispatch(_ action: NativeAction, target: NativeActionTarget, method: String,
                  poster: NativeInputPoster) throws -> NativeActionDispatch
}

@MainActor protocol NativeInputPoster {
    func post(_ action: NativeAction, target: NativeActionTarget,
              checkpoint: (CGPoint?) throws -> Void) -> NativeActionDispatch
}

struct NativeInputEvent {
    enum Kind: Equatable { case keyDown, keyUp, mouseDown, mouseUp, mouseMove, scroll }
    var kind: Kind
    var keyCode: CGKeyCode = 0
    var keyFlags: CGEventFlags = []
    var unicode: [UInt16] = []
    var point = CGPoint.zero
    var button = "left"
    var clickCount = 1
    var scrollUnits: CGScrollEventUnit = .line
    var deltaX = 0
    var deltaY = 0
}

@MainActor protocol NativeEventSink {
    func post(_ event: NativeInputEvent, deadline: TimeInterval) throws
}

@MainActor protocol NativeActionClock {
    func now() -> TimeInterval
    func sleep(_ duration: TimeInterval, deadline: TimeInterval) throws
}

@MainActor struct SystemNativeActionClock: NativeActionClock {
    func now() -> TimeInterval { ProcessInfo.processInfo.systemUptime }
    func sleep(_ duration: TimeInterval, deadline: TimeInterval) throws {
        guard duration >= 0, now() + duration < deadline else { throw ProbeFailure(code: "budget_exceeded") }
        if duration > 0 { Thread.sleep(forTimeInterval: duration) }
    }
}

@MainActor struct QuartzEventSink: NativeEventSink {
    func post(_ value: NativeInputEvent, deadline: TimeInterval) throws {
        guard ProcessInfo.processInfo.systemUptime < deadline else { throw ProbeFailure(code: "budget_exceeded") }
        guard CGPreflightPostEventAccess(), let source = CGEventSource(stateID: .hidSystemState) else {
            throw ProbeFailure(code: "permission_denied")
        }
        let event: CGEvent?
        switch value.kind {
        case .keyDown, .keyUp:
            event = CGEvent(keyboardEventSource: source, virtualKey: value.keyCode, keyDown: value.kind == .keyDown)
            if let event {
                event.flags = value.keyFlags
                if !value.unicode.isEmpty {
                    var units = value.unicode
                    units.withUnsafeMutableBufferPointer { buffer in
                        if let base = buffer.baseAddress { event.keyboardSetUnicodeString(stringLength: buffer.count, unicodeString: base) }
                    }
                }
            }
        case .mouseDown, .mouseUp, .mouseMove:
            let type: CGEventType
            let button: CGMouseButton
            switch value.button {
            case "left": button = .left; type = value.kind == .mouseDown ? .leftMouseDown : value.kind == .mouseUp ? .leftMouseUp : .leftMouseDragged
            case "right": button = .right; type = value.kind == .mouseDown ? .rightMouseDown : value.kind == .mouseUp ? .rightMouseUp : .rightMouseDragged
            case "middle": button = .center; type = value.kind == .mouseDown ? .otherMouseDown : value.kind == .mouseUp ? .otherMouseUp : .otherMouseDragged
            default: throw ProbeFailure(code: "invalid_request")
            }
            event = CGEvent(mouseEventSource: source, mouseType: type, mouseCursorPosition: value.point, mouseButton: button)
            if let event { event.setIntegerValueField(.mouseEventClickState, value: Int64(value.clickCount)) }
        case .scroll:
            event = CGEvent(scrollWheelEvent2Source: source, units: value.scrollUnits, wheelCount: 2,
                            wheel1: Int32(value.deltaY), wheel2: Int32(value.deltaX), wheel3: 0)
            event?.location = value.point
        }
        guard ProcessInfo.processInfo.systemUptime < deadline, let event else { throw ProbeFailure(code: "unsupported") }
        event.post(tap: .cghidEventTap)
    }
}

@MainActor struct QuartzInputPoster: NativeInputPoster {
    let sink: NativeEventSink
    let clock: NativeActionClock

    init(sink: NativeEventSink = QuartzEventSink(), clock: NativeActionClock = SystemNativeActionClock()) {
        self.sink = sink
        self.clock = clock
    }

    func post(_ action: NativeAction, target: NativeActionTarget,
              checkpoint: (CGPoint?) throws -> Void) -> NativeActionDispatch {
        guard let route = nativeActionRoute(action) else {
            return NativeActionDispatch(method: "", completedSteps: [], execution: "not_applied", cleanup: "complete")
        }
        let deadline = clock.now() + 10
        var completed: [String] = []
        var held: [NativeInputEvent] = []
        var cleanup = "complete"
        var failureBeforeDispatch = false
        var failureOccurred = false
        var failureCode: String?
        var eventAttempted = false
        func add(_ step: String) { if !completed.contains(step) { completed.append(step) } }
        func deliver(_ event: NativeInputEvent, point: CGPoint?, step: String, down: Bool = false) throws {
            try checkpoint(point)
            if down { held.append(releaseEvent(for: event, at: point ?? .zero)) }
            eventAttempted = true
            try sink.post(event, deadline: deadline)
            add(step)
            if event.kind == .keyUp || event.kind == .mouseUp {
                if let index = held.lastIndex(where: { sameHeldInput($0, releasedBy: event) }) { held.remove(at: index) }
            }
        }
        do {
            try checkpoint(nil)
            switch action.kind {
            case "insert", "type_text":
                let delay = action.kind == "type_text" ? Double(action.delayMS) / 1000 : 0
                for (index, scalar) in action.text.unicodeScalars.enumerated() {
                    let units = Array(String(scalar).utf16)
                    let down = NativeInputEvent(kind: .keyDown, unicode: units)
                    let up = NativeInputEvent(kind: .keyUp, unicode: units)
                    try deliver(down, point: nil, step: "key_down", down: true)
                    try clock.sleep(Double(action.holdMS) / 1000, deadline: deadline)
                    try deliver(up, point: nil, step: "key_up")
                    if delay > 0 && index + 1 < action.text.unicodeScalars.count { try clock.sleep(delay, deadline: deadline) }
                }
            case "click":
                guard let point = checkedPoint(action.x, action.y, target: target) else { throw ProbeFailure(code: "policy_refused") }
                for index in 1...action.count {
                    let down = NativeInputEvent(kind: .mouseDown, point: point, button: action.button, clickCount: index)
                    var up = down; up.kind = .mouseUp
                    try deliver(down, point: point, step: "mouse_down", down: true)
                    try clock.sleep(Double(action.holdMS) / 1000, deadline: deadline)
                    try deliver(up, point: point, step: "mouse_up")
                }
            case "press_key":
                try postKeyChord(action.keys, holdMS: action.holdMS, deadline: deadline, deliver: deliver, clock: clock)
            case "coordinate_scroll", "scroll":
                let point: CGPoint
                let dx: Int
                let dy: Int
                if action.kind == "scroll" {
                    guard let bounds = target.elementBounds ?? target.windowBounds,
                          bounds.isFinitePositive else { throw ProbeFailure(code: "unsupported") }
                    point = CGPoint(x: bounds.midX, y: bounds.midY)
                    let horizontal = action.direction == "left" || action.direction == "right"
                    let extent = horizontal ? bounds.width : bounds.height
                    guard extent <= CGFloat(Int32.max), extent >= 1 else { throw ProbeFailure(code: "unsupported") }
                    let amount = action.amount == "page" ? Int(ceil(extent)) : 1
                    dx = action.direction == "left" ? amount : action.direction == "right" ? -amount : 0
                    dy = action.direction == "up" ? amount : action.direction == "down" ? -amount : 0
                } else {
                    guard let checked = checkedPoint(action.x, action.y, target: target) else { throw ProbeFailure(code: "policy_refused") }
                    point = checked; dx = action.dx; dy = action.dy
                }
                let semanticPage = action.kind == "scroll" && action.amount == "page"
                try deliver(NativeInputEvent(kind: .scroll, point: point,
                                             scrollUnits: semanticPage ? .pixel : .line,
                                             deltaX: dx, deltaY: dy), point: point, step: "scroll")
            case "drag":
                guard let start = checkedPoint(action.x, action.y, target: target),
                      let end = checkedPoint(action.endX, action.endY, target: target) else { throw ProbeFailure(code: "policy_refused") }
                let down = NativeInputEvent(kind: .mouseDown, point: start)
                try deliver(down, point: start, step: "mouse_down", down: true)
                for step in 1...action.steps {
                    let fraction = CGFloat(step) / CGFloat(action.steps)
                    let point = CGPoint(x: start.x + (end.x - start.x) * fraction, y: start.y + (end.y - start.y) * fraction)
                    var move = down; move.kind = .mouseMove; move.point = point
                    try deliver(move, point: point, step: "mouse_move")
                    try clock.sleep(Double(action.durationMS) / 1000 / Double(action.steps), deadline: deadline)
                }
                var up = down; up.kind = .mouseUp; up.point = end
                try deliver(up, point: end, step: "mouse_up")
            default: throw ProbeFailure(code: "unsupported")
            }
        } catch {
            failureOccurred = true
            failureBeforeDispatch = !eventAttempted && held.isEmpty && completed.isEmpty
            failureCode = (error as? ProbeFailure)?.code ?? "unknown_outcome"
        }
        if !held.isEmpty {
            let cleanupDeadline = clock.now() + 3
            var clean = true
            for release in held.reversed() {
                do { try sink.post(release, deadline: cleanupDeadline) }
                catch { clean = false }
            }
            cleanup = clean ? "complete" : "unknown"
            if clean { add("cleanup") }
            held.removeAll()
        }
        let execution: String
        if failureBeforeDispatch { execution = "not_applied" }
        else if cleanup == "unknown" || (failureOccurred && completed.isEmpty) { execution = "unknown" }
        else if failureOccurred { execution = "partially_applied" }
        else { execution = "applied" }
        return NativeActionDispatch(method: route.method, completedSteps: completed, execution: execution,
                                    cleanup: cleanup, failureCode: failureBeforeDispatch ? failureCode : nil)
    }
}

private func releaseEvent(for down: NativeInputEvent, at point: CGPoint) -> NativeInputEvent {
    var event = down
    if down.kind == .keyDown { event.kind = .keyUp }
    else { event.kind = .mouseUp; event.point = point }
    return event
}

private func sameHeldInput(_ release: NativeInputEvent, releasedBy event: NativeInputEvent) -> Bool {
    release.kind == event.kind && release.keyCode == event.keyCode && release.button == event.button && release.unicode == event.unicode
}

func checkedPoint(_ x: Double, _ y: Double, target: NativeActionTarget) -> CGPoint? {
    guard x.isFinite, y.isFinite, target.displayID != nil,
          let window = target.windowBounds, let display = target.displayBounds,
          window.isFinitePositive, display.isFinitePositive else { return nil }
    let point = CGPoint(x: x, y: y)
    return window.contains(point) && display.contains(point) ? point : nil
}

extension CGRect {
    var isFinitePositive: Bool {
        origin.x.isFinite && origin.y.isFinite && width.isFinite && height.isFinite && width > 0 && height > 0
    }
}

@MainActor private func postKeyChord(_ keys: String, holdMS: Int, deadline: TimeInterval,
                          deliver: (NativeInputEvent, CGPoint?, String, Bool) throws -> Void,
                          clock: NativeActionClock) throws {
    let codes: [String: CGKeyCode] = ["a": 0, "b": 11, "c": 8, "d": 2, "e": 14, "f": 3,
        "g": 5, "h": 4, "i": 34, "j": 38, "k": 40, "l": 37, "m": 46, "n": 45,
        "o": 31, "p": 35, "q": 12, "r": 15, "s": 1, "t": 17, "u": 32, "v": 9,
        "w": 13, "x": 7, "y": 16, "z": 6, "0": 29, "1": 18, "2": 19, "3": 20,
        "4": 21, "5": 23, "6": 22, "7": 26, "8": 28, "9": 25, "enter": 36,
        "escape": 53, "tab": 48, "space": 49, "backspace": 51, "delete": 117,
        "up": 126, "down": 125, "left": 123, "right": 124, "home": 115, "end": 119,
        "page_up": 116, "page_down": 121]
    let modifiers: [String: CGEventFlags] = ["ctrl": .maskControl, "alt": .maskAlternate, "shift": .maskShift, "meta": .maskCommand]
    let tokens = keys.split(separator: " ").map(String.init)
    guard let key = tokens.last, let code = codes[key] else { throw ProbeFailure(code: "unsupported") }
    var flags: CGEventFlags = []
    for modifier in tokens.dropLast() { guard let value = modifiers[modifier] else { throw ProbeFailure(code: "unsupported") }; flags.insert(value) }
    let down = NativeInputEvent(kind: .keyDown, keyCode: code, keyFlags: flags)
    let up = NativeInputEvent(kind: .keyUp, keyCode: code, keyFlags: flags)
    try deliver(down, nil, "key_down", true)
    try clock.sleep(Double(holdMS) / 1000, deadline: deadline)
    try deliver(up, nil, "key_up", false)
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
        guard let method = route?.method else { throw ProbeFailure(code: "unsupported") }
        if route?.requiresEnabled == true, target.enabled != true { throw ProbeFailure(code: "state_expired") }
        if route?.requiresFocused == true, !target.focused { throw ProbeFailure(code: "state_expired") }
        let initialPoint = action.kind == "click" || action.kind == "coordinate_scroll" || action.kind == "drag"
            ? CGPoint(x: action.x, y: action.y) : nil
        guard try access.supports(action.kind, target: target, point: initialPoint) else { throw ProbeFailure(code: "unsupported") }
        if action.kind == "insert" {
            guard validNativeTextSelection(try access.selection(target: target)) else {
                throw ProbeFailure(code: "state_expired")
            }
        }
        try access.check(requestID: requestID, deadline: deadline)
        let dispatched = try access.dispatch(action, target: target, method: method, poster: poster)
        if dispatched.execution == "not_applied", let failureCode = dispatched.failureCode {
            throw ProbeFailure(code: failureCode)
        }
        guard dispatched.method == method, nativeStepsValid(dispatched.completedSteps),
              ["not_applied", "applied", "partially_applied", "unknown"].contains(dispatched.execution),
              ["complete", "dirty", "unknown"].contains(dispatched.cleanup) else {
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

private func nativeStepsValid(_ steps: [String]) -> Bool {
    let allowed: Set<String> = ["focus", "press", "pick", "set_value", "unicode", "key_down", "key_up",
                                "mouse_down", "mouse_up", "mouse_move", "scroll", "cleanup"]
    return steps.count <= 128 && steps.allSatisfy(allowed.contains)
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
    switch action.kind {
    case "press": return NativeActionRoute(method: "ax_press", step: "press", requiresEnabled: true, requiresFocused: false)
    case "replace": return NativeActionRoute(method: "ax_set_value", step: "set_value", requiresEnabled: true, requiresFocused: false)
    case "insert": return NativeActionRoute(method: "cg_unicode", step: "unicode", requiresEnabled: true, requiresFocused: true)
    case "click": return NativeActionRoute(method: "cg_click", step: "mouse_down", requiresEnabled: false, requiresFocused: false)
    case "pick": return NativeActionRoute(method: "ax_pick", step: "pick", requiresEnabled: true, requiresFocused: false)
    case "focus": return NativeActionRoute(method: "ax_focus", step: "focus", requiresEnabled: true, requiresFocused: false)
    case "scroll": return NativeActionRoute(method: "cg_scroll", step: "scroll", requiresEnabled: true, requiresFocused: false)
    case "type_text": return NativeActionRoute(method: "cg_unicode", step: "key_down", requiresEnabled: false, requiresFocused: false)
    case "press_key": return NativeActionRoute(method: "cg_key", step: "key_down", requiresEnabled: false, requiresFocused: false)
    case "coordinate_scroll": return NativeActionRoute(method: "cg_scroll", step: "scroll", requiresEnabled: false, requiresFocused: false)
    case "drag": return NativeActionRoute(method: "cg_drag", step: "mouse_down", requiresEnabled: false, requiresFocused: false)
    case "focus_window": return NativeActionRoute(method: "ax_focus_window", step: "focus", requiresEnabled: false, requiresFocused: false)
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
