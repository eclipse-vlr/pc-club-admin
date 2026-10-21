# syntax=docker/dockerfile:1
FROM node:24.19.0-bookworm-slim AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=secret,id=proxy_ca \
    if [ -f /run/secrets/proxy_ca ]; then export NODE_EXTRA_CA_CERTS=/run/secrets/proxy_ca; fi; npm ci --strict-ssl=true
COPY web/ ./
RUN npm run build

FROM golang:1.26.10-bookworm AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=secret,id=proxy_ca \
    if [ -f /run/secrets/proxy_ca ]; then export SSL_CERT_FILE=/run/secrets/proxy_ca; fi; go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY migrations/ migrations/
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -o /out/migrate ./cmd/migrate && \
    CGO_ENABLED=0 go build -trimpath -o /out/admin ./cmd/admin && \
    CGO_ENABLED=0 go build -trimpath -o /out/healthcheck ./cmd/healthcheck

FROM debian:bookworm-slim
ARG SOURCE_REVISION=unknown
LABEL org.opencontainers.image.source="https://github.com/eclipse-vlr/pc-club-admin" \
      org.opencontainers.image.revision=$SOURCE_REVISION
COPY --from=backend /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
WORKDIR /app
COPY --from=backend /out/ /app/
COPY --from=frontend /src/web/dist/ /app/web/dist/
USER 10001:10001
ENV HTTP_ADDR=0.0.0.0:8080 WEB_DIST_DIR=/app/web/dist
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 CMD ["/app/healthcheck"]
ENTRYPOINT ["/app/api"]
