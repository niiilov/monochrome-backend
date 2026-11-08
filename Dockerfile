FROM golang:1.23
WORKDIR /src
COPY . .
RUN go build -o /out/mono ./cmd/mono && go build -o /out/monod ./cmd/monod
ENTRYPOINT ["/out/mono"]
