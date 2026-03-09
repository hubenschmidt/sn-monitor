#!/usr/bin/env bash
# Detect a second mouse and set up MPX (Multi-Pointer X) for it.
# Idempotent: safe to run repeatedly.

set -euo pipefail

MASTER_NAME="Secondary"

# Always tear down stale master first
if xinput list --name-only 2>/dev/null | grep -q "^${MASTER_NAME} pointer$"; then
	./teardown-mpx.sh
fi

# Reattach any floating pointer devices back to Virtual core pointer first
mapfile -t FLOATING < <(
	xinput list --short |
	grep 'floating slave' |
	grep -iv 'keyboard\|virtual\|XTEST\|consumer\|power' |
	sed -n 's/.*id=\([0-9]*\).*/\1/p'
)

for fid in "${FLOATING[@]}"; do
	echo "mpx: reattaching floating device $fid to Virtual core pointer"
	xinput reattach "$fid" "Virtual core pointer" 2>/dev/null || true
done

# Find slave pointer IDs attached to "Virtual core pointer"
# Exclude virtual devices, touchpads, and keyboards
mapfile -t MICE < <(
	xinput list --short |
	grep 'slave  pointer' |
	grep -iv 'virtual\|touchpad\|trackpoint\|touch\|tablet\|stylus\|eraser\|pad\|consumer\|power' |
	sed -n 's/.*id=\([0-9]*\).*/\1/p'
)

if [ "${#MICE[@]}" -lt 2 ]; then
	echo "mpx: only ${#MICE[@]} mouse/mice detected, skipping MPX setup"
	exit 0
fi

echo ""
echo "Detected mice:"
for i in "${!MICE[@]}"; do
	name=$(xinput list --name-only "${MICE[$i]}" 2>/dev/null || echo "device ${MICE[$i]}")
	printf "  %d: %s (id=%s)\n" "$((i + 1))" "$name" "${MICE[$i]}"
done

printf "\nSelect secondary mouse [2]: "
read -r CHOICE
CHOICE="${CHOICE:-2}"

IDX=$((CHOICE - 1))
if [ "$IDX" -lt 0 ] || [ "$IDX" -ge "${#MICE[@]}" ]; then
	echo "mpx: invalid selection"
	exit 1
fi

SECOND_ID="${MICE[$IDX]}"
SECOND_NAME=$(xinput list --name-only "$SECOND_ID" 2>/dev/null || echo "device $SECOND_ID")

echo "mpx: creating ${MASTER_NAME} master pointer"
xinput create-master "$MASTER_NAME"

echo "mpx: attaching '${SECOND_NAME}' (id=${SECOND_ID}) to ${MASTER_NAME}"
xinput reattach "$SECOND_ID" "${MASTER_NAME} pointer"

echo "mpx: done — two independent cursors active"
