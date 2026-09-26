# syntax=docker/dockerfile:1
FROM postgres:17-bookworm

# Pin the MinIO client (mc) to one dated GitHub release and verify its
# sha256. dl.min.io no longer serves the `mc` binary (as of 2026, GET
# requests to dl.min.io/client/mc/... return 410 Gone; MinIO gated it
# behind their commercial subscription), so this pulls straight from
# MinIO's GitHub release assets instead, which are still public.
#
# To bump: pick a new tag from https://github.com/minio/mc/releases,
# download mc.linux-<arch>.<tag> and its .sha256sum for both amd64 and
# arm64, verify sha256sum -c locally, then update MC_VERSION and both
# MC_SHA256_* values below together.
ARG TARGETARCH
ARG MC_VERSION=RELEASE.2025-08-13T08-35-41Z
ARG MC_SHA256_AMD64=01f866e9c5f9b87c2b09116fa5d7c06695b106242d829a8bb32990c00312e891
ARG MC_SHA256_ARM64=14c8c9616cfce4636add161304353244e8de383b2e2752c0e9dad01d4c27c12c

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/* \
 && case "$TARGETARCH" in \
      amd64) sha256="$MC_SHA256_AMD64" ;; \
      arm64) sha256="$MC_SHA256_ARM64" ;; \
      *) echo "backup: unsupported TARGETARCH '$TARGETARCH' (need amd64 or arm64)" >&2; exit 1 ;; \
    esac \
 && curl -fsSL -o /usr/local/bin/mc \
      "https://github.com/minio/mc/releases/download/${MC_VERSION}/mc.linux-${TARGETARCH}.${MC_VERSION}" \
 && echo "${sha256}  /usr/local/bin/mc" | sha256sum -c - \
 && chmod +x /usr/local/bin/mc

COPY backup.sh restore.sh restore-test.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/*.sh

ENTRYPOINT ["/usr/local/bin/backup.sh"]
