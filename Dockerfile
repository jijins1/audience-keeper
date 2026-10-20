FROM golang:1.26.5 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/audience-keeper ./cmd/audience-keeper

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/audience-keeper /audience-keeper
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/audience-keeper"]
