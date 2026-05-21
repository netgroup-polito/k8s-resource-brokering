FROM golang:1.24 AS builder

WORKDIR /app
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -o mock-geo ./cmd/mock-geo

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/mock-geo .

EXPOSE 8080
ENTRYPOINT ["./mock-geo"]
