FROM golang:1.13-alpine3.10 as builder
COPY go.* /modbuild/
WORKDIR /modbuild
ENV GOPROXY=https://proxy.golang.org
RUN go mod download
COPY *.go /modbuild/
RUN go build -o /app *.go
 
FROM alpine:3.10
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app .
ENTRYPOINT ["/app"]