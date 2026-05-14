#!/usr/bin/env bash
set -euo pipefail

mode="${1:-full}"
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

banned_pattern='babel''force'
secret_pattern='-----BEGIN (RSA|OPENSSH|EC|DSA|PRIVATE) PRIVATE KEY-----|AKIA[0-9A-Z]{16}|ASIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,}|gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9]{20,}|AIza[0-9A-Za-z_-]{35}'

require_tool() {
	local tool="$1"
	if ! command -v "$tool" >/dev/null 2>&1; then
		printf 'security-scan: missing required tool: %s\n' "$tool" >&2
		exit 1
	fi
}

run() {
	printf 'security-scan: %s\n' "$*"
	"$@"
}

check_staged() {
	require_tool gitleaks
	require_tool trufflehog

	if git grep --cached -n -i -e "$banned_pattern" -- .; then
		printf 'security-scan: banned keyword found in staged index: %s\n' "$banned_pattern" >&2
		exit 1
	fi

	if git grep --cached -n -I -E -e "$secret_pattern" -- .; then
		printf 'security-scan: high-confidence secret pattern found in staged index\n' >&2
		exit 1
	fi

	run gitleaks git . --staged --redact --no-color --log-level warn
	git diff --cached --binary | trufflehog stdin --no-verification --no-update --fail --log-level=-1
}

check_full() {
	require_tool rg
	require_tool gitleaks
	require_tool trufflehog
	local revs=()
	mapfile -t revs < <(git rev-list --all)

	if rg -n -i --hidden --glob '!.git/**' --glob '!vendor/**' --glob '!node_modules/**' "$banned_pattern" .; then
		printf 'security-scan: banned keyword found in working tree: %s\n' "$banned_pattern" >&2
		exit 1
	fi

	if ((${#revs[@]} > 0)) && git grep -n -i -e "$banned_pattern" "${revs[@]}"; then
		printf 'security-scan: banned keyword found in Git history: %s\n' "$banned_pattern" >&2
		exit 1
	fi

	if rg -l --hidden --glob '!.git/**' --glob '!vendor/**' --glob '!node_modules/**' --pcre2 "(?i)($secret_pattern)" .; then
		printf 'security-scan: high-confidence secret pattern found in working tree\n' >&2
		exit 1
	fi

	if ((${#revs[@]} > 0)) && git grep -l -I -E -e "$secret_pattern" "${revs[@]}"; then
		printf 'security-scan: high-confidence secret pattern found in Git history\n' >&2
		exit 1
	fi

	run gitleaks git . --redact --no-color --log-level warn
	run gitleaks dir . --redact --no-color --log-level warn
	run trufflehog git "file://$repo_root" --no-verification --no-update --fail --log-level=-1
	run trufflehog filesystem "$repo_root" --no-verification --no-update --fail --log-level=-1
}

case "$mode" in
	staged)
		check_staged
		;;
	full)
		check_full
		;;
	*)
		printf 'usage: %s [staged|full]\n' "$0" >&2
		exit 2
		;;
esac

printf 'security-scan: ok\n'
