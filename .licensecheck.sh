#!/bin/bash

# Scan each non-vendored Go source file for license.
EXIT=0
GOFILES=$(find . -name "*.go" | grep -v "./vendor/")

for FILE in $GOFILES; do
	BLOCK=$(head -n 1 $FILE)

	if [[ "$BLOCK" != "// Copyright 20"* ]]; then
		echo "file missing license: $FILE"
		EXIT=1
	fi
done

exit $EXIT
