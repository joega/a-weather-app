# Building and verifying releases

Normal Omarchy users install with the root README's single command. The three
compiled runtime artifacts are versioned alongside their source so Omarchy's
Git-based installer and updater receive them without an install hook or download.

`runtime.json` records SHA-256 hashes, architecture, the Hyprland source commit,
source-file hashes, package versions, container digest and Arch snapshot date.
The launcher checks the shader; effects check their binaries and running
Hyprland commit before loading. Checksums detect mismatched files; GitHub's build
attestation provides separate provenance for the release bundle.

## Release pipeline

The `Build installable runtime` GitHub Actions workflow is explicitly dispatched.
It uses an immutable Arch container and signed packages from the dated Arch
snapshot in `.github/workflows/package.yml`. It builds the shader and both native
components twice in different directories and fails unless their bytes match.
It checks native symbols, signs provenance, and uploads a runtime tar archive and
checksums. It has read-only source permissions; it cannot push changes or publish
a marketplace submission.

To build a new release:

1. Commit and push the intended sources. If changing the supported platform,
   update both the workflow and `packaging/release.py` environment constants.
2. Run `gh workflow run package.yml --ref main` and wait for success.
3. Download that run's `a-weather-app-linux-x86_64` artifact using `gh run download`.
4. Verify the downloaded archive before extracting it:

   ```sh
   gh attestation verify a-weather-app-linux-x86_64.tar --repo joega/a-weather-app \
     --signer-workflow joega/a-weather-app/.github/workflows/package.yml \
     --deny-self-hosted-runners
   ```

5. Confirm the attestation identifies the intended source commit and this
   repository's `package.yml` workflow. Extract only its three artifact files
   and `packaging/runtime.json` into a clean checkout of those sources.
   The archive also includes the applicable license notices; retain them when
   redistributing its binaries.
6. Run `python3 -I -B packaging/release.py verify`. Commit those four generated
   files together, test a fresh plugin installation and update/removal, then push.
   Publish the unchanged CI archive and checksums as release downloads if desired.

Keep the published source inventory and compiled artifacts together. Source
changes require another build; `release.py verify` rejects a stale source
inventory. The provenance names the build's source commit; the subsequent
artifact commit contains identical source bytes plus the generated artifacts.

## Local source builds

For an isolated audit of the current checkout (including uncommitted changes),
run from a Docker-enabled terminal:

```sh
bash scripts/run_docker_audit.sh
```

The wrapper resolves this checkout's absolute path, requests sudo only if needed,
and prints an exit status and private `/tmp` log path. The audit installs
signed build dependencies from the pinned Arch snapshot inside the disposable
container, copies sources to temporary storage, runs offline regressions and
sanitizers, and builds/checks native artifacts and shaders twice, requiring
byte-identical pairs. It does not mount
desktop sockets or replace checkout artifacts. It does not provide release
provenance or verification against the final committed release source inventory.
Docker access may require sudo; this procedure does not change account groups.

With development dependencies already installed,
`bash packaging/check_reproducibility.sh` performs only the temporary two-build
comparison locally. It does not install packages, replace checkout artifacts or
activate effects. It prints the retained temporary build directory for inspection.

Use a separate development checkout. Install Python 3, Quickshell, Qt 6 Shader
Tools, a C++23 compiler, make, pkg-config, GTK4, gtk4-layer-shell, JSON-GLib,
libepoxy, EGL/GLES and matching Hyprland development headers. On Arch these come
from `python`, `quickshell`, `qt6-shadertools`, `base-devel`, `gtk4`,
`gtk4-layer-shell`, `json-glib`, `libepoxy`, `mesa`, `libglvnd`, and `hyprland`
with their dependencies.

In that development checkout, deliberately remove the packaged manifest before
replacing its artifacts:

```sh
rm packaging/runtime.json
python3 -I -B packaging/build_shaders.py
make -B -C native/frame-alignment
make -B -C native/atmosphere
./a-weather-app
```

This selects source-build behavior without claiming CI provenance. Never replace
a loaded native library. The native plugin retains its independent Hyprland API
hash check and symbol preflight. Building does not activate effects. Shader
compilation is offline; Godot is not required. Repeat builds after relevant
source or system-library changes. Development changes should not be pushed as
release artifacts without running the release pipeline.
