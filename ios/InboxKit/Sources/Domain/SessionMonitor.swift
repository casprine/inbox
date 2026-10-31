import Foundation
import Observation
import API

/// Whether the device token still works (C-20). The API client reports a rejected token here (401 on an authenticated call, token
/// already cleared); the Mail root then shows "You've been signed out" and the person signs in again with the saved server address.
/// Mail already on the phone stays readable. `epoch` changes on every sign-in so long-running work (the event stream) restarts.
@Observable
public final class SessionMonitor: @unchecked Sendable {
    public private(set) var isSignedOut = false
    /// The sign-in screen (signed-out mode) is open.
    public var isSigningIn = false
    public private(set) var epoch = 0
    /// How many times the server rejected the token in this launch (tests assert it stays at 1, i.e. no retry storm).
    public private(set) var rejections = 0

    public init() {}

    public func markSignedOut() {
        if !isSignedOut { rejections += 1 }
        isSignedOut = true
    }

    /// A sign-in finished: the new token is stored.
    public func signedIn() {
        isSignedOut = false; isSigningIn = false; epoch += 1
    }

    /// Wires the client so a rejected token lands here.
    func attach(to api: APIClient) {
        api.onUnauthorized { [weak self] in Task { @MainActor in self?.markSignedOut() } }
    }
}
