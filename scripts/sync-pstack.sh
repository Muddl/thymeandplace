#!/usr/bin/env bash
# Re-syncs the vendored pstack skills from upstream. See .cursor/skills/PSTACK_VENDORED.md.
# Usage: scripts/sync-pstack.sh [git-ref]   (default: main)
set -euo pipefail

repo_url="https://github.com/cursor/plugins"
ref="${1:-main}"
root="$(cd "$(dirname "$0")/.." && pwd)"
skills_dir="$root/.cursor/skills"
agents_dir="$root/.cursor/agents"
manifest="$skills_dir/PSTACK_VENDORED.md"

pstack_principles=(
  attack-the-premise boundary-discipline build-the-lever encode-lessons-in-structure
  exhaust-the-design-space experience-first explain-the-number fix-root-causes
  foundational-thinking guard-the-context-window laziness-protocol
  make-operations-idempotent migrate-callers-then-delete-legacy-apis
  minimize-reader-load model-the-domain never-block-on-the-human
  outcome-oriented-execution prove-it-works redesign-from-first-principles
  separate-before-serializing-shared-state sequence-verifiable-units
  subtract-before-you-add test-behavior-not-implementation type-system-discipline
)
pstack_skills=(
  poteto-mode architect arena benchmark-checklist blast-radius figure-it-out how why
  interrogate no-comments reflect swarm technical-writing unslop correct tdd
  create-verification-skill maintain-verification-skill show-me-your-work
)
pstack_agents=(poteto-agent comment-sicko)
team_kit_skills=(deslop control-ui control-cli)

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
git clone --quiet "$repo_url" "$tmp/plugins"
git -C "$tmp/plugins" checkout --quiet "$ref"
sha="$(git -C "$tmp/plugins" rev-parse HEAD)"

copy_skill() {
  local src="$1" name="$2"
  [ -f "$src/SKILL.md" ] || { echo "missing upstream skill: $src" >&2; exit 1; }
  rm -rf "${skills_dir:?}/$name"
  cp -R "$src" "$skills_dir/$name"
}

for s in "${pstack_skills[@]}"; do copy_skill "$tmp/plugins/pstack/skills/$s" "$s"; done
for p in "${pstack_principles[@]}"; do copy_skill "$tmp/plugins/pstack/skills/principle-$p" "principle-$p"; done
for s in "${team_kit_skills[@]}"; do copy_skill "$tmp/plugins/cursor-team-kit/skills/$s" "$s"; done

mkdir -p "$agents_dir"
for a in "${pstack_agents[@]}"; do cp "$tmp/plugins/pstack/agents/$a.md" "$agents_dir/$a.md"; done

cp "$tmp/plugins/pstack/LICENSE" "$skills_dir/PSTACK_LICENSE"
cp "$tmp/plugins/cursor-team-kit/LICENSE" "$skills_dir/CURSOR_TEAM_KIT_LICENSE"

# Upstream paths assume pstack/ at a repo root; here the skills live in .cursor/skills/.
grep -rlF 'pstack/skills/' "$skills_dir" --include='*.md' --include='*.mjs' --include='*.ts' 2>/dev/null \
  | grep -v PSTACK_VENDORED.md \
  | while read -r f; do sed -i 's#pstack/skills/#.cursor/skills/#g' "$f"; done
if grep -rlF 'pstack/skills/' "$skills_dir" | grep -qv PSTACK_VENDORED.md; then
  echo "unpatched plugin-relative paths remain" >&2; exit 1
fi

# Playbooks name poteto-mode's helper scripts relative to the skill. The repo root has its own scripts/.
sed -i -E 's#([` ])scripts/(watch-pr/|orch/|worktree-audit\.sh)#\1.cursor/skills/poteto-mode/scripts/\2#g' \
  "$skills_dir"/poteto-mode/playbooks/*.md "$skills_dir"/poteto-mode/references/*.md

# Cloud sessions skip skills that set disable-model-invocation, and a name that differs from the folder.
# Both edits touch only the frontmatter (the block between the first two --- lines) and are no-ops on re-run.
edit_frontmatter() {
  local file="$1" name_override="${2:-}" out
  out="$(mktemp)"
  awk -v name="$name_override" '
    NR == 1 && $0 == "---" { infm = 1; print; next }
    infm && $0 == "---" { infm = 0; print; next }
    infm && $0 ~ /^disable-model-invocation:[[:space:]]*true[[:space:]]*$/ { next }
    infm && name != "" && $0 ~ /^name:/ { print "name: " name; next }
    { print }
  ' "$file" > "$out"
  cat "$out" > "$file"
  rm -f "$out"
}
for f in "$skills_dir"/*/SKILL.md; do
  skill="$(basename "$(dirname "$f")")"
  if [ "$skill" = "poteto-mode" ]; then edit_frontmatter "$f" poteto-mode; else edit_frontmatter "$f"; fi
done
if grep -rq '^disable-model-invocation: true' "$skills_dir"/*/SKILL.md; then
  echo "disable-model-invocation still set in a vendored skill" >&2; exit 1
fi

sed -i "s#^Source commit: .*#Source commit: \`$sha\`#" "$manifest"
echo "Synced pstack @ $sha. Review with: git diff --stat"
