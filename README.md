OpenStack shell creds helper
===========================
This is a tool to make managing shell credentials easier, to be used in
combination with included shell function scripts (and completion files) for
bash and fish.

It is written in Go and supports the following Keystone authentication types:
* Password (scoped and unscoped)
* Password + TOTP
* Application Credential

It can also load arbitrary `export` lines from other pass entries (e.g. AWS,
Kubernetes, Ansible), configured via an optional config file.

The tool scans your password store directory for credentials and displays them
in a list for you to choose. The list is powered by the `fzf` tool, which is
natively included in the binary. This allows powerful auto-complete
functionality and should make it super quick to get the credentials you need
loaded fast.

For OpenStack openrc files that don't specify a project, it will request a
list of projects you're a member of from Keystone and allow you to choose,
saving you from duplicating credentials if you're a member of lots of projects.

This tool also has preliminary support for TOTP, so for accounts that have a
registered TOTP secret, it can prompt for your 6-digit TOTP code (e.g.
Google Authenticator, Yubikey OATH) before requesting a token from Keystone.

After loading OpenStack credentials and making a request to Keystone, the tool
will set environment variables for you to make subsequent OpenStack API calls
with the token auth method. For other credential types, all `export` lines from
the pass entry are sourced directly into your shell.


Demo
----
<p align="center"><img width="800" src="chcreds.svg"></p>


Use
---
The shell function scripts in this repository provide the following commands:

  * `chcreds` to select and load credentials in the current environment
  * `recred` to reload all currently loaded credentials, which is useful if a token expires
  * `rmcreds` to remove loaded credentials (shows fzf selector if multiple types are loaded)
  * `prcreds` to print the current credentials (masks passwords/tokens/secrets)


How it works
-------------
The `chcreds` function will call out to the `oscreds` binary to present the list
of credentials available.

Once a credential is chosen, `oscreds` will call out to `pass` to actually
decrypt and return the contents to `oscreds`.

For OpenStack openrc entries, `oscreds` will then interpret the credentials and
make subsequent API calls to Keystone to eventually return an OpenStack token.

For other credential types (e.g. AWS, Kubernetes), `oscreds` will output the
`export` lines from the pass entry directly, which are then sourced into your
shell.

Multiple credential types can be loaded simultaneously — for example, you can
have both OpenStack and AWS credentials active at the same time.

The shell function scripts will load the appropriate environment variables for
your tools to work (almost) seamlessly.


Loading multiple credential types
----------------------------------
By default, only OpenStack `.openrc` entries in your pass store are found. To
load credentials for other tools, create a config file at
`~/.config/oscreds/config.json`:

``` json
{
  "extra_creds": [
    {
      "name": "aws",
      "extension": ".awsrc",
      "prefix": "AWS"
    },
    {
      "name": "k8s",
      "extension": ".k8src",
      "prefix": "KUBE"
    }
  ]
}
```

Each entry defines:

* `name` — a label used in the fzf selector and tracking (e.g. `aws`)
* `extension` — the pass file suffix to scan for (e.g. `.awsrc` matches files
  like `my-profile.awsrc.gpg` in your password store)
* `prefix` — the environment variable prefix, used by `rmcreds` and `prcreds`
  to know which variables to unset or display (e.g. `AWS` unsets all `AWS_*`
  variables)

With this config, `chcreds` will scan for both `*.openrc.gpg` and
`*.awsrc.gpg` files and present them all in a single fzf selector. Entries are
displayed with their type as a prefix (e.g. `aws: my-profile`,
`openrc: production/cloud`).

When you select a non-openrc entry, `oscreds` decrypts it via `pass` and
outputs all `export` lines verbatim — no API calls are made.

You can load credentials of different types simultaneously. Loading one type
does not affect credentials of another type that are already loaded.

### Adding raw credential entries to pass

Add your credential files into pass with the appropriate extension:

``` sh
    pass insert -m aws/my-profile.awsrc
```

The file contents should be `export` lines, for example:

``` sh
    export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE
    export AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
    export AWS_DEFAULT_REGION=us-east-1
```

### Removing credentials

`rmcreds` will remove loaded credentials. If only one type is loaded, it will
be removed immediately. If multiple types are loaded (e.g. both OpenStack and
AWS), an fzf selector will appear letting you choose which to remove.

`rmcreds` unsets all environment variables matching the type's prefix (e.g.
all `AWS_*` variables for an AWS credential).

### Re-reading credentials

`recred` will reload all currently loaded credentials by re-calling `chcreds`
for each entry tracked in `__OSCREDS_LOADED`.


Prompt integration
------------------
Optionally, you can display your currently loaded credentials in your prompt:

**Bash** — add `${OS_CRED:+ \[$OS_CRED\]}` to your `PS1` var. For example (coloured):

