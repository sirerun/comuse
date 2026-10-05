import Foundation

public struct FixtureConfiguration: Equatable, Sendable {
    public let nonce: String

    public init(nonce: String) throws {
        guard !nonce.isEmpty, nonce.utf8.count <= 64,
              nonce.unicodeScalars.allSatisfy({
                  CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-").contains($0)
              }) else {
            throw FixtureConfigurationError.invalidNonce
        }
        self.nonce = nonce
    }

    public static func parse(arguments: [String]) throws -> FixtureConfiguration {
        var value: String?
        var index = 0
        while index < arguments.count {
            let argument = arguments[index]
            guard argument == "--fixture-nonce", value == nil, index + 1 < arguments.count else {
                throw FixtureConfigurationError.invalidArguments
            }
            value = arguments[index + 1]
            index += 2
        }
        guard let value else { throw FixtureConfigurationError.missingNonce }
        return try FixtureConfiguration(nonce: value)
    }

    public var windowTitle: String { "Comuse Fixture \(nonce)" }
}

public enum FixtureConfigurationError: Error, Equatable {
    case missingNonce
    case invalidNonce
    case invalidArguments
}

public struct FixtureState: Equatable, Sendable {
    public private(set) var counter = 0
    public private(set) var text = "synthetic text"
    public private(set) var secureText = "synthetic-secret"
    public private(set) var delayedText = "Waiting for deterministic delayed state"
    public private(set) var childVisible = true
    public private(set) var editInterferenceCount = 0

    public init() {}

    public mutating func incrementCounter() {
        counter += 1
    }

    public mutating func replaceText(_ value: String) {
        text = value
    }

    public mutating func replaceSecureText(_ value: String) {
        secureText = value
    }

    public mutating func publishDelayedState() {
        delayedText = "Delayed state ready"
    }

    public mutating func removeChild() {
        childVisible = false
    }

    public mutating func interfereWithEdit() {
        text = "synthetic external edit \(editInterferenceCount + 1)"
        editInterferenceCount += 1
    }
}
