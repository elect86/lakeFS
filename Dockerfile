FROM golang:1.16.2-alpine AS build

ARG VERSION=dev

WORKDIR /build

# Packages required to build
RUN apk add --no-cache build-base

# Copy project deps first since they don't change often
COPY go.mod go.sum ./
RUN go mod download

# Copy project
COPY . ./

# Build a binaries
RUN go build -ldflags "-X github.com/treeverse/lakefs/pkg/version.Version=${VERSION}" -o lakefs ./cmd/lakefs
RUN go build -ldflags "-X github.com/treeverse/lakefs/pkg/version.Version=${VERSION}" -o lakectl ./cmd/lakectl

# lakectl image
FROM alpine:3.12.0 AS lakectl
WORKDIR /app
ENV PATH /app:$PATH
COPY --from=build /build/lakectl ./
RUN addgroup -S lakefs && adduser -S lakefs -G lakefs
USER lakefs
WORKDIR /home/lakefs
ENTRYPOINT ["/app/lakectl"]

# lakefs image
FROM alpine:3.12.0 AS lakefs

# Be Docker compose friendly (i.e. support wait-for)
RUN apk add netcat-openbsd

WORKDIR /app
COPY ./scripts/wait-for ./
ENV PATH /app:$PATH
COPY --from=build /build/lakefs /build/lakectl ./

EXPOSE 8000/tcp

# Setup user
RUN addgroup -S lakefs && adduser -S lakefs -G lakefs
USER lakefs
WORKDIR /home/lakefs

# Configuration location
VOLUME /etc/lakefs.yaml

ENTRYPOINT ["/app/lakefs"]
CMD ["run"]

# nessie image
FROM golang:1.16.2-alpine AS nessie

# Linux utilities for 'script'
RUN apk add --no-cache util-linux

