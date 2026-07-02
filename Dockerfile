FROM ubuntu:26.04

ENV DEBIAN_FRONTEND=noninteractive
ENV IS_SANDBOX=1

RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		ca-certificates \
		curl \
		git \
		less \
		make \
		python-is-python3 \
		python3 \
		ripgrep \
		sudo \
		zsh \
	&& rm -rf /var/lib/apt/lists/* \
	&& userdel -r ubuntu \
	&& useradd --create-home --uid 1000 --shell /bin/zsh agent \
	&& echo "agent ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/agent \
	&& chmod 0440 /etc/sudoers.d/agent

COPY --from=ghcr.io/astral-sh/uv:0.11.25 /uv /uvx /bin/

ENV PATH=/home/agent/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

WORKDIR /home/agent

USER agent
RUN mkdir -p /home/agent/workspace \
	&& curl -fsSL https://claude.ai/install.sh | bash \
	&& curl -sSL https://raw.githubusercontent.com/eycjur/dotfiles/main/remote-install.sh | zsh

CMD ["zsh", "-l"]
