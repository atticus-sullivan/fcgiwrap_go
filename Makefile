# SPDX-FileCopyrightText: 2025 2025 Lukas Heindl
#
# SPDX-License-Identifier: MIT

.PHONY: build cover test mod weight size

build:
	go build

cover:
	go test -coverprofile=coverage.out ./...
	# go tool cover -func=coverage.out
	go tool cover -html=coverage.out

test:
	go test fcgiwrap_go/... -timeout 30s

mod:
	tmp=$$(mktemp) ; go mod graph | modgraphviz | dot -Tpng -o "$$tmp" ; sxiv "$$tmp" ; rm $$tmp

weight:
	goweight --json fcgiwrap_go | jq '.|=sort_by(.size)|.[] | [(.size_human,.name)] | @sh'

# https://github.com/xaionaro/documentation/blob/master/golang/reduce-binary-size.md
size:
	GOOS=linux GOARCH=arm GOARM=5 go build
	-go tool nm -size -sort size fcgiwrap_go | head -10
	-go tool nm fcgiwrap_go | grep -c DeadVariable
