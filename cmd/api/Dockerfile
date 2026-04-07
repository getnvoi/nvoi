FROM golang:1.25-alpine AS build
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /nvoi-api ./cmd/api

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /nvoi-api .
EXPOSE 8080
CMD ["./nvoi-api"]
