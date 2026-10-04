package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "server":
		execServer(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		separator := -1
		for i, arg := range os.Args {
			if arg == "--" {
				separator = i
				break
			}
		}
		if separator < 0 || separator+1 >= len(os.Args) {
			fmt.Fprintln(os.Stderr, "missing command separator or command")
			usage()
		}
		execClient(os.Args[1:separator], os.Args[separator+1:])
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `
Execute a local Windows command as another user by providing their username and password.

This requires Administration privileges.

Usage:

	%[1]s --username USERNAME --password PASSWORD [--stdin] [--env NAME[=VALUE]]... [--workdir DIR] [--debug] [--log PATH] [--server-log PATH] -- command [args...]

Examples:

	%[1]s --username vagrant@example.test --password vagrant -- whoami -all
	%[1]s --username vagrant@example.test --password vagrant -- klist
	%[1]s --username vagrant@example.test --password vagrant -- pwsh -command dir env:
	%[1]s --username vagrant@example.test --password vagrant -- reg query HKCU\Environment

Parameters:

--username USERNAME

	the value can be set with the EXECST_USERNAME environment variable.

	it supports the following syntaxes:

		User Principal Name (UPN):   <username>@<dns-domain>      (e.g. vagrant@example.test)
		Down-level Logon Name (DLN): <netbios-domain>\\<username> (e.g. EXAMPLE\\vagrant)

--password PASSWORD

	the value can be set with the EXECST_PASSWORD environment variable.

--env NAME[=VALUE]

	sets an environment variable for the command. This flag can be repeated.

 	without a VALUE, it copies the current environment variable value.

--stdin

	stdin is attached to the executed command.

--debug

	the scheduled-task path is skipped entirely: the client spawns
	the server subprocess directly (as the current user) so the SSH-over-pipe
	flow can be exercised without schtasks, credentials, or a target account.

--log PATH

	writes the client's own log to PATH (default: stderr).

--server-log PATH

	writes the server's log to PATH.

	when --server-log is omitted, the server does not log anything.

`, filepath.Base(os.Args[0]))
	os.Exit(2)
}
