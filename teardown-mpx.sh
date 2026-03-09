#!/usr/bin/env bash
# Tear down MPX: reattach all slave pointers back to Virtual core pointer,
# then remove the Secondary master.

set -uo pipefail

MASTER_NAME="Secondary"

# Reattach any floating pointer devices regardless of master state
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

if ! xinput list --name-only 2>/dev/null | grep -q "^${MASTER_NAME} pointer$"; then
	exit 0
fi

# Get the Secondary master's ID
SEC_ID=$(xinput list --short | grep "${MASTER_NAME} pointer" | grep 'master pointer' | sed -n 's/.*id=\([0-9]*\).*/\1/p')

# Reattach only slaves that belong to the Secondary master
mapfile -t SLAVES < <(
	xinput list --short |
	grep 'slave  pointer' |
	grep -v 'Virtual\|XTEST' |
	sed -n 's/.*id=\([0-9]*\).*/\1/p'
)

for sid in "${SLAVES[@]}"; do
	attached=$(xinput list "$sid" 2>/dev/null | sed -n 's/.*Attached to: \([0-9]*\).*/\1/p')
	[ "$attached" != "$SEC_ID" ] && continue
	echo "mpx: reattaching device $sid to Virtual core pointer"
	xinput reattach "$sid" "Virtual core pointer" 2>/dev/null || true
done

echo "mpx: removing ${MASTER_NAME} master"
xinput remove-master "${MASTER_NAME} pointer" 2>/dev/null || true
echo "mpx: teardown complete"
