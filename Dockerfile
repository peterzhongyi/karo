# Build the operator binary
FROM google-go.pkg.dev/golang:1.23.3 as builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# TODO(bogdanbe): update to vendored deps
# Cache deps before building and copying source.
RUN go mod download

# Copy the go source
COPY cmd/ cmd
COPY assets/ assets/
COPY pkg/api/ pkg/api/
COPY pkg/controller/ pkg/controller/
COPY pkg/transformer/ pkg/transformer/

# Build
USER root
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -tags strictfipsruntime -a -o dist/manager cmd/manager/main.go


# Use a base image with a shell for the final container
FROM ubuntu:latest 
RUN apt-get update && apt-get install -y curl ca-certificates

    # Set the working directory for the final image
WORKDIR /

# Copy the compiled binary from the builder stage
COPY --from=builder /workspace/dist/manager .

# Set a non-root user for the final container
USER 65532:65532

# Set the entry point for the container
ENTRYPOINT ["/manager"]