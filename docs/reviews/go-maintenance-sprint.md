# Go maintenance sprint — October 7, 2026

Completed against baseline `a8671eceeaeba0d15a71e4786f4f6218050b36d3`
(clean checkout), using Go 1.27.1. The application minimum remains Go 1.24.0.
Release target: **v0.61.0**. The subsequent native/QML cleanup is recorded in
[its review](native-qml-maintenance-sprint.md).

## Completed work

- [x] Added [project conventions](../../GO_STYLE.md), pinned goimports `v0.44.0`
  and Staticcheck `v0.8.1` (2026.2.1), and local/release-build lint gates.
  Preserved vet/race checks; fixed four findings without suppressions.
  Formatting-tool failures also fail the gate.
- [x] Separated imports, removed terminating-branch nesting, and made larger
  `readSaved`/`importRuntime` returns explicit.
- [x] Added checked JSON cloning at IPC/native-fixture input boundaries,
  retained trusted internal cloning, and returned IPC deadline errors.
  Updater progress/status-write failures remain in the error chain and prevent
  shutdown/activation. Manual update checks surface unpersisted failures.
- [x] Documented useful safeio, IPC, updater, App/options, notification, and
  effects ownership, cancellation, concurrency, defaults, and failure contracts.
- [x] Extracted CLI parsing and bounded bar refresh; benchmark document loading,
  private fixture preparation, and finite-effects activation; effects target
  verification and weather publication. Preserved ownership checks, independent
  cleanup, observation timestamps, heartbeat cadence, and restart budgets.
- [x] Removed test mutations of `http.DefaultTransport`, using private provider
  transports and an injected CLI resolver. Preserved endpoint/proxy/redirect
  restrictions. Added independent subtests, channel-based update completion,
  and regressions for invalid JSON, status persistence, endpoint policy,
  mutation isolation, and untouched benchmark saved files.
- [x] Completed final checks, allocation comparison, and diff audit. Prepared
  the 0.61 release series and [release notes](../../packaging/releases/v0.61.0.md).

Implementation commits: `3f610eb`, `9281a77`, `0d18540`, `0d169e7`, `2ea1b61`;
final verification, audit corrections, and release preparation follow them.

## Validation

- `make test-go`: goimports check, pinned Staticcheck, `go test -race ./...`,
  and `go vet ./...` pass. Focused suites passed after substantive stages.
- `make -C native/qt test`: 43 protocol and 28 frontend cases pass; four
  optional screenshot/private-capture cases skip.
- `make go APP_VERSION=0.61.0`: development build succeeds and reports 0.61.0.
  `make -C native/qt test-e2e`: 24 service/frontend cases pass; two optional
  private-map/live-provider cases skip.
- Native display-free input checks pass using the existing atmosphere executable
  and the updated Go `weather-native-check inputs` command.
- Release-version script tests pass; the selector chooses `version=0.61.0`.
- October 7 Docker release build: exit 0 against clean source
  `141bbd922e939cc67835ccdffc40a688681c1546`, with Go 1.27.1 in the pinned
  Arch container. Lint, race/vet, Qt tests, native address/undefined-behavior/leak
  sanitizers, symbols/hardening, input validation, inventory checks, and packaged
  offline startup/shutdown pass. Five artifacts are byte-identical across two
  independent builds. Archive metadata declares 0.61.0; SHA256SUMS verifies.
  Archive SHA-256: `29f60068a71e5ff17677be475e91c7b785ccb61011d24a89b554f6d955ff87b4`.
  Local artifacts: `dist/go-migration.aqrjWqpv/`;
  log: `/tmp/weather-go-migration.pI9pa5Kc.log` (temporary).
- User reports live desktop effects also work correctly. This is manual,
  user-verified evidence; the agent did not operate the live compositor.
- `git diff --check` passes. Reviewed changed boundaries, lifecycle extraction,
  protocol keys, default provider routing, saved-state handling, and release pins.

Snapshot benchmark, three runs before/after with the same Go toolchain:

| Measurement | Baseline | After |
| --- | --- | --- |
| Time | 2.298–2.358 ms/op | 2.296–2.418 ms/op |
| Memory | 1.562–1.616 MB/op | 1.560–1.653 MB/op |
| Allocations | 2316–2320/op | 2316–2322/op |

Median allocations remain 2317/op. Results are comparable, with run-to-run
variation; these short runs do not establish a performance improvement.

## Deliberate limits and exceptions

Applied [Effective Go](https://go.dev/doc/effective_go),
[Code Review Comments](https://go.dev/wiki/CodeReviewComments), and Google's
[guide](https://google.github.io/styleguide/go/guide),
[decisions](https://google.github.io/styleguide/go/decisions), and
[best practices](https://google.github.io/styleguide/go/best-practices.html)
with judgment. Audit counts are leads, not quotas; no arbitrary size limits,
blanket suppressions, architecture rewrite, or redundant comment sweep.

JSON aliases, float64/nil representation, nesting/size limits, named results
needed by deferred cleanup, and wall-clock tests of actual cadence remain.
Trusted cloning may panic on programmer invariant violations. Read-only close
and failed-staging cleanup errors remain best effort; durable write errors are
handled. ELF dynamic-tag reads rely on the existing bounded in-memory preflight.
Lint tools need Go 1.26+; Go can select that toolchain automatically.

Automated verification used isolated/offscreen fixtures and the complete
Docker release/reproducibility build. Live effects were checked by the user;
optional private visual captures and live-provider checks remain skipped.
Existing runtime pins and source version defaults remain at the published
0.60.0 until the release workflow verifies and pins new artifacts. The final combined source still needs the
Docker/live checks recorded in the native/QML review.
No tag, push, or release publication was performed.
