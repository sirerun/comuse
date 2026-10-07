import Foundation
import XCTest
@testable import ComuseMacOS

final class WireTests: XCTestCase {
    func testRemainingNativeInventoryUsesSyntheticAccessAndPosterRoutes() async throws {
        try await MainActor.run {
            let payloads = [
                #"{"id":"a1","window_ref":"w1","element_ref":"e1","state_id":"s1","kind":"press"}"#,
                #"{"id":"a1","window_ref":"w1","element_ref":"e1","state_id":"s1","kind":"pick"}"#,
                #"{"id":"a1","window_ref":"w1","element_ref":"e1","state_id":"s1","kind":"focus"}"#,
                #"{"id":"a1","window_ref":"w1","element_ref":"e1","state_id":"s1","kind":"scroll","direction":"down","amount":"line"}"#,
                #"{"id":"a1","window_ref":"w1","kind":"click","x":10,"y":20,"button":"left","count":1,"hold_ms":0}"#,
                #"{"id":"a1","window_ref":"w1","kind":"type_text","text":"hello","delay_ms":0}"#,
                #"{"id":"a1","window_ref":"w1","kind":"press_key","keys":"ctrl a","hold_ms":0}"#,
                #"{"id":"a1","window_ref":"w1","kind":"coordinate_scroll","x":10,"y":20,"dx":0,"dy":1}"#,
                #"{"id":"a1","window_ref":"w1","kind":"drag","x":10,"y":20,"end_x":30,"end_y":40,"steps":2,"duration_ms":1}"#,
                #"{"id":"a1","window_ref":"w1","kind":"focus_window"}"#
            ]
            for json in payloads {
                let action = try Self.decodeAction(json: json)
                XCTAssertTrue(validNativeAction(action), "invalid route \(action.kind)")
                let access = FakeNativeActionAccess()
                let poster = FakeNativeInputPoster()
                let result = try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0)
                XCTAssertEqual(result["method"] as? String, nativeTestMethod(action))
                XCTAssertEqual(result["completed_steps"] as? [String], [nativeTestStep(action)])
                XCTAssertEqual(result["cleanup"] as? String, "complete")
                if nativeTestMethod(action).hasPrefix("cg_") {
                    XCTAssertEqual(poster.events, ["\(action.kind):down", "\(action.kind):up"])
                }
            }
        }
    }

    func testSyntheticPosterReleasesEveryHeldRouteAfterPartialDispatch() async throws {
        try await MainActor.run {
            let payloads = [
                #"{"id":"a1","window_ref":"w1","element_ref":"e1","state_id":"s1","kind":"insert","text":"x"}"#,
                #"{"id":"a1","window_ref":"w1","kind":"type_text","text":"x"}"#,
                #"{"id":"a1","window_ref":"w1","kind":"click","x":10,"y":20,"button":"left","count":1}"#,
                #"{"id":"a1","window_ref":"w1","kind":"press_key","keys":"ctrl a"}"#,
                #"{"id":"a1","window_ref":"w1","kind":"coordinate_scroll","x":10,"y":20,"dy":1}"#,
                #"{"id":"a1","window_ref":"w1","kind":"drag","x":10,"y":20,"end_x":30,"end_y":40,"steps":2,"duration_ms":1}"#
            ]
            for json in payloads {
                let action = try Self.decodeAction(json: json)
                let access = FakeNativeActionAccess()
                let poster = FakeNativeInputPoster()
                poster.failAfterDown = true
                XCTAssertThrowsError(try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0)) { error in
                    XCTAssertEqual((error as? ProbeFailure)?.code, "unknown_outcome")
                }
                XCTAssertEqual(poster.events, ["\(action.kind):down", "\(action.kind):up"])
                XCTAssertEqual(access.dispatchCount, 1)
            }
        }
    }

    func testNativeActionExecutorUsesOneQualifiedMethodPerKind() async throws {
        try await MainActor.run {
            let cases: [(String, String, String, String)] = [
                ("press", "", "ax_press", "press"),
                ("replace", "new value", "ax_set_value", "set_value"),
                ("insert", "inserted", "cg_unicode", "unicode")
            ]
            for (kind, text, method, step) in cases {
                let action = try Self.decodeAction(kind: kind, text: text)
                let access = FakeNativeActionAccess()
                access.target = NativeActionTarget(actionID: "a1", windowRef: "w1", elementRef: "e1", stateID: "s1",
                                                   process: NativeProcess(pid: 1, bundleID: "test", launchID: "launch"),
                                                   role: "AXButton", classification: "normal", enabled: true, focused: true, windowFocused: true)
                let poster = FakeNativeInputPoster()
                let result = try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0)
                XCTAssertEqual(access.dispatchCount, 1)
                XCTAssertEqual(access.dispatchedMethod, method)
                XCTAssertEqual(result["method"] as? String, method)
                XCTAssertEqual(result["completed_steps"] as? [String], [step])
                XCTAssertEqual(result["execution"] as? String, "applied")
                XCTAssertEqual(poster.values, kind == "insert" ? [text] : [])
            }
        }
    }

    func testNativeActionExecutorFailsClosedOnUnqualifiedOrUncertainTarget() async throws {
        try await MainActor.run {
            let runtime = NativeRuntime(id: 78,
                                        config: NativeConfig(schemaVersion: 1,
                                                             scope: NativeScope(processes: [], expiresAtUnixMilli: 1),
                                                             allowValues: false),
                                        processes: [])
            let action = try Self.decodeAction(kind: "press", text: "")
            XCTAssertThrowsError(try runtime.executeScopedAction(
                NativeRequest(schemaVersion: 1, requestID: "r", operation: "execute", windowRef: nil,
                              elementRef: nil, stateID: nil, budget: nil, action: action), requestID: 0)) { error in
                XCTAssertEqual((error as? ProbeFailure)?.code, "unsupported")
            }

            let access = FakeNativeActionAccess()
            let poster = FakeNativeInputPoster()
            access.target = NativeActionTarget(actionID: "a1", windowRef: "w1", elementRef: "e1", stateID: "s1",
                                               process: NativeProcess(pid: 1, bundleID: "test", launchID: "launch"),
                                               role: "AXButton", classification: "unknown", enabled: true, focused: true, windowFocused: true)
            XCTAssertThrowsError(try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0)) { error in
                XCTAssertEqual((error as? ProbeFailure)?.code, "policy_refused")
            }
            XCTAssertEqual(access.dispatchCount, 0)
        }
    }

    func testNativeActionExecutorDoesNotRetryAfterUncertainDispatch() async throws {
        try await MainActor.run {
            let access = FakeNativeActionAccess()
            access.failure = ProbeFailure(code: "unknown_outcome")
            XCTAssertThrowsError(try NativeActionExecutor(access: access, poster: FakeNativeInputPoster())
                .execute(try Self.decodeAction(kind: "press", text: ""), requestID: 0)) { error in
                XCTAssertEqual((error as? ProbeFailure)?.code, "unknown_outcome")
            }
            XCTAssertEqual(access.dispatchCount, 1)
        }
    }

    func testNativeActionExecutorRejectsStaleIdentityFocusAndUnreliableSelectionBeforeDispatch() async throws {
        try await MainActor.run {
            let action = try Self.decodeAction(kind: "insert", text: "x")
            let poster = FakeNativeInputPoster()
            let access = FakeNativeActionAccess()

            access.target = NativeActionTarget(actionID: "other", windowRef: "w1", elementRef: "e1", stateID: "s1",
                                               process: NativeProcess(pid: 1, bundleID: "test", launchID: "launch"),
                                               role: "AXTextField", classification: "normal", enabled: true, focused: true, windowFocused: true)
            XCTAssertThrowsError(try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0))
            XCTAssertEqual(access.dispatchCount, 0)

            access.target = NativeActionTarget(actionID: "a1", windowRef: "w1", elementRef: "e1", stateID: "s1",
                                               process: NativeProcess(pid: 1, bundleID: "test", launchID: "launch"),
                                               role: "AXTextField", classification: "normal", enabled: true, focused: true, windowFocused: false)
            XCTAssertThrowsError(try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0)) { error in
                XCTAssertEqual((error as? ProbeFailure)?.code, "state_expired")
            }
            XCTAssertEqual(access.dispatchCount, 0)

            access.target = NativeActionTarget(actionID: "a1", windowRef: "w1", elementRef: "e1", stateID: "s1",
                                               process: NativeProcess(pid: 1, bundleID: "test", launchID: "launch"),
                                               role: "AXTextField", classification: "normal", enabled: true, focused: false, windowFocused: true)
            XCTAssertThrowsError(try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0))
            XCTAssertEqual(access.dispatchCount, 0)

            access.target = NativeActionTarget(actionID: "a1", windowRef: "w1", elementRef: "e1", stateID: "s1",
                                               process: NativeProcess(pid: 1, bundleID: "test", launchID: "launch"),
                                               role: "AXTextField", classification: "normal", enabled: true, focused: true, windowFocused: true)
            access.textSelection = NativeTextSelection(text: "short", location: 5, length: 1)
            XCTAssertThrowsError(try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0)) { error in
                XCTAssertEqual((error as? ProbeFailure)?.code, "state_expired")
            }
            XCTAssertEqual(access.dispatchCount, 0)

            access.textSelection = NativeTextSelection(text: "short", location: 5, length: 0)
            access.supportsActions = false
            XCTAssertThrowsError(try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0)) { error in
                XCTAssertEqual((error as? ProbeFailure)?.code, "unsupported")
            }
            XCTAssertEqual(access.dispatchCount, 0)
            XCTAssertTrue(poster.values.isEmpty)
        }
    }

    func testNativeActionTextBoundsAreUTF8Bytes() throws {
        XCTAssertTrue(validNativeAction(try Self.decodeAction(kind: "replace", text: "")))
        XCTAssertFalse(validNativeAction(try Self.decodeAction(kind: "insert", text: "")))
        XCTAssertTrue(validNativeAction(try Self.decodeAction(kind: "insert", text: "🙂")))
        XCTAssertFalse(validNativeAction(try Self.decodeAction(kind: "replace", text: String(repeating: "x", count: 8193))))
        XCTAssertTrue(validNativeTextSelection(NativeTextSelection(text: "a🙂b", location: 1, length: 2)))
        XCTAssertFalse(validNativeTextSelection(NativeTextSelection(text: "a🙂b", location: 4, length: 1)))
    }

    private static func decodeAction(kind: String, text: String) throws -> NativeAction {
        let value: [String: Any] = ["id": "a1", "window_ref": "w1", "element_ref": "e1",
                                    "state_id": "s1", "kind": kind, "text": text]
        return try JSONDecoder().decode(NativeAction.self, from: JSONSerialization.data(withJSONObject: value))
    }

    private static func decodeAction(json: String) throws -> NativeAction {
        try JSONDecoder().decode(NativeAction.self, from: Data(json.utf8))
    }

    func testDecodeGoObserveRequest() throws {
        let payload = #"{"schema_version":1,"request_id":"r1","operation":"observe","window_ref":"w1","budget":{"max_depth":8,"max_nodes":64,"max_bytes":8192,"timeout":1000000000}}"#.data(using: .utf8)!
        let request = try JSONDecoder().decode(NativeRequest.self, from: payload)
        XCTAssertEqual(request.requestID, "r1")
        XCTAssertEqual(request.windowRef, "w1")
        XCTAssertEqual(request.budget?.timeoutNanoseconds, 1_000_000_000)
        XCTAssertEqual(request.budget?.maxDepth, 8)
    }

    func testDecodeGoPressAndReplaceWithOmittedText() throws {
        let press = #"{"schema_version":1,"request_id":"r2","operation":"execute","action":{"id":"a1","window_ref":"w1","element_ref":"e1","state_id":"s1","kind":"press"}}"#.data(using: .utf8)!
        let decodedPress = try JSONDecoder().decode(NativeRequest.self, from: press)
        XCTAssertEqual(decodedPress.action?.text, "")

        let replace = #"{"schema_version":1,"request_id":"r3","operation":"execute","action":{"id":"a2","window_ref":"w1","element_ref":"e1","state_id":"s1","kind":"replace"}}"#.data(using: .utf8)!
        let decodedReplace = try JSONDecoder().decode(NativeRequest.self, from: replace)
        XCTAssertEqual(decodedReplace.action?.text, "")
    }

    func testErrorEnvelopeUsesCodeStringAndNoResult() throws {
        let data = try JSONEncoder().encode(NativeEnvelope(requestID: "r4", status: "error", result: nil,
                                                          error: "permission_denied"))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(object["error"] as? String, "permission_denied")
        XCTAssertNil(object["result"])
        XCTAssertEqual(object["status"] as? String, "error")
    }

    func testNativeErrorClassificationKeepsInternalFailuresDistinct() throws {
        let malformedInput: Error
        do {
            _ = try JSONDecoder().decode(NativeRequest.self, from: Data("{}".utf8))
            XCTFail("expected malformed request")
            return
        } catch {
            malformedInput = error
        }

        XCTAssertEqual(nativeErrorCode(for: malformedInput), "invalid_request")
        XCTAssertEqual(nativeErrorCode(for: ProbeFailure(code: "policy_refused")), "policy_refused")
        XCTAssertEqual(nativeErrorCode(for: ProbeFailure(code: "private diagnostic")), "internal_error")
        XCTAssertEqual(nativeErrorCode(for: NSError(domain: "synthetic", code: 7)), "internal_error")
    }

    func testPermissionLossPurgesReferencesAndSnapshots() async throws {
        try await MainActor.run {
            let process = NativeProcess(pid: 1, bundleID: "example.app", launchID: "1.1")
            let runtime = NativeRuntime(
                id: 77,
                config: NativeConfig(schemaVersion: 1,
                                     scope: NativeScope(processes: [process], expiresAtUnixMilli: 1),
                                     allowValues: false),
                processes: [process]
            )
            let element = AXUIElementCreateSystemWide()
            ReferenceStore.shared.set(runtimeID: 77, value: [
                "ref": NativeReference(element: element, process: process, windowRef: nil,
                                        kind: .window, lastSeen: 0)
            ])
            SnapshotStore.shared.set(runtimeID: 77, value: [
                "state": NativeSnapshot(stateID: "state", windowRef: "window", process: process,
                                         digest: "digest", complete: true, coverageReason: "", refs: [],
                                         createdAt: 0, byteCount: 0)
            ])

            XCTAssertThrowsError(try runtime.requireAccessibilityPermission(false))
            XCTAssertTrue(ReferenceStore.shared.get(runtimeID: 77).isEmpty)
            XCTAssertTrue(SnapshotStore.shared.get(runtimeID: 77).isEmpty)
            XCTAssertNoThrow(try runtime.requireAccessibilityPermission(true))
            XCTAssertTrue(ReferenceStore.shared.get(runtimeID: 77).isEmpty)
            XCTAssertTrue(SnapshotStore.shared.get(runtimeID: 77).isEmpty)
        }
    }

    func testBoundedTextUsesUTF8BytesAndPreservesScalarBoundaries() {
        let bounded = boundedUTF8Prefix("a🙂b", byteLimit: 4)
        XCTAssertEqual(bounded.text, "a")
        XCTAssertTrue(bounded.truncated)

        let exact = boundedUTF8Prefix("a🙂", byteLimit: 5)
        XCTAssertEqual(exact.text, "a🙂")
        XCTAssertFalse(exact.truncated)
    }

    func testChildFrontierBudgetReportsDroppedNodes() {
        XCTAssertEqual(boundedChildCount(100_000, limit: 12).count, 12)
        XCTAssertTrue(boundedChildCount(100_000, limit: 12).truncated)
        XCTAssertEqual(boundedChildCount(4, limit: 4).count, 4)
        XCTAssertFalse(boundedChildCount(4, limit: 4).truncated)
        XCTAssertEqual(boundedChildCount(4, limit: 0).count, 0)
        XCTAssertTrue(boundedChildCount(4, limit: 0).truncated)
    }

    func testDepthBoundMarksEvenAnOmittedLeafAsOutOfCoverage() {
        let maxDepth = 3
        let leafAtFrontierDepth = maxDepth
        XCTAssertFalse(depthIsIncluded(leafAtFrontierDepth, maximum: maxDepth))
        XCTAssertTrue(depthIsIncluded(maxDepth - 1, maximum: maxDepth))
    }
}

