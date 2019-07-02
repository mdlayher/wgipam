#!/bin/bash

EXIT=0
GOFILES=$(find . -name "*.go" | grep -v "./vendor/")

for FILE in $GOFILES; do
	# Scan each non-vendored Go source file for license.
	BLOCK=$(head -n 1 $FILE)
	if [[ "$BLOCK" != "// Copyright 20"* ]]; then
		echo "file missing license: $FILE"
		EXIT=1
	fi

	# Ensure each file is gofmt'd.
	DIFF=$(gofmt -d -s $FILE)
	if [ ! -z "$DIFF" ]; then
		echo "file is not gofmt'd: $FILE"
		EXIT=1
	fi
done

exit $EXIT