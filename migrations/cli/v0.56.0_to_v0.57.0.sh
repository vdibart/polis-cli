#!/usr/bin/env bash
#
# Migration: v0.56.0 → v0.57.0
# Adds default policy files for the policy-driven blessing/notification system.
#
# Changes:
# - Creates policies/rules.jsonl (public) with default emit rules if missing
# - Adds version header + new default rules to .polis/policies/rules.jsonl (private)
# - Idempotent: safe to run multiple times
# - No network access required
#
set -e

DATA_DIR="${1:-.}"

# ============================================================================
# PUBLIC POLICY FILE: policies/rules.jsonl
# ============================================================================

PUBLIC_POLICY_DIR="${DATA_DIR}/policies"
PUBLIC_POLICY_FILE="${PUBLIC_POLICY_DIR}/rules.jsonl"

if [[ ! -f "$PUBLIC_POLICY_FILE" ]]; then
  mkdir -p "$PUBLIC_POLICY_DIR"
  cat > "$PUBLIC_POLICY_FILE" <<'JSONL'
{"version":1,"generator":"polis-cli/0.57.0"}
{"active":true,"policy":"emit pub.polis.comment.blessing from self"}
{"active":true,"policy":"emit pub.polis.comment.blessing from following"}
{"active":true,"policy":"emit pub.polis.comment.blessing from thread-blessed"}
JSONL
  echo "[i] Created public policy file: policies/rules.jsonl"
else
  # Check for version header, add if missing
  if ! head -1 "$PUBLIC_POLICY_FILE" | grep -q '"version"'; then
    # Prepend version header
    TEMP_FILE=$(mktemp)
    echo '{"version":1,"generator":"polis-cli/0.57.0"}' > "$TEMP_FILE"
    cat "$PUBLIC_POLICY_FILE" >> "$TEMP_FILE"
    mv "$TEMP_FILE" "$PUBLIC_POLICY_FILE"
    echo "[i] Added version header to public policy file"
  fi

  # Migrate old allow rule to emit rules if present
  if grep -q '"allow pub.polis.comment.blessing from following"' "$PUBLIC_POLICY_FILE" 2>/dev/null; then
    # Replace old allow rule with new emit rules
    TEMP_FILE=$(mktemp)
    grep -v '"allow pub.polis.comment.blessing from following"' "$PUBLIC_POLICY_FILE" > "$TEMP_FILE" || true
    if ! grep -q '"emit pub.polis.comment.blessing from self"' "$TEMP_FILE"; then
      echo '{"active":true,"policy":"emit pub.polis.comment.blessing from self"}' >> "$TEMP_FILE"
    fi
    if ! grep -q '"emit pub.polis.comment.blessing from following"' "$TEMP_FILE"; then
      echo '{"active":true,"policy":"emit pub.polis.comment.blessing from following"}' >> "$TEMP_FILE"
    fi
    if ! grep -q '"emit pub.polis.comment.blessing from thread-blessed"' "$TEMP_FILE"; then
      echo '{"active":true,"policy":"emit pub.polis.comment.blessing from thread-blessed"}' >> "$TEMP_FILE"
    fi
    mv "$TEMP_FILE" "$PUBLIC_POLICY_FILE"
    echo "[i] Migrated public policy rules from allow to emit verbs"
  else
    # Just ensure emit rules exist
    for RULE in \
      "emit pub.polis.comment.blessing from self" \
      "emit pub.polis.comment.blessing from following" \
      "emit pub.polis.comment.blessing from thread-blessed"; do
      if ! grep -q "\"$RULE\"" "$PUBLIC_POLICY_FILE" 2>/dev/null; then
        echo "{\"active\":true,\"policy\":\"$RULE\"}" >> "$PUBLIC_POLICY_FILE"
        echo "[i] Added missing public rule: $RULE"
      fi
    done
  fi
fi

# ============================================================================
# PRIVATE POLICY FILE: .polis/policies/rules.jsonl
# ============================================================================

PRIVATE_POLICY_DIR="${DATA_DIR}/.polis/policies"
PRIVATE_POLICY_FILE="${PRIVATE_POLICY_DIR}/rules.jsonl"

mkdir -p "$PRIVATE_POLICY_DIR"
chmod 700 "$PRIVATE_POLICY_DIR" 2>/dev/null || true

if [[ ! -f "$PRIVATE_POLICY_FILE" ]]; then
  cat > "$PRIVATE_POLICY_FILE" <<'JSONL'
{"version":1,"generator":"polis-cli/0.57.0"}
{"active":true,"policy":"allow pub.polis.post from all"}
{"active":true,"policy":"allow pub.polis.comment from all"}
{"active":true,"policy":"allow pub.polis.follow from all"}
{"active":true,"policy":"allow pub.polis.site from all"}
{"active":true,"policy":"allow pub.polis.dm from following"}
{"active":true,"policy":"deny pub.polis.dm from all"}
{"active":true,"policy":"omit pub.polis.notification from self"}
JSONL
  chmod 600 "$PRIVATE_POLICY_FILE" 2>/dev/null || true
  echo "[i] Created private policy file: .polis/policies/rules.jsonl"
else
  # Add version header if missing
  if ! head -1 "$PRIVATE_POLICY_FILE" | grep -q '"version"'; then
    TEMP_FILE=$(mktemp)
    echo '{"version":1,"generator":"polis-cli/0.57.0"}' > "$TEMP_FILE"
    cat "$PRIVATE_POLICY_FILE" >> "$TEMP_FILE"
    mv "$TEMP_FILE" "$PRIVATE_POLICY_FILE"
    chmod 600 "$PRIVATE_POLICY_FILE" 2>/dev/null || true
    echo "[i] Added version header to private policy file"
  fi

  # Append missing default rules (preserves user customizations)
  for RULE in \
    "allow pub.polis.post from all" \
    "allow pub.polis.comment from all" \
    "allow pub.polis.follow from all" \
    "allow pub.polis.site from all" \
    "omit pub.polis.notification from self"; do
    if ! grep -q "\"$RULE\"" "$PRIVATE_POLICY_FILE" 2>/dev/null; then
      echo "{\"active\":true,\"policy\":\"$RULE\"}" >> "$PRIVATE_POLICY_FILE"
      echo "[i] Added missing private rule: $RULE"
    fi
  done

  # DM rules already handled by ensureDMPolicies in init
  if ! grep -q 'pub.polis.dm' "$PRIVATE_POLICY_FILE" 2>/dev/null; then
    echo '{"active":true,"policy":"allow pub.polis.dm from following"}' >> "$PRIVATE_POLICY_FILE"
    echo '{"active":true,"policy":"deny pub.polis.dm from all"}' >> "$PRIVATE_POLICY_FILE"
    echo "[i] Added default DM policy rules"
  fi
fi

echo "[✓] Policy migration complete (v0.56.0 → v0.57.0)"
