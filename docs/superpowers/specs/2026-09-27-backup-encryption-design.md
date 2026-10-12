# Thyme & Place - Backup encryption and retention protection - Design

- **Status:** Accepted
- **Date:** 2026-09-27
- **Source:** Architectural audit, 2026-09-27 (finding F12)
- **Related issues:** [#12](https://github.com/Muddl/thymeandplace/issues/12) (F12)
- **Amends:** core spec §14 (Backups: encrypted before upload; bucket-side protection against deletion), §16 (new row: backup encryption)

---

## 1. Problem

Nightly backups go to the bucket as plaintext database dumps. The same credentials that write them can also delete them.

- **Unencrypted upload.** `deploy/backup/backup.sh:12-13` runs `pg_dump --format=custom` to `/tmp` and checks the file isn't empty. `:15-18` uploads the file unchanged with `mc cp` to `backups/daily/<date>.dump`, and on Sundays to `backups/weekly/<week>.dump`.
- **What a dump contains:**
  - `admin_backup`: the Argon2id password hash, and the TOTP secret sealed with `ADMIN_SECRET_KEY`. The key isn't in the dump.
  - `admin_passkey` public keys and `admin_session` token hashes.
  - `admin_login_log` with IP addresses (core spec §15).
  - The whole catalog and its history.
- **Who can read it.** Anyone with bucket read access. That includes a leak of the bucket keys, which sit in the one env file every container gets today (see the least-privilege design, `2026-09-27-least-privilege-deployment-design.md`, "S2"). It also includes a compromise at the storage provider.
- **Who can delete it.** The same keys: `backup.sh:24-29` prunes with `mc rm --recursive --force --older-than`. Someone who compromises the host can wipe every backup along with the live database.
- **Restore paths.** `deploy/backup/restore.sh` and `restore-test.sh` download and run `pg_restore` directly (`restore.sh:48-55`, `restore-test.sh:15`). `docs/operations.md` documents both, including a verified local recipe.

## 2. Goals and non-goals

Goals:

1. Every dump is encrypted before it leaves the host. The bucket only ever holds ciphertext.
2. Full disaster recovery needs a key that never lives on the VPS.
3. The monthly restore test keeps working unattended. The observability design automates it.
4. A host compromise can't delete backup history inside the retention window, where the storage provider supports it.
5. The "fail loudly" behaviour stays: an empty or unreadable dump never gets uploaded as a success.

Non-goals:

- Encrypting the raw reference datasets in the bucket. They are public data.
- Encrypting the live database volume (disk encryption is a VPS-level choice).
- Changing retention: 7 daily and 4 weekly copies, core spec §14.

## 3. Decision log

| Decision | Chosen | Rejected and why |
|---|---|---|
| Tool | `age`, encrypting to X25519 public recipients | GPG (heavier key management for the same result). Provider server-side encryption alone (the provider and anyone with the bucket keys can still read) |
| Recipients | Two. A **recovery** key held off-host (password manager plus paper), and a **restore-test** key on the VPS, root-only, mounted only into the `restore` service | Recovery key only (the monthly restore test would need the owner to supply a key each time). On-host key only (a host compromise would then decrypt all history, and there'd be no recovery if the VPS is lost) |
| Order of operations | Dump to a file, verify it (non-empty and `pg_restore --list`), encrypt to `.age`, delete the plaintext, upload | Stream `pg_dump \| age` (`set -o pipefail` in dash wasn't verified, and the dump couldn't be checked before upload) |
| Installing `age` | Debian's `age` package in the backup image, which is `postgres:17-bookworm`. Fall back to a pinned GitHub release tarball with its checksum, like `mc`, if the package is missing | An unpinned download (supply-chain risk, see the supply-chain design) |
| Deletion protection | Bucket versioning with noncurrent versions kept 35 days, and where supported, a backup-writer key without delete permission plus lifecycle expiry in place of `mc rm` | Object lock in compliance mode (provider-specific, and hard to undo if misconfigured). Status quo (host compromise wipes history) |
| Legacy plaintext dumps | Let them age out under the existing prune (7 and 28 days). Restores accept them until then | Re-encrypting them (needs a download and re-upload, for copies that disappear within a month anyway) |

## 4. Design

### 4.1 Keys

- **Recovery key.** The owner runs `age-keygen -o recovery.key` on their own machine. The private key goes into the password manager and onto a paper copy. It never goes to the VPS.
- **Restore-test key.** It is generated on the VPS into `/etc/thymeandplace/restore-test.agekey`, mode 600, root-owned.
- Both public keys go into `BACKUP_AGE_RECIPIENTS` in the backup service's env file, separated by spaces. That is `backup.env` under S2.

The threat model, in `docs/operations.md`:
- **Bucket-only compromise** (a key leak or a provider incident) reveals nothing.
- *(Amended in the branch review:* bucket **write** access lets an attacker plant or replace objects, and a restore runs the dump's SQL as the superuser. `age` authenticates against the key holder only, so anyone with a recipient's public key can make a `.dump.age` that decrypts. The public keys are therefore kept confidential, `restore.sh` refuses plaintext keys unless `RESTORE_ALLOW_PLAINTEXT=1`, and `restore-test.sh` picks only today's or yesterday's exact `<date>.dump.age` name. See §4.3 and `docs/operations.md`, "Threat model".)
- **Host compromise** reveals the backup history through the restore-test key. The live database is already exposed in that case, so the extra loss is older rows and deleted history. The recovery key and the provider-side protection in §4.4 still preserve the ability to restore.

### 4.2 `backup.sh`

The changes go on top of the observability design's `EXIT` trap and step names (`2026-09-27-observability-and-alerting-design.md` §4.4):

1. Check that `BACKUP_AGE_RECIPIENTS` is set and has one or more `age1…` entries. If not, `exit 2` and name the variable.
2. `step=dump`: run `pg_dump` to `$file` as today.
3. `step=verify`: the file is non-empty and `pg_restore --list "$file"` succeeds. This is the same check `restore.sh:51` makes, moved earlier so a bad dump never gets uploaded.
4. `step=encrypt`: run `age -e -r <each recipient> -o "$file.age" "$file"`, then `rm -f "$file"`.
5. `step=upload`: upload `$file.age` to `backups/daily/<date>.dump.age`, and on Sundays to `backups/weekly/<week>.dump.age`.
6. `step=prune`: unchanged, unless `BACKUP_PRUNE=lifecycle` (§4.4), which skips the step.

The trap removes both `$file` and `$file.age`.

### 4.3 `restore.sh` and `restore-test.sh`

- `restore.sh <key> [target]`: if the key ends in `.age`, it needs `AGE_IDENTITY`, a path to an identity file. It then runs `age -d -i "$AGE_IDENTITY" -o "$tmp.dump" "$tmp"` before the existing checks. Without an identity, it refuses before downloading: "encrypted backup; set AGE_IDENTITY to your recovery or restore-test key". A decryption failure refuses before the database is touched (`age` authenticates its ciphertext). A key without `.age` is restored as today, which covers legacy dumps. *(Amended in the branch review:* only with `RESTORE_ALLOW_PLAINTEXT=1`; without it a plaintext key is refused before downloading, since nothing shows who wrote it.)
- `restore-test.sh`: takes the newest file in `backups/daily/`, `.dump` or `.dump.age`, as today, and passes `AGE_IDENTITY` through to `restore.sh`. *(Amended in the branch review:* it takes only `<today>.dump.age`, or else `<yesterday>.dump.age` (UTC, exact names). The lexicographically newest name could be a planted `2099-12-31.dump`.)
- **Compose.**
  - The restore-test key is mounted read-only at `/run/secrets/restore-test.agekey` only into the service that runs restores. That is S2's `restore` service, or the `backup` service's `restore-test.sh` entrypoint before S2.
  - `AGE_IDENTITY` points at that path by default.
  - For a real disaster restore, the operator copies the recovery key to the VPS for the duration. They pass `-v /root/recovery.key:/run/secrets/recovery.agekey:ro -e AGE_IDENTITY=/run/secrets/recovery.agekey` and delete the copy afterwards. `docs/operations.md` gives the exact commands.

### 4.4 Deletion protection

Which S3-compatible provider hosts the bucket isn't recorded in the repo (§8 question 1), so this part has two levels:

- **Minimum (any provider with versioning):** enable versioning on the bucket, with a lifecycle rule that expires noncurrent versions after 35 days. The `mc rm` prune then only creates delete markers. A deleted backup can be restored from its previous version for 35 days. This needs no script change.
- **Preferred (provider supports policies and lifecycle rules):**
  - A backup-writer key that may only upload objects under `backups/`: no delete, and no list beyond `backups/daily/`, which `restore-test` needs. Restores use a separate read-only key.
  - Lifecycle rules replace the prune: expire `backups/daily/` after 8 days and `backups/weekly/` after 29 days. `BACKUP_PRUNE=lifecycle` makes `backup.sh` skip its prune.

`docs/operations.md` records which level is in place and how to check it.

## 5. Implementation outline

1. `deploy/backup/Dockerfile`: install `age` (§3). `age --version` runs at build time, as a check.
2. `deploy/backup/backup.sh`: steps 1–6 from §4.2 and `BACKUP_PRUNE`.
3. `deploy/backup/restore.sh` and `restore-test.sh`: `.age` handling and `AGE_IDENTITY`.
4. `deploy/compose.yaml` (and S2's `restore` service, if present): the identity mount. Update the env example files with `BACKUP_AGE_RECIPIENTS`, `BACKUP_PRUNE` and `AGE_IDENTITY`.
5. `deploy/e2e.env` (S2: `deploy/e2e/backup.env`): a committed throwaway test recipient, with its identity in `deploy/e2e/restore-test.agekey`. It is clearly marked as test-only and never used in production.
6. `docs/operations.md`: key generation, the updated backup, restore and restore-test commands, the disaster-restore procedure with the recovery key, the deletion-protection level and the threat model. Update the verified local recipe. Core spec §14 and §16.

## 6. Testing

- **Local recipe** (extends the one in `docs/operations.md`, with `zenko/cloudserver` as the bucket and the e2e test keys):
  1. `backup` uploads `backups/daily/<date>.dump.age`. `mc cat` of it doesn't start with `PGDMP`, the custom-format magic.
  2. `restore-test` prints `restore-test ok`.
  3. `restore.sh` without `AGE_IDENTITY` refuses before downloading, and the database is untouched.
  4. With a wrong identity it refuses, and the database is untouched.
  5. With the key and one flipped byte in the object, it refuses, and the database is untouched.
  6. A legacy `.dump` still restores.
  7. An unset `BACKUP_AGE_RECIPIENTS` makes `backup` exit 2 without uploading.
- **Fail loudly:** a zero-byte dump (use a `pg_dump` shim in the test image, or point it at a stopped database) exits non-zero at `verify` and uploads nothing.
- **CI:** these run in a new `backup` job on `main`, next to e2e, using the e2e stack plus `zenko/cloudserver` (§8 question 3). Until that job exists, the recipe is run by hand before merging, and its output is pasted into the pull request.
- **Deletion protection:** once versioning is on, `mc rm` a test object under `backups/daily/` and restore its previous version with `mc undo` or the provider console. Record the steps in `docs/operations.md`.

## 7. Rollout, operations and rollback

1. Generate both keys (§4.1). Put the recipients in the backup env and the restore-test key file on the VPS.
2. Deploy the new backup image.
3. Run `backup`, then `restore-test`. Both must succeed before the next nightly run.
4. Enable versioning (the minimum level) at the provider. Then consider the preferred level.
5. After 28 days, `mc ls` shows no plaintext `.dump` objects left. Then drop legacy support from `restore.sh` in a follow-up, or keep it (§8 question 2).

**Rollback:** redeploy the previous backup image. It uploads plaintext `.dump` files again. Encrypted backups already uploaded stay restorable with the new image and a key. *(Amended after #8:* production deploys every image of a release together by commit SHA, and every release image encrypts, so there is no previous backup image to redeploy. Rollback is fix-forward; `docs/operations.md`, "Backup encryption" › "Rollout", has the details and the source-build exception before the first `deploy.sh`.)

**Key rotation:** add the new recipient, deploy, and wait 28 days. Then remove the old recipient. Backups made before the removal stay decryptable with the old key until they age out.

## 8. Open questions for the owner

1. **Storage provider.** Which S3-compatible provider holds the bucket? That decides the deletion-protection level (§4.4). Recommended: at least versioning before launch.
2. **Legacy plaintext restore support.** Recommended: keep it. It costs a few lines and helps with an old dump found elsewhere. *(Amended in the branch review:* kept, behind `RESTORE_ALLOW_PLAINTEXT=1`.)
3. **A CI job for the backup recipe.** It adds a few minutes to `main` builds. Recommended: yes. Backup scripts that nobody runs until a disaster are the riskiest code in the repo.
4. **The on-host restore-test key.** The alternative is a monthly manual restore test with the recovery key, which avoids any decryptable history on the host. Recommended: keep the on-host key and the automated test. The live database is on the same host anyway.
