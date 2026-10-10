#!/usr/bin/env bash
# PreToolUse hook (Bash, registered with `if: Bash(git *)`). Enforces the
# repository's git rules before a command runs. Exit 2 blocks the command and
# the stderr text is shown to Claude.
#
#   - `git commit` needs the DCO sign-off (`-s` / `--signoff`); CI checks it.
#   - `--no-verify` (`-n` on commit) skips the pre-commit hooks.
#   - `git push` must not force, delete remote refs or push tags: humans cut
#     releases by pushing a signed v* tag (CONTRIBUTING.md, "Releasing").
#
# This is best-effort text matching on the command Claude wrote. The deny
# rules in .claude/settings.json and the CI checks stay the hard gates.
#
# Test: echo '{"tool_input":{"command":"git commit -m x"}}' | .claude/hooks/guard-git.sh; echo $?
set -euo pipefail

input=$(cat)

hook_field() {
  if command -v jq >/dev/null 2>&1; then
    jq -r "$1 // empty" <<<"$input"
  elif command -v python3 >/dev/null 2>&1; then
    python3 -I -c '
import json, sys
value = json.load(sys.stdin)
for key in sys.argv[1].strip(".").split("."):
    value = value.get(key, "") if isinstance(value, dict) else ""
print(value if isinstance(value, str) else "")' "$1" <<<"$input"
  else
    echo "guard-git: neither jq nor python3 found; skipping the check" >&2
  fi
}

command_text=$(hook_field .tool_input.command)
[ -n "$command_text" ] || exit 0

block() {
  printf 'Blocked: %s\n' "$1" >&2
  exit 2
}

push_blocked() {
  block "git push with \`$1\` is not allowed here: no force pushes, no deleting remote refs, no tag pushes. Releases are cut by a human pushing a signed v* tag (CONTRIBUTING.md, Releasing)."
}

# One shell command per line: split on &&, ||, ;, | and newlines.
segments=${command_text//&&/$'\n'}
segments=${segments//\|\|/$'\n'}
segments=${segments//;/$'\n'}
segments=${segments//\|/$'\n'}

while IFS= read -r segment; do
  tokens=()
  read -ra tokens <<<"$segment" || true
  [ "${#tokens[@]}" -gt 0 ] || continue

  # Skip leading VAR=value assignments.
  i=0
  while [ "$i" -lt "${#tokens[@]}" ]; do
    case "${tokens[$i]}" in
      [A-Za-z_]*=*) i=$((i + 1)) ;;
      *) break ;;
    esac
  done
  [ "$i" -lt "${#tokens[@]}" ] || continue
  [ "${tokens[$i]}" = git ] || continue
  i=$((i + 1))

  # Skip git's own options (`git -C dir push`, `git -c k=v commit`) to find
  # the subcommand.
  sub=""
  while [ "$i" -lt "${#tokens[@]}" ]; do
    case "${tokens[$i]}" in
      -C|-c|--git-dir|--work-tree|--namespace) i=$((i + 2)) ;;
      -*) i=$((i + 1)) ;;
      *) sub=${tokens[$i]}; i=$((i + 1)); break ;;
    esac
  done
  args=()
  if [ "$i" -lt "${#tokens[@]}" ]; then
    args=("${tokens[@]:$i}")
  fi

  case "$sub" in
    commit)
      signoff=no
      for t in ${args[@]+"${args[@]}"}; do
        case "$t" in
          --no-verify) block "\`git commit --no-verify\` skips the pre-commit hooks (gitleaks, formatters) and is not allowed in this repository." ;;
          --signoff) signoff=yes ;;
          --no-signoff) signoff=no ;;
          --*) ;;
          -*)
            # Short option cluster such as -sm or -asm.
            if [[ $t =~ ^-[A-Za-z]+$ ]]; then
              [[ $t == *n* ]] && block "\`git commit -n\` (--no-verify) skips the pre-commit hooks (gitleaks, formatters) and is not allowed in this repository."
              [[ $t == *s* ]] && signoff=yes
            fi
            ;;
        esac
      done
      [ "$signoff" = yes ] || block "\`git commit\` needs the DCO sign-off: add -s (or --signoff), for example \`git commit -s -m \"...\"\` (CONTRIBUTING.md, DCO)."
      ;;
    push)
      for t in ${args[@]+"${args[@]}"}; do
        case "$t" in
          --force|--force-with-lease|--force-with-lease=*|--force-if-includes|--mirror|--delete|--prune|--tags|--follow-tags) push_blocked "$t" ;;
          --no-verify) block "\`git push --no-verify\` skips the git hooks and is not allowed in this repository." ;;
          +*) push_blocked "$t" ;;        # forced refspec
          :*) push_blocked "$t" ;;        # deletes a remote ref
          tag|refs/tags/*|*:refs/tags/*) push_blocked "$t" ;;
          --*) ;;
          -*)
            if [[ $t =~ ^-[A-Za-z]+$ ]] && [[ $t == *f* || $t == *d* ]]; then
              push_blocked "$t"
            fi
            ;;
          *)
            # A release tag name (v1.2.3, v1.2.0-rc.1).
            if [[ $t =~ ^v[0-9]+(\.[0-9]+){1,2}(-[0-9A-Za-z.]+)?$ ]]; then
              push_blocked "$t"
            fi
            ;;
        esac
      done
      ;;
  esac
done <<<"$segments"

exit 0
