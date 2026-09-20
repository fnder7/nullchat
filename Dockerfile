FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/nullchat ./cmd/server

FROM scratch
COPY --from=build /out/nullchat /nullchat
USER 65532:65532
ENV ADDR=0.0.0.0:8080
EXPOSE 8080
ENTRYPOINT ["/nullchat"]
