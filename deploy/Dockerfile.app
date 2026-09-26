# syntax=docker/dockerfile:1
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# End-to-end builds swap in synthetic calibration fixtures.
ARG CALIBRATION_FIXTURES=
RUN if [ -n "$CALIBRATION_FIXTURES" ]; then rm -f calibration/fixtures/*.json && cp "$CALIBRATION_FIXTURES"/*.json calibration/fixtures/; fi
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/admin ./cmd/admin

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /out/admin /app/
USER nonroot
EXPOSE 8080
ENTRYPOINT ["/app/server"]
