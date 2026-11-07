FROM golang:1.23
WORKDIR /src
COPY . .
RUN go build -o /out/digest ./cmd/digest && go build -o /out/digestd ./cmd/digestd
ENTRYPOINT ["/out/digest"]
