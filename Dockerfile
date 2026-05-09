FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /mem7 ./cmd/mem7

FROM alpine:3.21
RUN mkdir -p /root/.mem7
COPY --from=builder /mem7 /usr/local/bin/mem7
ENTRYPOINT ["mem7"]
