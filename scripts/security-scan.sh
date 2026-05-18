#!/usr/bin/env bash
set -euo pipefail

mode="${1:-full}"
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

banned_pattern='babel''force'
secret_pattern='-----BEGIN (RSA|OPENSSH|EC|DSA|PRIVATE) PRIVATE KEY-----|AKIA[0-9A-Z]{16}|ASIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,}|gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9]{20,}|AIza[0-9A-Za-z_-]{35}'
working_tree_exclude_globs=(
	'!.git/**'
	'!.cache/**'
	'!**/.agents/index/**'
	'!vendor/**'
	'!node_modules/**'
)

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

rg_exclude_args() {
	printf -- '--glob\n%s\n' "${working_tree_exclude_globs[@]}"
}

trufflehog_exclude_paths_file() {
	local exclude_file="$1"
	cat >"$exclude_file" <<'EOF'
(^|.*/)\.git/
(^|.*/)\.cache/
(^|.*/)\.agents/index/
(^|.*/)vendor/
(^|.*/)node_modules/
EOF
}

allowed_trufflehog_json() {
	local line="$1"
	case "$line" in
		*'"file":'*'/.agents/index/'*)
			return 0
			;;
		*'"DetectorName":"Gitlab"'*'"Verified":false'*'"Raw":"ListAllProjectMembers"'*)
			return 0
			;;
		*'"DetectorName":"Gitlab"'*'"Verified":false'*'"Raw":"pipelinesForMRProject"'*)
			return 0
			;;
		*)
			return 1
			;;
	esac
}

check_trufflehog_findings() {
	local findings="$1"
	local label="$2"
	local blocked=0
	while IFS= read -r line; do
		if [[ -z "$line" ]] || allowed_trufflehog_json "$line"; then
			continue
		fi
		printf '%s\n' "$line" >&2
		blocked=1
	done <"$findings"
	if ((blocked)); then
		printf 'security-scan: trufflehog found findings in %s\n' "$label" >&2
		exit 1
	fi
}

run_trufflehog_staged() {
	local findings
	findings="$(mktemp)"
	git diff --cached --binary | trufflehog stdin --json --no-verification --no-update --log-level=-1 >"$findings"
	check_trufflehog_findings "$findings" "staged diff"
	rm -f "$findings"
}

run_trufflehog_json() {
	local findings
	findings="$(mktemp)"
	printf 'security-scan: %s\n' "$*"
	"$@" --json --no-verification --no-update --log-level=-1 >"$findings"
	check_trufflehog_findings "$findings" "$*"
	rm -f "$findings"
}

gitleaks_working_tree_targets() {
	find . \
		-path './.git' -prune -o \
		-path './.cache' -prune -o \
		-path '*/.agents/index' -prune -o \
		-path './vendor' -prune -o \
		-path './node_modules' -prune -o \
		-type f -print
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
	run_trufflehog_staged
}

check_full() {
	require_tool rg
	require_tool gitleaks
	require_tool trufflehog
	local revs=()
	mapfile -t revs < <(git rev-list --all)

	local rg_excludes=()
	mapfile -t rg_excludes < <(rg_exclude_args)

	if rg -n -i --hidden "${rg_excludes[@]}" "$banned_pattern" .; then
		printf 'security-scan: banned keyword found in working tree: %s\n' "$banned_pattern" >&2
		exit 1
	fi

	if ((${#revs[@]} > 0)) && git grep -n -i -e "$banned_pattern" "${revs[@]}"; then
		printf 'security-scan: banned keyword found in Git history: %s\n' "$banned_pattern" >&2
		exit 1
	fi

	if rg -l --hidden "${rg_excludes[@]}" --pcre2 "(?i)($secret_pattern)" .; then
		printf 'security-scan: high-confidence secret pattern found in working tree\n' >&2
		exit 1
	fi

	if ((${#revs[@]} > 0)) && git grep -l -I -E -e "$secret_pattern" "${revs[@]}"; then
		printf 'security-scan: high-confidence secret pattern found in Git history\n' >&2
		exit 1
	fi

	local trufflehog_excludes
	trufflehog_excludes="$(mktemp)"
	trap 'rm -f "$trufflehog_excludes"' RETURN
	trufflehog_exclude_paths_file "$trufflehog_excludes"

	local gitleaks_targets=()
	mapfile -t gitleaks_targets < <(gitleaks_working_tree_targets)

	run gitleaks git . --redact --no-color --log-level warn
	if ((${#gitleaks_targets[@]} > 0)); then
		run gitleaks dir --redact --no-color --log-level warn "${gitleaks_targets[@]}"
	fi
	run_trufflehog_json trufflehog git "file://$repo_root" --exclude-paths "$trufflehog_excludes"
	run_trufflehog_json trufflehog filesystem "$repo_root" --exclude-paths "$trufflehog_excludes"
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
