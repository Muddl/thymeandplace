# Hosting on Hetzner Cloud

A low-cost setup for the first deployment: one Hetzner Cloud server running the Compose stack, with its database dumps and datasets stored off-provider. Once the server exists, `docs/operations.md` ("First deploy") takes over.

**Checked 2026-09-28.** hetzner.com couldn't be reached from where this was written, so the prices and stock notes come from the secondary sources listed at the end, which disagree by a few tens of cents. Prices exclude VAT. Confirm them in the Hetzner Console before ordering.

## Summary

| Piece | Choice | Per month |
|---|---|---|
| Server | 1× **CX23**: x86 shared vCPU, 2 vCPU, 4 GB RAM, 40 GB NVMe, 20 TB traffic, in an EU location (Nuremberg, Falkenstein or Helsinki) | €5.49–5.99 |
| Public address | 1 Primary IPv4, plus the free IPv6 /64 | €0.50 |
| Server backups | Hetzner automated backups (seven daily images of the whole disk) | 20% of the server, ≈ €1.10–1.20 |
| Firewall | Hetzner Cloud Firewall | free |
| DNS | Hetzner DNS (in the Console), or your registrar's | free |
| Buckets | **Backblaze B2**, EU Central (Amsterdam): one bucket for datasets, one for backups | free up to 10 GB |
| **Total** | | **≈ €7–8** |

```
                    visitors (IPv4 and IPv6)
                             │  TCP 80, 443 · UDP 443 (HTTP/3)
                 ┌───────────▼────────────┐
                 │ Hetzner Cloud Firewall │  in: 80, 443/tcp, 443/udp, ICMP from anywhere;
                 │ (outside the server)   │      22/tcp from your IPs only
                 └───────────┬────────────┘
   ┌─────────────────────────▼───────────────────────────────┐
   │ CX23 · Ubuntu 24.04 · Docker Compose                    │
   │   caddy ──► app (as tp_app) ──► db (Postgres + PostGIS) │  db has no published port
   │   on demand: migrate, ingest, transform, backup, restore│
   └────────────┬─────────────────────────────┬──────────────┘
                │ nightly pg_dump (tp_backup) │ raw + transformed datasets (ingest)
   ┌────────────▼─────────────────────────────▼───┐
   │ Backblaze B2, EU Central: two private buckets│  one scoped key per bucket
   └──────────────────────────────────────────────┘
   Hetzner automated backups: a daily image of the whole server, including
   /etc/thymeandplace (the env files) and Caddy's certificates.
```

## Why this shape

