import Foundation

/// The app's notion of "now". Production code never sets `fixed`; snapshot tests pin it so relative times ("Yesterday", "6 days") render the same on every run.
public enum AppClock {
    nonisolated(unsafe) public static var fixed: Date?
    public static var now: Date { fixed ?? Date() }
}
