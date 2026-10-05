import AppKit
import ApplicationServices
import Foundation

private let inputRequestLimit = 32 * 1024
private let inputResponseLimit = 64 * 1024
private let inputTextLimit = 4096
private let normalFieldIdentifier = "textfield"
private let counterIdentifier = "counter-value"
private let buttonIdentifier = "buttoncounter"
// Runtime routing stays closed until the coordinator integrates admission and writer ownership.
private let nativeInputActionsEnabled = false

private struct InputScope {
    let pid: Int32
    let bundleID: String
    let nonce: String
    let processStartReference: String
    let windowReference: String
    let elementReference: String
    let expectedStateID: String
}

private struct InputRequest {
    let requestID: String
    let actionID: String
    let operation: String
    let scope: InputScope
    let text: String?
}

@MainActor
func handleInputProbe(_ requestData: Data) -> Data {
    guard requestData.count <= inputRequestLimit else {
        return inputResponse(requestID: "", execution: "not_applied", stateStatus: "unavailable", verification: "unavailable", cleanup: "not_required", error: "validation_error")
    }
    guard let request = decodeInputRequest(requestData) else {
        return inputResponse(requestID: "", execution: "not_applied", stateStatus: "unavailable", verification: "unavailable", cleanup: "not_required", error: "validation_error")
    }
    guard nativeInputActionsEnabled else {
        return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "not_applied", stateStatus: "unavailable", verification: "unavailable", cleanup: "not_required", error: "unsupported")
    }
    guard request.scope.bundleID == "com.sirerun.comuse.fixture" else {
        return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "not_applied", stateStatus: "unavailable", verification: "unavailable", cleanup: "not_required", error: "scope_mismatch")
    }

    return executeInputProbe(request)
}

@MainActor
private func decodeInputRequest(_ data: Data) -> InputRequest? {
    guard let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
          object["schema_version"] as? Int == 1,
          let requestID = object["request_id"] as? String, !requestID.isEmpty, requestID.utf8.count <= 128,
          let actionID = object["action_id"] as? String, !actionID.isEmpty, actionID.utf8.count <= 128,
          let operation = object["op"] as? String,
          let rawScope = object["scope"] as? [String: Any],
          let pidValue = rawScope["pid"] as? NSNumber, let pid = strictFixturePID(pidValue),
          let bundleID = rawScope["bundle_id"] as? String,
          let nonce = rawScope["fixture_nonce"] as? String, validFixtureNonce(nonce),
          let processStartReference = rawScope["process_start_ref"] as? String, !processStartReference.isEmpty,
          let windowReference = rawScope["window_ref"] as? String, !windowReference.isEmpty,
          let elementReference = rawScope["element_ref"] as? String, !elementReference.isEmpty,
          let expectedStateID = rawScope["expected_state_id"] as? String,
          expectedStateID.utf8.count == 64,
          expectedStateID.utf8.allSatisfy({ (48...57).contains($0) || (97...102).contains($0) }) else { return nil }
    guard ["read_value", "replace", "insert", "press"].contains(operation) else { return nil }
    let text = object["text"] as? String
    if operation == "replace" || operation == "insert" {
        guard let text, text.utf8.count <= inputTextLimit else { return nil }
    }
    if operation == "press", text != nil { return nil }
    return InputRequest(
        requestID: requestID,
        actionID: actionID,
        operation: operation,
        scope: InputScope(pid: pid, bundleID: bundleID, nonce: nonce, processStartReference: processStartReference, windowReference: windowReference, elementReference: elementReference, expectedStateID: expectedStateID),
        text: text
    )
}

