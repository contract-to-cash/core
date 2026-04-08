#!/usr/bin/env bash
# sync-docs.sh — Sync design docs from docs/ into website i18n (Japanese)
#
# The docs/design/ files (written in Japanese) are the single source of truth
# for design documentation. This script copies them into the website's Japanese
# locale, replacing the hand-maintained translations.
#
# English website docs (website/docs/) are maintained separately.
#
# Usage: Run from the website/ directory, or it auto-detects paths.
#   ./scripts/sync-docs.sh
#
# Integrated into website build via: npm run build (see package.json)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WEBSITE_DIR="$(dirname "$SCRIPT_DIR")"
REPO_ROOT="$(dirname "$WEBSITE_DIR")"

SOURCE_DIR="$REPO_ROOT/docs/design"
TARGET_DIR="$WEBSITE_DIR/i18n/ja/docusaurus-plugin-content-docs/current/concepts"

# Also sync architecture.md
SOURCE_ARCH="$REPO_ROOT/docs/architecture.md"
TARGET_ARCH="$WEBSITE_DIR/i18n/ja/docusaurus-plugin-content-docs/current/architecture.md"

# Map: source filename -> target filename, sidebar_position, title
declare -A FILE_MAP
FILE_MAP["domain-model.md"]="domain-model.md|1|ドメインモデル"
FILE_MAP["event-sourcing.md"]="event-sourcing.md|2|イベントソーシング"
FILE_MAP["plugin-system.md"]="plugin-system.md|3|プラグインシステム"
FILE_MAP["payment-gateway.md"]="payment-gateway.md|4|決済ゲートウェイ"

sync_file() {
    local src="$1"
    local dst="$2"
    local position="$3"
    local title="$4"

    if [ ! -f "$src" ]; then
        echo "WARN: Source not found: $src"
        return
    fi

    # Write frontmatter + source content (skip any existing H1 title line)
    {
        echo "---"
        echo "sidebar_position: $position"
        echo "title: \"$title\""
        echo "---"
        echo ""
        echo "<!-- docs/design/$(basename "$src") から自動同期。直接編集しないでください。 -->"
        echo "<!-- 実行: cd website && npm run sync-docs -->"
        echo ""
        # Copy content, skipping the first H1 line (# Title) since frontmatter title replaces it
        sed '1{/^# /d;}' "$src"
    } > "$dst"

    echo "Synced: $(basename "$src") -> ja/concepts/$(basename "$dst")"
}

mkdir -p "$TARGET_DIR"

for src_file in "${!FILE_MAP[@]}"; do
    IFS='|' read -r dst_file position title <<< "${FILE_MAP[$src_file]}"
    sync_file "$SOURCE_DIR/$src_file" "$TARGET_DIR/$dst_file" "$position" "$title"
done

# Sync architecture.md (strip "関連ドキュメント" section — links are docs/-relative)
if [ -f "$SOURCE_ARCH" ]; then
    {
        echo "---"
        echo "sidebar_position: 3"
        echo "title: \"アーキテクチャ\""
        echo "---"
        echo ""
        echo "<!-- docs/architecture.md から自動同期。直接編集しないでください。 -->"
        echo "<!-- 実行: cd website && npm run sync-docs -->"
        echo ""
        sed '1{/^# /d;}' "$SOURCE_ARCH" | sed '/^## 7\. 関連ドキュメント/,$d'
    } > "$TARGET_ARCH"
    echo "Synced: architecture.md -> ja/architecture.md"
fi

echo "Done. Synced design docs to website/i18n/ja/."
