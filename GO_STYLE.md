# Go maintenance conventions

Use [Effective Go](https://go.dev/doc/effective_go),
[Go Code Review Comments](https://go.dev/wiki/CodeReviewComments), and Google's
[guide](https://google.github.io/styleguide/go/guide),
[decisions](https://google.github.io/styleguide/go/decisions), and
[best practices](https://google.github.io/styleguide/go/best-practices.html).
Apply their principles in context; Google-specific infrastructure is not a
project dependency.

- Format Go source with goimports; keep standard-library imports first and
  project imports in a separate group. No fixed line or function length limit.
- Prefer clear names, guard clauses, and explicit returns in larger functions.
  Retain named results when deferred cleanup changes the returned error.
- Handle errors deliberately. Explain ignored errors whose safety is not
  obvious. Return errors at fallible input boundaries; reserve panic for
  documented programmer invariants.
- Document API ownership, mutation, cancellation, concurrency, non-obvious
  defaults, and error behavior. Prefer useful contracts over redundant prose.
- Keep JSON protocol/saved-state representations and bounds stable. Internal
  map aliases describe the existing wire objects; use structured types when
  they clarify new internal logic without unnecessary protocol migration.
- Make goroutine exit conditions clear. Independent shutdown/recovery contexts
  are intentional when caller cancellation must not abandon owned resources.
- Give independent table cases subtests, actionable failure messages, and
  deterministic synchronization where practical. Do not parallelize tests
  that share desktop/process state or change process-wide configuration.

Run `make lint` for pinned goimports and Staticcheck checks, or `make test-go`
for lint, race tests, and vet. `make test` additionally runs display-free Qt
protocol/frontend tests. The release build runs the same lint gate before its
existing tests. Tool binaries live under ignored `build/tools/`; the first run
downloads the pinned tools from the separate `packaging/lint-tools` module.
Staticcheck v0.8.1 uses the pinned x/tools v0.51.0 importer so it can read the
patched Go 1.27 compiler's export format; goimports uses the same tools release.
These build dependencies are separate from the application module and runtime.
Lint tools require Go 1.26+ (Go's automatic toolchain
selection can supply it); application source remains compatible with Go 1.24.
Staticcheck uses its default checks without blanket suppressions. Update pins
deliberately and verify them against the release toolchain.
