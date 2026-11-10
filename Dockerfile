# Built by goreleaser dockers_v2 (see .goreleaser.yaml); the binary is copied from the build context.
# distroless static: CA certificates and tzdata, no shell, runs as nonroot.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/tfcensus /usr/bin/tfcensus
ENTRYPOINT ["/usr/bin/tfcensus"]