@MainActor
private func executeInputProbe(_ request: InputRequest) -> Data {
    let scope = request.scope
    let fresh = freshInputSnapshot(scope: scope)
    guard let result = fresh["result"] as? [String: Any],
          let currentStateID = result["state_id"] as? String,
          let processStartReference = result["process_start_ref"] as? String,
          let windowReference = result["window_ref"] as? String,
          currentStateID == scope.expectedStateID,
          processStartReference == scope.processStartReference,
          windowReference == scope.windowReference else {
        return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "not_applied", stateStatus: "partial", verification: "unavailable", cleanup: "not_required", error: "state_expired")
    }
    guard let metadata = accessibilityTargetMetadata(scope.elementReference, pid: scope.pid),
          metadata.nonce == scope.nonce,
          metadata.processStartReference == scope.processStartReference,
          metadata.windowReference == scope.windowReference,
          metadata.reference == scope.elementReference,
          metadata.stateID == scope.expectedStateID,
          metadata.stateComplete else {
        return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "not_applied", stateStatus: "partial", verification: "unavailable", cleanup: "not_required", error: "element_stale")
    }
    guard !metadata.isProtected else {
        return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "not_applied", stateStatus: "available", verification: "unavailable", cleanup: "not_required", error: "protected_target")
    }

    let isTextField = metadata.identifier == normalFieldIdentifier && metadata.role == (kAXTextFieldRole as String)
    let isCounterButton = metadata.identifier == buttonIdentifier && metadata.role == (kAXButtonRole as String)
    if (request.operation == "press" && !isCounterButton) || (request.operation != "press" && !isTextField) {
        return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "not_applied", stateStatus: "available", verification: "unavailable", cleanup: "not_required", error: "unsupported")
    }
    guard case let .resolved(element) = resolveAccessibilityElementResult(scope.elementReference, pid: scope.pid) else {
        return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "not_applied", stateStatus: "partial", verification: "unavailable", cleanup: "not_required", error: "element_stale")
    }
    AXUIElementSetMessagingTimeout(element, 0.05)

    switch request.operation {
    case "read_value":
        guard let value = snapshotValue(result, ref: scope.elementReference) else {
            return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "not_applied", stateStatus: "partial", verification: "unavailable", cleanup: "not_required", error: "value_unavailable")
        }
        return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "not_applied", stateStatus: "available", verification: "verified", cleanup: "not_required", result: ["value": value, "state_id": currentStateID])
    case "replace":
        guard let oldValue = snapshotValue(result, ref: scope.elementReference), let newValue = request.text else {
            return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "not_applied", stateStatus: "partial", verification: "unavailable", cleanup: "not_required", error: "value_unavailable")
        }
        let setError = AXUIElementSetAttributeValue(element, kAXValueAttribute, newValue as CFString)
        return verifyTextDispatch(request, expectedValue: newValue, dispatchError: setError, originalValue: oldValue)
    case "insert":
        guard let oldValue = snapshotValue(result, ref: scope.elementReference), let insertion = request.text,
              let selectedRange = selectedUTF16Range(element),
              isValidUTF16Range(selectedRange, in: oldValue),
              let swiftRange = Range(selectedRange, in: oldValue) else {
            return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "not_applied", stateStatus: "available", verification: "unavailable", cleanup: "not_required", error: "selection_unavailable")
        }
        let expectedValue = oldValue.replacingCharacters(in: swiftRange, with: insertion)
        let setError = AXUIElementSetAttributeValue(element, kAXValueAttribute, expectedValue as CFString)
        return verifyTextDispatch(request, expectedValue: expectedValue, dispatchError: setError, originalValue: oldValue)
    case "press":
        guard let countText = snapshotValue(forIdentifier: counterIdentifier, result: result),
              let oldCount = parseCounter(countText) else {
            return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "not_applied", stateStatus: "partial", verification: "unavailable", cleanup: "not_required", error: "postcondition_unavailable")
        }
        let pressError = AXUIElementPerformAction(element, kAXPressAction as CFString)
        guard pressError == .success else {
            return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "unknown", stateStatus: "partial", verification: "unavailable", cleanup: "not_required", error: "dispatch_unknown")
        }
        let after = freshInputSnapshot(scope: scope)
        guard let afterResult = after["result"] as? [String: Any],
              afterResult["process_start_ref"] as? String == scope.processStartReference,
              afterResult["window_ref"] as? String == scope.windowReference,
              let afterCountText = snapshotValue(forIdentifier: counterIdentifier, result: afterResult),
              let afterCount = parseCounter(afterCountText) else {
            return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "unknown", stateStatus: "unavailable", verification: "unavailable", cleanup: "not_required", error: "verification_unavailable")
        }
        guard afterCount == oldCount + 1 else {
            return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "partial", stateStatus: "available", verification: "failed", cleanup: "not_required", error: "postcondition_failed", result: ["counter_before": oldCount, "counter_after": afterCount])
        }
        return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "applied", stateStatus: "available", verification: "verified", cleanup: "not_required", result: ["counter_before": oldCount, "counter_after": afterCount, "state_id": afterResult["state_id"] ?? NSNull()])
    default:
        return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "not_applied", stateStatus: "unavailable", verification: "unavailable", cleanup: "not_required", error: "unsupported")
    }
}

