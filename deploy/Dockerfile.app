# syntax=docker/dockerfile:1.27@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e
FROM golang:1.27.1@sha256:162be5298a40ed317005c8339c6de4d10d3eef336d66dc8e9259b03ab9d3a6d2 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# End-to-end builds swap in synthetic calibration fixtures.
ARG CALIBRATION_FIXTURES=
RUN if [ -n "$CALIBRATION_FIXTURES" ]; then rm -f calibration/fixtures/*.json && cp "$CALIBRATION_FIXTURES"/*.json calibration/fixtures/; fi
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/admin ./cmd/admin

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/server /out/admin /app/
USER nonroot
EXPOSE 8080
ENTRYPOINT ["/app/server"]
