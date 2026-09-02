FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /dig ./cmd/dig

FROM gcr.io/distroless/static-debian12:nonroot
USER 1001:1001
COPY --from=build /dig /dig
ENTRYPOINT ["/dig"]
