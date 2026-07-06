# OpenShell MicroVM sandbox image (Linux + GPU passthrough)
# Workspace is /sandbox (OpenShell default). CMD is replaced by the supervisor;
# pass the start command after `--` on `openshell sandbox create`.
FROM nvidia/cuda:12.6.3-base-ubuntu24.04

ENV DEBIAN_FRONTEND=noninteractive
ENV IS_SANDBOX=1

RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		ca-certificates \
		curl \
		git \
		iproute2 \
		less \
		make \
		python-is-python3 \
		python3 \
		ripgrep \
		sudo \
		zsh \
	&& rm -rf /var/lib/apt/lists/*

COPY --from=ghcr.io/astral-sh/uv:0.11.25 /uv /uvx /bin/

# OpenShell requires a non-root sandbox user in container images.
RUN groupadd -g 1000660000 sandbox \
	&& useradd -m -u 1000660000 -g sandbox sandbox

RUN install -d -o sandbox -g sandbox /sandbox
WORKDIR /sandbox

ENV PATH=/sandbox/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV HOME=/sandbox

RUN curl -fsSL https://claude.ai/install.sh | bash \
	&& curl -sSL https://raw.githubusercontent.com/eycjur/dotfiles/main/remote-install.sh | zsh \
	&& chown -R sandbox:sandbox /sandbox

CMD ["zsh", "-l"]
