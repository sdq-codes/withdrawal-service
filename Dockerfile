FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# modernc.org/sqlite is pure Go, so the binary is static and needs no cgo.
ENV CGO_ENABLED=0
# .git is excluded from the build context, so build metadata is passed in.
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown
RUN go test ./... && go build -trimpath \
    -ldflags="-s -w -X github.com/sdq-codes/withdrawal-service/version.GitCommit=${GIT_COMMIT} -X github.com/sdq-codes/withdrawal-service/version.BuildTime=${BUILD_TIME}" \
    -o /out/withdrawal-service .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home app \
    && mkdir -p /app/data && chown 10001:10001 /app/data
WORKDIR /app
COPY --from=build /out/withdrawal-service /app/withdrawal-service
USER 10001:10001
ENV PORT=8080 METRICS_PORT=9090 DATABASE_PATH=/app/data/withdrawals.db
VOLUME ["/app/data"]
EXPOSE 8080 9090
ENTRYPOINT ["/app/withdrawal-service"]
CMD ["serve"]