```
    PS1='\[\033[01;32m\]\u@\h\[\033[01;34m\] \w\[\033[01;33m\]${OS_CRED:+ \[$OS_CRED\]}\[\033[00m\] \$ '
```

**Fish** — add the following to your `fish_prompt` function:

```
    if set -q OS_CRED
        printf ' [%s]' $OS_CRED
    end
```

**Starship** — add the following to your `~/.config/starship.toml`:

```toml
[custom.oscreds]
command = 'echo $OS_CRED'
when = 'test -n "$OS_CRED"'
symbol = '☁️ '
style = 'bg:color_bg3'
format = '[[ $symbol( $output) ](fg:#83a598 bg:color_bg3)]($style)'

[openstack]
disabled = true
```

The `[openstack]` section disables the built-in OpenStack module (which uses
`OS_CLOUD`/`clouds.yaml` and won't detect `OS_CRED`).

Then insert `${custom.oscreds}` into your `format` string. For the Gruvbox
Rainbow preset, place it between `$pixi` and the `color_bg3` → `color_bg1`
powerline transition:

```diff
 $pixi\
+${custom.oscreds}\
 [](fg:color_bg3 bg:color_bg1)\
```


Using token auth
----------------
Using a Keystone token auth directly seems to works well with:
* OpenStack client
* OpenStack APIs

Some known exceptions are documented below:

### Swiftclient

The swiftclient doesn't work directly, but can work with a token by specifying
`--os-auth-token` and `--os-storage-url` directly, where the storage URL is
found from the OpenStack catalog.

```
OS_STORAGE_URL=$(openstack catalog show object-store -f json | jq -r '.endpoints[] | select(.interface=="public" and .region=="Melbourne") | .url')
swift --os-auth-token $OS_TOKEN --os-storage-url $OS_STORAGE_URL
```

Installation
------------
You can grab the latest build from the GitHub project releases page, or see
below for instructions on building it yourself.

Once you have the `oscreds` binary, put it somewhere in your path
(e.g. `~/.local/bin`)

``` sh
    mkdir -p ~/.local/bin
    cp oscreds ~/.local/bin/
```

### Bash

Grab a copy of the `bash-functions` file from this repo
and drop it into your `.bashrc.d` (or similar) or source it from your `.bashrc`
to load automatically in your shell.

### Fish

Source the `fish-functions` file from your `~/.config/fish/config.fish`:

``` sh
    source /path/to/fish-functions
```

Or copy the individual functions into `~/.config/fish/functions/` as
autoloaded `.fish` files (e.g. `~/.config/fish/functions/chcreds.fish`).

Adding OpenStack credentials
-----------------------------
Add your OpenStack openrc credentials files into pass, ensuring they have a
.openrc extension for oscreds to find them.

``` sh
    pass insert -m my-password.openrc
```

You can then arrange the files in your password store in a way that is
appropriate for your use.

Credential examples
-------------------

Standard password auth
``` sh
    export OS_AUTH_URL=https://keystone.domain.name/
    export OS_PROJECT_NAME=myproject
    export OS_USERNAME=username
    export OS_PASSWORD=password
```

Application credential
``` sh
    export OS_AUTH_URL=https://keystone.domain.name/
    export OS_AUTH_TYPE=v3applicationcredential
    export OS_APPLICATION_CREDENTIAL_ID=app_cred_id
    export OS_APPLICATION_CREDENTIAL_SECRET=app_cred_secret
```

You can also omit any `OS_PROJECT_NAME` or `OS_PROJECT_ID` to optionally
request a list of projects that you have roles assigned to choose from.

``` sh
    export OS_AUTH_URL=https://keystone.domain.name/
    export OS_USERNAME=username
    export OS_PASSWORD=password
```

To enable TOTP functionality (if password + TOTP is enabled for identity)
then you need to append `OS_TOTP_REQUIRED=true` to your openrc to trigger
the TOTP prompt.

``` sh
    export OS_AUTH_URL=https://keystone.domain.name/
    export OS_PROJECT_NAME=myproject
    export OS_PROJECT_ID=1234567890abcdef
    export OS_USERNAME=username
    export OS_PASSWORD=password
    export OS_USER_DOMAIN_NAME=Default
    export OS_PROJECT_DOMAIN_NAME=Default
    export OS_TOTP_REQUIRED=true
```

Shell completion
----------------
Completion scripts for both bash and fish are included.

### Bash

To install it for your user, the following should work:

``` sh
    mkdir -p ~/.local/share/bash-completion/completions
    cp bash-completion ~/.local/share/bash-completion/completions/chcreds
```

### Fish

To install it for your user, copy the completion file to your fish completions directory:

``` sh
    mkdir -p ~/.config/fish/completions
    cp fish-completion ~/.config/fish/completions/chcreds.fish
```

You can then use tab completion to complete the filename of the credentials file.

Building
--------
A simple `go build` should suffice to compile the binary.
