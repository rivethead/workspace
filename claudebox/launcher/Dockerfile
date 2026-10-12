# The launcher: the only container on a laptop with Docker access. It holds the
# docker CLI, the SPIRE agent CLI (to fetch its JWT-SVID) and the launcher.
ARG SPIRE_VERSION=1.15.3
FROM ghcr.io/spiffe/spire-agent:${SPIRE_VERSION} AS spire

FROM golang:1.26 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/claudebox-launcher ./cmd/claudebox-launcher

FROM docker:27-cli
COPY --from=spire /opt/spire/bin/spire-agent /usr/local/bin/spire-agent
COPY --from=build /out/claudebox-launcher /usr/local/bin/claudebox-launcher
ENTRYPOINT ["/usr/local/bin/claudebox-launcher"]
CMD ["serve"]
