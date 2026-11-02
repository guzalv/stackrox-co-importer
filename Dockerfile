# IMP-IMG-001: prep stage. ubi9-micro has no shell, so the writable /backups
# directory (the container's default --backup-dir, see IMP-CLI-032) is created
# here and copied into the final image with the right ownership.
# ubi9 9.x multi-arch index, pinned by digest for reproducible builds.
#   crane digest registry.access.redhat.com/ubi9:latest
FROM registry.access.redhat.com/ubi9@sha256:8f989cc9270da6abec0144c22846ed1e6e92e462dc7768ce0981c4ac1d574733 AS prep
RUN mkdir -p /backups && chown 65534:65534 /backups

# IMP-IMG-001, IMP-IMG-005
# ubi9-micro 9.8 (release 1791895366), pinned by digest for reproducible builds.
# Refresh this digest periodically (manually or via a bot) to pick up base-image
# security updates:
#   crane digest registry.access.redhat.com/ubi9-micro:latest
FROM registry.access.redhat.com/ubi9-micro@sha256:5be877a2a280ae36eb9ab80ca4efb28bbf77c57abbba38137cdc37a03b8465e6

LABEL org.opencontainers.image.title="stackrox-co-importer"
LABEL org.opencontainers.image.description="Compliance Operator to ACS scan configuration importer"
LABEL org.opencontainers.image.source="https://github.com/guzalv/stackrox-co-importer"

COPY compliance-operator-importer /compliance-operator-importer

# IMP-CLI-032: default --backup-dir for the container. The bare binary's relative
# default is not writable under USER 65534 (no WORKDIR) and would not persist
# across --rm; /backups is meant to be a mounted volume.
COPY --from=prep --chown=65534:65534 /backups /backups
ENV CO_IMPORTER_BACKUP_DIR=/backups

USER 65534:65534

ENTRYPOINT ["/compliance-operator-importer"]
