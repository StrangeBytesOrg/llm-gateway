FROM golang:1.27.3-alpine3.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /gateway ./cmd/gateway && mkdir -m 0700 /data

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /gateway /gateway
COPY --from=build --chown=nonroot:nonroot /data /data
WORKDIR /
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/gateway"]
CMD ["-config", "/etc/gateway/config.yaml"]
