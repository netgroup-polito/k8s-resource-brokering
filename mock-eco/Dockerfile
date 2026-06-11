FROM golang:1.24 AS builder

WORKDIR /app
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -o mock-eco ./cmd/mock-eco

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/mock-eco .

EXPOSE 8081
ENTRYPOINT ["./mock-eco"]
