# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder
WORKDIR /src

ARG VERSION=0.1.0-docker
ARG COMMIT=none
ARG DATE=unknown

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X github.com/LorranHippolyte/tagforge/internal/version.Version=${VERSION} -X github.com/LorranHippolyte/tagforge/internal/version.Commit=${COMMIT} -X github.com/LorranHippolyte/tagforge/internal/version.Date=${DATE}" \
    -o /out/tagforge ./cmd/tagforge

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/tagforge /tagforge

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/tagforge"]
CMD ["serve", "--http", "--addr", "0.0.0.0:8080"]
