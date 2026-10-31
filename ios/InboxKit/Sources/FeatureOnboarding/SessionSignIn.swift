import SwiftUI
import API
import DesignSystem
import Domain

/// "You've been signed out" -> Sign in: the OB01b screen in signed-out mode (D4 §1.4), shown full screen over the app. The server address
/// is pre-filled; success stores the new token and returns to where the person was, Back dismisses without signing in.
public struct SessionSignInScreen: View {
    @Environment(\.palette) private var p
    @State private var onboarding: OnboardingModel
    let env: AppEnvironment
    let close: () -> Void

    public init(env: AppEnvironment, defaults: UserDefaults = .standard, close: @escaping () -> Void) {
        self.env = env; self.close = close
        let m = OnboardingModel(env: env, defaults: defaults)
        m.openSignIn(.signedOut)
        _onboarding = State(initialValue: m)
    }

    public var body: some View {
        Group {
            if let model = onboarding.signIn { SignInStep(onboarding: onboarding, model: model) } else { p.ground }
        }
        .background(p.ground)
        .onChange(of: onboarding.signIn == nil) { _, gone in
            guard gone else { return }
            Task {
                // Back also clears the sign-in; only a stored token means the person signed in.
                if await env.tokenStore.token() != nil { env.session.signedIn() }
                close()
            }
        }
    }
}
