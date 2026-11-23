#!/usr/bin/bash
# Exercise the real host wrapper using a fake container command, never Docker.
set -euo pipefail
root=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
mkdir -p "$fixture/source/scripts" "$fixture/source/packaging" "$fixture/bin"
cp "$root/scripts/run_go_migration_build.sh" "$fixture/source/scripts/"
cp "$root/packaging/release-profile.sh" "$fixture/source/packaging/"
mkdir -p "$fixture/source/docs/reviews"
printf 'tracked report\n' > "$fixture/source/docs/reviews/tracked report.md"
printf 'private ignored audit\n' > "$fixture/source/docs/reviews/private.md"
printf 'docs/\nbuild/\n' > "$fixture/source/.gitignore"
(
  cd "$fixture/source"
  git init -q
  git add scripts
  git add packaging
  git add .gitignore
  git add -f 'docs/reviews/tracked report.md'
  git -c user.name=Fixture -c user.email=fixture@example.invalid commit -qm fixture
)
cat > "$fixture/bin/docker" <<'DOCKER'
#!/usr/bin/bash
set -euo pipefail
[[ $1 != info ]] || exit 0
[[ $1 == run ]]
for arg in "$@"; do
  if [[ $arg == type=bind,src=*,dst=/output ]]; then
    output=${arg#type=bind,src=};output=${output%,dst=/output}
  fi
  if [[ $arg == type=bind,src=*,dst=/source,readonly ]]; then
    source=${arg#type=bind,src=};source=${source%,dst=/source,readonly}
  fi
  if [[ $arg == WEATHER_BUILD_JOBS=* ]]; then jobs=${arg#WEATHER_BUILD_JOBS=}; fi
  if [[ $arg == WEATHER_VALIDATION_MODE=* ]]; then profile=${arg#WEATHER_VALIDATION_MODE=}; fi
  if [[ $arg == type=bind,src=*,dst=/package-cache,readonly ]]; then
    cache=${arg#type=bind,src=};cache=${cache%,dst=/package-cache,readonly}
  fi
done
[[ ${jobs:-} == "$FIXTURE_JOBS" ]]
[[ ${profile:-} == "$FIXTURE_PROFILE" ]]
[[ -f $source/scripts/run_go_migration_build.sh ]]
[[ -f $source/docs/reviews/tracked\ report.md ]]
[[ ! -e $source/docs/reviews/private.md ]]
[[ -z $(git -C "$source" status --porcelain --untracked-files=all) ]]
[[ -z $(find "$output" -mindepth 1 -print -quit) ]]
if [[ -n ${cache:-} ]]; then
  [[ $(cat "$cache/input.pkg.tar.zst") == cached-input ]]
  mkdir "$output/package-cache"
  printf 'downloaded-input\n' > "$output/package-cache/output.pkg.tar.zst"
fi
printf 'PHASE START fixture-build\n'
printf 'fixture full log status=%s\n' "$FIXTURE_EXIT"
printf 'PHASE START invalid extra fields\n'
printf 'PHASE END fixture-build elapsed_ms=1 status=%s\n' "$FIXTURE_EXIT"
exit "$FIXTURE_EXIT"
DOCKER
chmod +x "$fixture/bin/docker"
for scenario in '0 1' '17 2'; do
  read -r status jobs <<< "$scenario"
  result=0
  env PATH="$fixture/bin:$PATH" FIXTURE_EXIT="$status" FIXTURE_JOBS="$jobs" FIXTURE_PROFILE=full WEATHER_BUILD_JOBS="$jobs" WEATHER_VALIDATION_MODE=full \
    bash "$fixture/source/scripts/run_go_migration_build.sh" > "$fixture/wrapper-$status.log" 2>&1 || result=$?
  [[ $result == "$status" ]]
  directory=$(sed -n 's/^Artifacts: //p' "$fixture/wrapper-$status.log" | tail -1)
  gzip -cd "$directory/evidence/build.log.gz" | grep -Fx "fixture full log status=$status"
  grep -Fx 'PHASE START fixture-build' "$fixture/wrapper-$status.log"
  grep -Fx "PHASE END fixture-build elapsed_ms=1 status=$status" "$fixture/wrapper-$status.log"
  if [[ $status == 0 ]]; then
    ! grep -Fq 'fixture full log status=' "$fixture/wrapper-$status.log"
    ! grep -Fq 'PHASE START invalid extra fields' "$fixture/wrapper-$status.log"
  fi
  awk -F '\t' -v status="$status" '$1=="runner-total" && $3==status {found=1} END {exit !found}' \
    "$directory/evidence/phase-timings.tsv"
  [[ $(cat "$directory/evidence/build-settings.tsv") == $'build_jobs\t'"$jobs"$'\nvalidation_profile\tfull' ]]
done
mkdir -p "$fixture/source/build/package-cache"
printf 'cached-input\n' > "$fixture/source/build/package-cache/input.pkg.tar.zst"
env PATH="$fixture/bin:$PATH" FIXTURE_EXIT=0 FIXTURE_JOBS=2 FIXTURE_PROFILE=release WEATHER_BUILD_JOBS=2 \
  WEATHER_VALIDATION_MODE= WEATHER_CACHE_PACKAGES=1 \
  bash "$fixture/source/scripts/run_go_migration_build.sh" > "$fixture/cached.log" 2>&1
[[ $(cat "$fixture/source/build/package-cache/output.pkg.tar.zst") == downloaded-input ]]
directory=$(sed -n 's/^Artifacts: //p' "$fixture/cached.log" | tail -1)
[[ ! -e $directory/package-cache ]]
result=0
env PATH="$fixture/bin:$PATH" WEATHER_BUILD_JOBS=3 \
  bash "$fixture/source/scripts/run_go_migration_build.sh" > "$fixture/invalid-jobs.log" 2>&1 || result=$?
[[ $result == 2 ]]
grep -Fq 'WEATHER_BUILD_JOBS must be 1' "$fixture/invalid-jobs.log"
result=0
env PATH="$fixture/bin:$PATH" WEATHER_VALIDATION_MODE=unknown \
  bash "$fixture/source/scripts/run_go_migration_build.sh" > "$fixture/invalid-profile.log" 2>&1 || result=$?
[[ $result == 2 ]]
grep -Fq 'WEATHER_VALIDATION_MODE must be release or full' "$fixture/invalid-profile.log"
printf 'PASS: live bounded phase markers; private full logs, exit status and timing retained.\n'
