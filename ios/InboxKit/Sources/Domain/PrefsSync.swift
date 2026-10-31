import Foundation
import API
import Persistence

/// Keeps the few synced preferences in step with `/v1/prefs`: image allow-list, preview lines, retention.
public enum PrefsSync {
    public static let allowKey = "image_allow_senders"
    public static let retentionKey = "retention_days"

    /// Pulls server prefs into the local cache (the server wins; local-only senders are pushed back up).
    @MainActor public static func pull(_ env: AppEnvironment) async {
        guard let remote = try? await env.api.prefs() else { return }
        let local = await localSenders(env.database)
        let merged = Set(local).union(remote.imageAllowSenders).sorted()
        if let d = try? JSONEncoder().encode(merged), let s = String(data: d, encoding: .utf8) { try? await env.database.setPref(allowKey, s) }
        if merged != remote.imageAllowSenders.sorted() { _ = try? await env.api.updatePrefs(PrefsPatch(imageAllowSenders: merged)) }
        try? await env.database.setPref(retentionKey, remote.retentionDays.map(String.init) ?? "")
        await env.theme.setPreviewLines(remote.previewLines, env.database)
    }

    public static func localSenders(_ db: AppDatabase) async -> [String] {
        guard let raw = (try? await db.pref(allowKey)) ?? nil, let d = raw.data(using: .utf8), let a = try? JSONDecoder().decode([String].self, from: d) else { return [] }
        return a
    }

    /// Pushes the whole allow-list (the server replaces its copy); failures are fine, the next `pull` merges.
    public static func pushAllowList(_ env: AppEnvironment) async {
        let all = await localSenders(env.database)
        _ = try? await env.api.updatePrefs(PrefsPatch(imageAllowSenders: all))
    }
}
