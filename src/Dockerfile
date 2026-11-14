# syntax=docker/dockerfile:1

# Keep GO_VERSION in step with the `go` directive in go.mod.
ARG GO_VERSION=1.24

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bookings-api . \
 && mkdir /out/data

# Static binary on an empty base: no shell, no package manager, runs as a numeric non-root user.
FROM scratch
# /data is created here and owned by the runtime user, so a named volume mounted
# on it is writable on first use.
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build /out/bookings-api /bookings-api
USER 65532:65532
ENV PORT=8080 DATA_FILE=/data/state.json
EXPOSE 8080
ENTRYPOINT ["/bookings-api"]
