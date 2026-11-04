# CLAUDE.md

## Git workflow (always follow)

- **Remotes:** `origin` = `git@github.com:yaltayeh/gomontage.git` (push here).
  `upstream` = `AhmedHodiani/gomontage` (fetch only, never push, never open PRs
  against it).
- **Branches:** never commit on `main`. One branch per phase/task, created from
  an up-to-date `origin/main`.
- **Commits:** small Conventional Commits, one per logical change; reference
  known-bug numbers (e.g. "Fixes known bug #5").
- **Staging:** stage files explicitly; never commit media, renders, `my-video/`,
  scratch files or secrets.
- **Before every push, all must pass:**
  - gofmt on files you touched (the 4 pre-existing unformatted files on main —
    `effects/effects_test.go`, `kdenlive/doc.go`, `kdenlive/kdenlive.go`,
    `kdenlive/kdenlive_test.go` — are excluded)
  - `go build ./...`
  - `go vet ./...` and `go vet -tags e2e ./e2e/...`
  - `go test ./...`
  - `make test-e2e`

  Never push a failing branch, never skip or delete tests to make them pass,
  never use `--no-verify`, never force-push.
- **Known bugs:** when fixing a known bug, remove its `knownBug(...)` line in the
  same commit (see TESTING.md).
- **Pull requests:** when a phase/task is complete and pushed, open a PR with
  `gh pr create --repo yaltayeh/gomontage --base main --head <branch>`. Always
  pass `--repo yaltayeh/gomontage` explicitly (gh defaults to the upstream
  parent for forks). PR body: summary, list of changes, test results, open
  questions. Never merge PRs yourself — the owner reviews and merges.
- If a branch already has an open PR, just push new commits to it instead of
  opening another.
- **End every task with:** branch, commits (hash + message), test results,
  push status, PR URL.
