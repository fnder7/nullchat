FROM golang:1.26-alpine AS build
RUN apk add --no-cache build-base ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/nullchat ./cmd/server
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/nullchat /nullchat
COPY --from=build /out/migrate /migrate
USER 65532:65532
ENV ADDR=0.0.0.0:8080
EXPOSE 8080
ENTRYPOINT ["/nullchat"]
