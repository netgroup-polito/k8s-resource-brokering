# Build the manager binary
FROM golang:1.24 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the Go source (relies on .dockerignore to filter)
COPY . .

# Build: Saves the compiled file in the /workspace/manager
# the GOARCH has no default value to allow the binary to be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager ./cmd/main.go

# Use debian:bookworm-slim for the final image to have a package manager
FROM debian:bookworm-slim
WORKDIR /
COPY --from=builder /workspace/manager .


#NOTA: siccome dobbiamo fare il peering tra i cluster, dobbiamo installare kubectl e liqoctl anche nell'immagine del broker.
# QUindi c'è bisogno di eseguire i comandi kubectl e liqoctl anche nel broker-cluster.


# Add pre-downloaded dependencies (kubectl and liqoctl)
COPY kubectl /usr/local/bin/kubectl
COPY liqoctl /usr/local/bin/liqoctl
RUN chmod +x /usr/local/bin/kubectl /usr/local/bin/liqoctl

#User non-root has UID 65532
USER 65532:65532

#NOTA: quando il pod è pronto, al suo avvio grazie ad entrypoint, viene eseguito il comando /manager
# che è il nostro controller che si occupa di gestire le risorse del cluster.

# The entrypoint of the container is the manager binary
ENTRYPOINT ["/manager"]
