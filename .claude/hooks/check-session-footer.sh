#!/usr/bin/env bash
# PostToolUse hook (GitHub MCP tools that create a pull request, issue,
# comment or review). In cloud sessions the GitHub connector itself can
# append an attribution footer that links the claude.ai session to the text
# Claude posted, after guard-session-links.sh has checked the input. This
# hook cannot see what was posted, so it tells Claude to read it back and
# remove such a link at once. Exit 2 shows the stderr text to Claude; the
# post itself stays.
#
# Test: echo '{"tool_name":"mcp__github__create_pull_request","tool_input":{}}' | .claude/hooks/check-session-footer.sh; echo $?
set -euo pipefail

cat >/dev/null

cat >&2 <<'EOF'
Check what was just posted: the GitHub connector of cloud sessions can append a footer that links the claude.ai session (`claude.ai/code/session…`). This repository is public and must never carry such a link. Read the post back now (pull_request_read `get`, issue_read, or the comment) and, if it contains one, remove the link at once (update_pull_request `body`, issue_write, update_issue_comment) without adding any other session reference.
EOF
exit 2
