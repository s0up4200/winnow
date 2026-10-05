FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /winnow .

FROM gcr.io/distroless/static:nonroot
LABEL org.opencontainers.image.source=https://github.com/s0up4200/winnow
COPY --from=build /winnow /winnow
EXPOSE 8080
HEALTHCHECK CMD ["/winnow", "healthcheck"]
ENTRYPOINT ["/winnow"]
