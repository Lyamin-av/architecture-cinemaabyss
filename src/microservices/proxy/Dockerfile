FROM golang:1.23-alpine AS builder

WORKDIR /app

# Внешних зависимостей нет — только стандартная библиотека Go
COPY go.mod ./
COPY *.go ./

# Тесты маршрутизации выполняются при сборке образа: сломанная логика не попадёт в образ
RUN go test ./...
RUN CGO_ENABLED=0 GOOS=linux go build -o proxy-service .

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /app/proxy-service .

EXPOSE 8000

CMD ["./proxy-service"]
