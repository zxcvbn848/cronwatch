FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /cronwatch ./cmd/server

FROM scratch
COPY --from=build /cronwatch /cronwatch
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/cronwatch"]
