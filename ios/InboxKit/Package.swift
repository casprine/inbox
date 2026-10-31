// swift-tools-version: 6.0
import PackageDescription

let names = ["DesignSystem", "API", "Persistence", "Domain", "Web", "FeatureOnboarding", "FeatureMail", "FeatureMessage", "FeatureSearch", "FeatureAttention", "FeatureRules", "FeatureBusinesses", "FeatureGmail", "FeatureAlfather", "FeatureSettings"]

let package = Package(
    name: "InboxKit",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: names.map { .library(name: $0, targets: [$0]) },
    dependencies: [
        .package(url: "https://github.com/groue/GRDB.swift", exact: "7.11.1"),
    ],
    targets: [
        .target(name: "DesignSystem"),
        .target(name: "API", dependencies: []),
        .target(name: "Persistence", dependencies: [.product(name: "GRDB", package: "GRDB.swift")]),
        .target(name: "Domain", dependencies: ["API", "Persistence", .product(name: "GRDB", package: "GRDB.swift")]),
        .target(name: "Web", dependencies: []),
        .target(name: "FeatureOnboarding", dependencies: ["DesignSystem", "Domain", "API", "Persistence", "FeatureGmail"]),
        .target(name: "FeatureMail", dependencies: ["DesignSystem", "Domain", "API", "Persistence", "FeatureMessage", "FeatureSearch", "FeatureRules", "FeatureAttention", "FeatureGmail", "FeatureSettings", "FeatureOnboarding", "FeatureBusinesses"]),
        .target(name: "FeatureMessage", dependencies: ["DesignSystem", "Domain", "API", "Persistence", "Web"]),
        .target(name: "FeatureSearch", dependencies: ["DesignSystem", "Domain", "API", "Persistence"]),
        .target(name: "FeatureAttention", dependencies: ["DesignSystem", "Domain", "API", "Persistence", "FeatureRules"]),
        .target(name: "FeatureRules", dependencies: ["DesignSystem", "Domain", "API", "Persistence"]),
        .target(name: "FeatureBusinesses", dependencies: ["DesignSystem", "Domain", "API", "Persistence"]),
        .target(name: "FeatureGmail", dependencies: ["DesignSystem", "Domain", "API", "Persistence"]),
        .target(name: "FeatureAlfather", dependencies: ["DesignSystem", "Domain", "API", "Persistence"]),
        .target(name: "FeatureSettings", dependencies: ["DesignSystem", "Domain", "API", "Persistence", "FeatureGmail", "FeatureBusinesses", "FeatureOnboarding"]),
    ]
)
