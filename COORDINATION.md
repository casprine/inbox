# Multi-agent agreement

## How to adopt this
1. Save this file in the repo as `COORDINATION.md` (or paste it as a section of `AGENTS.md`).
2. Add one line to `AGENTS.md`: `Multi-agent work follows COORDINATION.md.` Agents read `AGENTS.md` at the start of a session, so this is what makes them follow it.
3. Start one coordinator session for the round. Give it the prompt in section 8, filled in with your goal.
4. The coordinator spawns workers using the briefs it writes under section 2. Don't start workers by hand.
5. When the round ends, close the coordinator and review the merged result.

## 1. Roles
- **Coordinator:** plans, writes briefs, spawns agents, merges results. Does not do the implementation work itself.
- **Worker:** does one brief, in its own file set, and returns one short report.
- **Checker:** runs once at the end, after all workers finish.

## 2. Before spawning, the coordinator must
- List every file or directory each worker may touch. No file appears in two lists.
- If two pieces share a file or one needs the other's output, put them in one worker or run them in order.
- Write a complete brief for each worker: area, what "done" looks like, what to leave alone, and what to do if blocked.
- Use fresh contexts by default. Fork only when the subtask truly needs the current conversation's history.

## 3. Worker rules
- Touch only your listed files. If you need to change something outside them, stop and report it. Don't edit it.
- Don't message other workers. The only messages are the brief in and the report out.
- Put detail in files or commits, not in chat.
- Don't commit unless the brief says to. Don't switch branches.

## 4. Report format (max ~10 lines)
- What changed, as file paths.
- What was verified, and how.
- What was not verified or left undone.
- Anything blocked or needing a decision.

## 5. Coordinator rules after spawning
- Don't poll. Wait for the reports.
- Check a report against the diff before calling the work done.
- A blocked worker gets one answer from the coordinator. If the answer changes another worker's scope, re-brief both.

## 6. Merge and check
- Merge worker results one at a time.
- The Checker runs tests or a review on the combined result once, and says what it could not cover.
- Failures go back to the worker who owned the files, not to a new agent.

## 7. Limits
- At most 3-4 workers at once.
- Close or compact any session that passes about 150 turns.
- Retire the coordinator when the round ends. Start a new one for the next round.

## 8. Coordinator prompt (paste to start a round)
```
You are the coordinator. Follow COORDINATION.md.

Goal: <one sentence>
Repo/area: <path>

Do this:
1. Split the goal into pieces with no shared files. List each piece's files.
2. If any pieces overlap, merge them or order them. Show me the split before spawning.
3. Write a full brief per worker (area, done criteria, off-limits, blocked behavior).
4. Spawn fresh workers, max 4. Do not poll them.
5. Check each report against its diff. Then run one checker on the combined result.
6. Report to me in under 10 lines: done, not verified, anything needing my decision.
```
