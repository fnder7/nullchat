#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
run_script="$repo_root/run.sh"
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

make_fake_tools() {
  local bin_dir=$1

  mkdir -p "$bin_dir"
  cat >"$bin_dir/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$DOCKER_CALLS"
if [[ ${1:-} == compose && ${2:-} == version ]]; then
  exit 0
fi
if [[ ${1:-} == compose && ${2:-} == up && ${3:-} == --build ]]; then
  exit 0
fi
exit 64
EOF
  cat >"$bin_dir/openssl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ ${1:-} == rand && ${2:-} == -hex && ${3:-} == 32 ]] || exit 64
printf '%064d\n' 0 | tr '0' 'a'
EOF
  chmod +x "$bin_dir/docker" "$bin_dir/openssl"
}

test_first_run_creates_secret_and_starts_compose() {
  local case_dir="$test_root/first-run"
  local fake_bin="$case_dir/bin"
  mkdir -p "$case_dir/project"
  make_fake_tools "$fake_bin"
  cp "$run_script" "$case_dir/project/run.sh"

  DOCKER_CALLS="$case_dir/docker.calls" \
    PATH="$fake_bin:/usr/bin:/bin" \
    "$case_dir/project/run.sh"

  [[ -f "$case_dir/project/.env" ]] || fail "first run did not create .env"
  [[ $(<"$case_dir/project/.env") == "IDEMPOTENCY_ENCRYPTION_KEY=$(printf '%064d' 0 | tr '0' 'a')" ]] ||
    fail "first run wrote an unexpected .env"
  [[ $(stat -c '%a' "$case_dir/project/.env") == 600 ]] || fail ".env permissions are not 600"
  grep -Fxq 'compose version' "$case_dir/docker.calls" || fail "Docker Compose availability was not checked"
  grep -Fxq 'compose up --build' "$case_dir/docker.calls" || fail "Compose was not started in foreground"
}

test_existing_secret_is_preserved() {
  local case_dir="$test_root/existing-secret"
  local fake_bin="$case_dir/bin"
  local existing_key
  existing_key=$(printf '%064d' 0 | tr '0' 'b')
  mkdir -p "$case_dir/project"
  make_fake_tools "$fake_bin"
  cp "$run_script" "$case_dir/project/run.sh"
  printf 'EXTRA=value\nIDEMPOTENCY_ENCRYPTION_KEY=%s\n' "$existing_key" >"$case_dir/project/.env"

  cat >"$fake_bin/openssl" <<'EOF'
#!/usr/bin/env bash
echo "openssl must not be called for an existing key" >&2
exit 99
EOF
  chmod +x "$fake_bin/openssl"

  DOCKER_CALLS="$case_dir/docker.calls" \
    PATH="$fake_bin:/usr/bin:/bin" \
    "$case_dir/project/run.sh"

  [[ $(<"$case_dir/project/.env") == $'EXTRA=value\nIDEMPOTENCY_ENCRYPTION_KEY='"$existing_key" ]] ||
    fail "existing .env was modified"
}

test_first_run_creates_secret_and_starts_compose
test_existing_secret_is_preserved
echo "run.sh tests: PASS"
