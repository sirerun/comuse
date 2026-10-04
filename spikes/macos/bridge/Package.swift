// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "ComuseBridgeProbe",
    platforms: [.macOS(.v14)],
    products: [.library(name: "BridgeProbe", type: .dynamic, targets: ["BridgeProbe"])],
    targets: [
        .target(name: "BridgeProbe", path: "Sources/BridgeProbe"),
        .testTarget(name: "BridgeProbeTests", dependencies: ["BridgeProbe"], path: "Tests/BridgeProbeTests"),
    ]
)
