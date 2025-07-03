# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Install dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o gamerpal ./cmd/gamerpal

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates
WORKDIR /root/

# Copy the binary from builder stage
COPY --from=builder /app/gamerpal .

# Create .env file template
# TODO this should be done in a different way.
RUN echo "DISCORD_BOT_TOKEN=your_bot_token_here" > .env.template

CMD ["./gamerpal"]
