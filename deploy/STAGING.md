# How to run the shared staging stack

Staging is a second CX23 that serves `https://staging.thymeand.place` for UX review. It runs the same images as production, behind basic auth, with fixture data only. Production is unchanged.

Add the GitHub secrets last. Until they exist, the `staging` workflow skips and stays green.

## What Wade does once

These steps are Wade-only. Do not reuse a production secret, key, bucket, or Healthchecks check.

### Provision the VM

1. Create a **separate** Hetzner CX23 (Ubuntu 24.04, x86, public IPv4 and IPv6) in an EU location. Use a new Cloud Firewall, not the production `web` firewall, so a mistake cannot open production SSH.
2. Inbound: TCP 80, TCP 443, UDP 443, ICMP from anywhere; TCP 22 from anywhere. GitHub-hosted runners change IP, so do not pin SSH to a home address. Key-only SSH is the control. Outbound stays open.
3. Turn on delete protection. Point `staging.thymeand.place` A and AAAA records at this VM only. Reverse DNS may stay unset.
4. First login, then 2 GB of swap (staging seeds fixtures and never runs the climate transform):

```bash
apt update && apt full-upgrade -y
adduser --disabled-password --gecos "" ops && usermod -aG sudo ops
install -d -m 700 -o ops -g ops /home/ops/.ssh && cp /root/.ssh/authorized_keys /home/ops/.ssh/ && chown ops:ops /home/ops/.ssh/authorized_keys
passwd ops
printf 'PasswordAuthentication no\nPermitRootLogin prohibit-password\n' > /etc/ssh/sshd_config.d/10-hardening.conf && systemctl reload ssh
fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
echo '/swapfile none swap sw 0 0' >> /etc/fstab
sysctl -w vm.swappiness=10 && echo 'vm.swappiness=10' > /etc/sysctl.d/90-swap.conf
```

5. Install Docker Engine and Compose v2 (≥ 2.24) from Docker's apt repository. Put `{"log-driver": "local"}` in `/etc/docker/daemon.json` and `systemctl restart docker`.
6. Clone the repo to `/opt/thymeandplace` as root. Log Docker in to `ghcr.io` with a classic token that has only `read:packages`, stored in root's Docker config. Give the VM its own read-only GitHub deploy key (or HTTPS token) so `git fetch origin <sha>` works. Do not put the Actions deploy key on this remote.

### Env files and throwaway credentials

Use `/etc/thymeandplace/` the same way as production (`docs/operations.md`, "First deploy" step 3), with **new** values.

```bash
sudo install -d -m 700 -o root -g root /etc/thymeandplace
for f in db migrate app ingest backup restore; do
  sudo install -m 600 -o root -g root /opt/thymeandplace/deploy/env/$f.env.example /etc/thymeandplace/$f.env
done
sudo install -m 600 -o root -g root \
  /opt/thymeandplace/deploy/env/Caddyfile.basicauth.example \
  /etc/thymeandplace/Caddyfile.basicauth
```

Generate new hex DB passwords and a new `ADMIN_SECRET_KEY`. Set:

- `ADMIN_RP_ID=staging.thymeand.place`
- `ADMIN_ORIGIN=https://staging.thymeand.place`
- `PUBLIC_BASE_URL=https://staging.thymeand.place`
- `DB_MAX_CONNS=10` in `app.env` as well if you like. The overlay sets it either way.
- `FEATURE_SEAFOOD` unset or `off`
- `DEVSEED_ALLOW=1` in `ingest.env` only on this VM
- every `HEARTBEAT_*` **unset**, or URLs from a **separate** Healthchecks.io project named for staging. Production ping URLs must never appear here.

Create a throwaway Backblaze B2 bucket and application key for staging dumps only. On your laptop, `age-keygen` a staging recovery key and keep it out of the production password-manager entry. On the VM:

```bash
cd /opt/thymeandplace/deploy
docker compose --profile tools build backup
docker compose --profile tools run --rm --no-deps -v /etc/thymeandplace:/out --entrypoint age-keygen backup -o /out/restore-test.agekey
```

Put both staging public keys in `BACKUP_AGE_RECIPIENTS`. Do not use the production recipients.

Hash the basic-auth password and edit `/etc/thymeandplace/Caddyfile.basicauth`:

```bash
docker compose -f compose.yaml run --rm --no-deps --entrypoint caddy caddy hash-password
# paste the $2a$… line into Caddyfile.basicauth next to the username
```

Remember the username and password. They become the `STAGING_BASIC_AUTH` GitHub secret (`username:password`).

### First start and seed

```bash
cd /opt/thymeandplace/deploy
docker compose -f compose.yaml -f compose.staging.yaml up -d --build --wait app caddy
docker compose -f compose.yaml -f compose.staging.yaml --profile tools run --rm --entrypoint /app/devseed ingest
```

`devseed ok` prints. Search Lisbon. Enroll an admin passkey against `staging.thymeand.place`. Run `devseed` only when you want the fixture catalog reset. Do not run it on every deploy.

### Actions deploy key

On your laptop, generate a key that will exist only as a GitHub secret and on this VM:

```bash
ssh-keygen -t ed25519 -f staging-deploy -N "" -C staging-deploy
```

Append `staging-deploy.pub` to `/root/.ssh/authorized_keys` on the **staging** VM only. Never add it to production.

Pin the host key:

```bash
ssh-keyscan -t ed25519 staging.thymeand.place
```

The line must use the same hostname or IP you will put in `STAGING_HOST`.

### GitHub secrets

Add these repository secrets, then stop. The next green `ci` run on `main` deploys.

| Secret | Value |
|---|---|
| `STAGING_SSH_KEY` | The private key (`staging-deploy`), including the BEGIN/END lines |
| `STAGING_HOST` | `staging.thymeand.place`, or `root@<ip>` |
| `STAGING_SSH_HOST_KEY` | The `ssh-keyscan` line |
| `STAGING_BASIC_AUTH` | `username:password` for the Caddy prompt |

If any of these is missing, `deploy/staging-gate.sh` prints `false` and the workflow stays green.

## What the workflow does

`.github/workflows/staging.yml` runs after a successful `ci` push on `main` (`workflow_run`), and on `workflow_dispatch` with a SHA. It never cancels an in-flight deploy. Permissions are `contents: read`. SSH uses `StrictHostKeyChecking=yes` and the pinned host key.

On the VM it fetches that SHA, then `deploy/staging-remote.sh` waits for the GHCR images and calls `deploy.sh` with `TP_COMPOSE_OVERLAY=compose.staging.yaml`. After start it checks `https://staging.thymeand.place/readyz` with basic auth.

To deploy a SHA by hand:

```bash
cd /opt/thymeandplace
git fetch origin <sha>
git checkout --detach <sha>
./deploy/staging-remote.sh <sha>
```

Rollback is the same command with an earlier SHA from `/var/lib/thymeandplace/deploys.log`.

## What staging is not

- Not a production replica. No real TerraClimate ingest, no production catalog, no production admin passkeys.
- Not a pager. Unset heartbeats, or a staging-only Healthchecks project.
- Not reachable without the basic-auth password.
