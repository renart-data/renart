FROM debian:bullseye-slim@sha256:cba95a21c96c1f5fc2470081829363eed57706634f7dc26e8c6712934303d57a

ENV DEBIAN_FRONTEND=noninteractive

# Keep the GLIBC 2.31 build baseline reproducible after Bullseye's LTS end.
COPY scripts/standalone-linux.sources.list /etc/apt/sources.list

RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		g++ \
		gcc \
		libgtk-3-dev \
		libwebkit2gtk-4.0-dev \
		pkg-config \
	&& rm -rf /var/lib/apt/lists/*
