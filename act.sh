#!/usr/bin/env bash
# Runs .github/workflows/release.yml locally with nektos/act, so a change to the
# CI is debugged without pushing a tag.
#
#   ./act.sh list              print the jobs and the events act can run
#   ./act.sh prepare [kind]    run the prepare job: version, tag, pre-release flag
#   ./act.sh linux [kind]      run prepare and the linux job end to end
#   ./act.sh dryrun            print the plan and start no container
#
# kind is "stable" (a vX.X.X tag on main) or "dev" (a release/vX.X.X branch) and
# defaults to "stable". VERSION sets the version number and defaults to the
# newest git tag. Flags after the kind are passed to act.
#
# act starts Linux containers only, so the macos and windows jobs do not run
# here. The publish job creates a real release, so this script never runs it.

set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$ROOT"

WORKFLOW=.github/workflows/release.yml
ACT_DIR=.act
EVENT="$ACT_DIR/event.json"

usage() {
  awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0"
}

fail() {
  printf 'act.sh: %s\n' "$1" >&2
  exit 1
}

cmd=${1:-help}
shift || true

kind=stable
extra=()
case "$cmd" in
  list | dryrun) extra=("$@") ;;
  help | -h | --help) usage && exit 0 ;;
  prepare | linux)
    # A leading dash is an act flag, so the kind keeps its default.
    if [ $# -gt 0 ] && [ "${1#-}" = "$1" ]; then
      kind=$1
      shift
    fi
    extra=("$@")
    ;;
  *) fail "unknown command '$cmd'; run ./act.sh help" ;;
esac

if ! command -v act >/dev/null 2>&1; then
  fail "act is not installed; run mise install"
fi

# The version the workflow reads from the ref. A tag vX.X.X carries X.X.X.
version=${VERSION:-}
if [ -z "$version" ]; then
  version=$(git describe --tags --abbrev=0 2>/dev/null || true)
fi
if [ -z "$version" ]; then
  version=0.0.0
fi
version=${version#v}

case "$kind" in
  stable) ref="refs/tags/v$version" ;;
  dev) ref="refs/heads/release/v$version" ;;
  *) fail "unknown kind '$kind'; use stable or dev" ;;
esac

# github.sha comes from the payload on a push event, so the dev tag carries the
# short sha of the local commit the same way the pushed build carries its own.
sha=$(git rev-parse HEAD)

# The workflow reads github.repository_owner and github.event.repository.name.
owner=local
repo=ofxhost
remote=$(git config --get remote.origin.url 2>/dev/null || true)
if [ -n "$remote" ]; then
  path=${remote#*github.com}
  path=${path#[:/]}
  owner=${path%%/*}
  repo=${path#*/}
  repo=${repo%.git}
fi

mkdir -p "$ACT_DIR"
cat >"$EVENT" <<JSON
{
  "ref": "$ref",
  "before": "$sha",
  "after": "$sha",
  "repository": {
    "name": "$repo",
    "full_name": "$owner/$repo",
    "owner": { "login": "$owner" }
  },
  "pusher": { "name": "act.sh" }
}
JSON

# list reads the workflow file only, so it needs no engine.
if [ "$cmd" != "list" ]; then
  if ! command -v docker >/dev/null 2>&1; then
    fail "docker is not installed; act needs a docker-compatible engine"
  fi
  if ! docker info >/dev/null 2>&1; then
    fail "the container engine does not answer; start Docker, or with podman run 'podman machine start'"
  fi
fi

# The release runner images are x86_64, so an Apple Silicon host runs them under
# emulation. The linux job is slow that way; the prepare job stays quick.
arch=()
case "$(uname -m)" in
  arm64 | aarch64) arch=(--container-architecture linux/amd64) ;;
  *) arch=() ;;
esac

run_act() {
  # act refuses --artifact-server-path in a project .actrc, so it comes from here.
  act --workflows "$WORKFLOW" --eventpath "$EVENT" \
    --artifact-server-path "$ACT_DIR/artifacts" \
    ${arch[@]+"${arch[@]}"} "$@"
}

# A local run prints the same version and tag that the run on GitHub prints, so
# the values are visible before act pulls an image or starts a container.
printf 'act.sh: %s, ref %s\n' "$cmd" "$ref"

case "$cmd" in
  list) run_act --list ;;
  prepare) run_act --job prepare ${extra[@]+"${extra[@]}"} ;;
  linux) run_act --job prepare --job linux ${extra[@]+"${extra[@]}"} ;;
  dryrun) run_act --dryrun --job prepare --job linux ${extra[@]+"${extra[@]}"} ;;
esac
