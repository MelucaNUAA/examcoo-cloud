# 第一阶段：编译 Go
FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /app/server ./cmd/server

# 第二阶段：运行
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/server .
COPY frontend/ ./frontend/
RUN mkdir -p /app/data
ENV DATA_DIR=/app/data
ENV PORT=8080
EXPOSE 8080
VOLUME ["/app/data"]
CMD ["./server"]
