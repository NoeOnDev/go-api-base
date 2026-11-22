# syntax=docker/dockerfile:1

# The image of the API: a static binary and nothing else (see docs/deploy.md).
#
#   docker build -t api .
#
# The migrations, the catalogs, the email templates and the time zones are inside the binary, so
# the image needs no other file of the repository.

# --- Build. The toolchain is the one go.mod asks for: if this image is older, Go downloads it. The
# official image turns that off (GOTOOLCHAIN=local), and the build would stop on a server whose
# copy of the image is from before the last update of go.mod.
FROM golang:1.27 AS build
ENV GOTOOLCHAIN=auto
WORKDIR /src

# The dependencies first: this layer is reused while go.mod and go.sum do not change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
# The same flags as `make build`.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api
# The directory of the uploaded files (STORAGE_DRIVER=local). It is created here because the final
# image has no shell; a volume mounted on it starts with its owner.
RUN mkdir -p /out/data/storage

# --- The final image: no shell, no package manager, and a user without privileges. It brings the
# certificates of the authorities, which the SMTP server and S3 need.
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/api /api
COPY --from=build --chown=nonroot:nonroot /out/data /data

ENV APP_ENV=production \
    HTTP_ADDR=:8080 \
    STORAGE_DIR=/data/storage
EXPOSE 8080

# It asks its own /healthz: the image has no curl.
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=3 CMD ["/api", "healthcheck"]

ENTRYPOINT ["/api"]
