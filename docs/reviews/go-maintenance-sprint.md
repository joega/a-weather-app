# Go maintenance sprint — October 7, 2026

Baseline: `a8671eceeaeba0d15a71e4786f4f6218050b36d3`; clean checkout.
Go module minimum: 1.24.0; local verification toolchain: 1.27.1.

## Scope and progress

- [x] Add concise conventions, formatting/import checks, and pinned Staticcheck to local/CI validation.
- [x] Group imports, simplify terminating branches, and make larger functions' returns explicit.
- [ ] Audit cloning and discarded errors; handle fallible boundaries and document invariants.
- [ ] Document important package/API ownership, concurrency, cancellation, defaults, and failures.
- [ ] Extract coherent CLI, benchmark, and effects helpers without changing lifecycle behavior.
- [ ] Improve affected test isolation, independent case reporting, and meaningful regressions.
- [ ] Run final format/lint/vet/race checks, applicable display-free integration tests, benchmarks, and diff review.

The initial scan found 28 mixed-import files, two larger functions with naked
returns, and widespread missing API comments. Counts are investigation leads,
not cleanup quotas. Preserve JSON representation and rejection limits, saved
state, protocol compatibility, security checks, heartbeat and shutdown/recovery
budgets. C/C++ cleanup and release publication are outside this sprint.

## References and conventions

Apply the [Google guide](https://google.github.io/styleguide/go/guide),
[decisions](https://google.github.io/styleguide/go/decisions), and
[best practices](https://google.github.io/styleguide/go/best-practices.html)
with judgment, together with [Effective Go](https://go.dev/doc/effective_go)
and [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments).
No arbitrary line/function length limits or wholesale architecture rewrite.

## Validation and decisions

Before this sprint, `gofmt`, `go vet ./...`, and `go test -race ./...` passed.
Sprint results and justified exceptions will be recorded here as work proceeds.

Stage 1: pinned Staticcheck 2026.2.1 (`v0.8.1`) and goimports `v0.44.0`,
local `make lint`/`make test-go`, and release CI lint gate. The application
minimum remains Go 1.24; development lint tools need Go 1.26+. Fixed four
Staticcheck findings without suppressions, preserving legacy NUL tar headers.
Imports and terminating branches cleaned; larger naked returns made explicit.
`make lint` and focused saved-state/import tests pass. Socket tests require
escalation in this environment; rerun with sockets enabled passed.

Snapshot baseline, three runs: 2.298–2.358 ms/op, 1.562–1.616 MB/op,
2316–2320 allocations/op; retained for comparison if snapshot paths change.
