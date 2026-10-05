FROM golang:1.27-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
WORKDIR /src
COPY main.go .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /vpn-country main.go

FROM scratch
COPY --from=build /vpn-country /vpn-country
EXPOSE 8090
ENTRYPOINT ["/vpn-country"]