@MainActor private final class FakeNativeActionAccess: NativeActionAccess {
    var target = NativeActionTarget(actionID: "a1", windowRef: "w1", elementRef: "e1", stateID: "s1",
                                    process: NativeProcess(pid: 1, bundleID: "test", launchID: "launch"),
                                    role: "AXButton", classification: "normal", enabled: true, focused: false, windowFocused: true)
    var dispatchCount = 0
    var dispatchedMethod: String?
    var failure: Error?
    var textSelection = NativeTextSelection(text: "hello", location: 1, length: 0)
    var supportsActions = true

    func check(requestID: UInt64, deadline: TimeInterval) throws {}
    func revalidate(_ action: NativeAction) throws -> NativeActionTarget {
        if action.elementRef.isEmpty && action.stateID.isEmpty {
            target = NativeActionTarget(actionID: action.id, windowRef: action.windowRef, elementRef: "",
                                        stateID: "", process: target.process, role: target.role,
                                        classification: target.classification, enabled: target.enabled,
                                        focused: target.focused, windowFocused: target.windowFocused)
        }
        return target
    }
    func supports(_ kind: String, target: NativeActionTarget) throws -> Bool { supportsActions }
    func selection(target: NativeActionTarget) throws -> NativeTextSelection { textSelection }
    func dispatch(_ action: NativeAction, target: NativeActionTarget, method: String,
                  poster: NativeInputPoster) throws -> NativeActionDispatch {
        dispatchCount += 1
        dispatchedMethod = method
        if let failure { throw failure }
        if method.hasPrefix("cg_") { return try poster.post(action, target: target) }
        let step = ["ax_press": action.kind == "click" ? "click" : "press",
                    "ax_set_value": "set_value", "ax_pick": "pick", "ax_focus": "focus",
                    "ax_scroll": "scroll", "ax_focus_window": "focus_window"][method] ?? ""
        return NativeActionDispatch(method: method, completedSteps: [step], execution: "applied")
    }
}

