# Independent review packet: #73

## Scope

This change is test-only. It synchronizes the fake progress messenger snapshot used by `TestJobManagerReplyToCardStatusRefreshesCanonicalCard`; production job and progress code is unchanged.

## Baseline reproduction

- Source: main `be2609ae29f4c03e83eeb9bd087742fdf32ac5c4`
- Runtime: Go `go1.27.4 darwin/arm64`
- Command: `go test -race ./pkg/routing -run '^TestJobManagerReplyToCardStatusRefreshesCanonicalCard$' -count=20`
- Result: exit 1; race detector reported a read at `pkg/routing/jobs_test.go:939` (`len(progress.messages)`) concurrent with a write at `pkg/routing/jobs_test.go:43` in `fakeProgressMessenger.SendOrEditProgress`.

## Root cause and repair

The test reads the fake messenger's `messages` slice while an asynchronous job can append to it under the fake's mutex. `snapshotMessages` now locks the same mutex, copies the slice, and unlocks before assertions. Assertions remain unchanged in meaning; no sleeps or race suppression were added.

## Overlap check

- PR #64 / commit `95192477c58f20fa1cfa6324108bb576f3858a92` changes queued-progress/final-ordering test synchronization; comparison shows the target fake slice reads remain unprotected.
- PR #74 / commit `a880aabdc6987d28d790b7f334ed61ec519bb2a4` serializes and coalesces production progress-card operations; it does not modify this fake or target assertion.

## Verification

- `go test -race ./pkg/routing -run '^TestJobManagerReplyToCardStatusRefreshesCanonicalCard$' -count=20` — pass
- `go test -race ./pkg/routing` — pass
- `go test -p 1 ./...` — pass
- `go vet ./...` — pass
- `go build ./...` — pass
- `git diff --check` — pass

No live services, credentials, merge, or deployment were used.
