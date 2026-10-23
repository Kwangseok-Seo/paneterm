FROM node:24-trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends chromium zsh procps \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /e2e
COPY package.json ./
RUN npm install --no-audit --no-fund
# zsh like macOS, with an empty rc so it skips its first-run wizard.
ENV SHELL=/bin/zsh
RUN touch /root/.zshrc
COPY panel.mjs ./
CMD ["node", "panel.mjs"]
