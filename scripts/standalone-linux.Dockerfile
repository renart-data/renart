FROM debian:bullseye-slim@sha256:cba95a21c96c1f5fc2470081829363eed57706634f7dc26e8c6712934303d57a

ENV DEBIAN_FRONTEND=noninteractive

# Keep the GLIBC 2.31 build baseline reproducible after Bullseye's LTS end.
COPY scripts/standalone-linux.sources.list /etc/apt/sources.list

# The snapshot mirror can reset individual transfers. Retry the same pinned,
# checksum-verified packages rather than failing the entire native build.
RUN apt-get -o Acquire::Retries=3 update \
	&& apt-get -o Acquire::Retries=3 install -y --no-install-recommends \
		g++ \
		gcc \
		libgtk-3-dev \
		libwebkit2gtk-4.0-dev \
		pkg-config \
	&& rm -rf /var/lib/apt/lists/*
