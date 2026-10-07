import CoreGraphics
import Foundation
import XCTest
@testable import ComuseMacOS

final class DesktopContextTests: XCTestCase {
    private func facts(primary: UInt32 = 1, second: UInt32 = 2,
                       firstScale: Double = 2, secondScale: Double = 1,
                       firstBounds: CGRect = CGRect(x: 0, y: 0, width: 1000, height: 800)) -> DesktopFacts {
        DesktopFacts(primaryID: primary, displays: [
            DesktopDisplayFact(id: 1, bounds: firstBounds, scale: firstScale),
            DesktopDisplayFact(id: second, bounds: CGRect(x: 1000, y: 0, width: 800, height: 600), scale: secondScale)
        ])
    }

    func testMissingAndMalformedDisplayFactsInvalidateProof() {
        var tracker = DesktopContextTracker(displayID: "runtime-display")
        XCTAssertNil(tracker.update(nil))
        XCTAssertFalse(tracker.proofAvailable)
        XCTAssertNil(tracker.update(facts(primary: 9)))
        XCTAssertNil(tracker.update(facts(firstBounds: CGRect(x: 0, y: 0, width: CGFloat.infinity, height: 800))))
        XCTAssertNil(tracker.update(facts(firstScale: .nan)))
        XCTAssertNil(tracker.update(facts(second: 1))) // duplicate display identity
    }

    func testGenerationIsPositiveBoundedAndTracksTopologyPrimaryAndScale() throws {
        var tracker = DesktopContextTracker(displayID: "opaque-runtime-id")
        var generation = try XCTUnwrap(tracker.update(facts()))
        XCTAssertEqual(generation, 1)
        XCTAssertLessThanOrEqual(generation, maximumDesktopGeneration)

        let changedTopology = DesktopFacts(primaryID: 1, displays: [
            DesktopDisplayFact(id: 1, bounds: CGRect(x: 0, y: 0, width: 1000, height: 800), scale: 2)
        ])
        generation = try XCTUnwrap(tracker.update(changedTopology))
        XCTAssertEqual(generation, 2)
        generation = try XCTUnwrap(tracker.update(facts(primary: 2)))
        XCTAssertEqual(generation, 3)
        generation = try XCTUnwrap(tracker.update(facts(secondScale: 1.5)))
        XCTAssertEqual(generation, 4)

        tracker.invalidate()
        generation = try XCTUnwrap(tracker.update(facts(secondScale: 1.5)))
        XCTAssertEqual(generation, 5)
    }

    func testNoUnlockedInferenceOrExtraContextAuthorityFields() {
        let focus: [String: Any] = ["ref": "window-ref", "title": "Editor",
                                    "process": ["pid": 10, "bundle_id": "example.editor", "launch_id": "launch"]]
        let context: [String: Any] = ["display_id": "opaque", "display_generation": 1, "focused_window": focus]
        XCTAssertEqual(Set(context.keys), Set(["display_id", "display_generation", "focused_window"]))
        XCTAssertNil(context["unlocked"])
        XCTAssertNil(context["permission"])
    }

    func testFocusRequiresScopeAndUnknownIsNotNoFocus() {
        if case .noAuthorizedWindow = desktopFocusAuthorization(pid: 20, scopedPIDs: [10]) {} else {
            XCTFail("foreign frontmost process must not disclose a focused window")
        }
        if case .unknown = desktopFocusAuthorization(pid: 10, scopedPIDs: [10]) {} else {
            XCTFail("scoped frontmost process still requires an actual AX focus inspection")
        }
        XCTAssertFalse(desktopFocusEvidence(.unknown).known)
        XCTAssertTrue(desktopFocusEvidence(.noAuthorizedWindow).known)
    }

    func testStaleReferenceCannotCrossDisplayGeneration() {
        XCTAssertTrue(desktopReferenceIsCurrent(issued: 7, current: 7))
        XCTAssertFalse(desktopReferenceIsCurrent(issued: 7, current: 8))
        XCTAssertFalse(desktopReferenceIsCurrent(issued: 0, current: 0))
        XCTAssertFalse(desktopReferenceIsCurrent(issued: maximumDesktopGeneration + 1, current: maximumDesktopGeneration + 1))
    }
}
