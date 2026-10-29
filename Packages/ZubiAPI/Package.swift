// swift-tools-version:6.0
// Everything HTTP: the server, its routes, the DTOs and the OpenAPI document they must match.
// The app supplies the EventKit and Notes.app sources through the protocols in Sources.swift.
import PackageDescription

let package = Package(
    name: "ZubiAPI",
    platforms: [.macOS(.v14)],
    products: [
        .library(name: "ZubiAPI", targets: ["ZubiAPI"])
    ],
    dependencies: [
        .package(url: "https://github.com/hummingbird-project/hummingbird.git", from: "2.0.0")
    ],
    targets: [
        .target(
            name: "ZubiAPI",
            dependencies: [.product(name: "Hummingbird", package: "hummingbird")],
            resources: [.copy("Resources/openapi.json")]
        ),
        .testTarget(
            name: "ZubiAPITests",
            dependencies: [
                "ZubiAPI",
                .product(name: "HummingbirdTesting", package: "hummingbird"),
            ]
        ),
    ],
    // The app builds in Swift 5 mode; keeping the package there too avoids a concurrency migration.
    swiftLanguageModes: [.v5]
)
