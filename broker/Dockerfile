FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/credential-service ./cmd/credential-service

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/credential-service /credential-service
ENTRYPOINT ["/credential-service"]
CMD ["serve"]
