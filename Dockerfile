FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/mono ./cmd/mono \
 && go build -trimpath -ldflags="-s -w" -o /out/monod ./cmd/monod

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 65532 --create-home --home-dir /app mono \
 && mkdir -p /app/runs \
 && chown -R mono:mono /app
WORKDIR /app
COPY --from=build /out/mono /out/monod /usr/local/bin/
COPY config.yaml sources.yaml ./
COPY testdata/train/publications.json ./testdata/train/publications.json
USER mono
EXPOSE 8080
ENTRYPOINT ["monod"]
