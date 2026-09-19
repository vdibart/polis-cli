#!/bin/bash
# assertions.sh - Assertion utilities for Polis CLI tests
#
# Provides:
#   - JSON field validation
#   - Exit code checking
#   - File/directory existence checks
#   - Content pattern matching

# Assert JSON field equals expected value
# Usage: assert_json_field "$json" ".field.path" "expected_value"
assert_json_field() {
    local json="$1"
    local field="$2"
    local expected="$3"
    local actual

    actual=$(echo "$json" | jq -r "$field" 2>/dev/null)

    if [[ "$actual" == "$expected" ]]; then
        log "  [OK] $field == \"$expected\""
        return 0
    else
        log_error "[FAIL] $field: expected \"$expected\", got \"$actual\""
        return 1
    fi
}

# Assert JSON field exists (is not null)
# Usage: assert_json_has_field "$json" ".field.path"
assert_json_has_field() {
    local json="$1"
    local field="$2"

    if echo "$json" | jq -e "$field" > /dev/null 2>&1; then
        log "  [OK] Field $field exists"
        return 0
    else
        log_error "[FAIL] Field $field does not exist or is null"
        return 1
    fi
}

# Assert JSON field is not empty string
# Usage: assert_json_field_not_empty "$json" ".field.path"
assert_json_field_not_empty() {
    local json="$1"
    local field="$2"
    local value

    value=$(echo "$json" | jq -r "$field" 2>/dev/null)

    if [[ -n "$value" && "$value" != "null" ]]; then
        log "  [OK] Field $field is not empty"
        return 0
    else
        log_error "[FAIL] Field $field is empty or null"
        return 1
    fi
}

# Assert exit code matches expected
# Usage: assert_exit_code 0 $?
assert_exit_code() {
    local expected="$1"
    local actual="$2"

    if [[ "$actual" -eq "$expected" ]]; then
        log "  [OK] Exit code: $actual"
        return 0
    else
        log_error "[FAIL] Exit code: expected $expected, got $actual"
        return 1
    fi
}

# Assert file exists
# Usage: assert_file_exists "path/to/file"
assert_file_exists() {
    local path="$1"

    if [[ -f "$path" ]]; then
        log "  [OK] File exists: $path"
        return 0
    else
        log_error "[FAIL] File does not exist: $path"
        return 1
    fi
}

# Assert file does not exist
# Usage: assert_file_not_exists "path/to/file"
assert_file_not_exists() {
    local path="$1"

    if [[ ! -f "$path" ]]; then
        log "  [OK] File does not exist: $path"
        return 0
    else
        log_error "[FAIL] File should not exist: $path"
        return 1
    fi
}

# Assert directory exists
# Usage: assert_dir_exists "path/to/dir"
assert_dir_exists() {
    local path="$1"

    if [[ -d "$path" ]]; then
        log "  [OK] Directory exists: $path"
        return 0
    else
        log_error "[FAIL] Directory does not exist: $path"
        return 1
    fi
}

# Assert directory does not exist
# Usage: assert_dir_not_exists "path/to/dir"
assert_dir_not_exists() {
    local path="$1"

    if [[ ! -d "$path" ]]; then
        log "  [OK] Directory does not exist: $path"
        return 0
    else
        log_error "[FAIL] Directory should not exist: $path"
        return 1
    fi
}

# Assert file contains pattern
# Usage: assert_file_contains "file" "pattern"
assert_file_contains() {
    local file="$1"
    local pattern="$2"

    if grep -q "$pattern" "$file" 2>/dev/null; then
        log "  [OK] File contains: $pattern"
        return 0
    else
        log_error "[FAIL] File does not contain: $pattern"
        return 1
    fi
}

# Assert file does not contain pattern
# Usage: assert_file_not_contains "file" "pattern"
assert_file_not_contains() {
    local file="$1"
    local pattern="$2"

    if ! grep -q "$pattern" "$file" 2>/dev/null; then
        log "  [OK] File does not contain: $pattern"
        return 0
    else
        log_error "[FAIL] File should not contain: $pattern"
        return 1
    fi
}

# Assert JSON is valid
# Usage: assert_valid_json "$json"
assert_valid_json() {
    local json="$1"

    if echo "$json" | jq empty 2>/dev/null; then
        log "  [OK] Valid JSON"
        return 0
    else
        log_error "[FAIL] Invalid JSON"
        return 1
    fi
}

# Assert JSON error response has expected code
# Usage: assert_json_error_code "$json" "ERROR_CODE"
assert_json_error_code() {
    local json="$1"
    local expected_code="$2"

    assert_json_field "$json" ".status" "error" || return 1
    assert_json_field "$json" ".error.code" "$expected_code" || return 1
    return 0
}

# Assert JSON success response
# Usage: assert_json_success "$json" "command_name"
assert_json_success() {
    local json="$1"
    local command="$2"

    assert_valid_json "$json" || return 1
    assert_json_field "$json" ".status" "success" || return 1
    assert_json_field "$json" ".command" "$command" || return 1
    return 0
}

# Assert file is staged in git
# Usage: assert_file_staged "path/to/file"
assert_file_staged() {
    local file="$1"

    if git diff --cached --name-only | grep -q "$(basename "$file")"; then
        log "  [OK] File staged: $file"
        return 0
    else
        log_error "[FAIL] File not staged: $file"
        return 1
    fi
}

# Assert timestamp format (ISO 8601)
# Usage: assert_timestamp_format "2025-01-04T12:00:00Z"
assert_timestamp_format() {
    local value="$1"

    if [[ "$value" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]]; then
        log "  [OK] Valid timestamp format: $value"
        return 0
    else
        log_error "[FAIL] Invalid timestamp format: $value"
        return 1
    fi
}

# Assert hash format (sha256:...)
# Usage: assert_hash_format "sha256:abc123..."
assert_hash_format() {
    local value="$1"

    if [[ "$value" =~ ^sha256:[a-f0-9]{64}$ ]]; then
        log "  [OK] Valid hash format"
        return 0
    else
        log_error "[FAIL] Invalid hash format: $value"
        return 1
    fi
}
