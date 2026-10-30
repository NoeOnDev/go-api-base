# Authentication

The API uses **sessions stored in PostgreSQL** and sent in an **`HttpOnly` cookie**. It does not use JWT.

It includes:
- registration, login and logout;
- profile and password change;
- password reset by email;
- email verification (optional);
- 2FA with an authenticator app and recovery codes (opt-in for each user);
- `user`/`admin` roles.

Each feature can be removed if a project does not need it (see [the end](#removing-features)).

## How sessions work

1. **Login:** `POST /auth/register` or `POST /auth/login` create a session. The API generates a random 256-bit token, stores only its SHA-256 in the database and sends the token in the `session` cookie. If that browser already had a session (for example, when switching accounts), it is closed; the ones on other devices stay open.
2. **Each request:** the browser sends the cookie automatically. `RequireUser` finds the session, checks that it has not expired (`SESSION_TTL`, 7 days by default) and puts the user in the context.
3. **Logout:** `POST /auth/logout` deletes the session from the database and the cookie from the browser.

The cookie has these attributes:

| Attribute | What for |
|---|---|
| `HttpOnly` | JavaScript cannot read it: an XSS attack cannot steal the session |
| `SameSite=Lax` | The browser does not send it in requests triggered by another site |
| `Secure` | It only travels over HTTPS. Controlled with `COOKIE_SECURE`, and on by default in production |
| `Path=/` | It applies to the whole API |
| No `Domain` | It only applies to the host that created it, not to its subdomains |
| `__Host-` prefix | Only in production (`COOKIE_SECURE=true`): the cookie is called `__Host-session`, and the browser rejects it unless it is `Secure`, with `Path=/` and without `Domain`. That way no subdomain can create or overwrite it. In development it is called `session` |

The 2FA challenge uses a cookie with the same attributes: `two_factor_challenge`, or `__Host-two_factor_challenge` in production. The frontend does not need to know these names, because the cookies are `HttpOnly` and travel on their own.

## Endpoints

| Method | Path | Access | Body | Response |
|---|---|---|---|---|
| `POST` | `/auth/register` | public | `{name, email, password}` | `201` with the user and the cookie; `409` if the email already exists; `422` |
| `POST` | `/auth/login` | public | `{email, password}` | `200` with the user and the cookie; `202 {two_factor_required: true}` if they have 2FA; `401` if the credentials do not match |
| `POST` | `/auth/logout` | public | | `204`, always (also if the session already expired) |
| `GET` | `/auth/me` | session | | `200` with the user |
| `PUT` | `/auth/me` | session | `{name, locale?}` | `200` with the user |
| `DELETE` | `/auth/me` | session | `{password}` | `204`, deletes the account; `422` if the password is wrong; `409` if it is the only admin |
| `PUT` | `/auth/email` | session | `{email, password}` | `202`, sends the link to the new email; `409` if it is in use; `422` |
| `POST` | `/auth/email/confirm` | public | `{token}` | `204`, changes the email; `422` if the link is not valid or expired; `409` if another account took the email |
| `GET` | `/auth/sessions` | session | | `200 {data: [{id, user_agent, ip, created_at, last_seen_at, current}]}` |
| `DELETE` | `/auth/sessions/{id}` | session | | `204`; `404` if it is not the user's; `422` if it is the current one |
| `POST` | `/auth/sessions/revoke-others` | session | `{password}` | `204`, closes all the others |
| `PUT` | `/auth/password` | user | `{current_password, password}` | `204`; `422` if the current password is wrong |
| `POST` | `/auth/password/forgot` | public | `{email}` | `202`, always |
| `POST` | `/auth/password/reset` | public | `{token, password}` | `204`; `422` if the link is not valid or expired |
| `POST` | `/auth/email/verify` | public | `{token}` | `204`; `422` if the link is not valid or expired |
| `POST` | `/auth/email/resend` | session | | `202`; `204` if the email was already verified |
| `POST` | `/auth/two-factor/challenge` | challenge cookie | `{code}` or `{recovery_code}` | `200` with the user and the session cookie; `422` if the code is wrong; `401` if the challenge expired; `503` if the secret cannot be decrypted |
| `POST` | `/auth/two-factor/setup` | user | | `200 {secret, otpauth_url}`; `409` if it is already on |
| `POST` | `/auth/two-factor/enable` | user | `{code, password}` | `200 {recovery_codes}`; `422` if the code or the password are wrong |
| `POST` | `/auth/two-factor/disable` | user | `{password, code}` or `{password, recovery_code}` | `204`; `422` if the password or the code are wrong; `409` if it is not on |
| `POST` | `/auth/two-factor/recovery-codes` | user | `{password}` | `200 {recovery_codes}`, new ones |
| `GET` | `/admin/users` | admin | `?q=&role=&status=&limit=&offset=` | `200 {data, meta}` (see [managing users](#managing-users)) |
| `POST` | `/admin/users` | admin | `{name, email, role, locale?, password?}` | `201` with the invited user; `409` if the email already exists; `422` (see [invitations](#invitations)) |
| `POST` | `/admin/users/{id}/invitation` | admin | | `204`, sends a new link; `409` if it was already accepted; `429` |
| `POST` | `/auth/invitation/accept` | public | `{token, password}` | `200` with the user and the cookie; `422` if the link is not valid or expired |
| `GET` | `/admin/users/{id}` | admin | | `200` with the user; `404` |
| `PUT` | `/admin/users/{id}/role` | admin | `{role, password}` | `200` with the user; `422` if the role or the admin's password are wrong; `409` |
| `POST` | `/admin/users/{id}/suspend`, `/unsuspend` | admin | | `200` with the user; `409` |
| `DELETE` | `/admin/users/{id}` | admin | `{password}` | `204`; `422` if the admin's password is wrong; `409` |

In the access column:
- **session:** any logged-in user, even without a verified email.
- **user:** also requires a verified email, if `REQUIRE_EMAIL_VERIFICATION=true`.

The user is returned like this (it never includes the password):

```json
{"id": 1, "name": "Alice", "email": "alice@example.com", "role": "user", "email_verified_at": null, "two_factor_enabled": false, "locale": "es-MX", "created_at": "..."}
```

**Rules:**
- Required name, up to 100 characters.
- Valid email, stored in lowercase.
- Password from 8 to 128 characters, and not one of the 10,000 most common ones (`validation.common_password`). The list compares without letter case, so `Password1` is rejected too.

## Profile and password change

- **`PUT /auth/me`** changes the name and, if present, the language (`locale`: one of the available ones, like `en` or `es-MX`). The language decides which language the emails and notifications reach the user in; registration stores the one the browser asked for (see [languages](i18n.md)). The email is changed separately (see "Changing the email"), because the new address must be verified.
- **`PUT /auth/password`** asks for the current password, so someone who finds the session open cannot change it. Changing it **closes every other session** of the user while the current one stays active, and **cancels a pending email change** (see "Changing the email").

## Sessions per device

Each session stores the browser (user agent), the IP it started from and its last activity, which is updated at most every 5 minutes so the database is not written on every request.

- **`GET /auth/sessions`** lists them, from the newest to the oldest, and marks the one of this device (`current`). The user agent arrives as is: to show it readably ("Firefox on Windows"), the frontend can use a library like `ua-parser-js`.
- **`DELETE /auth/sessions/{id}`** closes one, for example that of a device the user does not recognize. Its `GET /events` streams are cut at once. The current one is closed with `POST /auth/logout`.
- **`POST /auth/sessions/revoke-others`** with `{password}` closes all the others. It asks for the password, like Laravel Jetstream, because it is a bulk action: that way someone with a stolen session cannot kick the owner out of all their devices.

**Idle expiry (optional):** with `SESSION_IDLE_TIMEOUT` (for example `30m`), a session without activity for that long stops working, besides the fixed `SESSION_TTL` expiry. It must be at least 15 minutes, because activity is stored every 5. An open `GET /events` stream does not count as activity: a forgotten tab does not keep the session alive. Sessions expired by inactivity do not show up in `GET /auth/sessions` either.

## Changing the email

1. The frontend calls **`PUT /auth/email`** with `{email, password}`.
   - **It asks for the password:** otherwise, someone with a stolen session would change the email, then reset the password with it and keep the account.
   - If the email is already in use it responds `409`, like registration.
2. The API sends a link to **`FRONTEND_URL/confirm-email?token=…`** to the **new** address, which expires in 1 hour, and a **notice to the current one**, in case the owner did not make the request.
3. **The email does not change yet.** The frontend's `/confirm-email` screen takes the `token` and calls **`POST /auth/email/confirm`** with `{token}`. Only then does it change, and it is verified. It is public, like verification, so it works even if the link is opened on another device.

On confirmation the `email_changed` notification is sent. If another account took that email in the meantime, it responds `409` and nothing changes.

**What gets cancelled:**
- On confirmation, the pending password reset links, which were sent to the previous email: if the user changed it because they lost that mailbox, whoever has it cannot use them.
- On a password change or reset, the pending email change: it could have been requested by whoever knew the previous password, to keep the email and, from there, the account.

## Deleting the account

**`DELETE /auth/me`** with `{password}` deletes the account and all its data: sessions, tokens, recovery codes, notes and notifications, through `ON DELETE CASCADE`. Its streams are cut, the cookie is deleted and a last email is sent ("your account was deleted"), in case it was not the owner.

- **The only administrator account cannot be deleted** (`409`): another admin must be appointed first. That way nobody leaves the API without an administrator by mistake.
- **Every table of a project with user data must reference the user with `ON DELETE CASCADE`** (or `SET NULL`, if the data must stay without an owner, like an invoice). With `RESTRICT`, the deletion fails with `409` and nothing is deleted.
- The emails already in the queue for that user are sent anyway.

## Resetting the password

1. The user asks for the link: `POST /auth/password/forgot` with their email.
   - The API **always** responds `202` with the same message, whether the email exists or not, so as not to reveal which accounts exist.
   - If it exists, it sends an email with a link to **`FRONTEND_URL/reset-password?token=…`**, which expires in 1 hour and works only once.
2. The frontend's `/reset-password` screen takes the `token` from the URL, asks for the new password and calls `POST /auth/password/reset` with `{token, password}`.
3. The API changes the password, **closes every session** of the user and marks their email as verified (they proved they get emails at that address). The user logs in again.

Asking for a new link invalidates the previous one. If the password does not meet the rules, the token is not spent and the user can fix it.

## Verifying the email

With **`REQUIRE_EMAIL_VERIFICATION=true`**:

1. On registration, the user gets an email with a link to **`FRONTEND_URL/verify-email?token=…`**, which expires in 24 hours.
2. Until they open it, the protected routes (`RequireUser`, `RequireAdmin`) respond **`403` with the `email_not_verified` code**. The frontend should show a notice with a button to send the email again (`POST /auth/email/resend`).
3. The frontend's `/verify-email` screen takes the `token` and calls `POST /auth/email/verify` with `{token}`.

While unverified, the user can still use `/auth/me` (which has `email_verified_at: null`), edit their profile, log out and ask for another link. Admins created with `create-admin` are verified.

With the option set to `false` (the default) no email is sent on registration and nothing is blocked.

## 2FA (two-factor authentication)

Each user can turn it on if they want; administrators can be required to, with `REQUIRE_ADMIN_TWO_FACTOR=true` (see [roles](#roles)). It works with any authenticator app: Google Authenticator, Microsoft Authenticator, Authy, 1Password, Bitwarden, etc.

### Turning it on

1. The frontend calls `POST /auth/two-factor/setup` and gets `{secret, otpauth_url}`.
2. It shows `otpauth_url` as a **QR code** (in React, with `qrcode.react`: `<QRCodeSVG value={otpauth_url} />`) and `secret` as text, to type it by hand.
3. The user scans it with their app and types the 6-digit code that shows up.
4. The frontend calls `POST /auth/two-factor/enable` with `{code, password}`. If both are right, 2FA is on and the response has **10 recovery codes**.
5. The frontend shows the codes **only once**, with the option to copy or download them. They let the user in if they lose their phone.

Until it is confirmed with `enable`, 2FA is not on, so a badly scanned QR code does not lock anyone out. Turning it on closes the other sessions.

`enable` **asks for the password**, like turning it off: otherwise, someone with a stolen session would turn on 2FA with their own app and, as the other sessions close, lock the owner out (a password reset does not turn off 2FA).

### Logging in with 2FA

1. `POST /auth/login` with the right email and password responds **`202 {"two_factor_required": true}`** and an `HttpOnly` cookie with the challenge, which expires in 5 minutes. There is no session yet.
2. The frontend shows the code field and calls `POST /auth/two-factor/challenge` with:
   - `{"code": "123456"}`, the app's code; or
   - `{"recovery_code": "xxxxx-xxxxx"}`, a recovery code (accepted in uppercase or without the hyphen).
3. If it is right, it responds `200` with the user and creates the session, like a normal login.

A wrong code responds `422` and allows retrying. Each challenge allows **5 attempts**:
- On the fifth wrong code it responds **`401 two_factor_locked`**, deletes the challenge and sends the user a **warning email** with the date and the IP: whoever reached that step knows their password.
- If the challenge expired or no longer exists, it responds `401` and the password must be entered again.
- Besides, each user has **10 attempts in a row and then one every 6 minutes**, whatever IP they come from. Past that it responds `429` with `Retry-After`.

### Turning it off and regenerating codes

- **`POST /auth/two-factor/disable`** with the password and a second factor, `{password, code}` or `{password, recovery_code}`: turns off 2FA, deletes the recovery codes and closes the other sessions. With only the password, whoever had a stolen session and the password could turn it off and then log in without the phone. Whoever lost the phone uses a recovery code. The wrong codes count toward the same per-user limit as the login's second step.
- **`POST /auth/two-factor/recovery-codes`** with `{password}`: generates 10 new codes; the previous ones stop working.

### 2FA security

- **Standard TOTP codes** (RFC 6238): 6 digits, they change every 30 seconds and a clock drift of ±30 seconds is tolerated.
- **A code already used is rejected** even if it is still valid, so an intercepted code is useless for another login.
- **The secret is stored encrypted** with AES-256-GCM and the `ENCRYPTION_KEY` key, bound to the user. The recovery codes are stored as HMACs, never in plain text, and each one works once.
- **Brute force of the code:** 5 attempts per challenge and a per-user limit, independent of the IP (see above). With that, guessing a code stays around 0.07% per day.
- **A password reset does not skip 2FA:** the next login asks for the code again.
- **If a user loses their phone and the codes**, an admin can turn off their 2FA from the database:

  ```sql
  UPDATE users SET totp_secret = NULL, totp_enabled_at = NULL, totp_last_step = NULL WHERE email = '…';
  ```

**Be careful with `ENCRYPTION_KEY`:** it is required and must be kept like any production secret. It can be rotated without affecting anyone, and if it is lost, the second step of the login responds `503 two_factor_unavailable` with a clear message. All of this is in [docs/encryption.md](encryption.md).

## Emails

| Variable | What for |
|---|---|
| `MAIL_DRIVER` | `log`: writes the emails to the log, for development without a server. `smtp`: sends them |
| `MAIL_FROM` | Sender |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD` | Your provider's SMTP details (Resend, Brevo, Amazon SES, Mailgun or your hosting's mail), usually with port 587 |
| `FRONTEND_URL` | Base of the links in the emails |

**How they are sent:**
- **Through the job queue** (`jobs.MailSender`, see [docs/jobs.md](jobs.md)): the HTTP response does not wait for the mail server, and the email is stored encrypted in the database until it goes out.
- **If a sending fails**, it is retried up to 5 times, with a longer wait each time, and each error is logged. An SMTP outage or an API restart does not lose emails.
- **Encrypted connection:** with port 465, TLS is used from the start; with the others, STARTTLS, which is mandatory except against `localhost`.

**In development**, **Mailpit** is recommended, a fake SMTP server that shows the emails at `http://localhost:8025`:

```env
MAIL_DRIVER=smtp
SMTP_HOST=localhost
SMTP_PORT=1025
```

**Templates.** Each email goes out in HTML and in plain text, in the user's language. They are in `internal/auth/emails/<language>/` (`en` and `es-MX`), two files per email:

- `<name>.txt`: the plain-text version, for the clients without HTML (it also helps against spam filters). It defines the subject with `{{define "subject"}}`.
- `<name>.html`: the HTML version. It defines `preheader` (the line the inbox shows next to the subject) and `content`, written with plain `<h1>` and `<p>` tags, and uses the blocks `{{template "button" (button .Link "Label")}}` and `{{template "link_help" .Link}}`.

The HTML goes inside a shared layout, `internal/mail/layout/` (the "Basic" layout of [Postmark's templates](https://github.com/ActiveCampaign/postmark-templates), MIT license): the app name (`APP_NAME`) on top, linked to `FRONTEND_URL`, the white box with the content, and the footer. It works in the usual clients, Outlook on Windows included, and in dark mode. To adapt it to a project, change the colors in `layout.html` and `blocks.html`, and the styles of `<h1>` and `<p>` in `contentStyles` (`internal/mail/templates.go`): email clients ignore part of the `<style>` of the head, so every element carries its styles inline.

The user's data is escaped (`html/template`): a name like `<b>Alice</b>` shows up as text. To change the texts, edit both files of the email; to add a language, see [languages](i18n.md).

**Check how they look** in Mailpit: each email has the HTML and Text tabs, and **HTML Check** estimates how well each client supports it (with the data of [caniemail.com](https://www.caniemail.com)).

## Roles

- **`user`:** the role of whoever registers with `/auth/register`.
- **`admin`:** created with the `create-admin` command:

  ```bash
  make create-admin email=admin@client.com name="Administrator"
  ```

  In production, with the binary:

  ```bash
  ./api create-admin -email admin@client.com
  ```

  The password is asked for in the terminal without being shown, or taken from the `ADMIN_PASSWORD` variable.

In the routes, `RequireAdmin` responds `401` if there is no session and `403` if the user is not an admin. In a handler, check admin permissions with `user.IsAdmin()`, never by comparing `Role` (see below).

**Mandatory 2FA for administrators:** with `REQUIRE_ADMIN_TWO_FACTOR=true`, an admin acts as one only once they turn on 2FA. Until then:

- the admin routes respond `403 admin_two_factor_required`, so the frontend can send them to set it up;
- `user.IsAdmin()` is `false`, so a route with `OptionalUser` does not show them what only admins see (like inactive products);
- the account routes (`/auth/...`) work as usual: they can log in and turn on 2FA. Their `role` is still `admin`.

If they turn it off, they lose the admin permissions until they turn it on again. `create-admin` reminds it when the option is on. It is off by default; it is recommended in production (see "Before production" in the README).

## Managing users

The administrators manage the accounts from `/admin/users` (`internal/auth/admin_users.go`). Each user comes as the account (the same fields as `/auth/me`) plus `status` (`active`, `invited` or `suspended`) and `suspended_at`:

- **List:** `GET /admin/users`, from the newest, with `?q=` (searches the name and the email), `?role=user|admin` and `?status=active|invited|suspended`, paginated like the other lists, with `total`.
- **Invite:** `POST /admin/users` (see [invitations](#invitations)).
- **Change the role:** `PUT /admin/users/{id}/role` with `{role, password}`. It asks for the admin's password: with a stolen session, making another account an admin would leave the attacker with access even after that session is closed. It applies at once, and the user's real-time streams are cut so their topics are authorized again with the new role.
- **Suspend:** `POST /admin/users/{id}/suspend`. The account cannot log in (`403 account_suspended`, and only after the right password, so nobody learns it exists), its sessions and real-time streams are cut, and its pending links and two-factor challenge are deleted. "Forgot my password" sends it nothing. `POST /admin/users/{id}/unsuspend` undoes it.
- **Delete:** `DELETE /admin/users/{id}` with `{password}`, the same as when the user deletes their own account (`ON DELETE CASCADE`, `409 account_has_data`). It asks for the password because it cannot be undone: if in doubt, suspend.

**Protections**, with `409`:

- `own_account`: an admin does not change their own role, suspend or delete themselves from here; their account is managed from `/auth/me`.
- `last_admin`: the last **active** admin cannot be demoted, suspended or deleted. A suspended admin, or one who has not accepted their invitation, does not count.

Changing the role and deleting have the per-user limit of the account actions, because they check the password. The actions are logged with the admin's `user_id` and the `target_user_id`.

## Invitations

For systems without open registration (an internal tool, a client's team), an admin creates the account and the person chooses their password (`internal/auth/invitations.go`):

1. **The admin invites:** `POST /admin/users` with `{name, email, role}`, and optionally `locale` (the language of the email and of the account). Inviting another admin also asks for the admin's `password`, like changing a role. The user is created **without a password** (`status: "invited"`) and with the email not verified, and gets an email with a link to `FRONTEND_URL/accept-invitation?token=…`, valid for **7 days**.
2. **The person accepts:** the frontend reads the token from the URL and sends `POST /auth/invitation/accept` with `{token, password}`. The password follows the usual rules. The email is marked as verified (the link reached it), the language is the browser's if the admin did not choose one, and the session starts, as in the registration.

**To keep in mind:**

- **Until they accept, nobody can log in to the account:** the login answers the usual `401`, without revealing that the invitation is pending.
- **Sending it again:** `POST /admin/users/{id}/invitation` sends a new link, and the previous one stops working. It has the per-recipient email limit (`429 too_many_emails`), and answers `409 invitation_accepted` if the account is already active.
- **"Forgot my password" also works:** an invited user can set their password from there, which also activates the account. After that, the invitation link no longer works, so it cannot overwrite that password.
- **Withdrawing an invitation:** delete the user (`DELETE /admin/users/{id}`), or suspend them, which also deletes the link.
- **The email is reserved** from the invitation on: registering with it answers `409`.

With invitations, open registration can be removed (see [removing features](#removing-features)): the first admin is created with `create-admin`, and the rest of the accounts are invited.

## Built-in security

- **Passwords with argon2id** (19 MiB, 2 iterations, 1 thread, as OWASP recommends), with a random salt.
- **Common passwords rejected** on registration, password change and reset: the 10,000 most common ones of 8 or more characters, from real breaches (ASVS 6.2.4). The list is embedded in the binary (`internal/auth/passwords/common.txt`), so it needs no external call.
- **Login without leaks:** it always responds the same `401` and takes the same time whether the email exists or not. "Forgot my password" also always responds the same. Registration does say whether an email is in use (`409`), because otherwise the user would not know why it fails.
- **Single-use tokens** to reset the password and verify the email: only their SHA-256 is stored in the database, they expire, and they are validated and deleted in a single operation, so they cannot be used twice.
- **Sessions are closed on a password change or reset**, in case someone had logged in with the previous one.
- **A new password cuts half-finished 2FA logins and pending email changes:** changing or resetting it deletes the challenges and the email change link, which were obtained with the previous one. A reset also resets the user's 2FA attempt limit, in case someone with the old password had used it up.
- **Request limits per group of routes**, each one separately (the second step of 2FA does not spend the login attempts). Past them, the API responds `429` with `Retry-After`. The values are together in `internal/server/routes.go`, to adjust them:

  | Group | Routes | Per | Limit |
  |---|---|---|---|
  | General | every route except `/healthz` (also the ones outside `/auth`) | IP | 200 in a row, then 10 per second |
  | Login | `POST /auth/login` | IP | 20 in a row, then 1 every 3 s |
  | Registration | `POST /auth/register` | IP | 10 in a row, then 1 every 10 s |
  | 2FA | `POST /auth/two-factor/challenge` | IP | 20 in a row, then 1 every 3 s |
  | Tokens | `reset`, `email/verify` | IP | 10 in a row, then 1 every 6 s |
  | Emails | `forgot`, `resend` | IP | 10 in a row, then 1 per minute |
  | Account | changing the password and managing 2FA | **user** | 5 in a row, then 1 every 12 s |

  The IP ones are generous because many people can share an IP (an office, a university, mobile data). IPv6 addresses are counted per `/64` block. The account ones go per user, so users behind the same IP do not get mixed up.
- **Per-user limits**, even if the requests come from many IPs:
  - **Login:** 10 attempts in a row per email and then one per minute. It also counts emails that do not exist, so as not to reveal which are registered. A password reset resets it, although against an attack still in progress that is not enough (see [decisions](decisions.md#attempt-limits)).
  - **2FA code:** 10 attempts in a row and then one every 6 minutes.
  - **Emails to the same person:** 3 in a row of each kind (reset, verification, notice) and then one every 20 minutes. In `forgot`, past the limit nothing is sent and the response is the usual one, so as not to reveal whether the email exists; the last link sent still works. `resend` responds `429`.
- **Where they are counted:** the IP ones, in memory, in each instance of the API. The user, account and recipient ones (those in this list and the Account group) are in PostgreSQL (`internal/ratelimit`): they apply to every instance and are not reset on a deploy.
- **At most one argon2 hash per core at a time**, so many simultaneous logins do not exhaust the server's memory. The rest wait their turn.
- **Headers:** `X-Content-Type-Options: nosniff` on every response, `Cache-Control: no-store` on every `/auth` one and, in production with `COOKIE_SECURE=true`, `Strict-Transport-Security` (HSTS, one year) on every response.
- **2FA**, optional for each user and, with `REQUIRE_ADMIN_TWO_FACTOR=true`, mandatory for administrators (see the [2FA](#2fa-two-factor-authentication) section).
- **CSRF protection** (Go's `http.CrossOriginProtection`): it rejects with `403` the `POST`/`PUT`/`DELETE` requests the browser sends from another site. Requests without browser headers (`curl`, mobile apps) are not affected.
- **Cleanup:** an hourly job deletes the expired sessions and tokens, and the keys of the limits that already refilled (see [jobs](jobs.md)).

## From the frontend (React)

**Screens the frontend needs:**
- login and registration;
- profile (name and password change);
- "forgot my password";
- `/reset-password`, the one of the email link;
- `/verify-email`, the one of the email link, only with verification on;
- a "confirm your email" notice when a response has `email_not_verified`;
- the second step of the login (the 2FA code) when the login responds `202`. If that step responds `401` (for example, `two_factor_locked`), go back to the login screen;
- in the profile, turning 2FA on (QR code, code and recovery codes) and off;
- in the profile, the sessions per device (with "close" and "close all the others"), changing the email and deleting the account;
- `/confirm-email`, the one of the email link to confirm a new email.

### Same domain (recommended)

If the frontend and the API are served under the same domain, for example `client.com` and `client.com/api`, CORS is not needed and the cookie works without configuring anything:

```js
const res = await fetch("/api/auth/login", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ email, password }),
});
```

### Different domains

If the frontend and the API are on different domains, for example `app.client.com` and `api.client.com`:

1. Set the frontend's origin in the API:

   ```env
   CORS_ALLOWED_ORIGINS=https://app.client.com
   FRONTEND_URL=https://app.client.com
   ```

2. Send the cookie in each `fetch` with `credentials: "include"`:

   ```js
   const res = await fetch("https://api.client.com/auth/me", { credentials: "include" });
   if (res.status === 401) {
     // no session: redirect to the login
   }
   ```

With `SameSite=Lax`, the browser sends the cookie between **subdomains of the same site** (`app.client.com` → `api.client.com`). If the frontend were on a completely different site (`client-app.com` → `api.other.com`), `SameSite=None` would be needed. I don't recommend it: it is better to put both under the same domain.

### In development

If Vite runs on `http://localhost:5173` and the API on `http://localhost:8080`, the site is the same (`localhost`), so the cookie travels. This is enough:

```env
CORS_ALLOWED_ORIGINS=http://localhost:5173
FRONTEND_URL=http://localhost:5173
```

`COOKIE_SECURE` is already `false` in development, so the cookie works without HTTPS.

## Protecting new routes

In each resource's `routes.go`:

```go
mux.Handle("GET /orders", authn.RequireUser(httpx.Handle(h.list)))            // any user (with a verified email, if required)
mux.Handle("DELETE /orders/{id}", authn.RequireAdmin(httpx.Handle(h.delete))) // admin only
mux.Handle("GET /products", httpx.Handle(h.list))                             // public
mux.Handle("GET /products", authn.OptionalUser(httpx.Handle(h.list)))         // public, but knows whether there is a user
```

Inside the handler:

```go
user := auth.MustUser(r.Context())
if user.IsAdmin() { … } // admin permissions: never compare user.Role
```

## Removing features

All the routes are in `internal/auth/routes.go`. To remove a feature, delete its routes, its files and its tests, and make the changes in the last column. What several features share is in files that are not deleted: `account.go` (including deleting a user, which `DELETE /auth/me` and the admins use), `session.go`, `tokens.go` and `emails.go`, and in the tests, `helpers_test.go`. The email templates are in each language folder (`emails/en/`, `emails/es-MX/`): delete them from all of them.

| Feature | Routes | Files to delete | Also |
|---|---|---|---|
| Open registration (only invited users and admins created with `create-admin`) | `POST /auth/register` | The `register` function of `handlers.go`, and the `TestRegister…` tests of `handlers_test.go` | Several tests create the user with `POST /auth/register`: move them to `testutil.Login` |
| Profile and password change | `PUT /auth/me`, `PUT /auth/password` | `profile.go`, `account_test.go` | |
| Sessions per device | `/auth/sessions…` | `sessions.go`, `sessions_test.go` | |
| Changing the email | `PUT /auth/email`, `/auth/email/confirm` | `email_change.go`, `email_change_test.go`, `emails/*/email_change*.*` | |
| Deleting the account | `DELETE /auth/me` | `delete_account.go`, `delete_account_test.go`, `emails/*/account_deleted.*` | |
| Resetting the password | `/auth/password/forgot`, `/auth/password/reset` | `password_reset.go`, `password_reset_test.go`, `emails/*/password_reset.*` | |
| Verifying the email | `/auth/email/verify`, `/auth/email/resend` | `email_verification.go`, `email_verification_test.go`, `emails/*/verify_email.*` | In `register` (`handlers.go`), the `if s.opts.RequireVerifiedEmail` block. Leave `REQUIRE_EMAIL_VERIFICATION=false` |
| Invitations | `POST /admin/users`, `/admin/users/{id}/invitation`, `/auth/invitation/accept` | `invitations.go`, `invitations_test.go`, `emails/*/invitation.*` | Optionally, the `invitation` purpose (`tokens.go` and the CHECK of migration `00006`) and the `SetInitialPassword` query (then, `make sqlc`). `password_hash` can stay nullable: a user without a password cannot log in |
| Managing users | `/admin/users…` | `admin_users.go`, `admin_users_test.go` | Optionally, the suspension: the `SuspendedAt` checks of `Authenticate` (`auth.go`) and `forgotPassword` (`password_reset.go`), the `suspended_at` column (migration `00001`, `users.sql` and the `GetSessionUser` filter of `sessions.sql`) and the user queries of the list, the role and the suspension (then, `make sqlc`) |
| 2FA | `/auth/two-factor/…` | `two_factor.go`, `two_factor_login.go`, `recovery_codes.go`, `two_factor*_test.go`, `emails/*/two_factor_locked.*` | In `handlers.go`, the login's `if u.TwoFactorEnabled` block; in `auth.go`, `twoFactorLimiter`; in `password_reset.go`, its `resetLimit`; in `keys.go`, the call to `rotateTwoFactorKeys`. Optionally, also `internal/totp`, `testutil/twofactor.go` (with `TestTwoFactorChangesNotify` of `internal/notifications`) and migration `00007` with `recovery_codes.sql` (then, `make sqlc`) |

Finally, `make check`. If a test of another feature fails, it is one that used the feature you removed: to set up the case, or to test how the two combine (for example, that changing the password cancels a pending email change). Delete that case, or move its setup to `testutil.Login`.

Each recipe was tested separately, on a copy of the project: it compiles, passes `go vet` and staticcheck, and the failing tests are only the ones above. To remove several features, follow the steps of each one; if something is left unused, staticcheck points it out. If you change auth, keep this table up to date.

## What it does not include

- Mandatory 2FA for every user (for administrators, see `REQUIRE_ADMIN_TWO_FACTOR` in [roles](#roles)).
- Physical security keys or passkeys (WebAuthn).
- Login with Google or other providers (OAuth).
- Permissions finer than `user`/`admin`.
- Checking passwords against breaches (Have I Been Pwned), which needs an external call. The 10,000 most common ones are rejected.
- A persistent audit log: the security events go to the logs and to the user's notifications.
