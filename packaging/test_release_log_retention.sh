#!/usr/bin/bash
# Exercise the real host wrapper using a fake container command, never Docker.
set -euo pipefail
root=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
mkdir -p "$fixture/source/scripts" "$fixture/bin"
cp "$root/scripts/run_go_migration_build.sh" "$fixture/source/scripts/"
(
  cd "$fixture/source"
  git init -q
  git add scripts
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
done
[[ ${jobs:-} == "$FIXTURE_JOBS" ]]
[[ -f $source/scripts/run_go_migration_build.sh ]]
[[ -z $(find "$output" -mindepth 1 -print -quit) ]]
printf 'fixture full log status=%s\n' "$FIXTURE_EXIT"
exit "$FIXTURE_EXIT"
DOCKER
chmod +x "$fixture/bin/docker"
for scenario in '0 1' '17 2'; do
  read -r status jobs <<< "$scenario"
  result=0
  env PATH="$fixture/bin:$PATH" FIXTURE_EXIT="$status" FIXTURE_JOBS="$jobs" WEATHER_BUILD_JOBS="$jobs" \
    bash "$fixture/source/scripts/run_go_migration_build.sh" > "$fixture/wrapper-$status.log" 2>&1 || result=$?
  [[ $result == "$status" ]]
  directory=$(sed -n 's/^Artifacts: //p' "$fixture/wrapper-$status.log" | tail -1)
  gzip -cd "$directory/evidence/build.log.gz" | grep -Fx "fixture full log status=$status"
  awk -F '\t' -v status="$status" '$1=="runner-total" && $3==status {found=1} END {exit !found}' \
    "$directory/evidence/phase-timings.tsv"
  [[ $(cat "$directory/evidence/build-settings.tsv") == $'build_jobs\t'"$jobs" ]]
done
result=0
env PATH="$fixture/bin:$PATH" WEATHER_BUILD_JOBS=3 \
  bash "$fixture/source/scripts/run_go_migration_build.sh" > "$fixture/invalid-jobs.log" 2>&1 || result=$?
[[ $result == 2 ]]
grep -Fq 'WEATHER_BUILD_JOBS must be 1' "$fixture/invalid-jobs.log"
printf 'PASS: success and failure retain full logs, exit status and end-to-end timing.\n'
