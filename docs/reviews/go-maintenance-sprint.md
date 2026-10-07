# Go maintenance sprint — October 7, 2026

Baseline: `a8671eceeaeba0d15a71e4786f4f6218050b36d3`; clean checkout.
Go module minimum: 1.24.0; local verification toolchain: 1.27.1.

## Scope and progress

- [x] Add concise conventions, formatting/import checks, and pinned Staticcheck to local/CI validation.
- [x] Group imports, simplify terminating branches, and make larger functions' returns explicit.
- [x] Audit cloning and discarded errors; handle fallible boundaries and document invariants.
- [x] Document important package/API ownership, concurrency, cancellation, defaults, and failures.
- [x] Extract coherent CLI, benchmark, and effects helpers without changing lifecycle behavior.
- [x] Improve affected test isolation, independent case reporting, and meaningful regressions.
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

Stage 2: added checked cloning at IPC/native-fixture boundaries while retaining
trusted internal cloning and its JSON number/nil representation. IPC deadline
errors are returned. Updater progress/failure persistence errors remain in the
error chain; failed progress cannot reach app shutdown/activation. Manual
checks surface failures before a status result could be persisted; automatic
checks retain the previous useful notice. Focused race tests for clone isolation,
invalid IPC input, installation/rollback, progress persistence, and manual
checks passed. Internal read-close/cleanup errors remain best effort where
primary operation/durable journal errors already determine success.

Release target: v0.61.0, requested by the user. Runtime pins remain tied to
published verified artifacts; the release-series setting will be updated at
completion, with release notes prepared but no release published.

Stage 3: documented private-directory descriptor/lock ownership, IPC framing
and cancellation, immutable callback dependencies, application snapshots and
shutdown, updater pin/status/transaction contracts, notification serialization,
and native manager admission. Added the missing notifications package comment.
Reviewed rendered `go doc` output for safeio and app.Options; lint passes.
Existing JSON map aliases are retained to avoid broad protocol churn. Existing
useful lifecycle comments and named errors modified by deferred cleanup remain.
Missing comments on trivial constants/private command implementation methods
are not treated as a mandatory documentation quota.

Stage 4: weather/geocoding and air-quality fixtures now inject private TLS
transports through internal helpers; no tests replace http.DefaultTransport.
Endpoint, redirect, proxy, timeout, size, and cancellation rules remain in the
same production paths. Added a regression proving injected routing cannot
bypass endpoint policy, and ran independent fixtures in parallel. Provider
race suites and lint pass. Version/publication cases have independent subtests;
update-check tests wait on worker completion rather than polling with sleeps.
Focused application/updater race tests pass. Timing tests that exercise actual
heartbeat cadence retain wall-clock sampling deliberately.

Stage 5: separated CLI argument parsing/validation and bounded saved-bar refresh
from startup dispatch. Extracted benchmark saved-document loading, private state
preparation, and finite-effects admission/activation. Effects ticks delegate
changed-target verification and cadence-aware weather publication, retaining
observation timestamps and independent bounded cleanup. Full CLI, benchmark,
and effects race suites pass, including generation/expiry, fresh denial evidence,
policy cadence, and recovery. Added a regression proving benchmark preparation
leaves saved files untouched. Lint passes. No live compositor/effects benchmark
was run; these checks use isolated fixtures.
