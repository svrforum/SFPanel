#!/usr/bin/env bash
#
# new-appstore-app.sh — scaffold a new SFPanel App Store entry.
#
# Usage:
#   scripts/new-appstore-app.sh <app-id>
#
# Creates appstore/apps/<app-id>/{metadata.json,docker-compose.yml} from a
# commented template. Refuses to overwrite an existing folder. After running,
# edit the files, add <app-id> to appstore/index.json, and validate with
# `go test ./internal/feature/appstore/`.

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "Usage: $(basename "$0") <app-id>" >&2
  echo "  <app-id>: lowercase letters/digits/hyphen/underscore, max 50 chars" >&2
  exit 1
fi

APP_ID="$1"

# Mirror the backend validation regex: ^[a-z0-9][a-z0-9_-]{0,49}$
if ! [[ "$APP_ID" =~ ^[a-z0-9][a-z0-9_-]{0,49}$ ]]; then
  echo "Error: invalid app id '$APP_ID'." >&2
  echo "Must match ^[a-z0-9][a-z0-9_-]{0,49}\$ (lowercase, digits, '-', '_')." >&2
  exit 1
fi

# Resolve repo root from this script's location so it works from anywhere.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
APP_DIR="$REPO_ROOT/appstore/apps/$APP_ID"

if [[ -e "$APP_DIR" ]]; then
  echo "Error: $APP_DIR already exists; refusing to overwrite." >&2
  exit 1
fi

mkdir -p "$APP_DIR"

cat > "$APP_DIR/metadata.json" <<EOF
{
  "// id": "App ID — MUST equal the folder name and be in index.json",
  "id": "$APP_ID",

  "// name": "Display name shown on cards and the detail page",
  "name": "$APP_ID",

  "// description": "Short bilingual description; both ko and en are required",
  "description": {
    "ko": "한 줄 설명을 작성하세요",
    "en": "Write a one-line description"
  },

  "// category": "Category id — must exist in categories.json",
  "category": "dev",

  "// version": "App Store package version (bump when you change this entry)",
  "version": "1.0.0",

  "// website": "Official website",
  "website": "https://example.com",

  "// source": "GitHub repo — used for auto version detection + README display",
  "source": "https://github.com/org/repo",

  "// icon": "Optional icon URL; omit to use apps/$APP_ID/icon.svg instead",
  "icon": "https://raw.githubusercontent.com/org/repo/main/icon.svg",

  "// ports": "Fixed host ports this app exposes (each must be 1-65535)",
  "ports": [8080],

  "// featured": "Optional: true renders the app first with a Featured badge",
  "featured": false,

  "// screenshots": "Optional: image URLs shown in the detail-page gallery",
  "screenshots": [],

  "// features": "Optional feature cards (4 recommended). Each is bilingual.",
  "features": [
    {
      "title": { "ko": "기능 제목", "en": "Feature title" },
      "description": { "ko": "기능 설명", "en": "Feature description" },
      "icon": "🚀"
    }
  ],

  "// env": "Variables written to .env. type ∈ {text,port,password,select,path}.",
  "env": [
    {
      "key": "PORT",
      "label": { "ko": "외부 포트", "en": "External Port" },
      "type": "port",
      "default": "8080"
    },
    {
      "key": "ADMIN_PASSWORD",
      "label": { "ko": "관리자 비밀번호", "en": "Admin Password" },
      "type": "password",
      "required": true,
      "// generate": "true → a 32-char random value is created at install time",
      "generate": true
    }
  ]
}
EOF

cat > "$APP_DIR/docker-compose.yml" <<EOF
services:
  $APP_ID:
    image: org/$APP_ID:latest
    container_name: $APP_ID
    restart: unless-stopped
    ports:
      # \${PORT} comes from .env (the "port"-type env above); :-8080 is the default.
      - "\${PORT:-8080}:80"
    environment:
      - ADMIN_PASSWORD=\${ADMIN_PASSWORD}
    volumes:
      - $APP_ID-data:/data

volumes:
  $APP_ID-data:
EOF

echo "Created:"
echo "  $APP_DIR/metadata.json"
echo "  $APP_DIR/docker-compose.yml"
echo
echo "Next steps:"
echo "  1. Edit metadata.json (strip the \"// ...\" comment keys — they are"
echo "     placeholders) and base docker-compose.yml on the project's official one."
echo "  2. Add \"$APP_ID\" to appstore/index.json (alphabetical order preferred)."
echo "  3. Regenerate the catalog bundle: make appstore-catalog"
echo "  4. Validate: go test ./internal/feature/appstore/"
echo "  5. Open a pull request."
