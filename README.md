<!--
SPDX-FileCopyrightText: 2025 2025 Lukas Heindl

SPDX-License-Identifier: MIT
-->

# fcgiwrap_go

`fcgiwrap_go` is a re-implementation of [the original `fcgiwrap`
tool](https://github.com/gnosek/fcgiwrap). Written in golang the codebase is
considerably smaller and the server structure fits quite well into the golang
idioms. Also the native [fcgi library](https://pkg.go.dev/net/http/fcgi) in the
stdlib of go offers a nice support in the golang ecosystem (without having to
implement some state-machine in order to parse the response).

The main driver for re-implementing the `fcgiwrap` tool was to be able to stop
the server if for some time no new requests are made. This is supposed to save
resources, as the process does not need to be around forever.

With most systems using `systemd` nowadays, you might want to start this tool by
a systemd service unit which in turn is started by a systemd socket unit. This
way the `fcgiwrap_go` tool only is activated after a request came in and handles
the request. Then (optionally) it waits some time for new requests, deactivates
again and is started only if needed afterwards.

The commandline arguments are quite similar to those of `fcgiwrap`. But see `-h`
for a full (up-to-date) explanation.

## Environment
Like the original `fcgiwrap`, this tool evaluates the following environment
variables set in the fcgi request:
- `DOCUMENT_ROOT`: directory which the script resides in
- `SCRIPT_NAME`: name of the actual executable
- `SCRIPT_FILENAME`: complete path to CGI script. When set, overrides
`DOCUMENT_ROOT` and `SCRIPT_NAME`

When `SCRIPT_FILENAME` is not set, the executable being executed will be
`DOCUMENT_ROOT/SCRIPT_NAME`.

## Testing
For adhoc testing, you can use
```bash
./fcgiwrap_go -s unix:./test -t 10
```
```bash
SCRIPT_FILENAME=$PWD/test.sh REQUEST_METHOD=GET SERVER_PROTOCOL=HTTP/1.1 cgi-fcgi -connect ./test $PWD/test.sh
```
