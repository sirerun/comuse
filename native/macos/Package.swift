// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "ComuseMacOS",
    platforms: [.macOS(.v14)],
    products: [
        .library(name: "ComuseMacOS", type: .dynamic, targets: ["ComuseMacOS"])
    ],
    targets: [
        .systemLibrary(name: "ComuseABI", path: "include"),
        .target(
            name: "ComuseMacOS",
            dependencies: ["ComuseABI"],
            path: "Sources/ComuseMacOS",
            linkerSettings: [.linkedFramework("AppKit"), .linkedFramework("ApplicationServices")]
        ),
        .testTarget(name: "ComuseMacOSTests", dependencies: ["ComuseMacOS"], path: "Tests/ComuseMacOSTests")
    ]
)
