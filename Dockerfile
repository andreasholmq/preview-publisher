FROM --platform=$BUILDPLATFORM golang:1.26.0-bookworm@sha256:2a0ba12e116687098780d3ce700f9ce3cb340783779646aafbabed748fa6677c AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/preview-gateway ./cmd/preview-gateway \
  && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/preview ./cmd/preview

FROM gcr.io/distroless/static-debian12:nonroot@sha256:d093aa3e30dbadd3efe1310db061a14da60299baff8450a17fe0ccc514a16639

WORKDIR /
COPY --from=build /out/preview-gateway /preview-gateway
COPY --from=build /out/preview /preview

USER nonroot:nonroot
EXPOSE 8080
VOLUME ["/data"]

ENTRYPOINT ["/preview-gateway"]

