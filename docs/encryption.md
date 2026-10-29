# Encryption and `ENCRYPTION_KEY`

## What it protects

`ENCRYPTION_KEY` is a 32-byte key. It is used only for what **must be readable later** or **compared without storing the original value**:

| Data | How it is stored | Why |
|---|---|---|
| 2FA secret | Encrypted with AES-256-GCM | The API needs to read it to check the codes |
| 2FA recovery codes | HMAC-SHA256 | They are only compared; without the key, they cannot be brute-forced |

Both are **bound to their user**: the secret is encrypted with the user's id as associated data (`users.totp_secret:<id>`), and the HMAC is computed over the code and the id. If someone with write access to the database copies their own secret or codes to another user's row, they are useless to log in as that user.

Everything else **does not depend on this key**, on purpose:

| Data | How it is stored | Why it needs no key |
|---|---|---|
| Session cookie | Random 256-bit token; in the database, its SHA-256 | It holds no data, so there is nothing to encrypt. If the database leaks, the hashes are useless to log in |
| Reset and verification tokens | Random, 256 bits; in the database, their SHA-256 | With 2²⁵⁶ options, a fast hash is enough |
| Passwords | argon2id, with a random salt | The protection is that the algorithm is slow, not a key |

That is why **changing the key logs nobody out** and only affects users with 2FA.

## Compared with Laravel's `APP_KEY`

Laravel uses a single key for everything: encrypting the cookies (including the session one), `Crypt::encrypt()`, signed URLs and Fortify's 2FA. Here the key protects only 2FA, so the damage of a leak is smaller:

| The attacker gets… | Laravel (sessions in the database) | This template |
|---|---|---|
| Only a database backup | Logs in as any user with an open session (the IDs are in plain text) | Nothing directly; they can only try to crack passwords, and argon2id makes it very costly |
| Only the key | Decrypts cookies and signs URLs (for example, verifies any email) | Nothing: the key alone gives access to no data |
| The database and the key | Reads the 2FA secrets and every recovery code | Reads the 2FA secrets; the recovery codes need brute force. They still lack each user's password |

## Generating and storing the key

```bash
make key
```

In production, with the binary: `./api generate-key`.

It prints a new key (32 random bytes in base64). Copy it into `ENCRYPTION_KEY`. Unlike `php artisan key:generate`, it does not modify any file.

**Keep it like any production secret:**
- in the server's `.env`, with 600 permissions, or in the secrets manager you use;
- **never** in the repository;
- **with a backup apart from the database's:** if it is lost, the 2FA secrets cannot be recovered.

## Rotating the key

Rotate it if it may have leaked (a compromised server, someone with access who left) or as a periodic practice.

Each encrypted value carries the identifier of its key (`52bf1807:…`), so the API knows which one decrypts it. During the rotation, the new key and the previous one coexist.

1. **Generate the new key:** `make key`.
2. **Set both keys and restart the API:**

   ```env
   ENCRYPTION_KEY=<new key>
   ENCRYPTION_PREVIOUS_KEYS=<previous key>
   ```

   From then on, new values are encrypted with the new key, and old ones are still read with the previous one. Each time a user uses their 2FA, their secret is encrypted again with the new key.
3. **Run `make rotate-keys`** (or `./api rotate-keys` on the server). It encrypts again every 2FA secret that is left and reports:
   - the secrets that could not be decrypted;
   - how many users have **recovery codes** of the previous key;
   - how many **queued emails** encrypted with the previous key have not gone out yet;
   - which previous keys can already be removed.

   If something still depends on the previous key, the command ends with an error.
4. **Recovery codes:** they cannot be recomputed without the original code (that is why they are safe). The users the report lists have to **regenerate them from their profile** (`POST /auth/two-factor/recovery-codes`). Meanwhile, their old codes keep working.
5. **When the report says "Done"**, remove the key from `ENCRYPTION_PREVIOUS_KEYS` and restart. "Done" includes that no queued emails encrypted with it are left: an email encrypted with the key you remove can no longer be sent (it is cancelled). They usually go out in seconds; if SMTP was down, one may be retrying. In that case, wait and run `make rotate-keys` again.

If you cannot wait for everyone to regenerate their codes (for example, because the key leaked), you can remove it anyway. Those users lose their old recovery codes, but **their authenticator app keeps working**, because their secret is already under the new key. They can generate new codes from their profile.

`ENCRYPTION_PREVIOUS_KEYS` accepts several keys separated by commas, in case two rotations overlap.

## If the key is lost

Users with 2FA cannot finish the login: the second step responds `503` with the `two_factor_unavailable` code and a message that asks them to contact the administrator. The log records the cause and the identifier of the missing key.

If the key does not turn up, 2FA has to be turned off for those users, so they set it up again:

```sql
UPDATE users SET totp_secret = NULL, totp_enabled_at = NULL, totp_last_step = NULL WHERE totp_secret IS NOT NULL;
DELETE FROM user_recovery_codes;
```

For a single user, add `AND email = '…'` to the `UPDATE` and `WHERE user_id = …` to the `DELETE`.

Sessions, passwords and the rest of the data are not affected.
