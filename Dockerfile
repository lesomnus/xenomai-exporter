ARG BUILD_HASH="0000000000000000000000000000000000000000"
ARG BUILD_DATE="YYMMDD"
ARG BUILD_ID="r0"
ARG TARGETARCH

FROM library/golang:1.26 AS base

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0



FROM base AS test

RUN --mount=type=cache,target=/root/.cache/go-build \
	go test -v -trimpath ./...



FROM base AS builder

ARG BUILD_HASH
ARG BUILD_DATE
ARG BUILD_ID
RUN BUILD_HASH=${BUILD_HASH} \
	BUILD_DATE=${BUILD_DATE} \
	BUILD_ID=${BUILD_ID} \
	./scripts/gen-version-file.sh

SHELL ["/bin/bash", "-c"]
RUN --mount=type=cache,target=/root/.cache/go-build \
	flags=(-trimpath -ldflags="-s -w") \
	&& GOARCH=arm64 go build "${flags[@]}" -o xenomai-exporter-arm64 . \
	&& GOARCH=amd64 go build "${flags[@]}" -o xenomai-exporter-amd64 .

FROM scratch AS build

COPY --from=builder \
	/app/xenomai-exporter-arm64 \
	/app/xenomai-exporter-amd64 \
	/



FROM busybox AS app

SHELL ["/bin/sh", "-c"]
ARG TARGETARCH
COPY ./xenomai-exporter-${TARGETARCH} /xenomai-exporter
RUN /xenomai-exporter version
ENTRYPOINT ["/xenomai-exporter"]
