# ---------- build stage ----------
FROM golang:1.22 AS build
WORKDIR /app

# Cache modules
COPY go.mod go.sum ./
RUN go env -w GOPROXY=https://proxy.golang.org,direct \
 && go mod download

# Copy the source
COPY . .

# Build (no VCS stamps; smaller binary)
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /bin/worker

# ---------- runtime stage ----------
FROM gcr.io/distroless/base-debian12
COPY --from=build /bin/worker /worker
ENTRYPOINT ["/worker"]
