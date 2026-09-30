# Qt service teardown diagnostics

Run bounded offline batches from the repository root:

```sh
python3 scripts/diagnose_qt_teardown.py build /tmp/weather-qt-diagnostic
python3 scripts/diagnose_qt_teardown.py run /tmp/weather-qt-diagnostic --repeat 3
python3 scripts/diagnose_qt_teardown.py run /tmp/weather-qt-diagnostic --variant sanitized --repeat 1
```

The output directory must not already contain a source snapshot. Keep paths free
of spaces (qmake project paths). The harness needs the existing Go/C++/Qt build
tools and GDB, plus permission for GDB to trace its own child. It installs nothing.

Each process executes the original hide/wait/show/minimize/wait/show sequence.
The data rows distinguish:

- `fixture-terminate`: return with the service alive; its local fixture sends
  SIGTERM and waits before QtTest destroys the QML engine and transport. This
  preserves the original failing destruction order, without a UI quit request.
- `orderly`: request shutdown through the QML bridge and await service exit.
- `kill-disconnect`: kill the owned service, allow EOF delivery, and verify the
  frontend reports disconnection and refuses further commands.

Use `--modes fixture-terminate` to narrow a batch. Every process defaults to a
45-second deadline, repetition is capped at ten, and the batch stops at its first
failure. `--render-loop threaded --perturb 165` requests a different software
render loop and allocator layout. `--trace-transport` records transport failure
stacks and map request entry; on the sanitized build it also queries ASan's
allocation metadata. These options change timing and must be recorded when
comparing results.

The harness snapshots tracked worktree files, preserves the worktree diff and
source hashes, and builds a separate development service. Commands, environment
overrides, build logs, binary hashes, dependency paths, raw test logs, debugger
scripts, actual inferior exit status, timeouts and counts are retained. Test
state, HOME and XDG directories are private; the service is offline and cannot
connect to the user's desktop session. The process supervisor cleans up only
its owned descendants, including after a crash. QtTest's crash handler is
disabled. GDB captures all thread stacks on a signal stop and kills the inferior
without forwarding the fatal signal to the system core handler. No persistent
core configuration is changed and no cores are retained.

The sanitized variant instruments the application C++ test, transport, protocol,
map loader and generated C++ with ASan/UBSan. System Qt, its QML/renderer plugins,
libc and the Go service are **not** sanitizer-instrumented. Leak detection is
disabled for this focused lifetime investigation. An ASan pass alone cannot
exclude invalid accesses executed inside system Qt.

## September 2026 teardown defect

`WeatherTransport` declared its socket before its receive buffer. C++ therefore
destroyed the buffer first. Closing the socket during its destructor emitted
`disconnected` while its callback to the transport was still connected; that
callback called `fail()` and cleared the already-destroyed buffer. QObject's
automatic disconnection occurs too late, during base destruction.

The original fixture sequence reproduced `corrupted double-linked list` after
transport cleanup. Debugger allocation evidence confirmed the callback reached
the freed receive buffer in that same sequence. The allocator detected the
damage later while freeing deferred Qt raster image resources. The relevant
transport and protocol sources were identical in baseline `47c140e` and reviewed
head `9f8b975`; the separate map cancellation fix was not its cause.

The explicit transport destructor now disconnects its socket callbacks and
aborts the socket while all members still exist. The protocol regression checks
that deleting a connected transport, including pending peer EOF, emits no
transport callbacks during destruction. Both rows fail on baseline/pre-fix
sources and pass with the fix. Live disconnect handling remains covered by the
existing protocol test and the forced-disconnect integration row.

The verified follow-up recorded one matching abort in one valid pre-fix optimized
run, then 15/15 passing fixed teardown runs across default optimized, ASan/UBSan,
and requested threaded software rendering with allocator perturbation. Complete
fixed suites passed: protocol 43, frontend 26 (four optional skips), service/
frontend 24 (two opt-in skips). Relevant Go service/IPC/launcher race tests passed.
These bounded software-rendered checks do not claim exhaustive GPU or leak
coverage; the original crash had no retained stack to re-symbolize.
