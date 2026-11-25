// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "OmaSealBar",
    platforms: [
        .macOS(.v14),
    ],
    products: [
        .executable(name: "OmaSealBar", targets: ["OmaSealBar"]),
        .executable(name: "omaseal-presence", targets: ["omaseal-presence"]),
        .library(name: "OmaSealBarCore", targets: ["OmaSealBarCore"]),
    ],
    targets: [
        .target(
            name: "OmaSealBarCore",
            path: "Sources/OmaSealBarCore"
        ),
        .executableTarget(
            name: "OmaSealBar",
            dependencies: ["OmaSealBarCore"],
            path: "Sources/OmaSealBar"
        ),
        .executableTarget(
            name: "omaseal-presence",
            dependencies: ["OmaSealBarCore"],
            path: "Sources/omaseal-presence"
        ),
        .testTarget(
            name: "OmaSealBarTests",
            dependencies: ["OmaSealBarCore"],
            path: "Tests/OmaSealBarTests"
        ),
    ]
)