@MainActor
private func verifyTextDispatch(_ request: InputRequest, expectedValue: String, dispatchError: AXError, originalValue: String) -> Data {
    guard dispatchError == .success else {
        return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "unknown", stateStatus: "partial", verification: "unavailable", cleanup: "not_required", error: "dispatch_unknown", result: ["original_value": originalValue])
    }
    let after = freshInputSnapshot(scope: request.scope)
    guard let result = after["result"] as? [String: Any],
          result["process_start_ref"] as? String == request.scope.processStartReference,
          result["window_ref"] as? String == request.scope.windowReference,
          let actualValue = snapshotValue(result, ref: request.scope.elementReference) else {
        return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "unknown", stateStatus: "unavailable", verification: "unavailable", cleanup: "not_required", error: "verification_unavailable")
    }
    guard actualValue == expectedValue else {
        return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "partial", stateStatus: "available", verification: "failed", cleanup: "not_required", error: "postcondition_failed", result: ["expected_value": expectedValue, "actual_value": actualValue])
    }
    return inputResponse(requestID: request.requestID, actionID: request.actionID, action: request.operation, execution: "applied", stateStatus: "available", verification: "verified", cleanup: "not_required", result: ["value": actualValue, "state_id": result["state_id"] ?? NSNull()])
}

@MainActor
private func freshInputSnapshot(scope: InputScope) -> [String: Any] {
    let request: [String: Any] = [
        "schema_version": 1,
        "request_id": UUID().uuidString,
        "op": "a11y",
        "window_ref": scope.windowReference,
        "include_values": true,
        "scope": ["pid": scope.pid, "bundle_id": scope.bundleID, "fixture_nonce": scope.nonce],
    ]
    guard let data = try? JSONSerialization.data(withJSONObject: request),
          let response = try? JSONSerialization.jsonObject(with: handleAccessibilityProbe(data)) as? [String: Any],
          response["status"] as? String == "completed" else { return [:] }
    return response
}

@MainActor
private func snapshotValue(_ result: [String: Any], ref: String) -> String? {
    guard let elements = result["elements"] as? [[String: Any]],
          let element = elements.first(where: { $0["ref"] as? String == ref }),
          element["value_status"] as? String == "included_synthetic_normal" else { return nil }
    return element["value"] as? String
}

@MainActor
private func snapshotValue(forIdentifier identifier: String, result: [String: Any]) -> String? {
    guard let elements = result["elements"] as? [[String: Any]],
          let element = elements.first(where: { $0["identifier"] as? String == identifier }),
          element["value_status"] as? String == "included_synthetic_counter" else { return nil }
    return element["value"] as? String
}

@MainActor
private func selectedUTF16Range(_ element: AXUIElement) -> NSRange? {
    var raw: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, kAXSelectedTextRangeAttribute as CFString, &raw) == .success,
          let value = raw as? AXValue,
          AXValueGetType(value) == .cfRange else { return nil }
    var range = CFRange()
    guard AXValueGetValue(value, .cfRange, &range), range.location >= 0, range.length >= 0 else { return nil }
    return NSRange(location: range.location, length: range.length)
}

private func isValidUTF16Range(_ range: NSRange, in string: String) -> Bool {
    let length = (string as NSString).length
    guard range.location <= length, range.length <= length - range.location else { return false }
    return Range(range, in: string) != nil
}

private func parseCounter(_ value: String) -> Int? {
    guard value.hasPrefix("Counter: ") else { return nil }
    return Int(value.dropFirst("Counter: ".count))
}

private func inputResponse(requestID: String, actionID: String? = nil, action: String = "input", execution: String, stateStatus: String, verification: String, cleanup: String, error: String? = nil, result: [String: Any] = [:]) -> Data {
    var envelope: [String: Any] = [
        "schema_version": "fixture.v0",
        "ok": error == nil,
        "request_id": requestID,
        "action_id": actionID,
        "action": action,
        "execution": execution,
        "verification": ["status": verification],
        "state_status": stateStatus,
        "cleanup": ["status": cleanup],
        "result": error == nil ? result : NSNull(),
        "error": error as Any? ?? NSNull(),
    ]
    guard let data = try? JSONSerialization.data(withJSONObject: envelope, options: [.sortedKeys]), data.count <= inputResponseLimit else {
        return Data(#"{"schema_version":"fixture.v0","ok":false,"execution":"not_applied","verification":{"status":"unavailable"},"state_status":"unavailable","cleanup":{"status":"not_required"},"error":"response_limit_exceeded","result":null}"#.utf8)
    }
    return data
}
