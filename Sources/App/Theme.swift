import SwiftUI

/// The few design tokens the Software Update window shares with Blitz, so its views read the same.
enum Theme {
    enum Spacing {
        static let xxs: CGFloat = 2
        static let sm: CGFloat = 6
        static let md: CGFloat = 8
        static let lg: CGFloat = 10
        static let xl: CGFloat = 12
        static let xxl: CGFloat = 20
    }

    enum Radius {
        static let card: CGFloat = 10
    }

    enum Colors {
        static let textSecondary = Color.primary.opacity(0.6)
        static let sheen = Color.primary.opacity(0.04)
        static let cardFill = Color.primary.opacity(0.05)
        static let cardStroke = Color.primary.opacity(0.10)
        /// The app icon's teal.
        static let brand = Color(red: 0.0, green: 0.62, blue: 0.56)
    }
}
