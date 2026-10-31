import Foundation
import Observation
import AuthenticationServices
import UIKit
import API
import Domain

/// Runs the browser half of the OAuth handshake; swapped for a fake in tests.
public protocol WebAuthenticating: Sendable {
    /// Opens `url` and returns the callback URL (`inbox://gmail/connected…`). Throws when the user cancels.
    @MainActor func authenticate(url: URL, callbackScheme: String) async throws -> URL
}

public struct SystemWebAuthenticator: WebAuthenticating {
    public init() {}
    @MainActor public func authenticate(url: URL, callbackScheme: String) async throws -> URL {
        let box = SessionBox()
        return try await withCheckedThrowingContinuation { cont in
            let s = ASWebAuthenticationSession(url: url, callbackURLScheme: callbackScheme) { callback, error in
                if let callback { cont.resume(returning: callback) } else { cont.resume(throwing: error ?? CancellationError()) }
            }
            s.presentationContextProvider = box
            s.prefersEphemeralWebBrowserSession = false
            box.session = s
            if !s.start() { cont.resume(throwing: CancellationError()) }
        }
    }
    private final class SessionBox: NSObject, ASWebAuthenticationPresentationContextProviding {
        var session: ASWebAuthenticationSession?
        func presentationAnchor(for session: ASWebAuthenticationSession) -> ASPresentationAnchor {
            UIApplication.shared.connectedScenes.compactMap { ($0 as? UIWindowScene)?.keyWindow }.first ?? ASPresentationAnchor()
        }
    }
}

/// Settings → Gmail: connect / reconnect, disconnect, status.
@MainActor @Observable
public final class GmailModel {
    public private(set) var isWorking = false
    public var message: String?
    public var confirmingDisconnect = false
    private let env: AppEnvironment
    private let authenticator: any WebAuthenticating

    public init(env: AppEnvironment, authenticator: any WebAuthenticating = SystemWebAuthenticator()) {
        self.env = env; self.authenticator = authenticator
    }

    public var store: GmailStatusStore { env.gmail }

    public func load() async { await env.gmail.refresh(env.api) }

    /// Gets the consent URL, runs the browser session, then re-reads the status. Cancelling is silent.
    public func connect() async {
        guard !isWorking else { return }
        isWorking = true; message = nil
        defer { isWorking = false }
        do {
            let start = try await env.api.gmailConnect()
            let callback = try await authenticator.authenticate(url: start.authUrl, callbackScheme: start.callbackScheme)
            handle(callback: callback)
            await env.gmail.refresh(env.api)
        } catch let e as APIError {
            if case .problem(let p, _) = e, p.code == "gmail_not_configured" { message = GmailConnectResult.message(for: "not_configured") }
            else { message = e.isRetryable ? "Couldn't reach the server. Try again." : "Couldn't start Gmail sign-in." }
        } catch { /* user cancelled the sheet */ }
    }

    /// Also called from the `inbox://gmail/connected` deep link.
    public func handle(callback url: URL) {
        guard let r = GmailConnectResult.parse(url) else { return }
        switch r {
        case .connected: message = nil
        case .failed(let reason): message = GmailConnectResult.message(for: reason)
        }
    }

    public func disconnect() async {
        isWorking = true; defer { isWorking = false }
        do { try await env.api.gmailDisconnect(); message = nil }
        catch let e as APIError {
            if case .problem(let p, _) = e, p.code == "gmail_not_connected" { message = nil }
            else { message = "Couldn't disconnect. Try again." }
        } catch { message = "Couldn't disconnect. Try again." }
        await env.gmail.refresh(env.api)
    }
}

/// Mock mode: pretends the browser round trip succeeded.
public struct MockWebAuthenticator: WebAuthenticating {
    public init() {}
    @MainActor public func authenticate(url: URL, callbackScheme: String) async throws -> URL {
        try await Task.sleep(for: .milliseconds(300))
        return URL(string: "inbox://gmail/connected?status=ok")!
    }
}
