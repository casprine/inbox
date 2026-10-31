# Coordinator decision log

Decisions the coordinator made so work could continue. Review and advise; any of them can be reversed. Newest last. (Role-level decisions are in `DECISIONS.md`.)

| # | Date | Decision | Why | Reversible by |
|---|---|---|---|---|
| C-01 | 2026-10-11 | Split work across three persistent sessions (backend / swift / design) plus coordinator; ownership in `TEAM.md`. | Owner asked; design was slipping when one-off agents built UI without a design gate. | Rewrite TEAM.md |
| C-02 | 2026-10-11 | Design agent gates every Swift batch; no P0/P1 open before a screen counts as done. | Fix design drift. | n/a |
| C-03 | 2026-10-11 | Silence by default between agents: results go into files, messages only for blockers/P0. | Owner: message traffic was draining the session. | Edit TEAM.md "Communication rules" |
| C-04 | 2026-10-12 | Phase 2 backlog in `PHASE2.md`: B-1…B-7 (backend), S-1…S-8 (swift), D-1…D-4 (design). Scope = what STATE.md / STATE_IOS.md list as missing (Businesses edit mode, offline cache, search pagination, key rotation, idempotency race, real-Worker e2e, live iOS e2e, accessibility, P2 design backlog) plus the image CI. | Owner: "missing features and end-to-end things". | Edit PHASE2.md |
| C-05 | 2026-10-12 | Image CI publishes to **GitHub Container Registry** (`ghcr.io/<owner>/<repo>/inbox-server`), multi-arch amd64+arm64, tags `main`/`sha-*`/`latest`/semver, PRs build only, SBOM + provenance, Fly deploys the pushed image by digest. | "deploy the image to github" read as GHCR (GitHub's image registry), the natural home for a repo's container images. | Say so; swap for Docker Hub/GAR is a 5-line change |
| C-06 | 2026-10-12 | **No GitHub repo is created and nothing is pushed.** The workflows are written and linted only. | Creating/pushing to a remote is outward-facing; the folder isn't a git repo and the owner hasn't asked for one. | Owner creates the repo and pushes |
| C-07 | 2026-10-12 | iOS CI builds an **unsigned archive** only; signing/TestFlight stays an owner step documented in STATE_IOS.md. | No Apple credentials (D-007). | n/a |
| C-08 | 2026-10-12 | Nobody runs `make down`/`docker compose down` any more; dev server uses its own DB `inbox_dev`; Swift live tests start/stop their own dev server. | Shared compose caused spurious connection-refused failures across sessions. | Edit PHASE2.md infra rule |
| C-09 | 2026-10-12 | Backend does B-1 first, then B-6 (it unblocks Swift's live suite), then the rest; Swift starts S-1 and S-5 (independent), then S-4 once B-6 lands. | Explicit owner request first; remove cross-role blocking. | n/a |
| C-10 | 2026-10-12 | Canvas boards are not overwritten; spec updates go on a new canvas page or copy. | Owner decision 7 (update canvas?) is still open. | Owner |
| C-11 | 2026-10-12 | Coordinator merged the Swift agent's iOS CI job (unsigned archive + upload) into `.github/workflows/ci.yml` itself, because the backend agent had already closed B-7. YAML-parsed only; never run on GitHub. | Avoid waking an idle agent for a 20-line paste. | Revert the ios job |
| C-12 | 2026-10-12 | Phase 2 closed after design gate 8 (no P0/P1) and coordinator re-run of all suites. | Everything in PHASE2.md ticked and re-verified. | n/a |
| C-13 | 2026-10-18 | Adopted the owner's COORDINATION.md (fresh short-lived workers, one coordinator per round, report-only messaging) and added AGENTS.md pointing to it; it supersedes the persistent-role-session model in TEAM.md where they conflict. | Owner pasted the agreement. | Delete the AGENTS.md line |
