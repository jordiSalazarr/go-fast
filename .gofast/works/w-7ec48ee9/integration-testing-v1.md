# Integration testing: skill improvements flow back to the plugin (w-7ec48ee9, visit 1, attempt 1)

This is the first visit, which comes after rework 2 (fixer-v2 → specify-v3 → implement-v3 → review-v3 passed). Review-v3 found that the rework changed nothing outside rule 7. So this visit runs the configured suite and the four checks the earlier stages left for integration testing: specify-v1 "Rules without a spec", implement-v1, and review-v3 R5.

All the probes ran through `go test -overlay` or against a scratch binary, with their files kept in the scratchpad. Nothing was written to the repository except this artifact.

## Configured suite
`.gofast/checks.json` → `go test ./...`. Run uncached (`-count=1`): every package is `ok`, with no failures. `go vet ./...` is clean.

## Deferred checks

### 1. A revise racing an approve on the server (S4, review-v3 R5): passes
This probe runs against the real HTTP handler and SQLite store (`improvements(t)` fixture). It ran 60 rounds, and in each round a revise (jordi) and an approve at revision 1 (ana) were released at the same moment. It then ran 3 more times under `-race`.
- Both orderings happened, for example 49 approve-first and 11 revise-first.
- When the approve won: approve returned 200, revise returned 409 `improvement-text-fixed`, and the stored text was the revision-1 text.
- When the revise won: revise returned 200, approve returned 409 `improvement-revised-since-read`, the state stayed proposed, and the revision was 2.
- Text nobody read was never approved. `/approved` served each id once.
- Burst test: 8 revises and 8 approves on one id at the same moment. Every approve got 200 (idempotent), every revise got 409 `improvement-text-fixed`, and the final state was approved at revision 1.
- The race detector found nothing.
- The `conflict` retry path (`errVersionTaken`) was not observed. SQLite serialised the writers before a version collision could happen. The invariant held anyway.

### 2. Two sessions starting the same skill under the record lock (P2, D14): passes
This probe used a real `gf` binary built into the scratchpad, run as **separate processes**, so the `flock` was taken by different processes.
- Setup: two approved improvements of aggregate-design, si-1 and si-2. Four sessions started at the same moment: two ran `improvement start si-1` and two ran `improvement start si-2`. This was repeated for 15 rounds.
- Every round: exactly one start exited 0, and the record held exactly one `in-progress` line, owned by the winner's session.
- The three losers got "Another improvement of this skill is in progress, so this one waits…".

### 3. The 3 s startup timeout against a slow server (A13): passes
- Slow server: it holds every request for 20 s. The startup SessionStart hook returned after **3.05 s** with exit 0. The brief contained "skill improvements: the history server could not be reached; nothing was applied", followed by the normal "To drive this work, run /gofast:drive." line.
- A server that answers after 2 s is still used: the brief points to `gofast:improve-skills`.

### 4. The viewer forms in a browser (A17 UI): passes
- Setup: a real handler with the viewer, seeded with wallets' history (aggregate-design at aggV1). It was driven in headless Google Chrome over CDP.
- 19 checks, all PASS, with **no** console errors or exceptions:
  - Sign in with a member token.
  - Empty list state.
  - The propose page from a went-wrong visit names "design, visit 1, of w-1". An empty text is refused in the page.
  - Propose → redirects to `#/improvements/si-<16 hex>`, shown as Proposed with "from what went wrong".
  - Save the text → revision 2.
  - Stale approve: ana revised to revision 3 behind the page's back. Clicking "Approve revision 2" showed "Not saved: the improvement was revised since its approver read it". After a reload, revision 3 is shown.
  - Approve for every project → "approved by jordi for every project of the organisation", and the edit form is gone. The payments project's secret is served the improvement with revision 3's text.
  - List and `state=withdrawn` filter.
  - Withdraw → "withdrawn by jordi" with no action buttons left, and wallets' `/approved` is empty.
  - The skill's version page has a "Propose an improvement" link.
- I checked the screenshots by eye. The layout is sound.

## Findings (not blocking)
- Cosmetic: the proposed-improvement edit panel reuses `.reason-form`, which has `background: var(--stop-wash)`. The panel is tinted red as if something went wrong.
- Cosmetic: `.lamp.proposed`, `.lamp.approved` and `.lamp.withdrawn` have no colour rules in app.css, so all three states show the same grey lamp.
- Review v1's S1, S2, S4 and S5 are still open, as review-v3 said: the torn record write, the version read before the lock, exfiltration through `--summary`, and the unbounded `landings` scan. None of them showed up in these probes.

## For stress testing
Review included stress for three things: shared record locking, a GET on every session start, and organisation-wide server scans. Probe 2 shows the lock is correct under contention with 4 processes. Stress should measure the other two: `ApprovedSkillImprovements` and `landings` with many improvements and many synced events.

## Verdict
Passed. The configured suite is green, and all four deferred integration checks hold.
