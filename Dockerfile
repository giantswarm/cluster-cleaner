# Use Go for building the app.
FROM --platform=${BUILDPLATFORM} golang:1.26 AS app

ARG TARGETOS
ARG TARGETARCH

# Copy sources.
WORKDIR /app
COPY . .

# Build app.
RUN GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" CGO_ENABLED=0 go build -o manager main.go

# Use a distroless image for running the app.
FROM gcr.io/distroless/static:nonroot

# Copy app.
COPY --from=app /app/manager /manager

# Define entrypoint.
USER 65532:65532
WORKDIR /
ENTRYPOINT [ "/manager" ]
