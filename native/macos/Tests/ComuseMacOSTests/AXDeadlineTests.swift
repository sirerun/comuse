import ApplicationServices
import Darwin
import Foundation
import XCTest
@testable import ComuseMacOS

final class AXDeadlineTests: XCTestCase {
    func testProductionAXDeadlineSeamCapsTimeoutForEveryIPCFamily() throws {
        let element = AXUIElementCreateApplication(getpid())
        let families = [
            "focused_window", "window_title", "hit_target", "ancestor", "action_names",
            "settable", "selected_range", "setter", "perform"
        ]

        for family in families {
            var clock = 100.0
            var configured: [(AXUIElement, Float)] = []
            var ipcCalls = 0
            let result = try nativeAXDeadlineIPC(element, deadline: 100.25, now: { clock },
                                                 configure: { configuredElement, timeout in
                configured.append((configuredElement, timeout))
                return .success
            }) {
                ipcCalls += 1
                clock += 0.1
                return family
            }

            XCTAssertEqual(result, family)
            XCTAssertEqual(ipcCalls, 1, "\(family) IPC should run once")
            XCTAssertEqual(configured.count, 1, "\(family) must configure the object immediately before IPC")
            XCTAssertTrue(CFEqual(configured[0].0, element), "\(family) must configure its own object")
            XCTAssertGreaterThan(configured[0].1, 0)
            XCTAssertLessThanOrEqual(Double(configured[0].1), 0.25)
        }
    }

    func testProductionAXDeadlineSeamRefusesBeforeIPCWhenExpired() {
        let element = AXUIElementCreateApplication(getpid())
        var configureCalls = 0
        var ipcCalls = 0
        XCTAssertThrowsError(try nativeAXDeadlineIPC(element, deadline: 10, now: { 10 },
                                                      configure: { _, _ in
            configureCalls += 1
            return .success
        }) {
            ipcCalls += 1
            return true
        }) { error in
            XCTAssertEqual((error as? ProbeFailure)?.code, "budget_exceeded")
        }
        XCTAssertEqual(configureCalls, 0)
        XCTAssertEqual(ipcCalls, 0)
    }

    func testProductionAXDeadlineSeamRefusesFailedObjectConfigurationBeforeIPC() {
        let element = AXUIElementCreateApplication(getpid())
        var ipcCalls = 0
        XCTAssertThrowsError(try nativeAXDeadlineIPC(element, deadline: 11, now: { 10 },
                                                      configure: { _, _ in .cannotComplete }) {
            ipcCalls += 1
            return true
        }) { error in
            XCTAssertEqual((error as? ProbeFailure)?.code, "backend_unavailable")
        }
        XCTAssertEqual(ipcCalls, 0)
    }
    func testDistinctEqualAXObjectsAreConfiguredSeparately() throws {
        let first = AXUIElementCreateApplication(getpid())
        let second = AXUIElementCreateApplication(getpid())
        XCTAssertTrue(CFEqual(first, second))
        let firstPointer = Unmanaged.passUnretained(first).toOpaque()
        let secondPointer = Unmanaged.passUnretained(second).toOpaque()
        XCTAssertNotEqual(firstPointer, secondPointer)
        var configured: [UnsafeMutableRawPointer] = []
        for element in [first, second] {
            _ = try nativeAXDeadlineIPC(element, deadline: 11, now: { 10 }, configure: { actual, _ in
                configured.append(Unmanaged.passUnretained(actual).toOpaque())
                return .success
            }) { true }
        }
        XCTAssertEqual(configured, [firstPointer, secondPointer])
    }

    func testConfigurationConsumingDeadlineRefusesBeforeIPC() {
        let element = AXUIElementCreateApplication(getpid())
        var clock = 10.0
        var ipcCalls = 0
        XCTAssertThrowsError(try nativeAXDeadlineIPC(element, deadline: 11, now: { clock }, configure: { _, _ in
            clock = 12
            return .success
        }) {
            ipcCalls += 1
            return true
        }) { error in
            XCTAssertEqual((error as? ProbeFailure)?.code, "budget_exceeded")
        }
        XCTAssertEqual(ipcCalls, 0)
    }

}
