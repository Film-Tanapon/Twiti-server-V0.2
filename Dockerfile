# Stage 1: Builder
FROM golang:1.25.7-alpine AS builder

WORKDIR /app

RUN go install github.com/cosmtrek/air@latest

# 1. ก๊อปปี้ทั้ง go.mod และ go.sum (รวบเป็นบรรทัดเดียวได้เลย)
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 2. Build แอปโดยปิด CGO เพื่อให้ได้ไฟล์ Binary ที่สมบูรณ์ รันที่ไหนก็ได้
RUN CGO_ENABLED=0 GOOS=linux go build -o main .

# Stage 2: Runner
FROM alpine:latest
WORKDIR /root/

# 3. ติดตั้ง ca-certificates (สำหรับยิง API Google Login) และ tzdata (สำหรับจัดการโซนเวลา)
RUN apk --no-cache add ca-certificates tzdata

COPY --from=builder /app/main .

# เปิด Port (Render จะจัดการต่อเอง)
EXPOSE 3000

CMD ["./main"]