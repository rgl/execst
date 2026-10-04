# About

[![Build](https://github.com/rgl/execst/actions/workflows/build.yml/badge.svg)](https://github.com/rgl/execst/actions/workflows/build.yml)

Execute a local Windows command as another user by providing their username and password.

The command is executed via an ephemeral Scheduled Task.

**This requires administrative privileges.**

**The target user must have the `Log on as a batch job` user right.** You can use the [Carbon.Security Grant-CPrivilege cmdlet](https://github.com/webmd-health-services/Carbon.Security/blob/main/Carbon.Security/Functions/Get-CPrivilege.ps1) as, e.g., `Grant-CPrivilege -Identity $env:EXECST_USERNAME -Privilege SeBatchLogonRight`.

## Usage

In a Windows machine, open a `pwsh` session, download a [release](https://github.com/rgl/execst/releases), extract, and execute `execst`:

```pwsh
curl.exe -sL https://github.com/rgl/execst/releases/download/v0.0.1/execst_0.0.1_windows_amd64v3.tar.gz | tar.exe xzf -

.\execst.exe `
    --username vagrant@example.test `
    --password vagrant `
    -- `
    whoami.exe -all

.\execst.exe `
    --username vagrant@example.test `
    --password vagrant `
    -- `
    klist.exe

$env:EXECST_USERNAME = 'vagrant@example.test'
$env:EXECST_PASSWORD = 'vagrant'
$env:EXAMPLE = 'example value'
.\execst.exe `
    --env EXAMPLE `
    --env EXAMPLE2=2 `
    --workdir c:\ `
    -- `
    pwsh.exe `
    -command "dir env:"
```
