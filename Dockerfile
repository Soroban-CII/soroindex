# Base image indexes checked against their registries on 9 October 2026.
FROM golang:1.27.2-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY go.mod go.sum ./
# Optional enterprise/proxy CA bundle is mounted only for the download step.
# Neither the bundle nor proxy credentials are copied into the runtime image.
RUN --mount=type=secret,id=ca-certificates \
    set -e; \
    if [ -f /run/secrets/ca-certificates ]; then export SSL_CERT_FILE=/run/secrets/ca-certificates; fi; \
    go mod download && go mod verify
COPY . .
ARG VERSION=dev
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/sep47idx ./cmd/sep47idx \
    && mkdir -p /out/data && chown 65532:65532 /out/data
RUN set -eu; mkdir -p /out/licenses; cp LICENSE /out/licenses/PROJECT-LICENSE; \
    cp "$(go env GOROOT)/LICENSE" /out/licenses/Go-LICENSE; \
    go list -deps -f '{{if .Module}}{{.Module.Path}} {{.Module.Version}} {{.Module.Dir}}{{end}}' ./cmd/sep47idx | sort -u | \
    while read -r module version directory; do \
      if [ -n "$directory" ]; then \
        target="/out/licenses/$module@$version"; mkdir -p "$target"; \
        find "$directory" -maxdepth 1 -type f \( -iname 'license*' -o -iname 'copying*' -o -iname 'notice*' \) -exec cp -t "$target" {} +; \
      fi; \
    done

FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
WORKDIR /app
COPY --from=build /out/sep47idx /app/sep47idx
COPY --from=build --chown=65532:65532 /out/data /app/data
COPY --from=build /out/licenses /app/licenses
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/sep47idx"]
CMD ["serve", "--network", "testnet", "--addr", ":8080"]