@MainActor private final class FakeNativeInputPoster: NativeInputPoster {
    var values: [String] = []
    var events: [String] = []
    var failAfterDown = false

    func post(_ action: NativeAction, target: NativeActionTarget) throws -> NativeActionDispatch {
        if action.kind == "insert" || action.kind == "type_text" { values.append(action.text) }
        let route = action.kind
        events.append("\(route):down")
        defer { events.append("\(route):up") }
        if failAfterDown { throw ProbeFailure(code: "unknown_outcome") }
        return NativeActionDispatch(method: nativeTestMethod(action), completedSteps: [nativeTestStep(action)], execution: "applied")
    }
}

private func nativeTestMethod(_ action: NativeAction) -> String {
    switch action.kind {
    case "insert", "type_text": return "cg_unicode"
    case "click": return "cg_click"
    case "press_key": return "cg_key"
    case "coordinate_scroll": return "cg_scroll"
    case "drag": return "cg_drag"
    case "pick": return "ax_pick"
    case "focus": return "ax_focus"
    case "scroll": return "ax_scroll"
    case "focus_window": return "ax_focus_window"
    case "replace": return "ax_set_value"
    default: return "ax_press"
    }
}

private func nativeTestStep(_ action: NativeAction) -> String {
    switch action.kind {
    case "insert", "type_text": return "unicode"
    case "click": return "click"
    case "press_key": return "key"
    case "coordinate_scroll", "scroll": return "scroll"
    case "drag": return "drag"
    case "pick": return "pick"
    case "focus": return "focus"
    case "focus_window": return "focus_window"
    case "replace": return "set_value"
    default: return "press"
    }
}