- **One server.** The system is built for it (core spec §4.1: Caddy, the app and Postgres under Compose). At launch traffic, a load balancer, a second server or a managed database would each add cost without any need yet. "Growing later" below lists the steps in order.
- **x86, not Arm.** `postgis/postgis:17-3.5` is published for `linux/amd64` only (checked with `docker buildx imagetools inspect` on 2026-09-28). The Arm CAX servers would need a different Postgres image. The other images (Caddy, distroless, Go, Python, `postgres:17-bookworm`) are multi-arch.
- **2 vCPU and 4 GB.** The capacity spec's defaults assume this size: 16 pooled connections and 12 public DB slots (`docs/superpowers/specs/2026-09-27-db-capacity-and-load-protection-design.md`). With 2 GB you'd have to drop to `DB_MAX_CONNS=10`, and the one-off climate transform would have little headroom.
- **An EU location.** EU plans include 20 TB of traffic, against 1 TB in Hetzner's US locations, where plans start around $20 a month. The pages are small and server-rendered, so the extra 100–150 ms for visitors across the Atlantic is acceptable. Pick the EU location nearest most of your expected visitors. All locations include Hetzner's DDoS protection.
- **Buckets at another provider.** Backups kept with a different provider survive losing the Hetzner account or project. B2 also fits the planned backup hardening (issue #12, `2026-09-27-backup-encryption-design.md`): application keys can be limited to one bucket and denied delete, and the bucket keeps earlier file versions. It's free up to 10 GB. This system stores roughly 4–8 GB: about 3 GB of NetCDF per climate version, plus a few small dumps and datasets.
- **The all-Hetzner alternative** is Hetzner Object Storage: about €6.49 a month including 1 TB, with free traffic to a server in the same location. It's one fewer vendor, but it roughly doubles the bill and keeps the backups with the server's provider.

## Getting a server (stock, September 2026)

Hetzner raised cloud prices twice in 2026 (1 April, and 15 June for new orders and rescales). Since June, a hardware shortage has made the CX and CAX types show "not available" at times. Stock trackers see CX23 come back within minutes to hours, varying by location.

- Try each EU location, and try again later. Setting up the site doesn't depend on having the server yet.
- Once you have a CX23, keep it. Existing servers keep their price; rescaling moves the server to current pricing, and the larger types may be out of stock.
- If you need a server immediately and CX23 won't come back, the orderable fallback is **CPX22** (2 vCPU, 4 GB, 80 GB) at about €19.49. **CPX12** (1 vCPU, 2 GB, about €12) costs twice as much as a CX23 for half the capacity; skip it.

## Setting it up

### In the Hetzner Console

1. **Project.** Create a project (e.g. `thymeandplace`) and add your SSH public key under Security → SSH keys.
2. **Firewall** named `web`. Inbound:
   - TCP 80, TCP 443 and UDP 443 (HTTP/3, which Caddy offers) from any IPv4 and IPv6;
   - ICMP;
   - TCP 22 from your own IP addresses only. If your home IP changes often, allow 22 from anywhere and rely on key-only SSH (below).

   Leave outbound open. The server needs Let's Encrypt, the Docker registries, GeoNames, the TerraClimate server, B2 and the Ubuntu mirrors.
3. **Server.**
   - Settings: your chosen location, Ubuntu 24.04, type CX23, public IPv4 and IPv6, your SSH key, the `web` firewall, **Backups** on. No volume and no private network.
   - Afterwards, turn on delete and rebuild protection for the server.
   - Under Primary IPs, turn off auto-delete and turn on protection, so your DNS records survive a server rebuild.
4. **Reverse DNS.** Set the PTR record of both primary IPs to `thymeand.place`.
5. **DNS.** Add A and AAAA records for `thymeand.place` and `www.thymeand.place`, pointing at the primary IPs; start with a short TTL (300 s). You can do this in Hetzner DNS (free, in the Console since May 2026) or at your registrar. If you add a CAA record, allow both of Caddy's default CAs, Let's Encrypt and ZeroSSL, or leave CAA out.

### On the server (first login)

```bash
# Updates. Ubuntu applies security updates automatically (unattended-upgrades).
apt update && apt full-upgrade -y

# A sudo user; then key-only SSH and no root login.
adduser --disabled-password --gecos "" ops && usermod -aG sudo ops
install -d -m 700 -o ops -g ops /home/ops/.ssh && cp /root/.ssh/authorized_keys /home/ops/.ssh/ && chown ops:ops /home/ops/.ssh/authorized_keys
passwd ops   # sudo needs a password
printf 'PasswordAuthentication no\nPermitRootLogin no\n' > /etc/ssh/sshd_config.d/10-hardening.conf && systemctl reload ssh

# 4 GB of swap, as headroom for the one-off climate transform.
fallocate -l 4G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
echo '/swapfile none swap sw 0 0' >> /etc/fstab && sysctl -w vm.swappiness=10 && echo 'vm.swappiness=10' > /etc/sysctl.d/90-swap.conf
```

Log in again as `ops` in a second terminal before closing the root session. Then:

1. Install Docker Engine and the Compose plugin from Docker's apt repository (runbook step 1).
2. Give Docker's logs a size limit before starting anything: the app logs one line per request, and Docker's default log driver never rotates. Put `{"log-driver": "local"}` in `/etc/docker/daemon.json` (that rotates at 100 MB per container), then `systemctl restart docker`.
3. Keep the clock in sync. Ubuntu's default, `systemd-timesyncd`, is enough, but the admin's TOTP backup login needs it.
4. There's no need for ufw. The Cloud Firewall sits outside the server, where Docker can't get around it. If you add ufw anyway, note that ports Docker publishes bypass it.

Then continue with `docs/operations.md`, "First deploy", from step 2.

### Buckets on B2

1. Create a Backblaze account and choose the **EU Central** region at sign-up (the region is fixed per account).
2. Create two **private** buckets. Names are global, so add a suffix, e.g. `thymeandplace-datasets-<suffix>` and `thymeandplace-backups-<suffix>`. On the backups bucket, set the lifecycle to keep earlier versions for 35 days. That way, a dump that pruning (or an attacker on the server) deletes stays recoverable, as the issue #12 design wants.
3. Create one application key per bucket, each limited to its bucket:
   - The datasets key goes in `ingest.env`.
   - The backups key goes in `backup.env` and `restore.env`. For now it needs delete permission, because `backup.sh` prunes old dumps with `mc rm`. Issue #12 replaces that with lifecycle expiry and a key that can't delete.
4. Set `BUCKET_ENDPOINT` to the S3 endpoint shown on the bucket page (e.g. `s3.eu-central-003.backblazeb2.com`), with `BUCKET_USE_SSL=true`.
5. Before relying on it, run a backup and `restore-test.sh` (runbook step 10) and check for `restore-test ok`.

## Watching it (free)

- The Hetzner Console graphs CPU, disk and network per server.
- Add an external uptime check on `https://thymeand.place/readyz` every few minutes, with email alerts. Most uptime services have a free tier that covers one site. It's one of the checks in `docs/operations.md`, "Monitoring and alerts", with the requirements it must meet.
- Disk is 40 GB, which is enough with some housekeeping:
  - check `df -h` and `docker system df` now and then;
  - run `docker builder prune -f` after app updates (images are built on the server);
  - once a climate version is live, delete its raw NetCDF files from the `ingest_work` volume, since the bucket keeps a copy.

## Growing later

In order, as the load test (`docs/capacity-baseline.md`) or real traffic calls for it:

1. **Rescale** to CX33 (4 vCPU, 8 GB). Choose "CPU and RAM only" so the disk stays small enough to scale back down. It takes a reboot, and the server moves to current pricing.
2. **Move Postgres to its own server** on a Hetzner private network (free). The app is stateless, so the Compose services split cleanly.
3. **Run a second app server** behind a Hetzner Load Balancer (LB11). The app runs as N replicas without code changes, but its rate limits and caches are per process.
4. **Add a CDN** only after issue #10 lands, because the rate limiter must see real client IPs through the extra proxy. Weigh the privacy cost too: the site avoids logging coordinates, and a CDN would see every request URL.

## Sources

- Hetzner June 2026 price adjustment (docs.hetzner.com, "Price Adjustment 15 June 2026"), as summarised by [wz-it.com](https://wz-it.com/en/blog/hetzner-price-increase-june-2026-cpx-ccx-alternatives/) and [byteiota](https://byteiota.com/hetzner-june-2026-price-shock/)
- CX/CAX availability: [Vincent Schmalbach](https://www.vincentschmalbach.com/hetzner-cheap-cloud-unavailable-price-increases/), [bex.co](https://bex.co/blog/2026/08/22/hetzner-capacity-crunch-limited-availability-fleet-provisioning), [stackvaluelab](https://stackvaluelab.com/hetzner-cx-cax-unavailable/)
- Plan specs and US pricing: [Spare Cores CPX22](https://sparecores.com/server/hcloud/cpx22), [Spare Cores CPX12](https://sparecores.com/server/hcloud/cpx12), [bestusavps.com](https://bestusavps.com/reviews/hetzner/)
- IPv4, backups, firewall, networking: [bex.co on IPv4](https://bex.co/blog/2026/08/04/hetzner-ipv6-only-worker-nodes-ipv4-savings), [Better Stack review](https://betterstack.com/community/guides/web-servers/hetzner-cloud-review/)
- Hetzner Object Storage: [bex.co](https://bex.co/blog/2026/09/11/hetzner-object-storage-tenant-backup-backend), [hetzner.com](https://www.hetzner.com/storage/object-storage/)
- Hetzner DNS move to the Console: [Hetzner status](https://status.hetzner.com/incident/c2146c42-6dd2-4454-916a-19f07e0e5a44), [AgentDeals](https://agentdeals.dev/vendor/hetzner-dns)
- Backblaze B2: [pricing](https://www.backblaze.com/cloud-storage/pricing), [application key capabilities](https://www.backblaze.com/docs/cloud-storage-application-key-capabilities), [data regions](https://www.backblaze.com/docs/cloud-storage-data-regions)
