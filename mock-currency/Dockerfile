FROM golang:1.24 AS builder

WORKDIR /app
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -o mock-currency ./cmd/mock-currency

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/mock-currency .

EXPOSE 8082
ENTRYPOINT ["./mock-currency"]
