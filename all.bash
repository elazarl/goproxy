#!/bin/bash

go test

find examples/* -maxdepth 0 -type d | while read d; do
	(cd $d;go build)
done
