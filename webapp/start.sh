#!/bin/bash
# Polis Local App - Start Script

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Run the server from the script directory
cd "$SCRIPT_DIR"
./polis-server
