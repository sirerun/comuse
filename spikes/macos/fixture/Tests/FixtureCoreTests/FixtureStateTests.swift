import Testing
@testable import FixtureCore

struct FixtureStateTests {
    @Test
    func nonceIsRequiredAndStableWindowTitleIsDerived() throws {
        let configuration = try FixtureConfiguration.parse(arguments: ["--fixture-nonce", "run_123"])
        #expect(configuration.nonce == "run_123")
        #expect(configuration.windowTitle == "Comuse Fixture run_123")
        #expect(throws: FixtureConfigurationError.self) {
            try FixtureConfiguration.parse(arguments: [])
        }
    }

    @Test
    func nonceRejectsAmbiguousAndUnsafeArguments() throws {
        #expect(throws: FixtureConfigurationError.self) {
            try FixtureConfiguration(nonce: "bad nonce")
        }
        #expect(throws: FixtureConfigurationError.self) {
            try FixtureConfiguration(nonce: String(repeating: "x", count: 65))
        }
        #expect(throws: FixtureConfigurationError.self) {
            try FixtureConfiguration.parse(arguments: ["--fixture-nonce", "a", "--other"])
        }
    }

    @Test
    func syntheticTransitionsHaveDeterministicPostconditions() {
        var state = FixtureState()
        state.incrementCounter()
        state.replaceText("synthetic replacement")
        state.replaceSecureText("synthetic private value")
        state.publishDelayedState()
        state.removeChild()
        state.interfereWithEdit()

        #expect(state.counter == 1)
        #expect(state.text == "synthetic external edit 1")
        #expect(state.secureText == "synthetic private value")
        #expect(state.delayedText == "Delayed state ready")
        #expect(state.childVisible == false)
        #expect(state.editInterferenceCount == 1)
    }
}
