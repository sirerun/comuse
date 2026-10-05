// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "ComuseFixture",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "ComuseFixture", targets: ["ComuseFixture"]),
        .library(name: "FixtureCore", targets: ["FixtureCore"]),
    ],
    targets: [
        .target(name: "FixtureCore"),
        .executableTarget(name: "ComuseFixture", dependencies: ["FixtureCore"]),
        .testTarget(name: "FixtureCoreTests", dependencies: ["FixtureCore"]),
    ]
)
