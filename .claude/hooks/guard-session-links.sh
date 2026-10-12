#!/usr/bin/env bash
# PreToolUse hook. Refuses to write a claude.ai session link (a `Claude-Session:`
# trailer or a `claude.ai/code/session…` URL) into anything this public
# repository publishes:
#
#   - Bash (registered with `if: Bash(git *)` and `if: Bash(gh *)`): a
#     `git commit`, `git commit-tree`, `git merge`, `git tag` or `git notes`
#     whose message, given inline or with `-F <file>`, carries one, and any
#     `gh` command that does.
#   - GitHub MCP write tools (pull requests, comments, issues, file commits):
#     any input field that carries one.
#
# Exit 2 blocks the call and the stderr text is shown to Claude. Cloud
# sessions add the trailer and the link by default; this hook is what keeps
# them out. Like guard-git.sh it matches text, so the commit message has to be
# in the command or in the file passed with -F.
#
# Test: echo '{"tool_name":"Bash","tool_input":{"command":"git commit -s -m \"x\n\nClaude-Session: https://claude.ai/code/session_x\""}}' | .claude/hooks/guard-session-links.sh; echo $?
set -euo pipefail

input=$(cat)

block() {
  printf 'Blocked: %s\n' "$1" >&2
  exit 2
}

# A Claude-Session trailer or a claude.ai session URL, in any case.
has_link() {
  grep -qiE 'claude-session:|claude\.ai/code/session' <<<"$1"
}

field() {
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
    echo "guard-session-links: neither jq nor python3 found; skipping the check" >&2
    exit 0
  fi
}

message="A claude.ai session link (Claude-Session trailer or claude.ai/code/session URL) must never appear in a commit, pull request, issue or comment of this repository: the repository is public. Remove the line and try again; do not add any other session reference."

tool=$(field .tool_name)

case "$tool" in
  Bash)
    command_text=$(field .tool_input.command)
    [ -n "$command_text" ] || exit 0
    # gh writes pull requests, issues and comments; any link in its
    # arguments ends up on GitHub.
    if grep -qE '(^|[[:space:];&|(])gh[[:space:]]' <<<"$command_text" && has_link "$command_text"; then
      block "$message"
    fi
    # No \b: the BSD grep of macOS does not know it.
    grep -qE '(^|[[:space:];&|(])git[[:space:]](.*[[:space:]])?(commit|commit-tree|merge|tag|notes)([[:space:]]|$)' <<<"$command_text" || exit 0
    # The message inline: -m, --message, or a heredoc fed to -F -.
    has_link "$command_text" && block "$message"
    # The message from a file: -F <file>, --file <file>, --file=<file>.
    read -ra tokens <<<"$(tr '\n' ' ' <<<"$command_text")" || true
    for ((i = 0; i < ${#tokens[@]}; i++)); do
      file=""
      case "${tokens[$i]}" in
        -F | --file) file=${tokens[$((i + 1))]:-} ;;
        --file=*) file=${tokens[$i]#--file=} ;;
      esac
      file=${file//\"/}
      file=${file//\'/}
      if [ -n "$file" ] && [ "$file" != - ] && [ -f "$file" ] && has_link "$(cat "$file")"; then
        block "$message"
      fi
    done
    ;;
  mcp__github__*)
    # Every string the tool would publish: body, title, commit message,
    # file contents.
    has_link "$(jq -r '.tool_input | .. | strings' <<<"$input" 2>/dev/null || printf '%s' "$input")" && block "$message"
    ;;
esac

exit 0
