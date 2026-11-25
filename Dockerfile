# syntax=docker/dockerfile:1

# The dashboard is not built here: run `task web:build` first and the build
# context's web/dist is embedded. Without it the image serves an empty UI.

# Stage 1: server binary
FROM golang:1.27 AS build
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

# Stage 2: runtime
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/lorvikd /lorvikd
EXPOSE 8080
ENTRYPOINT ["/lorvikd"]
CMD ["serve"]
