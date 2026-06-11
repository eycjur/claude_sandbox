# Dockerfile
FROM python:3.13-slim

RUN apt-get update && apt-get install -y \
    curl git sudo vim zsh \
    && curl -fsSL https://claude.ai/install.sh | bash \
    && chsh -s /bin/zsh

WORKDIR /workspace

RUN pip install --no-cache-dir \
    numpy \
    pandas

RUN curl -sSL https://raw.githubusercontent.com/eycjur/dotfiles/main/remote-install.sh | zsh

