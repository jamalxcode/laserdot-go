// swift-tools-version:5.9
// The Laserdot menu-bar app. It talks to the Go daemon (laserdotd) over its
// Unix socket; nothing from the core is linked. app/scripts/build-go.sh
// builds laserdotd (bundled beside the app) and regenerates
// Sources/LaserdotKit/Registry.generated.swift from the filter registry.
import PackageDescription

let package = Package(
    name: "Laserdot",
    defaultLocalization: "en",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "Laserdot", targets: ["Laserdot"]),
        .library(name: "LaserdotKit", targets: ["LaserdotKit"]),
    ],
    targets: [
        // The daemon client and the registry-driven guidance — no UI.
        .target(name: "LaserdotKit", path: "Sources/LaserdotKit"),
        // The SwiftUI menu-bar app.
        .executableTarget(
            name: "Laserdot",
            dependencies: ["LaserdotKit"],
            path: "Sources/Laserdot",
            resources: [.process("Resources")]
        ),
        .testTarget(name: "LaserdotKitTests", dependencies: ["LaserdotKit"], path: "Tests/LaserdotKitTests"),
    ]
)
