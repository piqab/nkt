---
title: Access and security
---

# Users, host accounts, security

## nkt users

![Users](/screens/en/users.png)

nkt's own accounts: `admin` and `viewer` (read-only) roles, creation,
disabling, password change, session reset. You cannot demote or delete
your own account — so access is not lost by accident. Logging in with a
host system account (PAM) is possible too.

From the command line:

```bash
sudo nkt passwd                      # change the admin password
sudo nkt passwd ops -role viewer     # a read-only account
sudo nkt passwd -random              # generate a password
sudo nkt users                       # who exists and who logged in when
```

`nkt passwd` writes straight to the database — that is also the cure for
“forgot the password”.

## Host accounts

System users with shell, home, groups and sudo. Creating a user with an
SSH key, granting passwordless sudo, deletion.

## Security

- Passwords — argon2id; sessions — random revocable tokens; `HttpOnly`,
  `SameSite=Lax` cookies; brute-force protection.
- Listens on `127.0.0.1` only; `NKT_COOKIE_SECURE=true` by default — the
  browser will not accept the cookie without HTTPS.
- `NKT_ALLOW_MUTATIONS=false` — read-only mode for the whole application.
- Runs under systemd in a sandbox (`ProtectSystem=strict`) with a
  controlled escape only for commands that need system access; sandbox
  diagnostics are in the UI.
- Every change is in the [audit log](/en/guide/monitoring#audit-log).

::: danger
Access to nkt equals root on the host. Do not expose it to the internet
without a separate authentication layer.
:::
