FROM alpine:latest

RUN apk add --no-cache ca-certificates \
    && update-ca-certificates

# Create the working directory for pico
RUN mkdir -p /var/lib/pilab/pico

# Set working directory
WORKDIR /var/lib/pilab/pico

COPY ./bin/pico-git-summary /usr/local/bin/

CMD ["pico-git-summary"]