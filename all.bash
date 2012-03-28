#!/bin/bash

go test

mkdir -p bin
find examples/* -maxdepth 0 -type d | while read d; do
	(cd $d;go build -o ../../bin/$(basename $d))
done
