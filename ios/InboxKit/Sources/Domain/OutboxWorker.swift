import Foundation
import Network
import API
import Persistence

/// Online/offline signal. `NWPathMonitor` in production, a manual one in tests.
public protocol Reachability: Sendable {
    func updates() -> AsyncStream<Bool>
}

public struct PathMonitorReachability: Reachability {
    public init() {}
    public func updates() -> AsyncStream<Bool> {
        AsyncStream { cont in
            let monitor = NWPathMonitor()
            monitor.pathUpdateHandler = { cont.yield($0.status == .satisfied) }
            cont.onTermination = { _ in monitor.cancel() }
            monitor.start(queue: DispatchQueue(label: "inbox.reachability"))
        }
    }
}

/// Flushes `pending_op` rows to `POST /v1/messages:batch`. Rows are grouped by kind under one
/// Idempotency-Key that is persisted before sending, so a replay after a crash/timeout is deduplicated.
public actor OutboxWorker {
    private let api: APIClient
    private let db: AppDatabase
    private let reachability: any Reachability
    private var online = true
    private var flushing = false
    private var runTask: Task<Void, Never>?
    private var kickTask: Task<Void, Never>?
    private let kickDelay: Duration

    /// `kickDelay` is the undo window: a queued op is held this long so an inverse op can cancel it unsent.
    public init(api: APIClient, db: AppDatabase, reachability: any Reachability = PathMonitorReachability(),
                kickDelay: Duration = .seconds(5)) {
        self.api = api; self.db = db; self.reachability = reachability; self.kickDelay = kickDelay
    }

    /// Begin watching connectivity; flushes whenever the network becomes reachable.
    public func start() {
        guard runTask == nil else { return }
        let updates = reachability.updates()
        runTask = Task { [weak self] in
            for await isOnline in updates {
                guard let self else { return }
                await self.setOnline(isOnline)
            }
        }
    }

    public func stop() { runTask?.cancel(); runTask = nil; kickTask?.cancel(); kickTask = nil }

    private func setOnline(_ v: Bool) async {
        online = v
        if v { await flush() }
    }

    /// Schedule a flush after the undo window (coalesces repeated kicks into the pending one).
    public func kick() {
        guard kickTask == nil else { return }
        kickTask = Task { [weak self, kickDelay] in
            try? await Task.sleep(for: kickDelay)
            guard !Task.isCancelled, let self else { return }
            await self.runKick()
        }
    }

    private func runKick() async { kickTask = nil; await flush() }

    /// Sends queued ops until the queue is empty, offline, or a retryable error occurs.
    public func flush() async {
        guard online, !flushing else { return }
        flushing = true
        defer { flushing = false }
        while online {
            guard let batch = try? await db.claimNextBatch(), let first = batch.first else { return }
            let ids = batch.compactMap(\.id)
            guard let op = BatchOp(rawValue: first.kind) else { try? await db.completeOps(ids); continue }
            do {
                let updated = try await api.batch(ids: batch.map(\.messageId), op: op, idempotencyKey: first.idempotencyKey)
                try? await db.completeOps(ids)
                try? await db.upsertMessages(updated.map(MessageRecord.init))
            } catch let e as APIError {
                // Signed out: keep the queued ops untouched (they go out after the next sign-in) and stop; nothing to retry until then.
                if e.isSessionLost { return }   // rows stay in flight with their key; the next claim replays them
                if e.isRetryable { try? await db.recordFailure(ids, error: "\(e)"); return }
                // Permanent rejection (e.g. 400/404): drop the ops; the next refresh restores server truth.
                try? await db.completeOps(ids)
                await restoreFromServer(batch.map(\.messageId))
            } catch { return }
        }
    }

    /// After a permanently rejected op, re-pull the affected rows so the optimistic value is replaced.
    private func restoreFromServer(_ ids: [UUID]) async {
        for id in ids {
            if let detail = try? await api.message(id: id) { try? await db.upsertMessages([MessageRecord(detail.message)]) }
        }
    }
}
