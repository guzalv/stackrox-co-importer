# IMP-IMG-001, IMP-IMG-005
FROM registry.access.redhat.com/ubi9-micro:latest

LABEL org.opencontainers.image.title="co-acs-importer"
LABEL org.opencontainers.image.description="Compliance Operator to ACS scan configuration importer"
LABEL org.opencontainers.image.source="https://github.com/guzalv/stackrox-co-importer"

COPY compliance-operator-importer /compliance-operator-importer

USER 65534:65534

ENTRYPOINT ["/compliance-operator-importer"]
