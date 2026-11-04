# Stress testing: w-7ec48ee9, visit 1

## Outcome
No k6 scripts were written and nothing was measured. This is a vacuous pass. It does not show that the change performs well under load.

Why:
- The repository has no NFRs. `.gofast/nfrs.json` does not exist.
- `current.stress.mandatory` is empty.
- design-v1.md "NFRs" proposed none ("The change does not affect behaviour under load"), and no proposal was adopted.

Each stress script belongs to an NFR id at `stress/<nfr-id>.js`, and gf takes the load and targets from that NFR. With no NFR there is no load to run and no target to measure against, so this stage has nothing to exercise. Making up an NFR id or target here would be inventing requirements. Only the design can propose NFRs (`nfrs.proposed-v<visit>.json`), and only the owner can adopt them (`gf nfrs adopt`). Neither is this stage's job.

This visit follows a rework (fixer-v2: rule 7 became an allowlist grammar). That change is to the improver's Bash guard, which is a local string match with no load-related behaviour, so it adds nothing to stress.

## What review and integration testing asked for, still unmeasured
Review v3 included stress for three reasons. Integration testing (probe 2) already showed the first one holds under contention with 4 processes. The other two have no NFR and are not covered:

1. **The startup GET.** `GET /v1/projects/{project}/skill-improvements/approved` (historyserver/http.go:56) runs on every session start in every clone. The client's HTTP timeout is 60s (workflow/remote/client.go:29). D8 says startup uses a short fixed timeout and never blocks. Load tests should check that.
2. **Organisation-wide scans.**
   - `Store.ApprovedSkillImprovements` (historyserver/skillimprovements.go:500) reads every improvement row of the organisation (`organisationRows`), folds them all into views, and sorts with an O(n²) insertion sort, all on every startup GET.
   - `Store.landings` (skillimprovements.go:546) reads every synced `skill-improvements` landing event of the organisation and decodes the JSON of each one in Go to find a single improvement id. The id is not filtered in SQL. This is review v1's S5, which is still open.

## Suggested NFRs for the owner to adopt (not adopted, not measured)
These are suggestions only. Adopting them would make a later stress visit mandatory and measurable:
- `skill-improvements-approved-get`: operation `GET /v1/projects/{project}/skill-improvements/approved` with the project secret. Data: an organisation with about 50 projects, about 2,000 improvements in mixed states and about 200 approved. Load: about 50 rps, standing in for session starts across the organisation. Target: p95 under 200 ms and an error rate under 0.1%.
- `skill-improvement-detail-get`: operation `GET /v1/projects/{project}/skill-improvements/{id}` as a member (this is what loads landings). Data: about 100,000 synced landing events in the organisation. Load: about 10 rps. Target: p95 under 300 ms.

## Verdict
Submitted as passed only because no NFR applies. The startup GET and the two org-wide scans are untested under load until the owner adopts NFRs for them.

## Submit result (attempt 1 of 3)
`gf submit --passed` failed `stress-scripts-valid` for two reasons: `.gofast/checks.json` has no "stress" section (the command that starts the service), and `.gofast/nfrs.json` does not exist. Both belong to the owner, and this stage may not edit either. Submitting again would fail the same way and spend another attempt, so I stopped. To unblock this stage, the owner either adds a stress config and adopts NFRs (see the suggestions above), or decides to skip this optional stage.
