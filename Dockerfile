# syntax=docker/dockerfile:1

# The lorvikd binary comes from one of two stages, chosen with BINARY:
#   source    (default) builds it from this checkout. The dashboard is not
#             built here: run `task web:build` first and the build context's
#             web/dist is embedded. Without it the image serves an empty UI.
#   released  copies it from an unpacked release archive at
#             linux_<arch>/lorvikd in the build context. The release workflow
#             uses this, so the image runs the bytes that were released.
ARG BINARY=source

FROM golang:1.27 AS source
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/BonfireTrunk/lorvik/internal/build.Version=${VERSION} -X github.com/BonfireTrunk/lorvik/internal/build.Commit=${COMMIT}" \
      -o /out/lorvikd ./cmd/lorvikd

FROM scratch AS released
ARG TARGETARCH
COPY linux_${TARGETARCH}/lorvikd /out/lorvikd

FROM ${BINARY} AS binary

FROM gcr.io/distroless/static-debian13:nonroot
LABEL org.opencontainers.image.source="https://github.com/BonfireTrunk/lorvik" \
      org.opencontainers.image.licenses="AGPL-3.0-only"
COPY --from=binary /out/lorvikd /lorvikd
EXPOSE 8080
ENTRYPOINT ["/lorvikd"]
CMD ["serve"]
