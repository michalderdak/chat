FROM golang:1.24-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY gen/go gen/go
COPY gateway gateway
RUN CGO_ENABLED=0 go build -o /gateway ./gateway

FROM alpine:3.20
COPY --from=build /gateway /gateway
ENTRYPOINT ["/gateway"]
