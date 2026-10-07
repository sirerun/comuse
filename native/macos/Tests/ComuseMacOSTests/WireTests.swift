import Foundation
import ApplicationServices
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
                if action.kind == "type_text" || action.kind == "press_key" {
                    access.target = access.target.withFocusedInput(role: "AXTextField", classification: "normal")
                }
                let result = try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0)
                XCTAssertEqual(result["method"] as? String, nativeTestMethod(action))
                XCTAssertEqual(result["completed_steps"] as? [String], nativeTestSteps(action))
                XCTAssertEqual(result["cleanup"] as? String, "complete")
                if nativeTestMethod(action).hasPrefix("cg_") {
                    XCTAssertFalse(poster.events.isEmpty)
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
                #"{"id":"a1","window_ref":"w1","kind":"drag","x":10,"y":20,"end_x":30,"end_y":40,"steps":2,"duration_ms":1}"#
            ]
            for json in payloads {
                let action = try Self.decodeAction(json: json)
                let access = FakeNativeActionAccess()
                let poster = FakeNativeInputPoster()
                if action.kind == "insert" { access.target = access.target.withFocus(true) }
                if action.kind == "type_text" || action.kind == "press_key" {
                    access.target = access.target.withFocusedInput(role: "AXTextField", classification: "normal")
                }
                poster.failAfterDown = true
                let result = try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0)
                XCTAssertTrue(["unknown", "partially_applied"].contains(result["execution"] as? String ?? ""))
                XCTAssertEqual(result["cleanup"] as? String, "complete")
                XCTAssertTrue(poster.events.contains(where: { $0.hasSuffix("up") || $0 == "cleanup" }))
                XCTAssertEqual(access.dispatchCount, 1)
            }
        }
    }

    func testSyntheticPosterRevalidatesFocusedClassificationBeforeEachEvent() async throws {
        try await MainActor.run {
            let action = try Self.decodeAction(json: #"{"id":"a1","window_ref":"w1","kind":"type_text","text":"ab"}"#)
            let access = FakeNativeActionAccess()
            access.target = access.target.withFocusedInput(role: "AXTextField", classification: "normal")
            let poster = FakeNativeInputPoster()
            poster.afterFirstPost = { access.target = access.target.withFocusedInput(role: "AXTextField", classification: "secure") }

            let result = try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0)
            XCTAssertEqual(result["execution"] as? String, "partially_applied")
            XCTAssertEqual(result["cleanup"] as? String, "complete")
            XCTAssertEqual(result["completed_steps"] as? [String], ["key_down", "cleanup"])
            XCTAssertEqual(poster.events, ["key_down", "key_up"])
        }
    }

    func testRawKeyboardStopsWhenSameRoleNormalFocusIdentityChangesAndCleansUp() async throws {
        try await MainActor.run {
            for (json, focusChange) in [
                (#"{"id":"a1","window_ref":"w1","kind":"type_text","text":"ab"}"#, "between_scalars"),
                (#"{"id":"a1","window_ref":"w1","kind":"press_key","keys":"ctrl a"}"#, "between_down_up")
            ] {
                let action = try Self.decodeAction(json: json)
                let access = FakeNativeActionAccess()
                access.target = access.target.withFocusedInput(role: "AXTextField", classification: "normal", identity: "first")
                let poster = FakeNativeInputPoster()
                let changeFocus = {
                    access.target = access.target.withFocusedInput(role: "AXTextField", classification: "normal", identity: "second")
                }
                if focusChange == "between_scalars" { poster.afterFirstKeyUp = changeFocus }
                else { poster.afterFirstPost = changeFocus }
                let result = try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0)
                XCTAssertEqual(result["execution"] as? String, "partially_applied")
                XCTAssertEqual(result["cleanup"] as? String, "complete")
                XCTAssertEqual(poster.events, ["key_down", "key_up"])
                XCTAssertEqual(access.dispatchCount, 1)
            }
        }
    }

    func testPhysicalKeyRoutesRequireInspectedStableUSLayoutAndAcceptModifierOrder() async throws {
        try await MainActor.run {
            XCTAssertFalse(nativeKeyboardLayoutQualified(initial: nil, current: nil))
            XCTAssertFalse(nativeKeyboardLayoutQualified(initial: "com.apple.keylayout.French", current: "com.apple.keylayout.French"))
            XCTAssertFalse(nativeKeyboardLayoutQualified(initial: "com.apple.keylayout.US", current: "com.apple.keylayout.French"))
            XCTAssertTrue(nativeKeyboardLayoutQualified(initial: "com.apple.keylayout.US", current: "com.apple.keylayout.US"))

            for keys in ["ctrl shift a", "shift ctrl a"] {
                let action = try Self.decodeAction(json: #"{"id":"a1","window_ref":"w1","kind":"press_key","keys":"\#(keys)"}"#)
                let access = FakeNativeActionAccess()
                access.target = access.target.withFocusedInput(role: "AXTextField", classification: "normal")
                let poster = FakeNativeInputPoster()
                poster.layoutIdentifier = nil
                XCTAssertThrowsError(try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0))
                XCTAssertTrue(poster.events.isEmpty)

                poster.layoutIdentifier = "com.apple.keylayout.US"
                poster.afterFirstPost = { poster.layoutIdentifier = "com.apple.keylayout.French" }
                let changed = try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0)
                XCTAssertEqual(changed["execution"] as? String, "partially_applied")
                XCTAssertEqual(changed["cleanup"] as? String, "complete")
                XCTAssertEqual(poster.events, ["key_down", "key_up"])
            }
        }
    }

    func testCancellationAfterDownUsesIndependentReleasePath() async throws {
        try await MainActor.run {
            let action = try Self.decodeAction(json: #"{"id":"a1","window_ref":"w1","kind":"type_text","text":"x"}"#)
            let access = FakeNativeActionAccess()
            access.target = access.target.withFocusedInput(role: "AXTextField", classification: "normal")
            let poster = FakeNativeInputPoster()
            poster.afterFirstPost = { access.cancelled = true }

            let result = try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0)
            XCTAssertEqual(result["execution"] as? String, "partially_applied")
            XCTAssertEqual(result["cleanup"] as? String, "complete")
            XCTAssertEqual(poster.events, ["key_down", "key_up"])
        }
    }

    func testCleanupFailureReturnsUnknownAndDoesNotClaimApplied() async throws {
        try await MainActor.run {
            let action = try Self.decodeAction(json: #"{"id":"a1","window_ref":"w1","kind":"click","x":10,"y":20,"button":"left","count":1}"#)
            let access = FakeNativeActionAccess()
            let poster = FakeNativeInputPoster()
            poster.failAfterDown = true
            poster.failCleanup = true

            let result = try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0)
            XCTAssertEqual(result["execution"] as? String, "unknown")
            XCTAssertEqual(result["cleanup"] as? String, "unknown")
            XCTAssertEqual(result["completed_steps"] as? [String], [])
            XCTAssertNotEqual(result["execution"] as? String, "applied")
        }
    }

    func testCoordinateRoutesRejectProtectedOrUnknownHitTargetsBeforeDispatch() async throws {
        try await MainActor.run {
            for classification in ["secure", "unknown"] {
                let action = try Self.decodeAction(json: #"{"id":"a1","window_ref":"w1","kind":"coordinate_scroll","x":10,"y":20,"dx":0,"dy":1}"#)
                let access = FakeNativeActionAccess()
                access.hitClassification = classification
                let poster = FakeNativeInputPoster()

                XCTAssertThrowsError(try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0)) { error in
                    XCTAssertEqual((error as? ProbeFailure)?.code, "policy_refused")
                }
                XCTAssertEqual(access.dispatchCount, 0)
                XCTAssertTrue(poster.events.isEmpty)
            }
        }
    }

    func testRawKeyboardRefusesSecureFocusedChildBeforeDispatch() async throws {
        try await MainActor.run {
            let action = try Self.decodeAction(json: #"{"id":"a1","window_ref":"w1","kind":"press_key","keys":"a"}"#)
            let access = FakeNativeActionAccess()
            access.target = access.target.withFocusedInput(role: "AXSecureTextField", classification: "secure")
            let poster = FakeNativeInputPoster()

            XCTAssertThrowsError(try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0)) { error in
                XCTAssertEqual((error as? ProbeFailure)?.code, "policy_refused")
            }
            XCTAssertEqual(access.dispatchCount, 0)
            XCTAssertTrue(poster.events.isEmpty)
        }
    }

    func testNativeActionExecutorUsesOneQualifiedMethodPerKind() async throws {
        try await MainActor.run {
            let cases: [(String, String, String, String)] = [
                ("press", "", "ax_press", "press"),
                ("replace", "new value", "ax_set_value", "set_value"),
                ("insert", "inserted", "cg_unicode", "key_down")
            ]
            for (kind, text, method, step) in cases {
                let action = try Self.decodeAction(kind: kind, text: text)
                let access = FakeNativeActionAccess()
                access.target = NativeActionTarget(actionID: "a1", windowRef: "w1", elementRef: "e1", stateID: "s1",
                                                   process: NativeProcess(pid: 1, bundleID: "test", launchID: "launch"),
                                                   role: "AXButton", classification: "normal", enabled: true, focused: true, windowFocused: true,
                                                   focusedRole: "AXTextField", focusedClassification: "normal", focusedIdentity: "focus-1",
                                                   windowBounds: CGRect(x: 0, y: 0, width: 1000, height: 1000),
                                                   displayBounds: CGRect(x: 0, y: 0, width: 1000, height: 1000), displayID: 1)
                let poster = FakeNativeInputPoster()
                let result = try NativeActionExecutor(access: access, poster: poster).execute(action, requestID: 0)
                XCTAssertEqual(access.dispatchCount, 1)
                XCTAssertEqual(access.dispatchedMethod, method)
                XCTAssertEqual(result["method"] as? String, method)
                XCTAssertEqual(result["completed_steps"] as? [String], kind == "insert" ? ["key_down", "key_up"] : [step])
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

    func testCoordinateHitValidatorRejectsForeignWrongWindowAndStaleGeometry() {
        let point = CGPoint(x: 5, y: 5)
        let bounds = CGRect(x: 0, y: 0, width: 10, height: 10)
        let base = NativeCoordinateHitFacts(point: point, expectedPID: 10, actualPID: 10,
                                           targetBounds: bounds, hitBounds: bounds, exactWindow: true,
                                           targetRelated: true, exactTarget: true, requiresExactTarget: false)
        XCTAssertTrue(validNativeCoordinateHit(base))
        XCTAssertFalse(validNativeCoordinateHit(NativeCoordinateHitFacts(point: point, expectedPID: 10, actualPID: 11,
            targetBounds: bounds, hitBounds: bounds, exactWindow: true, targetRelated: true,
            exactTarget: true, requiresExactTarget: false)))
        XCTAssertFalse(validNativeCoordinateHit(NativeCoordinateHitFacts(point: point, expectedPID: 10, actualPID: 10,
            targetBounds: bounds, hitBounds: bounds, exactWindow: false, targetRelated: true,
            exactTarget: true, requiresExactTarget: false)))
        XCTAssertFalse(validNativeCoordinateHit(NativeCoordinateHitFacts(point: point, expectedPID: 10, actualPID: 10,
            targetBounds: bounds, hitBounds: CGRect(x: 6, y: 6, width: 2, height: 2), exactWindow: true,
            targetRelated: true, exactTarget: false, requiresExactTarget: true)))
        XCTAssertFalse(validNativeCoordinateHit(NativeCoordinateHitFacts(point: point, expectedPID: 10, actualPID: 10,
            targetBounds: bounds, hitBounds: CGRect(x: CGFloat.infinity, y: 0, width: 1, height: 1), exactWindow: true,
            targetRelated: true, exactTarget: true, requiresExactTarget: false)))
    }

    func testReferenceProducersRejectForeignAXPIDAndChangedLaunchIdentity() {
        let scoped = NativeProcess(pid: 10, bundleID: "example.app", launchID: "launch-a")
        for producer in ["enumerated_window", "focused_window"] {
            XCTAssertFalse(nativeElementIdentityIsCurrent(actualPID: 11, expected: scoped, current: scoped), producer)
            XCTAssertFalse(nativeElementIdentityIsCurrent(actualPID: 10, expected: scoped,
                current: NativeProcess(pid: 10, bundleID: "example.app", launchID: "launch-b")), producer)
        }
        XCTAssertTrue(nativeElementIdentityIsCurrent(actualPID: 10, expected: scoped, current: scoped))
    }

    func testWindowTitleEvidenceDistinguishesObservedEmptyFromUnavailable() {
        XCTAssertEqual(nativeWindowTitleEvidence(""), "")
        XCTAssertNil(nativeWindowTitleEvidence(nil))
        XCTAssertNil(nativeWindowTitleEvidence(NSAttributedString(string: "not a title string")))
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
        var value: [String: Any] = ["id": "a1", "window_ref": "w1", "element_ref": "e1",
                                    "state_id": "s1", "kind": kind]
        if kind == "replace" || kind == "insert" { value["text"] = text }
        let data = try JSONSerialization.data(withJSONObject: value, options: [.sortedKeys])
        return try decodeAction(json: String(decoding: data, as: UTF8.self))
    }

    private static func decodeAction(json: String) throws -> NativeAction {
        guard var action = try JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any] else {
            throw ProbeFailure(code: "invalid_request")
        }
        action["element_ref"] = action["element_ref"] ?? ""
        action["state_id"] = action["state_id"] ?? ""
        switch action["kind"] as? String {
        case "click": action["hold_ms"] = action["hold_ms"] ?? 0
        case "type_text": action["delay_ms"] = action["delay_ms"] ?? 0
        case "press_key": action["hold_ms"] = action["hold_ms"] ?? 0
        default: break
        }
        let actionData = try JSONSerialization.data(withJSONObject: action, options: [.sortedKeys])
        let request = #"{"schema_version":1,"request_id":"r","operation":"execute","action":\#(String(decoding: actionData, as: UTF8.self))}"#
        return try XCTUnwrap(decodeNativeRequest(Data(request.utf8)).action)
    }

    func testDecodeGoObserveRequest() throws {
        let payload = #"{"schema_version":1,"request_id":"r1","operation":"observe","window_ref":"w1","budget":{"max_depth":8,"max_nodes":64,"max_bytes":8192,"timeout":1000000000}}"#.data(using: .utf8)!
        let request = try decodeNativeRequest(payload)
        XCTAssertEqual(request.requestID, "r1")
        XCTAssertEqual(request.windowRef, "w1")
        XCTAssertEqual(request.budget?.timeoutNanoseconds, 1_000_000_000)
        XCTAssertEqual(request.budget?.maxDepth, 8)
    }

    func testDecodeGoPressAndReplaceWithExplicitEmptyText() throws {
        let press = #"{"schema_version":1,"request_id":"r2","operation":"execute","action":{"id":"a1","window_ref":"w1","element_ref":"e1","state_id":"s1","kind":"press"}}"#.data(using: .utf8)!
        let decodedPress = try decodeNativeRequest(press)
        XCTAssertEqual(decodedPress.action?.text, "")

        let replace = #"{"schema_version":1,"request_id":"r3","operation":"execute","action":{"id":"a2","window_ref":"w1","element_ref":"e1","state_id":"s1","kind":"replace","text":""}}"#.data(using: .utf8)!
        let decodedReplace = try decodeNativeRequest(replace)
        XCTAssertEqual(decodedReplace.action?.text, "")
    }

    func testProductionAXValueInspectionRejectsWrongCFTypes() throws {
        XCTAssertNil(nativeAXValue("wrong type" as CFString))
        XCTAssertNil(nativeAXValue(kCFBooleanTrue))
        var point = CGPoint(x: 1, y: 2)
        let value = try XCTUnwrap(AXValueCreate(.cgPoint, &point))
        XCTAssertNotNil(nativeAXValue(value))
        XCTAssertEqual(AXValueGetType(try XCTUnwrap(nativeAXValue(value))), .cgPoint)
    }

    func testProductionDecoderRequiresAllActionFieldsIncludingZeroAndEmptyValues() throws {
        let fields: [[String: Any]] = [
            ["kind": "replace", "text": ""],
            ["kind": "click", "x": 0, "y": 0, "button": "left", "count": 1, "hold_ms": 0],
            ["kind": "type_text", "text": "x", "delay_ms": 0],
            ["kind": "press_key", "keys": "a", "hold_ms": 0],
            ["kind": "coordinate_scroll", "x": 0, "y": 0, "dx": 0, "dy": 1],
            ["kind": "drag", "x": 0, "y": 0, "end_x": 1, "end_y": 1, "steps": 2, "duration_ms": 1]
        ]
        for specific in fields {
            var action: [String: Any] = ["id": "a", "window_ref": "w", "element_ref": "", "state_id": ""]
            action.merge(specific) { _, new in new }
            let envelope: [String: Any] = ["schema_version": 1, "request_id": "r", "operation": "execute", "action": action]
            _ = try decodeNativeRequest(JSONSerialization.data(withJSONObject: envelope))
            for key in specific.keys where key != "kind" {
                var missing = action; missing.removeValue(forKey: key)
                var invalid = envelope; invalid["action"] = missing
                XCTAssertThrowsError(try decodeNativeRequest(JSONSerialization.data(withJSONObject: invalid)), key)
            }
        }
    }

    func testProductionDecoderRejectsDuplicateUnknownNullAndExtraneousMembersRecursively() throws {
        let samples = [
            #"{"schema_version":1,"request_id":"r","request_id":"x","operation":"doctor"}"#,
            #"{"schema_version":1,"request_id":"r","operation":"doctor","budget":{"max_depth":1,"max_depth":1,"max_nodes":1,"max_bytes":1,"timeout":1}}"#,
            #"{"schema_version":1,"request_id":"r","operation":"doctor","harmless":0}"#,
            #"{"schema_version":1,"request_id":"r","operation":"execute","action":{"id":"a","window_ref":"w","element_ref":"e","state_id":"s","kind":"replace","text":null}}"#,
            #"{"schema_version":1,"request_id":"r","operation":"execute","action":{"id":"a","window_ref":"w","element_ref":"e","state_id":"s","kind":"press","x":0}}"#,
            #"{"schema_version":1,"request_id":"r","operation":"observe","window_ref":"w","budget":{"max_depth":1,"max_nodes":1,"max_bytes":1,"timeout":1,"extra":0}}"#,
            #"{"schema_version":1,"request_id":"r","operation":"doctor"} trailing"#
        ]
        for sample in samples {
            XCTAssertThrowsError(try decodeNativeRequest(Data(sample.utf8)), sample)
        }
        XCTAssertThrowsError(try decodeNativeConfig(Data(#"{"schema_version":1,"schema_version":1,"scope":{"processes":[],"expires_at_unix_milli":1},"allow_values":false}"#.utf8)))
        XCTAssertThrowsError(try decodeNativeConfig(Data(#"{"schema_version":1,"scope":{"processes":[{"pid":1,"bundle_id":"x","launch_id":"l","extra":0}],"expires_at_unix_milli":1},"allow_values":false}"#.utf8)))
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
            _ = try decodeNativeRequest(Data("{}".utf8))
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
                                    role: "AXButton", classification: "normal", enabled: true, focused: false, windowFocused: true,
                                    focusedRole: nil, focusedClassification: nil, focusedIdentity: nil,
                                    windowBounds: CGRect(x: 0, y: 0, width: 1000, height: 1000),
                                    displayBounds: CGRect(x: 0, y: 0, width: 1000, height: 1000), displayID: 1)
    var dispatchCount = 0
    var dispatchedMethod: String?
    var failure: Error?
    var textSelection = NativeTextSelection(text: "hello", location: 1, length: 0)
    var supportsActions = true
    var cancelled = false
    var hitClassification = "normal"
    var layoutIdentifier: String? = "com.apple.keylayout.US"

    func check(requestID: UInt64, deadline: TimeInterval) throws {
        if cancelled { throw ProbeFailure(code: "cancelled") }
        if ProcessInfo.processInfo.systemUptime >= deadline { throw ProbeFailure(code: "budget_exceeded") }
    }
    func revalidate(_ action: NativeAction) throws -> NativeActionTarget {
        if action.elementRef.isEmpty {
            target = NativeActionTarget(actionID: target.actionID, windowRef: target.windowRef, elementRef: "", stateID: "",
                                        process: target.process, role: "AXWindow", classification: "normal",
                                        enabled: target.enabled, focused: false, windowFocused: target.windowFocused,
                                        focusedRole: target.focusedRole, focusedClassification: target.focusedClassification,
                                        focusedIdentity: target.focusedIdentity,
                                        windowBounds: target.windowBounds, elementBounds: target.elementBounds,
                                        displayBounds: target.displayBounds, displayID: target.displayID)
        }
        return target
    }
    func supports(_ kind: String, target: NativeActionTarget, point: CGPoint?) throws -> Bool {
        guard supportsActions else { return false }
        if point != nil && ["click", "coordinate_scroll", "scroll", "drag"].contains(kind), hitClassification != "normal" {
            throw ProbeFailure(code: "policy_refused")
        }
        if ["type_text", "press_key"].contains(kind), target.focusedClassification == "secure" {
            throw ProbeFailure(code: "policy_refused")
        }
        return true
    }
    func selection(target: NativeActionTarget) throws -> NativeTextSelection { textSelection }
    func dispatch(_ action: NativeAction, target: NativeActionTarget, method: String,
                  poster: NativeInputPoster) throws -> NativeActionDispatch {
        dispatchCount += 1
        dispatchedMethod = method
        if let failure { throw failure }
        if method.hasPrefix("cg_") {
            return poster.post(action, target: target) { point in
                try self.check(requestID: 0, deadline: ProcessInfo.processInfo.systemUptime + 10)
                let current = try self.revalidate(action)
                guard current == target else { throw ProbeFailure(code: "state_expired") }
                guard try self.supports(action.kind, target: current, point: point) else { throw ProbeFailure(code: "policy_refused") }
            }
        }
        let step = ["ax_press": "press",
                    "ax_set_value": "set_value", "ax_pick": "pick", "ax_focus": "focus",
                    "ax_scroll": "scroll", "ax_focus_window": "focus"][method] ?? ""
        return NativeActionDispatch(method: method, completedSteps: [step], execution: "applied")
    }
}

@MainActor private final class FakeNativeInputPoster: NativeInputPoster, NativeEventSink {
    var layoutIdentifier: String? = "com.apple.keylayout.US"
    var values: [String] = []
    var events: [String] = []
    var failAfterDown = false
    var failCleanup = false
    var afterFirstPost: (() -> Void)?
    var afterFirstKeyUp: (() -> Void)?
    private var failed = false
    private let clock = FakeNativeActionClock()

    private lazy var sequence = QuartzInputPoster(sink: self, clock: clock, keyboardLayout: { self.layoutIdentifier })

    func post(_ action: NativeAction, target: NativeActionTarget,
              checkpoint: (CGPoint?) throws -> Void) -> NativeActionDispatch {
        if action.kind == "insert" || action.kind == "type_text" { values.append(action.text) }
        return sequence.post(action, target: target, checkpoint: checkpoint)
    }

    func post(_ event: NativeInputEvent, deadline: TimeInterval) throws {
        let name: String
        switch event.kind {
        case .keyDown: name = "key_down"
        case .keyUp: name = "key_up"
        case .mouseDown: name = "mouse_down"
        case .mouseUp: name = "mouse_up"
        case .mouseMove: name = "mouse_move"
        case .scroll: name = "scroll"
        }
        events.append(name)
        if let callback = afterFirstPost, event.kind == .keyDown || event.kind == .mouseDown {
            afterFirstPost = nil
            callback()
        }
        if let callback = afterFirstKeyUp, event.kind == .keyUp {
            afterFirstKeyUp = nil
            callback()
        }
        if failAfterDown && !failed && (event.kind == .keyDown || event.kind == .mouseDown) {
            failed = true
            throw ProbeFailure(code: "unknown_outcome")
        }
        if failCleanup && failed && (event.kind == .keyUp || event.kind == .mouseUp) {
            throw ProbeFailure(code: "unknown_outcome")
        }
    }
}

@MainActor private struct FakeNativeActionClock: NativeActionClock {
    func now() -> TimeInterval { ProcessInfo.processInfo.systemUptime }
    func sleep(_ duration: TimeInterval, deadline: TimeInterval) throws {
        if ProcessInfo.processInfo.systemUptime + duration >= deadline { throw ProbeFailure(code: "budget_exceeded") }
    }
}

private extension NativeActionTarget {
    func withFocus(_ value: Bool) -> NativeActionTarget {
        NativeActionTarget(actionID: actionID, windowRef: windowRef, elementRef: elementRef, stateID: stateID,
                           process: process, role: role, classification: classification, enabled: enabled,
                           focused: value, windowFocused: windowFocused, focusedRole: focusedRole,
                           focusedClassification: focusedClassification, focusedIdentity: focusedIdentity, windowBounds: windowBounds,
                           elementBounds: elementBounds, displayBounds: displayBounds, displayID: displayID)
    }

    func withFocusedInput(role: String, classification: String, identity: String? = "focus") -> NativeActionTarget {
        NativeActionTarget(actionID: actionID, windowRef: windowRef, elementRef: elementRef, stateID: stateID,
                           process: process, role: self.role, classification: self.classification, enabled: enabled,
                           focused: focused, windowFocused: windowFocused, focusedRole: role,
                           focusedClassification: classification, focusedIdentity: identity, windowBounds: windowBounds,
                           elementBounds: elementBounds, displayBounds: displayBounds, displayID: displayID)
    }
}

private func nativeTestMethod(_ action: NativeAction) -> String {
    switch action.kind {
    case "insert", "type_text": return "cg_unicode"
    case "click": return action.elementRef.isEmpty ? "cg_click" : "ax_press"
    case "press_key": return "cg_key"
    case "coordinate_scroll": return "cg_scroll"
    case "drag": return "cg_drag"
    case "pick": return "ax_pick"
    case "focus": return "ax_focus"
    case "scroll": return "cg_scroll"
    case "focus_window": return "ax_focus_window"
    case "replace": return "ax_set_value"
    default: return "ax_press"
    }
}

private func nativeTestSteps(_ action: NativeAction) -> [String] {
    switch action.kind {
    case "insert", "type_text", "press_key": return ["key_down", "key_up"]
    case "click" where action.elementRef.isEmpty: return ["mouse_down", "mouse_up"]
    case "click": return ["press"]
    case "coordinate_scroll", "scroll": return ["scroll"]
    case "drag": return ["mouse_down", "mouse_move", "mouse_up"]
    case "pick": return ["pick"]
    case "focus", "focus_window": return ["focus"]
    case "replace": return ["set_value"]
    default: return ["press"]
    }
}
