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

USER 65534:65534

ENTRYPOINT ["/compliance-operator-importer"]
