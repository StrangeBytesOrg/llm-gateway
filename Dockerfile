FROM oven/bun:1.4.4 AS ui
WORKDIR /src/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web ./
RUN bun run build

FROM golang:1.27.3-alpine3.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /gateway ./cmd/gateway && mkdir -m 0700 /data

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /gateway /gateway
COPY --from=build --chown=nonroot:nonroot /data /data
WORKDIR /
USER nonroot:nonroot
EXPOSE 42080
ENTRYPOINT ["/gateway"]
CMD ["-config", "/etc/gateway/config.yaml"]
